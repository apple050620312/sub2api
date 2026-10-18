//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type dynamic5hFaultCache struct {
	*dynamic5hMemoryCache
	fault string
}

func (c *dynamic5hFaultCache) UserMeterValues(ctx context.Context, userID, latest int64, count int) ([]float64, error) {
	if c.fault == "usage" {
		return nil, errors.New("usage read unavailable")
	}
	return c.dynamic5hMemoryCache.UserMeterValues(ctx, userID, latest, count)
}
func (c *dynamic5hFaultCache) ReserveUserUsage(ctx context.Context, userID int64, token string, observed, limit, amount float64, now time.Time, ttl time.Duration) (bool, float64, error) {
	if c.fault == "reservation" {
		return false, 0, errors.New("reservation unavailable")
	}
	return c.dynamic5hMemoryCache.ReserveUserUsage(ctx, userID, token, observed, limit, amount, now, ttl)
}

func TestDynamic5hAdmissionFailuresAreObservableAndFailOpen(t *testing.T) {
	for _, fault := range []string{"usage", "reservation"} {
		t.Run(fault, func(t *testing.T) {
			svc, ctx := newDynamic5hTestService(t, time.Now())
			storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 100)
			svc.cache = &dynamic5hFaultCache{dynamic5hMemoryCache: svc.cache.(*dynamic5hMemoryCache), fault: fault}
			ctx = context.WithValue(ctx, ctxkey.RequestID, "request")
			require.NoError(t, svc.CheckUser(ctx, 1))
			if fault == "usage" {
				require.EqualValues(t, 1, svc.Metrics().MeterReadFailures)
			} else {
				require.EqualValues(t, 1, svc.Metrics().ReservationFailOpen)
			}
			require.Zero(t, svc.Metrics().Denials)
		})
	}
}
