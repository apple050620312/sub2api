//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dynamic5hSnapshotRepository struct {
	Dynamic5hPolicyRepository
	mu       sync.Mutex
	policies map[int64]Dynamic5hUserPolicy
	loads    int
	err      error
}

func (r *dynamic5hSnapshotRepository) ListUserPolicies(context.Context) (map[int64]Dynamic5hUserPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loads++
	result := make(map[int64]Dynamic5hUserPolicy, len(r.policies))
	for userID, policy := range r.policies {
		result[userID] = policy
	}
	return result, r.err
}

func (r *dynamic5hSnapshotRepository) GetUserPolicy(context.Context, int64) (Dynamic5hUserPolicy, bool, error) {
	panic("per-user DB reads must not occur")
}

func (r *dynamic5hSnapshotRepository) UpsertUserPolicy(_ context.Context, userID int64, policy Dynamic5hUserPolicy, _ int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[userID] = policy
	return nil
}

func TestDynamic5hPeakUsesOnePolicySnapshot(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, now)
	repo := &dynamic5hSnapshotRepository{policies: map[int64]Dynamic5hUserPolicy{2: {Exempt: true, Multiplier: 1}}}
	svc.policyRepo = repo
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 100)
	for userID := int64(1); userID <= 100; userID++ {
		svc.RecordUsage(ctx, userID, PlatformOpenAI, 1)
	}
	for attempt := 0; attempt < 20; attempt++ {
		require.NoError(t, svc.CheckUser(ctx, 1))
	}
	require.Equal(t, 1, repo.loads)
	require.NoError(t, svc.SetUserPolicy(ctx, 1, Dynamic5hUserPolicy{Exempt: true, Multiplier: 1}))
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.Equal(t, 2, repo.loads)
}

func TestDynamic5hPolicySnapshotsExpireAcrossInstances(t *testing.T) {
	current := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, current)
	svc.now = func() time.Time { return current }
	repo := &dynamic5hSnapshotRepository{policies: map[int64]Dynamic5hUserPolicy{}}
	svc.policyRepo = repo
	require.False(t, svc.userPolicy(ctx, 7).Exempt)
	repo.policies[7] = Dynamic5hUserPolicy{Exempt: true, Multiplier: 1}
	current = current.Add(31 * time.Second)
	require.True(t, svc.userPolicy(ctx, 7).Exempt)
	repo.err = errors.New("database offline")
	current = current.Add(3 * time.Minute)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 1)
	svc.RecordUsage(ctx, 8, PlatformOpenAI, 10)
	require.NoError(t, svc.CheckUser(ctx, 8))
	require.EqualValues(t, 1, svc.Metrics().PolicyFailOpen)
}

func TestDynamic5hConcurrentSnapshotLoad(t *testing.T) {
	svc, ctx := newDynamic5hTestService(t, time.Now())
	repo := &dynamic5hSnapshotRepository{policies: map[int64]Dynamic5hUserPolicy{}}
	svc.policyRepo = repo
	var workers sync.WaitGroup
	for worker := 0; worker < 50; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _ = svc.policySnapshot(ctx)
		}()
	}
	workers.Wait()
	require.Equal(t, 1, repo.loads)
}

func TestDynamic5hStalePeakStopsEnforcementWithoutErasingUsage(t *testing.T) {
	current := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, current)
	svc.now = func() time.Time { return current }
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 100)
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 120)
	require.ErrorIs(t, svc.CheckUser(ctx, 1), ErrDynamic5hPressureLimitExceeded)
	current = current.Add(91 * time.Second)
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.True(t, svc.AdminStatus(ctx, false).Stale)
	require.False(t, svc.AdminStatus(ctx, false).CalibrationReady)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 100)
	require.ErrorIs(t, svc.CheckUser(ctx, 1), ErrDynamic5hPressureLimitExceeded)
	require.EqualValues(t, 1, svc.Metrics().StaleFailOpen)
	require.EqualValues(t, 2, svc.Metrics().Denials)
}

func TestDynamic5hDisabledAndInvalidSnapshotsNeverEnforce(t *testing.T) {
	for _, evaluatedAt := range []time.Time{{}, time.Now().Add(time.Hour)} {
		svc, ctx := newDynamic5hTestService(t, time.Now())
		storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 1)
		status := svc.cachedStatus()
		status.EvaluatedAt = evaluatedAt
		svc.storeStatus(ctx, status)
		svc.RecordUsage(ctx, 1, PlatformOpenAI, 10)
		require.NoError(t, svc.CheckUser(ctx, 1))
	}
	svc, ctx := newDynamic5hTestService(t, time.Now())
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 1)
	svc.cfg.Enabled = false
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 10)
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.False(t, svc.AdminStatus(ctx, false).Enabled)
}
