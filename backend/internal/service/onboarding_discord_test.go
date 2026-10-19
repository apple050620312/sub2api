//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

type onboardingTestEncryptor struct{}

func (onboardingTestEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (onboardingTestEncryptor) Decrypt(value string) (string, error) { return value, nil }

type onboardingDiscordTransport func(*http.Request) (*http.Response, error)

func (transport onboardingDiscordTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestOnboardingAdminBindsOnlyUnclaimedGuildMembers(t *testing.T) {
	for _, scenario := range []string{"existing_member", "already_bound", "bot", "missing_member"} {
		t.Run(scenario, func(t *testing.T) {
			database, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
			t.Cleanup(func() { _ = client.Close() })
			svc := NewOnboardingService(client, &onboardingTestAdmin{}, nil, nil, nil, onboardingTestEncryptor{}, nil)
			payload, err := json.Marshal(onboardingStoredConfig{OnboardingConfig: OnboardingConfig{Enabled: true, Discord: OnboardingDiscordConfig{GuildID: "guild", RoleID: "role"}}, DiscordBotTokenEncrypted: "test-token"})
			require.NoError(t, err)
			mock.ExpectQuery("SELECT config FROM onboarding_config").WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow(payload))
			roles := 0
			svc.httpClient = &http.Client{Transport: onboardingDiscordTransport(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, "Bot test-token", request.Header.Get("Authorization"))
				status, body := http.StatusOK, `{"user":{"id":"12345","username":"discord-user"}}`
				if request.Method == http.MethodPut {
					roles++
					status, body = http.StatusNoContent, ""
					require.Equal(t, "/api/guilds/guild/members/12345/roles/role", request.URL.Path)
				}
				if scenario == "bot" {
					body = `{"user":{"id":"12345","bot":true}}`
				}
				if scenario == "missing_member" {
					status, body = http.StatusNotFound, `{"token":"must-not-leak"}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			columns := []string{"user_id", "discord_user_id", "discord_username", "guild_id", "role_id", "status", "last_checked_at", "created_at", "updated_at"}
			now := time.Now()
			if scenario == "existing_member" || scenario == "already_bound" {
				rows := sqlmock.NewRows(columns)
				if scenario == "already_bound" {
					rows.AddRow(8, "12345", "other", "guild", "role", "active", now, now, now)
				}
				mock.ExpectQuery("SELECT user_id, discord_user_id").WillReturnRows(rows)
			}
			if scenario == "existing_member" {
				mock.ExpectExec("INSERT INTO onboarding_discord_bindings").WithArgs(int64(7), "12345", "discord-user", "guild", "role").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT user_id, discord_user_id").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows(columns).AddRow(7, "12345", "discord-user", "guild", "role", "active", now, now, now))
			}
			binding, err := svc.BindDiscordMember(context.Background(), 7, "12345")
			if scenario == "existing_member" {
				require.NoError(t, err)
				require.Equal(t, "discord-user", binding.DiscordUsername)
				require.Equal(t, 1, roles)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "must-not-leak")
				require.Zero(t, roles)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
