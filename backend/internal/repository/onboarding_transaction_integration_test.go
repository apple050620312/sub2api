//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type onboardingTierTestAdmin struct {
	service.AdminService
	accounts service.AccountRepository
}

func (admin *onboardingTierTestAdmin) GetUser(_ context.Context, userID int64) (*service.User, error) {
	return &service.User{ID: userID}, nil
}

func (admin *onboardingTierTestAdmin) GetAccount(ctx context.Context, accountID int64) (*service.Account, error) {
	return admin.accounts.GetByID(ctx, accountID)
}

func (admin *onboardingTierTestAdmin) ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode, sortBy, sortOrder string) ([]service.Account, int64, error) {
	accounts, result, err := admin.accounts.ListWithFilters(ctx, pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}, platform, accountType, status, search, groupID, privacyMode)
	if err != nil {
		return nil, 0, err
	}
	return accounts, result.Total, nil
}

func (admin *onboardingTierTestAdmin) UpdateAccountExtra(ctx context.Context, accountID int64, extra map[string]any) error {
	return admin.accounts.UpdateExtra(ctx, accountID, extra)
}

func (admin *onboardingTierTestAdmin) UpdateAccount(ctx context.Context, accountID int64, input *service.UpdateAccountInput) (*service.Account, error) {
	account, err := admin.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if input.Status != "" {
		account.Status = input.Status
		if err := admin.accounts.Update(ctx, account); err != nil {
			return nil, err
		}
	}
	if input.GroupIDs != nil {
		if err := admin.accounts.BindGroups(ctx, accountID, *input.GroupIDs); err != nil {
			return nil, err
		}
	}
	return admin.accounts.GetByID(ctx, accountID)
}

func TestOnboardingCountTierLifecycle(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	accountRepo := NewAccountRepository(client, integrationDB, nil)
	subRepo := NewUserSubscriptionRepository(client)
	subService := service.NewSubscriptionService(NewGroupRepository(client, integrationDB), subRepo, nil, client, nil)
	t.Cleanup(subService.Stop)
	svc := service.NewOnboardingService(client, &onboardingTierTestAdmin{accounts: accountRepo}, nil, subService, nil, nil)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	var originalConfig []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT config FROM onboarding_config WHERE id=1`).Scan(&originalConfig))
	groups := make(map[int]int64)
	accountIDs := make([]int64, 0, 5)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `UPDATE onboarding_config SET config=$1::jsonb WHERE id=1`, originalConfig)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM user_subscriptions WHERE user_id=$1`, user.ID)
		for _, accountID := range accountIDs {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1`, accountID)
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, accountID)
		}
		for _, groupID := range groups {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, groupID)
		}
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	})
	rules := make(map[string]service.OnboardingSubscriptionRule)
	for tier := 1; tier <= 5; tier++ {
		group := mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
		groups[tier] = group.ID
		rules[strconv.Itoa(tier)] = service.OnboardingSubscriptionRule{GroupID: group.ID, ValidityDays: 30}
		plan := "plus"
		if tier == 2 {
			plan = "team"
		}
		account := mustCreateAccount(t, client, &service.Account{Name: uuid.NewString(), Platform: service.PlatformOpenAI, Credentials: map[string]any{"plan_type": plan}})
		accountIDs = append(accountIDs, account.ID)
	}
	payload, err := json.Marshal(service.OnboardingConfig{Enabled: true, SubscriptionRules: rules})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE onboarding_config SET config=$1::jsonb WHERE id=1`, payload)
	require.NoError(t, err)
	require.ErrorIs(t, svc.BindExistingAccount(ctx, user.ID, accountIDs[0]), service.ErrDiscordBindingRequired)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO onboarding_discord_bindings (user_id, discord_user_id, discord_username, status) VALUES ($1, $2, 'tier-test-member', 'active')`, user.ID, strconv.FormatInt(user.ID, 10))
	require.NoError(t, err)
	assertTier := func(tier int) []service.UserSubscription {
		t.Helper()
		subs, err := subRepo.ListByUserID(ctx, user.ID)
		require.NoError(t, err)
		active := make([]int64, 0)
		for _, sub := range subs {
			if sub.Status == service.SubscriptionStatusActive {
				active = append(active, sub.GroupID)
			}
		}
		require.Equal(t, []int64{groups[tier]}, active)
		return subs
	}
	for index, accountID := range accountIDs {
		require.NoError(t, svc.BindExistingAccount(ctx, user.ID, accountID))
		assertTier(index + 1)
	}
	subs := assertTier(5)
	var expiresAt time.Time
	for _, sub := range subs {
		if sub.GroupID == groups[5] {
			expiresAt = sub.ExpiresAt
		}
	}
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_subscriptions SET weekly_usage_usd=7 WHERE user_id=$1 AND group_id=$2`, user.ID, groups[5])
	require.NoError(t, err)
	require.NoError(t, svc.SetOwnedAccountPaused(ctx, user.ID, accountIDs[4], true))
	assertTier(4)
	require.NoError(t, svc.SetOwnedAccountPaused(ctx, user.ID, accountIDs[4], false))
	for _, sub := range assertTier(5) {
		if sub.GroupID == groups[5] {
			require.Equal(t, 7.0, sub.WeeklyUsageUSD)
			require.True(t, expiresAt.Equal(sub.ExpiresAt))
		}
	}
}

func TestOnboardingAccountAndSubscriptionTransaction(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	accounts := NewAccountRepository(client, integrationDB, nil)
	subscriptions := NewUserSubscriptionRepository(client)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	group := mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
	account := mustCreateAccount(t, client, &service.Account{Name: uuid.NewString(), Platform: service.PlatformOpenAI, Extra: map[string]any{"onboarding_owner_user_id": user.ID}})
	require.NoError(t, accounts.BindGroups(ctx, account.ID, []int64{group.ID}))
	subscription, err := client.UserSubscription.Create().SetUserID(user.ID).SetGroupID(group.ID).
		SetStartsAt(time.Now().Add(-time.Hour)).SetExpiresAt(time.Now().Add(time.Hour)).
		SetStatus(service.SubscriptionStatusActive).SetNotes("onboarding-managed:account").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM user_subscriptions WHERE id=$1`, subscription.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	})
	for _, action := range []string{"pause", "delete"} {
		t.Run(action, func(t *testing.T) {
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			txCtx := dbent.NewTxContext(ctx, tx)
			if action == "pause" {
				current, err := accounts.GetByID(txCtx, account.ID)
				require.NoError(t, err)
				current.Status = service.StatusDisabled
				require.NoError(t, accounts.UpdateExtra(txCtx, account.ID, map[string]any{"onboarding_user_paused": true}))
				current.Extra["onboarding_user_paused"] = true
				require.NoError(t, accounts.Update(txCtx, current))
				require.NoError(t, accounts.BindGroups(txCtx, account.ID, nil))
				changed, err := accounts.GetByID(txCtx, account.ID)
				require.NoError(t, err)
				require.Equal(t, service.StatusDisabled, changed.Status)
				require.Empty(t, changed.GroupIDs)
			} else {
				require.NoError(t, accounts.Delete(txCtx, account.ID))
				_, err := accounts.GetByID(txCtx, account.ID)
				require.ErrorIs(t, err, service.ErrAccountNotFound)
			}
			require.NoError(t, subscriptions.UpdateStatus(txCtx, subscription.ID, service.SubscriptionStatusSuspended))
			require.NoError(t, tx.Rollback())
			restored, err := accounts.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, service.StatusActive, restored.Status)
			require.Equal(t, []int64{group.ID}, restored.GroupIDs)
			require.NotEqual(t, true, restored.Extra["onboarding_user_paused"])
			remaining, err := subscriptions.ListByUserID(ctx, user.ID)
			require.NoError(t, err)
			require.Len(t, remaining, 1)
			require.Equal(t, service.SubscriptionStatusActive, remaining[0].Status)
		})
	}
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	created := &service.Account{Name: uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{}, Extra: map[string]any{}}
	require.NoError(t, accounts.Create(txCtx, created))
	visible, _, err := accounts.ListWithFilters(txCtx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.PlatformOpenAI, service.AccountTypeOAuth, "", created.Name, 0, "")
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, created.ID, visible[0].ID)
	require.NoError(t, tx.Rollback())
	_, err = accounts.GetByID(ctx, created.ID)
	require.ErrorIs(t, err, service.ErrAccountNotFound)
}
