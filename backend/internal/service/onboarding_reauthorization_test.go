//go:build unit

package service

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type onboardingReauthorizationInvalidator struct {
	called int
	check  func()
}

func (invalidator *onboardingReauthorizationInvalidator) InvalidateToken(_ context.Context, account *Account) error {
	invalidator.called++
	invalidator.check()
	return nil
}

func TestOnboardingReauthorizationIdentityRequiresSameWorkspaceAndUser(t *testing.T) {
	account := &Account{Credentials: map[string]any{"chatgpt_account_id": "workspace", "chatgpt_user_id": "original-user", "email": "owner@example.com"}}
	for _, token := range []*OpenAITokenInfo{
		nil,
		{ChatGPTAccountID: "other-workspace", ChatGPTUserID: "original-user", Email: "owner@example.com"},
		{ChatGPTAccountID: "workspace", ChatGPTUserID: "another-user", Email: "owner@example.com"},
		{ChatGPTAccountID: "workspace", Email: "owner@example.com"},
	} {
		require.ErrorIs(t, validateOnboardingReauthorizationIdentity(account, token), ErrOnboardingReauthorizationIdentity)
	}
	require.NoError(t, validateOnboardingReauthorizationIdentity(account, &OpenAITokenInfo{ChatGPTAccountID: "workspace", ChatGPTUserID: "original-user"}))
	delete(account.Credentials, "chatgpt_user_id")
	require.NoError(t, validateOnboardingReauthorizationIdentity(account, &OpenAITokenInfo{ChatGPTAccountID: "workspace", Email: "OWNER@example.com"}))
	require.Error(t, validateOnboardingReauthorizationIdentity(account, &OpenAITokenInfo{ChatGPTAccountID: "workspace", Email: "someone@example.com"}))
	delete(account.Credentials, "chatgpt_account_id")
	require.Error(t, validateOnboardingReauthorizationIdentity(account, &OpenAITokenInfo{Email: "owner@example.com"}))
}

func TestOnboardingReauthorizationStateBindsUserAccountAndOAuthSession(t *testing.T) {
	svc := &OnboardingService{cfg: &config.Config{JWT: config.JWTConfig{Secret: "reauthorization-test-secret"}}}
	state, err := svc.signState(7, onboardingReauthorizationKind(42, "session"))
	require.NoError(t, err)
	for _, scenario := range []struct {
		userID    int64
		accountID int64
		sessionID string
	}{{8, 42, "session"}, {7, 43, "session"}, {7, 42, "other-session"}} {
		_, err := svc.CompleteOwnedAccountReauthorization(context.Background(), scenario.userID, scenario.accountID, scenario.sessionID, "code", "oauth-state", state)
		require.ErrorIs(t, err, ErrOnboardingStateInvalid)
	}
	require.Error(t, svc.verifyState(state, "openai", 7))
}

func TestOnboardingReauthorizationStartsWithoutAccountTemplate(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		database, mock, err := sqlmock.New()
		require.NoError(t, err)
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
		t.Cleanup(func() { _ = client.Close() })
		account := Account{ID: 42, Name: "Original GPT", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{onboardingOwnerExtraKey: int64(7)}}
		if foreign {
			account.Extra[onboardingOwnerExtraKey] = int64(8)
		}
		oauth := NewOpenAIOAuthService(nil, nil)
		t.Cleanup(oauth.Stop)
		svc := NewOnboardingService(client, &onboardingTestAdmin{accounts: []Account{account}}, oauth, nil, &config.Config{JWT: config.JWTConfig{Secret: "reauthorization-test-secret"}}, nil, nil)
		mock.ExpectQuery("SELECT config FROM onboarding_config").WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow(`{"enabled":true,"account_template_id":0}`))
		now := time.Now()
		mock.ExpectQuery("SELECT user_id, discord_user_id").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "discord_user_id", "discord_username", "guild_id", "role_id", "status", "last_checked_at", "created_at", "updated_at"}).AddRow(7, "discord-7", "tester", "guild", "role", "active", now, now, now))
		result, err := svc.StartOwnedAccountReauthorization(context.Background(), 7, 42)
		if foreign {
			require.ErrorIs(t, err, ErrOnboardingAccountMissing)
		} else {
			require.NoError(t, err)
			authorization, err := url.Parse(result.AuthURL)
			require.NoError(t, err)
			require.Equal(t, result.RedirectURI, authorization.Query().Get("redirect_uri"))
			require.NoError(t, svc.verifyState(result.OnboardingState, onboardingReauthorizationKind(42, result.SessionID), 7))
		}
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestOnboardingReauthorizationPreservesSettingsAndQuotaAfterCommit(t *testing.T) {
	for _, scenario := range []string{"expired", "paused", "admin_disabled", "foreign", "wrong_identity", "missing_refresh", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			database, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
			t.Cleanup(func() { _ = client.Close() })
			now := time.Now()
			account := Account{ID: 42, Name: "Original GPT", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusError, Schedulable: true, Concurrency: 8, GroupIDs: []int64{9}, Credentials: map[string]any{"chatgpt_account_id": "workspace", "chatgpt_user_id": "original-user", "email": "owner@example.com", "plan_type": "plus", "access_token": "old-access", "refresh_token": "old-refresh", "model_mapping": map[string]any{"source": "target"}}, Extra: map[string]any{onboardingOwnerExtraKey: int64(7), "codex_cli_only": true}}
			if scenario == "paused" {
				account.Extra[onboardingPausedExtraKey] = true
			}
			if scenario == "admin_disabled" {
				account.Status = StatusDisabled
			}
			if scenario == "foreign" {
				account.Extra[onboardingOwnerExtraKey] = int64(8)
			}
			token := &OpenAITokenInfo{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresAt: now.Add(time.Hour).Unix(), ChatGPTAccountID: "workspace", ChatGPTUserID: "original-user", Email: "owner@example.com", PlanType: "plus"}
			if scenario == "wrong_identity" {
				token.ChatGPTUserID = "another-user"
			}
			if scenario == "missing_refresh" {
				token.RefreshToken = ""
			}
			admin := &onboardingTestAdmin{accounts: []Account{account}}
			expires := now.Add(24 * time.Hour)
			repo := &onboardingTestSubscriptions{subs: []UserSubscription{{ID: 1, UserID: 7, GroupID: 9, StartsAt: now.Add(-time.Hour), ExpiresAt: expires, Status: SubscriptionStatusActive, DailyUsageUSD: 2, WeeklyUsageUSD: 8, MonthlyUsageUSD: 16, Notes: onboardingManagedNotes + ":account"}}}
			invalidator := &onboardingReauthorizationInvalidator{check: func() { require.NoError(t, mock.ExpectationsWereMet()) }}
			svc := NewOnboardingService(client, admin, &OpenAIOAuthService{}, &SubscriptionService{userSubRepo: repo}, nil, nil, invalidator)
			mock.ExpectBegin()
			mock.ExpectExec("SELECT id FROM onboarding_config").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("SELECT id FROM users").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("SELECT id FROM accounts").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
			rejected := scenario == "foreign" || scenario == "wrong_identity" || scenario == "missing_refresh"
			if !rejected {
				mock.ExpectExec("UPDATE accounts SET error_message='' WHERE id").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT config FROM onboarding_config").WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow(`{"enabled":true,"subscription_rules":{"1":{"group_id":9,"validity_days":30}}}`))
				mock.ExpectExec("SELECT id FROM user_subscriptions").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if scenario == "rollback" {
				repo.err = errors.New("subscription update failed")
			}
			if rejected || scenario == "rollback" {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			updated, err := svc.applyOwnedAccountReauthorization(context.Background(), 7, 42, token)
			if rejected || scenario == "rollback" {
				require.Error(t, err)
				require.Zero(t, invalidator.called)
				if rejected {
					require.Zero(t, admin.updates)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, invalidator.called)
				require.Equal(t, int64(42), updated.ID)
				require.Equal(t, "Original GPT", updated.Name)
				require.Equal(t, "new-access", updated.GetCredential("access_token"))
				require.Equal(t, "new-refresh", updated.GetCredential("refresh_token"))
				require.Equal(t, map[string]any{"source": "target"}, updated.Credentials["model_mapping"])
				require.Equal(t, 8, updated.Concurrency)
				require.Equal(t, true, updated.Extra["codex_cli_only"])
				require.Equal(t, int64(7), accountOwnerID(*updated))
				if scenario == "paused" || scenario == "admin_disabled" {
					require.Equal(t, StatusDisabled, updated.Status)
					require.Equal(t, SubscriptionStatusSuspended, repo.subs[0].Status)
				} else {
					require.Equal(t, StatusActive, updated.Status)
				}
				require.Equal(t, float64(2), repo.subs[0].DailyUsageUSD)
				require.Equal(t, float64(8), repo.subs[0].WeeklyUsageUSD)
				require.Equal(t, float64(16), repo.subs[0].MonthlyUsageUSD)
				require.Equal(t, expires, repo.subs[0].ExpiresAt)
				require.Zero(t, admin.deletes)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
