//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDynamic5hSubscriptionRollingSpending(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-" + uuid.NewString(), Name: "quota"})
	account := mustCreateAccount(t, client, &service.Account{Name: "quota-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	weekly := 60.0
	group := mustCreateGroup(t, client, &service.Group{Name: "quota-" + uuid.NewString(), SubscriptionType: service.SubscriptionTypeSubscription, WeeklyLimitUSD: &weekly})
	now := time.Now().UTC().Truncate(time.Second)
	subscription, err := client.UserSubscription.Create().SetUserID(user.ID).SetGroupID(group.ID).
		SetStartsAt(now.Add(-24 * time.Hour)).SetExpiresAt(now.Add(24 * time.Hour)).SetStatus(service.SubscriptionStatusActive).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, deletion := range []struct {
			query string
			id    int64
		}{
			{"DELETE FROM dynamic_5h_audit_logs WHERE user_id=$1", user.ID},
			{"DELETE FROM usage_logs WHERE user_id=$1", user.ID},
			{"DELETE FROM user_subscriptions WHERE id=$1", subscription.ID},
			{"DELETE FROM api_keys WHERE id=$1", key.ID},
			{"DELETE FROM accounts WHERE id=$1", account.ID},
			{"DELETE FROM groups WHERE id=$1", group.ID},
			{"DELETE FROM users WHERE id=$1", user.ID},
		} {
			_, cleanupErr := integrationDB.ExecContext(ctx, deletion.query, deletion.id)
			require.NoError(t, cleanupErr)
		}
	})
	usageRepo := NewUsageLogRepository(client, integrationDB)
	for _, spending := range []struct {
		offset time.Duration
		amount float64
	}{
		{-5 * time.Hour, 100},
		{-4 * time.Hour, 4},
		{-4 * time.Hour, 2},
		{-time.Hour, 9},
		{time.Minute, 100},
	} {
		_, err := usageRepo.Create(ctx, &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: uuid.NewString(), Model: "quota", TotalCost: 999, ActualCost: spending.amount, CreatedAt: now.Add(spending.offset)})
		require.NoError(t, err)
	}
	repo := &dynamic5hPolicyRepository{db: integrationDB}
	quota, err := repo.ReadSubscription5hQuota(ctx, user.ID, now, 1)
	require.NoError(t, err)
	require.Equal(t, 60.0, quota.WeeklyLimitUSD)
	require.Equal(t, 15.0, quota.UsageUSD)
	require.NotNil(t, quota.RecoverAt)
	require.True(t, quota.RecoverAt.Equal(now.Add(4*time.Hour)), "at $9 the user remains limited until strictly below the $9 allowance")
	quota, err = repo.ReadSubscription5hQuota(ctx, user.ID, now, 2)
	require.NoError(t, err)
	require.True(t, quota.RecoverAt.Equal(now.Add(time.Hour)))
	_, err = integrationDB.ExecContext(ctx, "UPDATE usage_logs SET actual_cost=0 WHERE user_id=$1 AND created_at>$2", user.ID, now)
	require.NoError(t, err)
	quota, err = (&dynamic5hPolicyRepository{db: integrationDB}).ReadSubscription5hQuota(ctx, user.ID, now.Add(time.Hour), 1)
	require.NoError(t, err)
	require.Equal(t, 9.0, quota.UsageUSD, "recreated repository reads persisted logs and expires the exact five-hour boundary")
	_, err = integrationDB.ExecContext(ctx, "INSERT INTO dynamic_5h_audit_logs(user_id,action,created_at) VALUES($1,'usage_reset',$2)", user.ID, now.Add(-2*time.Hour))
	require.NoError(t, err)
	quota, err = repo.ReadSubscription5hQuota(ctx, user.ID, now, 1)
	require.NoError(t, err)
	require.Equal(t, 9.0, quota.UsageUSD)
	_, err = integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET expires_at=$2 WHERE id=$1", subscription.ID, now)
	require.NoError(t, err)
	quota, err = repo.ReadSubscription5hQuota(ctx, user.ID, now, 1)
	require.NoError(t, err)
	require.Zero(t, quota.WeeklyLimitUSD)
	userIDs, err := repo.ListSubscription5hUserIDs(ctx, now)
	require.NoError(t, err)
	require.NotContains(t, userIDs, user.ID)
}
