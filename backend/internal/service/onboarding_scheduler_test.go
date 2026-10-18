//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestOnboardingSchedulerPrefersOwnerAndFallsBackToPublic(t *testing.T) {
	for _, mode := range []string{"scheduler", "legacy_batch", "legacy_single"} {
		for _, ownerAvailable := range []bool{true, false} {
			t.Run(mode+map[bool]string{true: "/owner", false: "/fallback"}[ownerAvailable], func(t *testing.T) {
				groupID := int64(10121)
				accounts := []Account{
					{ID: 21611, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 9, GroupIDs: []int64{groupID}, Extra: map[string]any{onboardingOwnerExtraKey: int64(7)}, Credentials: map[string]any{"plan_type": "plus"}},
					{ID: 21612, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}, Extra: map[string]any{onboardingOwnerExtraKey: int64(8), onboardingPublicFallbackExtraKey: false}, Credentials: map[string]any{"plan_type": "plus"}},
				}
				concurrency := schedulerTestConcurrencyCache{acquireResults: map[int64]bool{21611: ownerAvailable, 21612: true}, loadMap: map[int64]*AccountLoadInfo{21611: {AccountID: 21611, LoadRate: 0}, 21612: {AccountID: 21612, LoadRate: 0}}}
				svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{}, cfg: newSchedulerTestOpenAIWSV2Config(), concurrencyService: NewConcurrencyService(concurrency)}
				svc.cfg.Gateway.Scheduling.LoadBatchEnabled = mode == "legacy_batch"
				if mode == "scheduler" {
					svc.cfg = newSchedulerTestSubscriptionPriorityConfig()
					svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
				}
				ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(7))
				var selection *AccountSelectionResult
				var err error
				if mode != "scheduler" {
					selection, err = svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "gpt-5.1", nil)
				} else {
					selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				}
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.True(t, selection.Acquired, "an available public account must be selected before waiting for the owner")
				want := int64(21612)
				if ownerAvailable {
					want = 21611
				}
				require.Equal(t, want, selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			})
		}
	}
}
