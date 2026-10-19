//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type poolResetIntegrationAdmin struct {
	service.AdminService
	accounts []service.Account
}

func (admin *poolResetIntegrationAdmin) ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]service.Account, int64, error) {
	return admin.accounts, int64(len(admin.accounts)), nil
}

type poolResetIntegrationQuota struct {
	at   time.Time
	used float64
}

func (quota *poolResetIntegrationQuota) QueryUsage(context.Context, int64) (*service.OpenAIQuotaUsage, error) {
	return &service.OpenAIQuotaUsage{FetchedAt: quota.at.Unix(), ResetCreditDetailsComplete: true,
		RateLimitResetCredits: &service.OpenAIRateLimitResetCredits{},
		RateLimit: &service.OpenAIRateLimit{
			PrimaryWindow:   &service.OpenAIRateLimitWindow{UsedPercent: quota.used, LimitWindowSeconds: 5 * 3600, ResetAt: quota.at.Add(3 * time.Hour).Unix()},
			SecondaryWindow: &service.OpenAIRateLimitWindow{UsedPercent: quota.used, LimitWindowSeconds: 7 * 24 * 3600, ResetAt: quota.at.Add(3 * 24 * time.Hour).Unix()},
		}}, nil
}

func TestServicePoolResetPersistsOnceAcrossRestartAndConcurrentScans(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now().UTC().Truncate(time.Second)
	var originalConfig, originalState []byte
	var originalSubscriptions []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM user_subscriptions s`).Scan(&originalSubscriptions))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT config FROM onboarding_config WHERE id=1`).Scan(&originalConfig))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM service_pool_reset_state WHERE id=1`).Scan(&originalState))
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	weekly := 100.0
	group := mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription, WeeklyLimitUSD: &weekly})
	sub, err := client.UserSubscription.Create().SetUserID(user.ID).SetGroupID(group.ID).SetStartsAt(now.Add(-24 * time.Hour)).SetExpiresAt(now.Add(24 * time.Hour)).SetDailyUsageUsd(5).SetWeeklyUsageUsd(30).SetMonthlyUsageUsd(60).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `UPDATE user_subscriptions s SET daily_usage_usd=b.daily_usage_usd,weekly_usage_usd=b.weekly_usage_usd,monthly_usage_usd=b.monthly_usage_usd,daily_window_start=b.daily_window_start,weekly_window_start=b.weekly_window_start,monthly_window_start=b.monthly_window_start,updated_at=b.updated_at FROM jsonb_to_recordset($1::jsonb) AS b(id BIGINT,daily_usage_usd NUMERIC,weekly_usage_usd NUMERIC,monthly_usage_usd NUMERIC,daily_window_start TIMESTAMPTZ,weekly_window_start TIMESTAMPTZ,monthly_window_start TIMESTAMPTZ,updated_at TIMESTAMPTZ) WHERE s.id=b.id`, originalSubscriptions)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM service_pool_reset_events WHERE notification_content='integration-reset'`)
		_, _ = integrationDB.ExecContext(ctx, `UPDATE onboarding_config SET config=$1::jsonb WHERE id=1`, originalConfig)
		_, _ = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET state=$1::jsonb,lease_until='-infinity' WHERE id=1`, originalState)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM user_subscriptions WHERE id=$1`, sub.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	})
	cfg, err := json.Marshal(service.OnboardingConfig{PoolReset: service.ServicePoolResetConfig{Enabled: true, ChannelID: "123456", Message: "integration-reset"}})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE onboarding_config SET config=$1::jsonb WHERE id=1`, cfg)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET state='{}',lease_until='-infinity' WHERE id=1`)
	require.NoError(t, err)
	admin := &poolResetIntegrationAdmin{}
	for index := 1; index <= 3; index++ {
		admin.accounts = append(admin.accounts, service.Account{ID: int64(index), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Credentials: map[string]any{"plan_type": "plus", "chatgpt_account_id": fmt.Sprintf("pool-reset-account-%d", index), "chatgpt_user_id": fmt.Sprintf("pool-reset-user-%d", index)}})
	}
	subs := service.NewSubscriptionService(NewGroupRepository(client, integrationDB), NewUserSubscriptionRepository(client), nil, client, nil)
	t.Cleanup(subs.Stop)
	onboarding := service.NewOnboardingService(client, admin, nil, subs, nil, nil)
	sampleNow := time.Now().UTC()
	quota := &poolResetIntegrationQuota{at: sampleNow.Add(-50 * time.Second), used: 40}
	observer := service.NewServicePoolResetService(onboarding, quota)
	run := func() {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET lease_until='-infinity' WHERE id=1`)
		require.NoError(t, err)
		require.NoError(t, observer.RunOnce(ctx))
	}
	run()
	quota.at, quota.used = sampleNow.Add(-35*time.Second), 0
	run()
	unchanged, err := client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, 30.0, unchanged.WeeklyUsageUsd)
	observer = service.NewServicePoolResetService(onboarding, quota)
	quota.at = time.Now().UTC()
	_, err = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET lease_until='-infinity' WHERE id=1`)
	require.NoError(t, err)
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() { defer workers.Done(); errors <- observer.RunOnce(ctx) }()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	reset, err := client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Zero(t, reset.DailyUsageUsd)
	require.Zero(t, reset.WeeklyUsageUsd)
	require.Zero(t, reset.MonthlyUsageUsd)
	require.True(t, reset.ExpiresAt.Equal(sub.ExpiresAt))
	require.Equal(t, sub.Status, reset.Status)
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_subscriptions SET weekly_usage_usd=7 WHERE id=$1`, sub.ID)
	require.NoError(t, err)
	run()
	reset, err = client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, 7.0, reset.WeeklyUsageUsd)
	var events int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM service_pool_reset_events WHERE notification_content='integration-reset'`).Scan(&events))
	require.Equal(t, 1, events)
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT notification_state FROM service_pool_reset_events WHERE notification_content='integration-reset'`).Scan(&state))
	require.Equal(t, "failed", state, "missing bot credentials must never send a real Discord message")
}
