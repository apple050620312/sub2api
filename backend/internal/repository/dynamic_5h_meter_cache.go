package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

func (c *dynamic5hPressureCache) ApplyMeterEvent(ctx context.Context, event service.Dynamic5hMeterEvent, now time.Time) (bool, error) {
	bucket := event.OccurredAt.Unix() / 300
	ttl := event.OccurredAt.Add(6 * time.Hour).Sub(now)
	if ttl <= 0 {
		return false, nil
	}
	keys := []string{
		fmt.Sprintf("%smeter_receipt:%d", dynamic5hPricePrefix, event.ID),
		fmt.Sprintf("%s%d", dynamic5hMeterBucketBase, bucket),
		dynamic5hUserMeterKey(event.UserID, bucket),
		dynamic5hActiveUsersKey,
		dynamic5hRollingUsersKey,
	}
	result, err := applyDynamic5hMeterEventScript.Run(ctx, c.rdb, keys, event.Amount, event.UserAmount, ttl.Milliseconds(), strconv.FormatInt(event.UserID, 10), event.OccurredAt.Unix(), now.Add(-15*time.Minute).Unix(), now.Add(-5*time.Hour).Unix()).Int()
	return result == 1, err
}

var applyDynamic5hMeterEventScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
for index = 2, 3 do
  local kind = redis.call('TYPE', KEYS[index]).ok
  if kind ~= 'none' and kind ~= 'string' then return redis.error_reply('invalid meter key type') end
  local value = redis.call('GET', KEYS[index])
  if value and not tonumber(value) then return redis.error_reply('invalid meter value') end
end
for index = 4, 5 do
  local kind = redis.call('TYPE', KEYS[index]).ok
  if kind ~= 'none' and kind ~= 'zset' then return redis.error_reply('invalid population key type') end
end
redis.call('INCRBYFLOAT', KEYS[2], ARGV[1])
redis.call('PEXPIRE', KEYS[2], ARGV[3])
if tonumber(ARGV[2]) > 0 then
  redis.call('INCRBYFLOAT', KEYS[3], ARGV[2])
  redis.call('PEXPIRE', KEYS[3], ARGV[3])
  for index = 4, 5 do
    local old = tonumber(redis.call('ZSCORE', KEYS[index], ARGV[4]) or '0')
    if tonumber(ARGV[5]) > old then redis.call('ZADD', KEYS[index], ARGV[5], ARGV[4]) end
  end
end
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', ARGV[6])
redis.call('ZREMRANGEBYSCORE', KEYS[5], '-inf', ARGV[7])
redis.call('PSETEX', KEYS[1], ARGV[3], '1')
return 1
`)
