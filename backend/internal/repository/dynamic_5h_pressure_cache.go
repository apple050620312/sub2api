package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	dynamic5hStatusKey       = "dynamic_5h_pressure:status"
	dynamic5hRefreshLockKey  = "dynamic_5h_pressure:refresh_lock"
	dynamic5hActiveUsersKey  = "dynamic_5h_pressure:active_users"
	dynamic5hRollingUsersKey = "dynamic_5h_pressure:rolling_users"
	dynamic5hMeterBucketBase = "dynamic_5h_pressure:meter:"
	dynamic5hUserMeterBase   = "dynamic_5h_pressure:user_meter:"
	dynamic5hPolicyBase      = "dynamic_5h_pressure:policy:"
	dynamic5hPendingUsersKey = "dynamic_5h_pressure:pending_users"
	dynamic5hPendingBase     = "dynamic_5h_pressure:pending:"
	dynamic5hReservationBase = "dynamic_5h_pressure:reservation:"
)

type dynamic5hPressureCache struct {
	rdb *redis.Client
}

func NewDynamic5hPressureCache(rdb *redis.Client) service.Dynamic5hPressureCache {
	return &dynamic5hPressureCache{rdb: rdb}
}

func (c *dynamic5hPressureCache) LoadStatus(ctx context.Context) (service.Dynamic5hPressureStatus, bool, error) {
	raw, err := c.rdb.Get(ctx, dynamic5hStatusKey).Bytes()
	if err == redis.Nil {
		return service.Dynamic5hPressureStatus{}, false, nil
	}
	if err != nil {
		return service.Dynamic5hPressureStatus{}, false, err
	}
	var status service.Dynamic5hPressureStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return service.Dynamic5hPressureStatus{}, false, err
	}
	return status, true, nil
}

func (c *dynamic5hPressureCache) StoreStatus(ctx context.Context, status service.Dynamic5hPressureStatus) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, dynamic5hStatusKey, raw, 0).Err()
}

func (c *dynamic5hPressureCache) AcquireRefreshLock(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, dynamic5hRefreshLockKey, token, ttl).Result()
}

func (c *dynamic5hPressureCache) ReleaseRefreshLock(ctx context.Context, token string) error {
	_, err := releaseDynamic5hRefreshLockScript.Run(ctx, c.rdb, []string{dynamic5hRefreshLockKey}, token).Result()
	return err
}

func (c *dynamic5hPressureCache) TouchActiveUser(ctx context.Context, userID int64, now, cutoff time.Time) error {
	pipe := c.rdb.TxPipeline()
	pipe.ZAdd(ctx, dynamic5hActiveUsersKey, redis.Z{Score: float64(now.Unix()), Member: strconv.FormatInt(userID, 10)})
	pipe.ZRemRangeByScore(ctx, dynamic5hActiveUsersKey, "-inf", strconv.FormatInt(cutoff.Unix(), 10))
	_, err := pipe.Exec(ctx)
	return err
}

func (c *dynamic5hPressureCache) ActiveUserIDs(ctx context.Context, cutoff time.Time) ([]string, error) {
	active, err := c.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     dynamic5hActiveUsersKey,
		Start:   strconv.FormatInt(cutoff.Unix(), 10),
		Stop:    "+inf",
		ByScore: true,
	}).Result()
	if err != nil {
		return nil, err
	}
	pending, err := c.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{Key: dynamic5hPendingUsersKey, Start: strconv.FormatInt(cutoff.Add(15*time.Minute).Unix(), 10), Stop: "+inf", ByScore: true}).Result()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(active)+len(pending))
	result := make([]string, 0, len(active)+len(pending))
	for _, id := range append(active, pending...) {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result, nil
}

func (c *dynamic5hPressureCache) RollingUserIDs(ctx context.Context, cutoff time.Time) ([]string, error) {
	return c.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: dynamic5hRollingUsersKey, Start: strconv.FormatInt(cutoff.Unix(), 10), Stop: "+inf", ByScore: true,
	}).Result()
}

func (c *dynamic5hPressureCache) RecordUsage(
	ctx context.Context,
	userID, bucket int64,
	now, cutoff time.Time,
	amount float64,
	ttl time.Duration,
) error {
	pipe := c.rdb.TxPipeline()
	pipe.ZAdd(ctx, dynamic5hActiveUsersKey, redis.Z{Score: float64(now.Unix()), Member: strconv.FormatInt(userID, 10)})
	pipe.ZRemRangeByScore(ctx, dynamic5hActiveUsersKey, "-inf", strconv.FormatInt(cutoff.Unix(), 10))
	pipe.ZAdd(ctx, dynamic5hRollingUsersKey, redis.Z{Score: float64(now.Unix()), Member: strconv.FormatInt(userID, 10)})
	pipe.ZRemRangeByScore(ctx, dynamic5hRollingUsersKey, "-inf", strconv.FormatInt(now.Add(-5*time.Hour).Unix(), 10))
	meterKey := fmt.Sprintf("%s%d", dynamic5hMeterBucketBase, bucket)
	pipe.IncrByFloat(ctx, meterKey, amount)
	pipe.Expire(ctx, meterKey, ttl)
	userKey := dynamic5hUserMeterKey(userID, bucket)
	pipe.IncrByFloat(ctx, userKey, amount)
	pipe.Expire(ctx, userKey, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *dynamic5hPressureCache) MeterValues(ctx context.Context, latestBucket int64, count int) ([]float64, error) {
	keys := make([]string, 0, count)
	for i := count - 1; i >= 0; i-- {
		keys = append(keys, fmt.Sprintf("%s%d", dynamic5hMeterBucketBase, latestBucket-int64(i)))
	}
	return c.floatValues(ctx, keys)
}

func (c *dynamic5hPressureCache) UserMeterValues(ctx context.Context, userID, latestBucket int64, count int) ([]float64, error) {
	keys := make([]string, 0, count)
	for i := count - 1; i >= 0; i-- {
		keys = append(keys, dynamic5hUserMeterKey(userID, latestBucket-int64(i)))
	}
	return c.floatValues(ctx, keys)
}

func (c *dynamic5hPressureCache) ReserveUserUsage(ctx context.Context, userID int64, token string, observed, limit, amount float64, now time.Time, ttl time.Duration) (bool, float64, error) {
	keys := []string{fmt.Sprintf("%s%d", dynamic5hPendingBase, userID), dynamic5hReservationBase + token, dynamic5hPendingUsersKey}
	result, err := reserveDynamic5hUsageScript.Run(ctx, c.rdb, keys, observed, limit, amount, int64(ttl/time.Millisecond), strconv.FormatInt(userID, 10), now.Add(ttl).Unix()).Slice()
	if err != nil {
		return false, 0, err
	}
	allowed, _ := result[0].(int64)
	pending, _ := strconv.ParseFloat(fmt.Sprint(result[1]), 64)
	return allowed == 1, pending, nil
}

func (c *dynamic5hPressureCache) SettleUserReservation(ctx context.Context, userID int64, token string) error {
	_, err := settleDynamic5hUsageScript.Run(ctx, c.rdb, []string{fmt.Sprintf("%s%d", dynamic5hPendingBase, userID), dynamic5hReservationBase + token}).Result()
	return err
}

func (c *dynamic5hPressureCache) PendingUserUsage(ctx context.Context, userID int64) (float64, error) {
	value, err := c.rdb.Get(ctx, fmt.Sprintf("%s%d", dynamic5hPendingBase, userID)).Float64()
	if err == redis.Nil {
		return 0, nil
	}
	return value, err
}

func (c *dynamic5hPressureCache) floatValues(ctx context.Context, keys []string) ([]float64, error) {
	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	result := make([]float64, len(values))
	for i, value := range values {
		if value == nil {
			continue
		}
		result[i], _ = strconv.ParseFloat(fmt.Sprint(value), 64)
	}
	return result, nil
}

func (c *dynamic5hPressureCache) LoadUserPolicy(ctx context.Context, userID int64) (service.Dynamic5hUserPolicy, error) {
	var p service.Dynamic5hUserPolicy
	raw, err := c.rdb.Get(ctx, fmt.Sprintf("%s%d", dynamic5hPolicyBase, userID)).Bytes()
	if err == redis.Nil {
		p.Multiplier = 1
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p)
	if p.Multiplier <= 0 {
		p.Multiplier = 1
	}
	return p, err
}

func (c *dynamic5hPressureCache) StoreUserPolicy(ctx context.Context, userID int64, policy service.Dynamic5hUserPolicy) error {
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, fmt.Sprintf("%s%d", dynamic5hPolicyBase, userID), raw, 0).Err()
}

func (c *dynamic5hPressureCache) ResetUserUsage(ctx context.Context, userID int64) error {
	pattern := fmt.Sprintf("%s%d:*", dynamic5hUserMeterBase, userID)
	var cursor uint64
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, pattern, 500).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return c.rdb.ZRem(ctx, dynamic5hRollingUsersKey, strconv.FormatInt(userID, 10)).Err()
		}
	}
}

func dynamic5hUserMeterKey(userID, bucket int64) string {
	return fmt.Sprintf("%s%d:%d", dynamic5hUserMeterBase, userID, bucket)
}

var releaseDynamic5hRefreshLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var reserveDynamic5hUsageScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 1 then
  return {1, tonumber(redis.call('GET', KEYS[1]) or '0')}
end
local pending = tonumber(redis.call('GET', KEYS[1]) or '0')
if tonumber(ARGV[1]) + pending + tonumber(ARGV[3]) > tonumber(ARGV[2]) then
  return {0, pending}
end
pending = redis.call('INCRBYFLOAT', KEYS[1], ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
redis.call('ZADD', KEYS[3], ARGV[6], ARGV[5])
return {1, pending}
`)

var settleDynamic5hUsageScript = redis.NewScript(`
local amount = redis.call('GET', KEYS[2])
if not amount then return 0 end
local pending = tonumber(redis.call('GET', KEYS[1]) or '0') - tonumber(amount)
if pending > 0 then redis.call('SET', KEYS[1], pending, 'KEEPTTL') else redis.call('DEL', KEYS[1]) end
redis.call('DEL', KEYS[2])
return 1
`)
