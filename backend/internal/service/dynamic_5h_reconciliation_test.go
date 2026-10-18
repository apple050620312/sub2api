//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dynamic5hMeterTestRepository struct {
	Dynamic5hPolicyRepository
	events       []Dynamic5hMeterEvent
	ackError     error
	acknowledged map[int64]bool
}

func (r *dynamic5hMeterTestRepository) EnqueueMeterEvent(_ context.Context, event Dynamic5hMeterEvent) error {
	r.events = append(r.events, event)
	return nil
}
func (r *dynamic5hMeterTestRepository) ListMeterEvents(_ context.Context, afterID int64, pending bool, cutoff time.Time, _ int) ([]Dynamic5hMeterEvent, error) {
	events := []Dynamic5hMeterEvent{}
	for _, event := range r.events {
		if event.ID > afterID && (!pending || !r.acknowledged[event.ID]) && !event.OccurredAt.Before(cutoff) {
			events = append(events, event)
		}
	}
	return events, nil
}
func (r *dynamic5hMeterTestRepository) AcknowledgeMeterEvent(_ context.Context, eventID int64) error {
	if r.ackError != nil {
		return r.ackError
	}
	r.acknowledged[eventID] = true
	return nil
}
func (r *dynamic5hMeterTestRepository) PruneMeterEvents(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type dynamic5hMeterTestCache struct {
	*dynamic5hMemoryCache
	receipts map[int64]bool
	err      error
}

func (c *dynamic5hMeterTestCache) ApplyMeterEvent(ctx context.Context, event Dynamic5hMeterEvent, now time.Time) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	if c.receipts[event.ID] {
		return false, nil
	}
	if err := c.RecordUsage(ctx, event.UserID, event.OccurredAt.Unix()/300, now, now.Add(-dynamic5hActiveLease), event.UserAmount, dynamic5hRedisTTL); err != nil {
		return false, err
	}
	c.receipts[event.ID] = true
	return true, nil
}

func TestDynamic5hReconciliationRetriesDeliveryAndAcknowledgment(t *testing.T) {
	now := time.Now().UTC()
	svc, ctx := newDynamic5hTestService(t, now)
	repo := &dynamic5hMeterTestRepository{events: []Dynamic5hMeterEvent{{ID: 1, UserID: 7, Amount: 25, UserAmount: 25, OccurredAt: now}}, acknowledged: map[int64]bool{}}
	cache := &dynamic5hMeterTestCache{dynamic5hMemoryCache: newDynamic5hMemoryCache(), receipts: map[int64]bool{}, err: errors.New("redis unavailable")}
	svc.policyRepo = repo
	svc.cache = cache
	_, err := svc.ReconcileMeters(ctx, false, 0)
	require.Error(t, err)
	require.False(t, repo.acknowledged[1])
	cache.err = nil
	repo.ackError = errors.New("postgres unavailable after Redis write")
	_, err = svc.ReconcileMeters(ctx, false, 0)
	require.Error(t, err)
	repo.ackError = nil
	result, err := svc.ReconcileMeters(ctx, false, 0)
	require.NoError(t, err)
	require.Equal(t, 1, result.Processed)
	require.Zero(t, result.Repaired)
	values, err := cache.UserMeterValues(ctx, 7, now.Unix()/300, 1)
	require.NoError(t, err)
	require.Equal(t, []float64{25}, values)
	require.EqualValues(t, 2, svc.Metrics().ReconciliationFailures)
	require.EqualValues(t, 1, svc.Metrics().RepairedMeterEvents)
	cache.dynamic5hMemoryCache = newDynamic5hMemoryCache()
	cache.receipts = map[int64]bool{}
	result, err = svc.ReconcileMeters(ctx, true, 0)
	require.NoError(t, err)
	require.Equal(t, 1, result.Repaired)
}

func TestDynamic5hReconciliationExpiresOldEvents(t *testing.T) {
	now := time.Now().UTC()
	svc, ctx := newDynamic5hTestService(t, now)
	repo := &dynamic5hMeterTestRepository{events: []Dynamic5hMeterEvent{{ID: 1, UserID: 7, Amount: 25, UserAmount: 25, OccurredAt: now.Add(-6 * time.Hour)}}, acknowledged: map[int64]bool{}}
	svc.policyRepo = repo
	svc.cache = &dynamic5hMeterTestCache{dynamic5hMemoryCache: newDynamic5hMemoryCache(), receipts: map[int64]bool{}}
	result, err := svc.ReconcileMeters(ctx, false, 0)
	require.NoError(t, err)
	require.Equal(t, 1, result.Expired)
	require.True(t, repo.acknowledged[1])
}
