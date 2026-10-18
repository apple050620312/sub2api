package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	dynamic5hPricePrefix    = "dynamic_5h_pressure:price_v1:"
	dynamic5hStatusKey      = dynamic5hPricePrefix + "status"
	dynamic5hRefreshLockKey = dynamic5hPricePrefix + "refresh_lock"
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
	return c.rdb.Set(ctx, dynamic5hStatusKey, raw, 6*time.Hour).Err()
}

func (c *dynamic5hPressureCache) AcquireRefreshLock(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, dynamic5hRefreshLockKey, token, ttl).Result()
}

func (c *dynamic5hPressureCache) ReleaseRefreshLock(ctx context.Context, token string) error {
	_, err := releaseDynamic5hRefreshLockScript.Run(ctx, c.rdb, []string{dynamic5hRefreshLockKey}, token).Result()
	return err
}

var releaseDynamic5hRefreshLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
