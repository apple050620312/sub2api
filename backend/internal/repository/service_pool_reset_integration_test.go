//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type poolResetIntegrationEncryptor struct{}

func (poolResetIntegrationEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (poolResetIntegrationEncryptor) Decrypt(value string) (string, error) { return value, nil }

type poolResetIntegrationTransport struct {
	messageID string
	received  time.Time
	analyses  atomic.Int32
}

func (transport *poolResetIntegrationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var payload any
	switch {
	case request.URL.Host == "discord.com" && request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/messages"):
		payload = []map[string]any{{"id": transport.messageID, "channel_id": "1561630415750561863", "timestamp": transport.received.Format(time.RFC3339Nano), "author": map[string]any{"id": "1001", "bot": true}, "embeds": []map[string]string{{"title": "Codex reset confirmed", "description": "The limits have been reset."}}}}
	case request.URL.Host == "discord.com" && strings.HasSuffix(request.URL.Path, "/channels/1561630415750561863"):
		payload = map[string]string{"guild_id": "12345"}
	case request.URL.Host == "127.0.0.1:8080":
		transport.analyses.Add(1)
		payload = map[string]any{"status": "completed", "output": []any{map[string]any{"content": []map[string]string{{"type": "output_text", "text": `{"kind":"direct_reset","confidence":0.99,"evidence":"The limits have been reset."}`}}}}}
	case request.URL.Host == "discord.com" && request.Method == http.MethodPost:
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: request}, nil
	default:
		return nil, fmt.Errorf("unexpected integration HTTP request: %s", request.URL.Host)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: request}, nil
}

func TestServicePoolResetPersistsOnceAcrossRestartAndConcurrentScans(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now().UTC().Truncate(time.Second)
	var originalConfig, originalState []byte
	var originalMonitor []byte
	var originalSubscriptions []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM user_subscriptions s`).Scan(&originalSubscriptions))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT config FROM onboarding_config WHERE id=1`).Scan(&originalConfig))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM service_pool_reset_state WHERE id=1`).Scan(&originalState))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(g) FROM service_pool_reset_monitor g WHERE id=1`).Scan(&originalMonitor))
	transport := &poolResetIntegrationTransport{messageID: strconv.FormatInt(time.Now().UnixNano(), 10), received: time.Now().UTC()}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	weekly := 100.0
	group := mustCreateGroup(t, client, &service.Group{Name: uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription, WeeklyLimitUSD: &weekly})
	sub, err := client.UserSubscription.Create().SetUserID(user.ID).SetGroupID(group.ID).SetStartsAt(now.Add(-24 * time.Hour)).SetExpiresAt(now.Add(24 * time.Hour)).SetDailyUsageUsd(5).SetWeeklyUsageUsd(30).SetMonthlyUsageUsd(60).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `UPDATE user_subscriptions s SET daily_usage_usd=b.daily_usage_usd,weekly_usage_usd=b.weekly_usage_usd,monthly_usage_usd=b.monthly_usage_usd,daily_window_start=b.daily_window_start,weekly_window_start=b.weekly_window_start,monthly_window_start=b.monthly_window_start,updated_at=b.updated_at FROM jsonb_to_recordset($1::jsonb) AS b(id BIGINT,daily_usage_usd NUMERIC,weekly_usage_usd NUMERIC,monthly_usage_usd NUMERIC,daily_window_start TIMESTAMPTZ,weekly_window_start TIMESTAMPTZ,monthly_window_start TIMESTAMPTZ,updated_at TIMESTAMPTZ) WHERE s.id=b.id`, originalSubscriptions)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM service_pool_reset_events WHERE notification_content='integration-reset'`)
		_, _ = integrationDB.ExecContext(ctx, `UPDATE onboarding_config SET config=$1::jsonb WHERE id=1`, originalConfig)
		_, _ = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET state=$1::jsonb,lease_until='-infinity',lease_token='',next_check_at='-infinity' WHERE id=1`, originalState)
		_, _ = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_monitor g SET config=b.config,owner_user_id=b.owner_user_id,monitor_since=b.monitor_since,version=b.version FROM jsonb_populate_record(NULL::service_pool_reset_monitor,$1::jsonb) b WHERE g.id=1`, originalMonitor)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM service_pool_reset_messages WHERE message_id=$1`, transport.messageID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM user_subscriptions WHERE id=$1`, sub.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	})
	cfg, err := json.Marshal(map[string]any{"pool_reset": service.ServicePoolResetConfig{Enabled: true, ChannelID: "123456", Message: "integration-reset"}, "discord": map[string]string{"guild_id": "12345"}, "discord_bot_token_encrypted": "integration-bot"})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE onboarding_config SET config=$1::jsonb WHERE id=1`, cfg)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET state='{}',lease_until='-infinity',next_check_at='-infinity' WHERE id=1`)
	require.NoError(t, err)
	apiKey, err := client.APIKey.Create().SetUserID(user.ID).SetName("reset-monitor").SetKey(uuid.NewString()).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM api_keys WHERE id=$1`, apiKey.ID) })
	monitorConfig, err := json.Marshal(service.ResetMonitorConfig{
		ChannelID: "1561630415750561863", PollSeconds: 300, PropagationMinutes: 30, CooldownMinutes: 720,
		BankedResetEnabled: true, ModelAPIKeyID: apiKey.ID, Model: "test-model", APIFormat: "responses",
	})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_monitor SET config=$1::jsonb,owner_user_id=$2,monitor_since=$3,version=version+1 WHERE id=1`, monitorConfig, user.ID, transport.received.Add(-time.Hour))
	require.NoError(t, err)
	subs := service.NewSubscriptionService(NewGroupRepository(client, integrationDB), NewUserSubscriptionRepository(client), nil, client, nil)
	t.Cleanup(subs.Stop)
	onboarding := service.NewServiceCenterService(client, nil, nil, subs, nil, poolResetIntegrationEncryptor{}, nil)
	observer := service.NewServicePoolResetService(onboarding)
	run := func() {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET lease_until='-infinity',next_check_at='-infinity' WHERE id=1`)
		require.NoError(t, err)
		require.NoError(t, observer.RunOnce(ctx))
	}
	run()
	run()
	unchanged, err := client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, 30.0, unchanged.WeeklyUsageUsd)
	observer = service.NewServicePoolResetService(onboarding)
	_, err = integrationDB.ExecContext(ctx, `UPDATE service_pool_reset_state SET lease_until='-infinity',next_check_at='-infinity',state=jsonb_set(state,'{monitor,pending,apply_at}',to_jsonb(NOW()-INTERVAL '1 minute')) WHERE id=1`)
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
	require.Equal(t, int32(1), transport.analyses.Load(), "persisted messages must not be analyzed again after restart")
	var outcome string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT outcome FROM service_pool_reset_messages WHERE message_id=$1`, transport.messageID).Scan(&outcome))
	require.Equal(t, "applied", outcome)
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT notification_state FROM service_pool_reset_events WHERE notification_content='integration-reset'`).Scan(&state))
	require.Equal(t, "failed", state, "a denied test notification must remain failed without repeating the reset")
}

func TestResetMonitorConfigMigrationPreservesOwnershipAndTiming(t *testing.T) {
	ctx := context.Background()
	migration, err := dbmigrations.FS.ReadFile("250_simplify_reset_monitor.sql")
	require.NoError(t, err)
	for _, scenario := range []struct {
		name   string
		before string
		after  string
	}{
		{"old_defaults", `{"channel_id":"12345","author_ids":["1001"],"poll_seconds":5,"propagation_minutes":15,"cooldown_minutes":360,"banked_reset_enabled":false,"model_api_key_id":42,"model":"custom-model","api_format":"chat_completions","prompt":"Custom rule","bots_only":true,"require_embeds":true,"aggregation_minutes":120,"max_age_hours":24,"batch_size":20,"max_model_attempts":3,"retention_days":30,"direct_reset_enabled":true,"min_confidence":0.95,"model_timeout_seconds":60,"max_output_tokens":2048,"max_body_chars":24000}`,
			`{"channel_id":"12345","author_ids":["1001"],"poll_seconds":300,"propagation_minutes":30,"cooldown_minutes":720,"banked_reset_enabled":false,"model_api_key_id":42,"model":"custom-model","api_format":"chat_completions","prompt":"Custom rule"}`},
		{"custom_timing", `{"poll_seconds":900,"propagation_minutes":45,"cooldown_minutes":4320}`, `{"poll_seconds":900,"propagation_minutes":45,"cooldown_minutes":4320}`},
		{"short_timing", `{"poll_seconds":10,"propagation_minutes":0,"cooldown_minutes":60}`, `{"poll_seconds":60,"propagation_minutes":0,"cooldown_minutes":120}`},
		{"unconfigured", `{}`, `{}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			tx, err := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE service_pool_reset_monitor (LIKE public.service_pool_reset_monitor INCLUDING DEFAULTS) ON COMMIT DROP`)
			require.NoError(t, err)
			since := time.Now().UTC().Truncate(time.Second)
			_, err = tx.ExecContext(ctx, `INSERT INTO service_pool_reset_monitor (id,config,owner_user_id,monitor_since,version) VALUES (1,$1::jsonb,42,$2,7)`, scenario.before, since)
			require.NoError(t, err)
			for range 2 {
				_, err = tx.ExecContext(ctx, string(migration))
				require.NoError(t, err)
				var config string
				var owner, version int64
				var monitorSince time.Time
				require.NoError(t, tx.QueryRowContext(ctx, `SELECT config,owner_user_id,monitor_since,version FROM service_pool_reset_monitor WHERE id=1`).Scan(&config, &owner, &monitorSince, &version))
				require.JSONEq(t, scenario.after, config)
				require.Equal(t, int64(42), owner)
				require.Equal(t, int64(7), version)
				require.True(t, since.Equal(monitorSince))
			}
		})
	}
}
