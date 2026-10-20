//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestResetMonitorConfirmedMailCoalescesAcrossSourcesAndRestarts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cfg := defaultResetMonitorConfig()
	state := servicePoolResetState{}
	first := resetMonitorMessageRecord{Key: "first", Kind: "direct_reset", Confidence: 0.99, ReceivedAt: now, Sender: "1001"}
	acceptResetMessage(&state, &first, cfg, now)
	require.Equal(t, "pending", first.Outcome)
	require.Nil(t, readyResetMonitor(&state, cfg, now.Add(14*time.Minute), false))
	second := resetMonitorMessageRecord{Key: "second", Kind: "direct_reset", Confidence: 0.98, ReceivedAt: now.Add(10 * time.Minute), Sender: "1002"}
	acceptResetMessage(&state, &second, cfg, now.Add(10*time.Minute))
	require.Equal(t, "merged", second.Outcome)
	require.Nil(t, readyResetMonitor(&state, cfg, now.Add(16*time.Minute), true))
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	var restarted servicePoolResetState
	require.NoError(t, json.Unmarshal(raw, &restarted))
	decision := readyResetMonitor(&restarted, cfg, now.Add(16*time.Minute), false)
	require.NotNil(t, decision)
	require.Equal(t, "discord:first", decision.Key)
	require.Equal(t, 2, decision.Observed)
	restarted.Monitor.LastEventReceived = now
	restarted.Monitor.CooldownUntil = now.Add(6 * time.Hour)
	restarted.Monitor.Pending = nil
	late := resetMonitorMessageRecord{Key: "late", Kind: "direct_reset", Confidence: 1, ReceivedAt: now.Add(29 * time.Minute)}
	acceptResetMessage(&restarted, &late, cfg, now.Add(7*time.Hour))
	require.Equal(t, "merged", late.Outcome)
	late.ReceivedAt = now.Add(3 * time.Hour)
	acceptResetMessage(&restarted, &late, cfg, now.Add(3*time.Hour))
	require.Equal(t, "cooldown", late.Outcome)
	late.ReceivedAt = now.Add(3 * time.Hour)
	acceptResetMessage(&restarted, &late, cfg, now.Add(7*time.Hour))
	require.Equal(t, "cooldown", late.Outcome)
	late.ReceivedAt = now.Add(7 * time.Hour)
	acceptResetMessage(&restarted, &late, cfg, now.Add(7*time.Hour))
	require.Equal(t, "pending", late.Outcome)
}

func TestResetMonitorDoesNotAcceptForecastOrLowConfidence(t *testing.T) {
	for _, scenario := range []struct {
		kind       string
		confidence float64
		banked     bool
		outcome    string
	}{
		{"forecast", 1, true, "ignored"}, {"unknown", 1, true, "ignored"},
		{"direct_reset", 0.94, true, "ignored"}, {"banked_reset", 0.99, false, "kind_disabled"},
		{"banked_reset", 0.99, true, "pending"},
	} {
		t.Run(scenario.kind+scenario.outcome, func(t *testing.T) {
			cfg := defaultResetMonitorConfig()
			cfg.BankedResetEnabled = scenario.banked
			state := servicePoolResetState{}
			record := resetMonitorMessageRecord{Kind: scenario.kind, Confidence: scenario.confidence, ReceivedAt: time.Now()}
			acceptResetMessage(&state, &record, cfg, time.Now())
			require.Equal(t, scenario.outcome, record.Outcome)
		})
	}
}

func TestResetMonitorExpiredPendingDoesNotReplay(t *testing.T) {
	now := time.Now()
	state := servicePoolResetState{Monitor: resetMonitorState{Pending: &resetMonitorPending{ReceivedAt: now.Add(-48 * time.Hour), ApplyAt: now.Add(-47 * time.Hour), Kind: "direct_reset"}}}
	require.Nil(t, readyResetMonitor(&state, defaultResetMonitorConfig(), now, false))
	require.Nil(t, state.Monitor.Pending)
}

func TestResetMonitorModelMustQuoteEvidenceAndReturnCompleteJSON(t *testing.T) {
	record := resetMonitorMessageRecord{Body: "The limits have been reset. All propagated."}
	_, err := parseResetMonitorAnalysis(`{"kind":"direct_reset","confidence":0.99,"evidence":"The limits have been reset."}`, record)
	require.NoError(t, err)
	for _, output := range []string{
		`{"kind":"direct_reset","confidence":0.99,"evidence":"invented evidence"}`,
		`{"kind":"direct_reset","confidence":2,"evidence":"The limits have been reset."}`,
		`{"kind":"execute","confidence":1}`,
		`{"kind":"direct_reset"`,
	} {
		_, err := parseResetMonitorAnalysis(output, record)
		require.Error(t, err)
	}
}

func TestResetMonitorModelProtocols(t *testing.T) {
	for _, format := range []string{"chat_completions", "responses"} {
		t.Run(format, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				require.Equal(t, "Bearer model-secret", request.Header.Get("Authorization"))
				var payload map[string]any
				require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
				require.Equal(t, "test-model", payload["model"])
				if format == "responses" {
					require.Equal(t, "/v1/responses", request.URL.Path)
					_, _ = io.WriteString(writer, `{"status":"completed","output":[{"content":[{"type":"output_text","text":"{\"kind\":\"forecast\",\"confidence\":0.99,\"evidence\":\"99% probability alert\"}"}]}]}`)
				} else {
					require.Equal(t, "/v1/chat/completions", request.URL.Path)
					_, _ = io.WriteString(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"kind\":\"forecast\",\"confidence\":0.99,\"evidence\":\"99% probability alert\"}"}}]}`)
				}
			}))
			defer server.Close()
			cfg := defaultResetMonitorConfig()
			cfg.Model, cfg.APIFormat = "test-model", format
			analysis, err := resetMonitorAnalyze(context.Background(), server.Client(), cfg, server.URL+"/v1", "model-secret", resetMonitorMessageRecord{Body: "99% probability alert"})
			require.NoError(t, err)
			require.Equal(t, "forecast", analysis.Kind)
		})
	}
}

func TestResetMonitorDuplicateMessageIsPersistentlyRecognized(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
	t.Cleanup(func() { _ = client.Close() })
	svc := NewServicePoolResetService(NewServiceCenterService(client, nil, nil, nil, nil, nil, nil))
	mock.ExpectQuery("SELECT attempts,outcome FROM service_pool_reset_messages").WithArgs("message-key").WillReturnRows(sqlmock.NewRows([]string{"attempts", "outcome"}).AddRow(1, "applied"))
	attempts, seen, err := svc.resetMonitorProgress(context.Background(), "message-key")
	require.NoError(t, err)
	require.Equal(t, 1, attempts)
	require.True(t, seen)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResetMonitorFailedAnalysisRetriesAcrossRestarts(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
	t.Cleanup(func() { _ = client.Close() })
	for _, scenario := range []struct {
		attempts int
		outcome  string
		seen     bool
	}{{1, "analysis_retry", false}, {2, "analysis_retry", false}, {3, "analysis_failed", true}} {
		svc := NewServicePoolResetService(NewServiceCenterService(client, nil, nil, nil, nil, nil, nil))
		mock.ExpectQuery("SELECT attempts,outcome FROM service_pool_reset_messages").WithArgs("failed-key").WillReturnRows(sqlmock.NewRows([]string{"attempts", "outcome"}).AddRow(scenario.attempts, scenario.outcome))
		attempts, seen, err := svc.resetMonitorProgress(context.Background(), "failed-key")
		require.NoError(t, err)
		require.Equal(t, scenario.attempts, attempts)
		require.Equal(t, scenario.seen, seen)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResetMonitorSeparatePendingCampaignReplacesOlderBacklog(t *testing.T) {
	now := time.Now().UTC()
	cfg := defaultResetMonitorConfig()
	state := servicePoolResetState{}
	first := resetMonitorMessageRecord{Key: "old", Kind: "direct_reset", Confidence: 1, ReceivedAt: now.Add(-4 * time.Hour)}
	acceptResetMessage(&state, &first, cfg, now)
	newer := resetMonitorMessageRecord{Key: "new", Kind: "banked_reset", Confidence: 1, ReceivedAt: now}
	acceptResetMessage(&state, &newer, cfg, now)
	require.Equal(t, "pending", newer.Outcome)
	require.Equal(t, "new", state.Monitor.Pending.Key)
	require.Equal(t, 1, state.Monitor.Pending.Count)
	acceptResetMessage(&state, &first, cfg, now)
	require.Equal(t, "superseded", first.Outcome)
	require.Equal(t, "new", state.Monitor.Pending.Key)
}

func TestResetMonitorConfigRejectsUnsafeRetryAndRetentionRanges(t *testing.T) {
	cfg := defaultResetMonitorConfig()
	require.NoError(t, validateResetMonitorConfig(cfg))
	cfg.MaxModelAttempts = 0
	require.Error(t, validateResetMonitorConfig(cfg))
	cfg.MaxModelAttempts = 3
	cfg.RetentionDays = 6
	require.Error(t, validateResetMonitorConfig(cfg))
}

func TestResetMonitorExtractsEmbedFieldsAndRechecksChangedContent(t *testing.T) {
	var message resetMonitorMessage
	require.NoError(t, json.Unmarshal([]byte(`{"id":"1234","channel_id":"1561630415750561863","timestamp":"2026-10-20T00:00:00Z","author":{"id":"1001","bot":true},"embeds":[{"title":"Codex reset confirmed","description":"The limits have been reset.","fields":[{"name":"Details","value":"All accounts have new limits."}]}]}`), &message))
	cfg := defaultResetMonitorConfig()
	record, allowed := resetMonitorRecord(message, cfg)
	require.True(t, allowed)
	require.Contains(t, record.Body, "The limits have been reset.")
	require.Contains(t, record.Body, "All accounts have new limits.")
	unchanged, allowed := resetMonitorRecord(message, cfg)
	require.True(t, allowed)
	require.Equal(t, record.Key, unchanged.Key)
	message.Embeds[0].Description = "A new reset credit has been issued."
	edited, allowed := resetMonitorRecord(message, cfg)
	require.True(t, allowed)
	require.NotEqual(t, record.Key, edited.Key)
	message.Embeds = nil
	_, allowed = resetMonitorRecord(message, cfg)
	require.False(t, allowed, "an empty placeholder must remain eligible for a later embed update")
}

func TestResetMonitorRestrictsMessageAuthors(t *testing.T) {
	cfg := defaultResetMonitorConfig()
	cfg.RequireEmbeds = false
	message := resetMonitorMessage{Content: "The limits have been reset."}
	message.Author.ID = "1001"
	_, allowed := resetMonitorRecord(message, cfg)
	require.False(t, allowed)
	message.Author.Bot = true
	cfg.AuthorIDs = []string{"1002"}
	_, allowed = resetMonitorRecord(message, cfg)
	require.False(t, allowed)
	cfg.AuthorIDs = []string{"1001"}
	_, allowed = resetMonitorRecord(message, cfg)
	require.True(t, allowed)
	message.Author.Bot = false
	message.WebhookID = "1002"
	cfg.AuthorIDs = []string{"1002"}
	_, allowed = resetMonitorRecord(message, cfg)
	require.True(t, allowed)
}

func TestResetMonitorDiscordRateLimitAndRedirectDoNotLeakCredentials(t *testing.T) {
	destinationCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		destinationCalls++
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/limited" {
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(writer, `{"retry_after":120}`)
			return
		}
		http.Redirect(writer, request, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	client := resetMonitorHTTPClient(time.Second, false)
	var target any
	err := resetMonitorGet(context.Background(), client, "bot-secret", source.URL+"/limited", &target)
	var limited *resetMonitorRateLimitError
	require.ErrorAs(t, err, &limited)
	require.Equal(t, 120*time.Second, limited.RetryAfter)
	require.Error(t, resetMonitorGet(context.Background(), client, "bot-secret", source.URL+"/redirect", &target))
	require.Zero(t, destinationCalls)
}
