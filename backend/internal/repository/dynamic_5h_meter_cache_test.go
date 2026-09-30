//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestDynamic5hMeterReplayIsIdempotentAndRestoresLostRedis(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &dynamic5hPressureCache{rdb: client}
	ctx := context.Background()
	now := time.Now().UTC()
	event := service.Dynamic5hMeterEvent{ID: 1, UserID: 7, Amount: 25, UserAmount: 25, OccurredAt: now}
	applied, err := cache.ApplyMeterEvent(ctx, event, now)
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = cache.ApplyMeterEvent(ctx, event, now)
	require.NoError(t, err)
	require.False(t, applied)
	bucket := now.Unix() / 300
	values, err := cache.UserMeterValues(ctx, 7, bucket, 1)
	require.NoError(t, err)
	require.Equal(t, []float64{25}, values)
	server.FlushAll()
	applied, err = cache.ApplyMeterEvent(ctx, event, now)
	require.NoError(t, err)
	require.True(t, applied)
	values, err = cache.UserMeterValues(ctx, 7, bucket, 1)
	require.NoError(t, err)
	require.Equal(t, []float64{25}, values)
	server.FlushAll()
	event.UserAmount = 0
	_, err = cache.ApplyMeterEvent(ctx, event, now)
	require.NoError(t, err)
	global, err := cache.MeterValues(ctx, bucket, 1)
	require.NoError(t, err)
	require.Equal(t, []float64{25}, global)
	values, err = cache.UserMeterValues(ctx, 7, bucket, 1)
	require.NoError(t, err)
	require.Equal(t, []float64{0}, values)
}

func TestDynamic5hMeterCorruptionCannotPartiallyIncrement(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &dynamic5hPressureCache{rdb: client}
	now := time.Now().UTC()
	event := service.Dynamic5hMeterEvent{ID: 1, UserID: 7, Amount: 25, UserAmount: 25, OccurredAt: now}
	server.Set(dynamic5hUserMeterKey(7, now.Unix()/300), "corrupt")
	_, err := cache.ApplyMeterEvent(context.Background(), event, now)
	require.Error(t, err)
	values, err := cache.MeterValues(context.Background(), now.Unix()/300, 1)
	require.NoError(t, err)
	require.Equal(t, []float64{0}, values)
	require.False(t, server.Exists(dynamic5hPricePrefix+"meter_receipt:1"))
}
