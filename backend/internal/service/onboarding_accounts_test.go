//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

type onboardingTestAdmin struct {
	AdminService
	accounts []Account
	updates  int
	deletes  int
}

func (admin *onboardingTestAdmin) GetAccount(_ context.Context, accountID int64) (*Account, error) {
	for index := range admin.accounts {
		if admin.accounts[index].ID == accountID {
			return &admin.accounts[index], nil
		}
	}
	return nil, ErrAccountNotFound
}

func (admin *onboardingTestAdmin) GetUser(_ context.Context, userID int64) (*User, error) {
	return &User{ID: userID, Email: "owner@example.com"}, nil
}

func (admin *onboardingTestAdmin) ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]Account, int64, error) {
	return append([]Account(nil), admin.accounts...), int64(len(admin.accounts)), nil
}

func (admin *onboardingTestAdmin) UpdateAccountExtra(ctx context.Context, accountID int64, extra map[string]any) error {
	account, err := admin.GetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	for key, value := range extra {
		account.Extra[key] = value
	}
	return nil
}

func (admin *onboardingTestAdmin) UpdateAccount(ctx context.Context, accountID int64, input *UpdateAccountInput) (*Account, error) {
	account, err := admin.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if input.Status != "" {
		account.Status = input.Status
		admin.updates++
	}
	return account, nil
}

func (admin *onboardingTestAdmin) DeleteAccount(_ context.Context, accountID int64) error {
	admin.deletes++
	admin.accounts = nil
	return nil
}

type onboardingTestSubscriptions struct {
	UserSubscriptionRepository
	subs   []UserSubscription
	writes int
	err    error
}

func (repo *onboardingTestSubscriptions) ListByUserID(context.Context, int64) ([]UserSubscription, error) {
	return append([]UserSubscription(nil), repo.subs...), repo.err
}

func (repo *onboardingTestSubscriptions) UpdateStatus(_ context.Context, subscriptionID int64, status string) error {
	for index := range repo.subs {
		if repo.subs[index].ID == subscriptionID {
			repo.subs[index].Status = status
		}
	}
	repo.writes++
	return repo.err
}

func (repo *onboardingTestSubscriptions) UpdateNotes(_ context.Context, subscriptionID int64, notes string) error {
	for index := range repo.subs {
		if repo.subs[index].ID == subscriptionID {
			repo.subs[index].Notes = notes
		}
	}
	return repo.err
}

func TestOnboardingSubscriptionEligibilityIsIdempotent(t *testing.T) {
	now := time.Now()
	repo := &onboardingTestSubscriptions{subs: []UserSubscription{
		{ID: 1, UserID: 7, GroupID: 9, Status: SubscriptionStatusActive, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), WeeklyUsageUSD: 8, Notes: onboardingManagedNotes + ":account"},
		{ID: 2, UserID: 7, GroupID: 10, Status: SubscriptionStatusActive, ExpiresAt: now.Add(24 * time.Hour), Notes: "purchased"},
	}}
	svc := &OnboardingService{subscriptions: &SubscriptionService{userSubRepo: repo}}
	selected := map[int64]OnboardingSubscriptionRule{9: {GroupID: 9, ValidityDays: 30}}
	for attempt := 0; attempt < 3; attempt++ {
		_, err := svc.syncOnboardingSubscriptions(context.Background(), 7, selected, repo.subs, now)
		require.NoError(t, err)
	}
	require.Zero(t, repo.writes)
	_, err := svc.syncOnboardingSubscriptions(context.Background(), 7, nil, repo.subs, now)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusSuspended, repo.subs[0].Status)
	require.Equal(t, SubscriptionStatusActive, repo.subs[1].Status)
	_, err = svc.syncOnboardingSubscriptions(context.Background(), 7, selected, repo.subs, now)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusActive, repo.subs[0].Status)
	require.Equal(t, now.Add(24*time.Hour), repo.subs[0].ExpiresAt)
	require.Equal(t, 8.0, repo.subs[0].WeeklyUsageUSD)
	repo.subs[0].Status = SubscriptionStatusSuspended
	_, err = svc.syncOnboardingSubscriptions(context.Background(), 7, selected, repo.subs, now)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusSuspended, repo.subs[0].Status, "administrator suspension must not be cleared")
	repo.subs[0].Notes += ":account_paused"
	repo.subs[0].ExpiresAt = now.Add(-time.Minute)
	_, err = svc.syncOnboardingSubscriptions(context.Background(), 7, selected, repo.subs, now)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusSuspended, repo.subs[0].Status, "restoring an account must not renew an expired subscription")
}

func TestOnboardingRejectsDuplicateOpenAIAccount(t *testing.T) {
	for _, exists := range []bool{false, true} {
		database, mock, err := sqlmock.New()
		require.NoError(t, err)
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
		t.Cleanup(func() { _ = client.Close() })
		svc := &OnboardingService{db: client}
		mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("onboarding-openai:workspace").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("workspace", "member", "user@example.com").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(exists))
		err = svc.checkOpenAIAccountNotAdded(context.Background(), &OpenAITokenInfo{ChatGPTAccountID: "workspace", ChatGPTUserID: "member", Email: "USER@example.com"})
		if exists {
			require.ErrorContains(t, err, "已接入")
		} else {
			require.NoError(t, err)
		}
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestOnboardingAccountMutationsCheckOwnershipAndReconcile(t *testing.T) {
	for _, action := range []string{"pause", "resume", "delete", "foreign", "failure", "admin_error"} {
		t.Run(action, func(t *testing.T) {
			database, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
			t.Cleanup(func() { _ = client.Close() })
			account := Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"plan_type": "plus"}, Extra: map[string]any{onboardingOwnerExtraKey: int64(7)}}
			if action == "resume" || action == "admin_error" {
				account.Status = StatusDisabled
				account.Extra[onboardingPausedExtraKey] = true
			}
			if action == "admin_error" {
				account.Status = StatusError
			}
			if action == "foreign" {
				account.Extra[onboardingOwnerExtraKey] = int64(8)
			}
			admin := &onboardingTestAdmin{accounts: []Account{account}}
			now := time.Now()
			repo := &onboardingTestSubscriptions{subs: []UserSubscription{{ID: 1, UserID: 7, GroupID: 9, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), Status: SubscriptionStatusActive, Notes: onboardingManagedNotes + ":account"}}}
			svc := NewOnboardingService(client, admin, nil, &SubscriptionService{userSubRepo: repo}, nil, nil)
			mock.ExpectBegin()
			mock.ExpectExec("SELECT id FROM onboarding_config").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("SELECT id FROM users").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("SELECT id FROM accounts").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
			if action != "foreign" && action != "admin_error" {
				mock.ExpectQuery("SELECT config FROM onboarding_config").WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow(`{"enabled":true,"subscription_rules":{"1":{"group_id":9,"validity_days":30}}}`))
				mock.ExpectExec("SELECT id FROM user_subscriptions").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if action == "failure" {
				repo.err = errors.New("subscription update failed")
			}
			if action == "foreign" || action == "failure" || action == "admin_error" {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			if action == "delete" {
				err = svc.DeleteOwnedAccount(context.Background(), 7, 42)
			} else {
				err = svc.SetOwnedAccountPaused(context.Background(), 7, 42, action != "resume" && action != "admin_error")
			}
			if action == "foreign" || action == "failure" || action == "admin_error" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if action == "foreign" || action == "admin_error" {
				require.Zero(t, admin.updates)
				require.Zero(t, admin.deletes)
			}
			if action == "pause" || action == "delete" {
				require.Equal(t, SubscriptionStatusSuspended, repo.subs[0].Status)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestOnboardingAccountDetailsExcludeCredentials(t *testing.T) {
	account := Account{ID: 42, Name: "My GPT", Platform: PlatformOpenAI, Credentials: map[string]any{"email": "owner@example.com", "plan_type": "plus", "access_token": "private-access", "refresh_token": "private-refresh"}, Extra: map[string]any{"private": "private-extra"}}
	payload, err := json.Marshal(onboardingAccountSummary(account))
	require.NoError(t, err)
	require.Contains(t, string(payload), "owner@example.com")
	for _, secret := range []string{"private-access", "private-refresh", "private-extra", "credentials", "access_token", "refresh_token"} {
		require.NotContains(t, string(payload), secret)
	}
}
