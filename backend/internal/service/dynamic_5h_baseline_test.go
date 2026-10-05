//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dynamic5hBaselineTestRepository struct {
	Dynamic5hPolicyRepository
	baseline     float64
	samples      int
	observations []Dynamic5hPlusObservation
}

func (r *dynamic5hBaselineTestRepository) ObservePlusBaseline(_ context.Context, observations []Dynamic5hPlusObservation, _ time.Time) (float64, int, error) {
	r.observations = observations
	return r.baseline, r.samples, nil
}

func TestDynamic5hPlusObservationRejectsUntrustedSamples(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus", "email": "owner@example.com"}, Extra: map[string]any{
		"codex_5h_used_percent": 99.0, "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339), "codex_usage_updated_at": now.Format(time.RFC3339),
	}}
	observation, valid := dynamic5hPlusObservation(&base, now)
	require.True(t, valid)
	require.EqualValues(t, 99, observation.UsedPercent)
	for _, mutate := range []func(*Account){
		func(account *Account) { account.Credentials["plan_type"] = "pro" },
		func(account *Account) { account.Extra["email"] = "other@example.com" },
		func(account *Account) {
			account.Extra["codex_usage_updated_at"] = now.Add(-time.Hour).Format(time.RFC3339)
		},
		func(account *Account) {
			account.Extra["codex_5h_reset_at"] = now.Add(-time.Minute).Format(time.RFC3339)
		},
	} {
		account := base
		account.Credentials = map[string]any{"plan_type": "plus", "email": "owner@example.com"}
		account.Extra = map[string]any{"codex_5h_used_percent": 99.0, "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339), "codex_usage_updated_at": now.Format(time.RFC3339)}
		mutate(&account)
		_, valid = dynamic5hPlusObservation(&account, now)
		require.False(t, valid)
	}
}

func TestDynamic5hRefreshDoesNotShrinkBaselineWithCheapTraffic(t *testing.T) {
	now := time.Now().UTC()
	svc, ctx := newDynamic5hTestService(t, now)
	svc.accountRepo = &dynamic5hPublicAccountRepository{accounts: []Account{{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"codex_5h_used_percent": 60.0, "codex_5h_reset_at": now.Add(4 * time.Hour).Format(time.RFC3339),
	}}}}
	repo := &dynamic5hBaselineTestRepository{baseline: 100, samples: 3}
	svc.policyRepo = repo
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 0.01)
	status, err := svc.Refresh(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 100, status.PlusBaseline)
	require.EqualValues(t, 100, status.PoolCapacity)
	svc.RecordUsage(ctx, 2, PlatformOpenAI, 50)
	status, err = svc.Refresh(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 100, status.PlusBaseline)
	svc.policyRepo = nil
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 100)
	status = svc.cachedStatus()
	status.PlusBaseline = 100
	svc.storeStatus(ctx, status)
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.InDelta(t, 0.01, svc.UserStatus(ctx, 1).UsagePercent, 1e-6)
}

func TestDynamic5hUncalibratedAndLegacySnapshotsNeverEnforce(t *testing.T) {
	now := time.Now().UTC()
	svc, ctx := newDynamic5hTestService(t, now)
	svc.accountRepo = &dynamic5hPublicAccountRepository{accounts: []Account{{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"codex_5h_used_percent": 95.0, "codex_5h_reset_at": now.Add(4 * time.Hour).Format(time.RFC3339),
	}}}}
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 1000)
	status, err := svc.Refresh(ctx)
	require.NoError(t, err)
	require.False(t, status.CalibrationReady)
	require.Zero(t, status.PoolCapacity)
	require.NoError(t, svc.CheckUser(ctx, 1))
	svc.accountRepo = nil
	cache := svc.cache.(*dynamic5hMemoryCache)
	cache.status = Dynamic5hPressureStatus{Enabled: true, DataAvailable: true, CalibrationReady: true, State: Dynamic5hPressurePeak, PoolCapacity: 1, EvaluatedAt: now}
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.False(t, svc.AdminStatus(ctx, false).CalibrationReady)
}

func TestDynamic5hFixedAllowanceCombinesScatteredPriceWeightedUsage(t *testing.T) {
	now := time.Now().UTC()
	svc, ctx := newDynamic5hTestService(t, now)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 600)
	status := svc.cachedStatus()
	status.AccountCount, status.PlusBaseline = 6, 100
	svc.storeStatus(ctx, status)
	for request := 0; request < 100; request++ {
		svc.RecordUsage(ctx, 1, PlatformOpenAI, dynamic5hPriceMeter(&CostBreakdown{TotalCost: 0.1}))
		svc.RecordUsage(ctx, 2, PlatformOpenAI, dynamic5hPriceMeter(&CostBreakdown{TotalCost: 2}))
	}
	require.InDelta(t, 10, svc.UserStatus(ctx, 1).UsagePercent, 1e-6)
	require.InDelta(t, 200, svc.UserStatus(ctx, 2).UsagePercent, 1e-6)
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.ErrorIs(t, svc.CheckUser(ctx, 2), ErrDynamic5hPressureLimitExceeded)
	status.AccountCount, status.PoolCapacity = 2, 200
	svc.storeStatus(ctx, status)
	require.InDelta(t, 10, svc.UserStatus(ctx, 1).UsagePercent, 1e-6)
}

func TestDynamic5hIdleUsageCannotIncreaseWithOtherUsersPressure(t *testing.T) {
	origin := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	current := origin
	svc, ctx := newDynamic5hTestService(t, origin)
	svc.now = func() time.Time { return current }
	status := Dynamic5hPressureStatus{Enabled: true, DataAvailable: true, CalibrationReady: true, PlusBaseline: 100, PoolCapacity: 600, AccountCount: 6, State: Dynamic5hPressureNormal, EvaluatedAt: current}
	svc.storeStatus(ctx, status)
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 10)
	current = origin.Add(time.Hour)
	status.EvaluatedAt = current
	svc.storeStatus(ctx, status)
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 10)
	require.InDelta(t, 20, svc.UserStatus(ctx, 1).UsagePercent, 1e-6)
	current = origin.Add(2 * time.Hour)
	svc.RecordUsage(ctx, 2, PlatformOpenAI, 1000)
	status.State, status.PeakActive, status.Pressure = Dynamic5hPressurePeak, true, 1.5
	status.AccountCount, status.PoolCapacity, status.EvaluatedAt = 2, 200, current
	svc.storeStatus(ctx, status)
	require.InDelta(t, 20, svc.UserStatus(ctx, 1).UsagePercent, 1e-6)
	current = origin.Add(dynamic5hWindow + dynamic5hMeterBucket)
	status.EvaluatedAt = current
	svc.storeStatus(ctx, status)
	require.InDelta(t, 10, svc.UserStatus(ctx, 1).UsagePercent, 1e-6)
	require.NoError(t, svc.CheckUser(ctx, 1))
}
