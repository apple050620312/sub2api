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
	dynamic5hMeterBucketBase = "dynamic_5h_pressure:meter:"
	dynamic5hUserMeterBase   = "dynamic_5h_pressure:user_meter:"
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
	return c.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     dynamic5hActiveUsersKey,
		Start:   strconv.FormatInt(cutoff.Unix(), 10),
		Stop:    "+inf",
		ByScore: true,
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

func dynamic5hUserMeterKey(userID, bucket int64) string {
	return fmt.Sprintf("%s%d:%d", dynamic5hUserMeterBase, userID, bucket)
}

var releaseDynamic5hRefreshLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
