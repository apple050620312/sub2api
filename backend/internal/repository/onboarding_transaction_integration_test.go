//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

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
