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
	status := service.Dynamic5hPressureStatus{Enabled: true, State: service.Dynamic5hPressurePeak}
	require.NoError(t, cache.StoreStatus(ctx, status))
	loaded, found, err := cache.LoadStatus(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, status, loaded)

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
