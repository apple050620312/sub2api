//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestOnboardingOpenAIUsesConfiguredOrNativeCallback(t *testing.T) {
	for _, callback := range []string{"", "https://example.com/oauth/callback"} {
		t.Run(callback, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			cfg := &config.Config{JWT: config.JWTConfig{Secret: "onboarding-unit-test-secret"}}
			admin := &onboardingTestAdmin{accounts: []Account{{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}}
			svc := NewOnboardingService(client, admin, NewOpenAIOAuthService(nil, nil), nil, cfg, nil)
			payload, err := json.Marshal(OnboardingConfig{Enabled: true, AccountTemplateID: 42, OpenAIRedirectURI: callback})
			require.NoError(t, err)
			mock.ExpectQuery("SELECT config FROM onboarding_config").WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow(payload)).RowsWillBeClosed()
			now := time.Now().UTC()
			mock.ExpectQuery("SELECT user_id, discord_user_id").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "discord_user_id", "discord_username", "guild_id", "role_id", "status", "last_checked_at", "created_at", "updated_at"}).AddRow(7, "discord-7", "tester", "guild", "role", "active", now, now, now)).RowsWillBeClosed()
			result, err := svc.StartOpenAI(context.Background(), 7, "https://untrusted.example/ignored")
			require.NoError(t, err)
			if callback == "" {
				callback = openai.DefaultRedirectURI
			}
			require.Equal(t, callback, result.RedirectURI)
			authorization, err := url.Parse(result.AuthURL)
			require.NoError(t, err)
			require.Equal(t, callback, authorization.Query().Get("redirect_uri"))
			require.NotEmpty(t, result.SessionID)
			require.NoError(t, svc.verifyState(result.OnboardingState, "openai", 7))
			require.Error(t, svc.verifyState(result.OnboardingState, "openai", 8))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
