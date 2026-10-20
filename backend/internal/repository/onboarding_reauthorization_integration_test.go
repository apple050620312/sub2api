//go:build integration

package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type onboardingReauthorizationOAuthClient struct {
	service.OpenAIOAuthClient
	idToken string
}

func (client *onboardingReauthorizationOAuthClient) ExchangeCode(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
	return &openai.TokenResponse{AccessToken: "new-access", RefreshToken: "new-refresh", IDToken: client.idToken, ExpiresIn: 3600}, nil
}

type onboardingReauthorizationCache struct {
	called int
	check  func(context.Context, *service.Account)
}

func (cache *onboardingReauthorizationCache) InvalidateToken(ctx context.Context, account *service.Account) error {
	cache.called++
	cache.check(ctx, account)
	return nil
}

func TestOnboardingReauthorizationUpdatesOriginalAccountTransactionally(t *testing.T) {
	client := testEntClient(t)
	for _, scenario := range []string{"expired", "admin_disabled", "wrong_identity"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var originalConfig []byte
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT config FROM onboarding_config WHERE id=1`).Scan(&originalConfig))
			user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
			group := mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
			workspace, gptUser := uuid.NewString(), uuid.NewString()
			status := service.StatusError
			if scenario == "admin_disabled" {
				status = service.StatusDisabled
			}
			account := mustCreateAccount(t, client, &service.Account{Name: "Original GPT " + uuid.NewString(), Platform: service.PlatformOpenAI, Status: status, ErrorMessage: "expired authorization", Concurrency: 7, Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh", "chatgpt_account_id": workspace, "chatgpt_user_id": gptUser, "email": user.Email, "plan_type": "plus", "model_mapping": map[string]any{"source": "target"}}, Extra: map[string]any{"onboarding_owner_user_id": user.ID, "codex_cli_only": true}})
			sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: user.ID, GroupID: group.ID, DailyUsageUSD: 2, WeeklyUsageUSD: 8, MonthlyUsageUSD: 16, Notes: "purchased"})
			t.Cleanup(func() {
				cleanupCtx := context.Background()
				_, _ = integrationDB.ExecContext(cleanupCtx, `UPDATE onboarding_config SET config=$1::jsonb WHERE id=1`, originalConfig)
				_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM onboarding_discord_bindings WHERE user_id=$1`, user.ID)
				_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM user_subscriptions WHERE id=$1`, sub.ID)
				_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM account_groups WHERE account_id=$1`, account.ID)
				_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, account.ID)
				_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM groups WHERE id=$1`, group.ID)
				_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM users WHERE id=$1`, user.ID)
			})
			_, err := integrationDB.ExecContext(ctx, `UPDATE onboarding_config SET config='{"enabled":true,"subscription_rules":{}}'::jsonb WHERE id=1`)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, `INSERT INTO onboarding_discord_bindings (user_id,discord_user_id,discord_username,status) VALUES ($1,$2,'reauth-test','active')`, user.ID, strconv.FormatInt(user.ID, 10))
			require.NoError(t, err)
			claims := openai.IDTokenClaims{Email: user.Email, Exp: time.Now().Add(time.Hour).Unix(), OpenAIAuth: &openai.OpenAIAuthClaims{ChatGPTAccountID: workspace, ChatGPTUserID: gptUser, ChatGPTPlanType: "plus"}}
			if scenario == "wrong_identity" {
				claims.OpenAIAuth.ChatGPTUserID = uuid.NewString()
			}
			payload, err := json.Marshal(claims)
			require.NoError(t, err)
			oauth := service.NewOpenAIOAuthService(nil, &onboardingReauthorizationOAuthClient{idToken: "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"})
			t.Cleanup(oauth.Stop)
			accounts := NewAccountRepository(client, integrationDB, nil)
			subscriptions := NewUserSubscriptionRepository(client)
			cache := &onboardingReauthorizationCache{check: func(cacheCtx context.Context, updated *service.Account) {
				persisted, err := accounts.GetByID(cacheCtx, updated.ID)
				require.NoError(t, err)
				require.Equal(t, "new-access", persisted.GetCredential("access_token"))
			}}
			svc := service.NewServiceCenterService(client, &onboardingTierTestAdmin{accounts: accounts}, oauth, service.NewSubscriptionService(NewGroupRepository(client, integrationDB), subscriptions, nil, client, nil), &config.Config{JWT: config.JWTConfig{Secret: "reauthorization-integration-secret"}}, nil, cache)
			start, err := svc.StartOwnedAccountReauthorization(ctx, user.ID, account.ID)
			require.NoError(t, err)
			authorization, err := url.Parse(start.AuthURL)
			require.NoError(t, err)
			_, err = svc.CompleteOwnedAccountReauthorization(ctx, user.ID, account.ID, start.SessionID, "code", authorization.Query().Get("state"), start.OnboardingState)
			persisted, readErr := accounts.GetByID(ctx, account.ID)
			require.NoError(t, readErr)
			if scenario == "wrong_identity" {
				require.ErrorIs(t, err, service.ErrOnboardingReauthorizationIdentity)
				require.Equal(t, "old-access", persisted.GetCredential("access_token"))
				require.Equal(t, service.StatusError, persisted.Status)
				require.Equal(t, "expired authorization", persisted.ErrorMessage)
				require.Zero(t, cache.called)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, cache.called)
				require.Equal(t, "new-access", persisted.GetCredential("access_token"))
				require.Equal(t, "new-refresh", persisted.GetCredential("refresh_token"))
				require.Empty(t, persisted.ErrorMessage)
				expectedStatus := service.StatusActive
				if scenario == "admin_disabled" {
					expectedStatus = service.StatusDisabled
				}
				require.Equal(t, expectedStatus, persisted.Status)
			}
			require.Equal(t, account.Name, persisted.Name)
			require.Equal(t, 7, persisted.Concurrency)
			require.Equal(t, map[string]any{"source": "target"}, persisted.Credentials["model_mapping"])
			require.Equal(t, true, persisted.Extra["codex_cli_only"])
			remaining, err := subscriptions.ListByUserID(ctx, user.ID)
			require.NoError(t, err)
			require.Len(t, remaining, 1)
			require.Equal(t, sub.ID, remaining[0].ID)
			require.Equal(t, service.SubscriptionStatusActive, remaining[0].Status)
			require.Equal(t, sub.ExpiresAt.Unix(), remaining[0].ExpiresAt.Unix())
			require.Equal(t, float64(2), remaining[0].DailyUsageUSD)
			require.Equal(t, float64(8), remaining[0].WeeklyUsageUSD)
			require.Equal(t, float64(16), remaining[0].MonthlyUsageUSD)
		})
	}
}
