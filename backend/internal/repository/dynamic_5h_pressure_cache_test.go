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

func TestDynamic5hPressureCache(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache := NewDynamic5hPressureCache(rdb)
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	bucket := now.Unix() / int64((5*time.Minute)/time.Second)

	status := service.Dynamic5hPressureStatus{Enabled: true, State: service.Dynamic5hPressurePeak, PoolCapacity: 500}
	require.NoError(t, cache.StoreStatus(ctx, status))
	loaded, found, err := cache.LoadStatus(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, status, loaded)

	require.NoError(t, cache.RecordUsage(ctx, 42, bucket, now, now.Add(-5*time.Hour), 25, 6*time.Hour))
	ids, err := cache.ActiveUserIDs(ctx, now.Add(-5*time.Hour))
	require.NoError(t, err)
	require.Equal(t, []string{"42"}, ids)
	values, err := cache.MeterValues(ctx, bucket, 2)
	require.NoError(t, err)
	require.Equal(t, []float64{0, 25}, values)
	userValues, err := cache.UserMeterValues(ctx, 42, bucket, 2)
	require.NoError(t, err)
	require.Equal(t, []float64{0, 25}, userValues)

	acquired, err := cache.AcquireRefreshLock(ctx, "owner", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = cache.AcquireRefreshLock(ctx, "other", time.Minute)
	require.NoError(t, err)
	require.False(t, acquired)
	require.NoError(t, cache.ReleaseRefreshLock(ctx, "owner"))
	acquired, err = cache.AcquireRefreshLock(ctx, "other", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
}
