//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type subscription5hTestRepository struct {
	quotas map[int64]Subscription5hQuota
	err    error
}

func (r *subscription5hTestRepository) ReadSubscription5hQuota(_ context.Context, userID int64, _ time.Time) (Subscription5hQuota, error) {
	return r.quotas[userID], r.err
}

func (r *subscription5hTestRepository) ListSubscription5hUserIDs(context.Context, time.Time) ([]int64, error) {
	var userIDs []int64
	for userID := range r.quotas {
		userIDs = append(userIDs, userID)
	}
	return userIDs, r.err
}

func TestDynamic5hSubscriptionPeakOnlyEnforcement(t *testing.T) {
	for _, test := range []struct {
		name    string
		state   Dynamic5hPressureState
		usage   float64
		exempt  bool
		limited bool
	}{
		{name: "off peak over allowance", state: Dynamic5hPressureNormal, usage: 60},
		{name: "peak below allowance", state: Dynamic5hPressurePeak, usage: 14.99},
		{name: "peak at allowance", state: Dynamic5hPressurePeak, usage: 15, limited: true},
		{name: "peak over allowance", state: Dynamic5hPressurePeak, usage: 60, limited: true},
		{name: "peak exemption", state: Dynamic5hPressurePeak, usage: 60, exempt: true},
		{name: "legacy multiplier cannot increase allowance", state: Dynamic5hPressurePeak, usage: 20, limited: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, ctx := newDynamic5hTestService(t, time.Now().UTC())
			svc.quotaRepo = &subscription5hTestRepository{quotas: map[int64]Subscription5hQuota{7: {WeeklyLimitUSD: 100, UsageUSD: test.usage}}}
			svc.policyRepo = &dynamic5hSnapshotRepository{policies: map[int64]Dynamic5hUserPolicy{7: {Exempt: test.exempt}}}
			storeDynamic5hTestStatus(svc, ctx, test.state, 0)
			status, err := svc.subscriptionQuotaUserStatus(ctx, 7)
			require.NoError(t, err)
			require.Equal(t, 15.0, status.LimitUSD)
			require.Equal(t, test.limited, status.CurrentlyLimited)
			require.Equal(t, test.limited, svc.CheckUser(ctx, 7) != nil)
		})
	}
}

func TestDynamic5hSubscriptionOtherUsersCannotChangePersonalUsage(t *testing.T) {
	svc, ctx := newDynamic5hTestService(t, time.Now().UTC())
	repo := &subscription5hTestRepository{quotas: map[int64]Subscription5hQuota{7: {WeeklyLimitUSD: 100, UsageUSD: 3}, 8: {WeeklyLimitUSD: 100, UsageUSD: 1}}}
	svc.quotaRepo = repo
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressureNormal, 200)
	before, err := svc.subscriptionQuotaUserStatus(ctx, 7)
	require.NoError(t, err)
	repo.quotas[8] = Subscription5hQuota{WeeklyLimitUSD: 100, UsageUSD: 1000}
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 1)
	after, err := svc.subscriptionQuotaUserStatus(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, before.UsageUSD, after.UsageUSD)
	require.Equal(t, before.LimitUSD, after.LimitUSD)
	require.Equal(t, before.UsagePercent, after.UsagePercent)
	require.NoError(t, svc.CheckUser(ctx, 7))
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressureNormal, 1)
	require.NoError(t, svc.CheckUser(ctx, 8), "leaving peak immediately removes the extra limit")
}

func TestDynamic5hSubscriptionFailOpen(t *testing.T) {
	svc, ctx := newDynamic5hTestService(t, time.Now().UTC())
	repo := &subscription5hTestRepository{quotas: map[int64]Subscription5hQuota{7: {UsageUSD: 100}}}
	svc.quotaRepo = repo
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 0)
	status, err := svc.subscriptionQuotaUserStatus(ctx, 7)
	require.NoError(t, err)
	require.False(t, status.QuotaAvailable)
	require.NoError(t, svc.CheckUser(ctx, 7))
	repo.err = errors.New("database unavailable")
	require.NoError(t, svc.CheckUser(ctx, 7))
	repo.err = nil
	repo.quotas[7] = Subscription5hQuota{WeeklyLimitUSD: 100, UsageUSD: 100}
	svc.now = func() time.Time { return time.Now().UTC().Add(24 * time.Hour) }
	require.NoError(t, svc.CheckUser(ctx, 7), "stale pressure must not enforce")
}

func TestDynamic5hSubscriptionAccountOwnership(t *testing.T) {
	owner := &Account{ID: 1, Extra: map[string]any{onboardingOwnerExtraKey: int64(7)}}
	other := &Account{ID: 2, Extra: map[string]any{onboardingOwnerExtraKey: int64(8), onboardingPublicFallbackExtraKey: false}}
	public := &Account{ID: 3}
	owned, fallback := partitionOnboardingAccounts([]*Account{other, nil, owner, public}, 7)
	require.Equal(t, []*Account{owner}, owned)
	require.Equal(t, []*Account{other, public}, fallback, "legacy fallback flags must not prevent contributed accounts from serving other users")
	require.False(t, onboardingAccountBelongsToUser(nil, 7))
	require.False(t, onboardingAccountBelongsToUser(public, 0))
}
