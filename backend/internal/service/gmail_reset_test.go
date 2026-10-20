//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestGmailResetConfirmedMailCoalescesAcrossSourcesAndRestarts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cfg := defaultGmailResetConfig()
	state := servicePoolResetState{}
	first := gmailResetMailRecord{Key: "first", Kind: "direct_reset", Confidence: 0.99, ReceivedAt: now, Sender: cfg.Senders[0]}
	acceptGmailResetMail(&state, &first, cfg, now)
	require.Equal(t, "pending", first.Outcome)
	require.Nil(t, readyGmailReset(&state, cfg, now.Add(14*time.Minute), false))
	second := gmailResetMailRecord{Key: "second", Kind: "direct_reset", Confidence: 0.98, ReceivedAt: now.Add(10 * time.Minute), Sender: cfg.Senders[1]}
	acceptGmailResetMail(&state, &second, cfg, now.Add(10*time.Minute))
	require.Equal(t, "merged", second.Outcome)
	require.Nil(t, readyGmailReset(&state, cfg, now.Add(16*time.Minute), true))
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	var restarted servicePoolResetState
	require.NoError(t, json.Unmarshal(raw, &restarted))
	decision := readyGmailReset(&restarted, cfg, now.Add(16*time.Minute), false)
	require.NotNil(t, decision)
	require.Equal(t, "gmail:first", decision.Key)
	require.Equal(t, 2, decision.Observed)
	restarted.Gmail.LastEventReceived = now
	restarted.Gmail.CooldownUntil = now.Add(6 * time.Hour)
	restarted.Gmail.Pending = nil
	late := gmailResetMailRecord{Key: "late", Kind: "direct_reset", Confidence: 1, ReceivedAt: now.Add(29 * time.Minute)}
	acceptGmailResetMail(&restarted, &late, cfg, now.Add(7*time.Hour))
	require.Equal(t, "merged", late.Outcome)
	late.ReceivedAt = now.Add(3 * time.Hour)
	acceptGmailResetMail(&restarted, &late, cfg, now.Add(3*time.Hour))
	require.Equal(t, "cooldown", late.Outcome)
	late.ReceivedAt = now.Add(3 * time.Hour)
	acceptGmailResetMail(&restarted, &late, cfg, now.Add(7*time.Hour))
	require.Equal(t, "cooldown", late.Outcome)
	late.ReceivedAt = now.Add(7 * time.Hour)
	acceptGmailResetMail(&restarted, &late, cfg, now.Add(7*time.Hour))
	require.Equal(t, "pending", late.Outcome)
}

func TestGmailResetDoesNotAcceptForecastOrLowConfidence(t *testing.T) {
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
			cfg := defaultGmailResetConfig()
			cfg.BankedResetEnabled = scenario.banked
			state := servicePoolResetState{}
			record := gmailResetMailRecord{Kind: scenario.kind, Confidence: scenario.confidence, ReceivedAt: time.Now()}
			acceptGmailResetMail(&state, &record, cfg, time.Now())
			require.Equal(t, scenario.outcome, record.Outcome)
		})
	}
}

func TestGmailResetExpiredPendingDoesNotReplay(t *testing.T) {
	now := time.Now()
	state := servicePoolResetState{Gmail: gmailResetMonitorState{Pending: &gmailResetPending{ReceivedAt: now.Add(-48 * time.Hour), ApplyAt: now.Add(-47 * time.Hour), Kind: "direct_reset"}}}
	require.Nil(t, readyGmailReset(&state, defaultGmailResetConfig(), now, false))
	require.Nil(t, state.Gmail.Pending)
}

func TestGmailResetValidatesSenderAndAuthentication(t *testing.T) {
	for _, sender := range defaultGmailResetConfig().Senders {
		var part gmailResetPart
		domain := strings.SplitN(sender, "@", 2)[1]
		require.NoError(t, json.Unmarshal([]byte(`{"headers":[{"name":"From","value":"Codex <`+sender+`>"},{"name":"Authentication-Results","value":"mx.google.com; dkim=pass; dmarc=pass (p=NONE) header.from=`+domain+`"}]}`), &part))
		address, allowed := gmailResetSenderAllowed(part, defaultGmailResetConfig())
		require.True(t, allowed)
		require.Equal(t, sender, address)
		part.Headers[1].Value = "attacker.example; dmarc=pass"
		_, allowed = gmailResetSenderAllowed(part, defaultGmailResetConfig())
		require.False(t, allowed)
		part.Headers[1].Value = "mx.google.com; dmarc=fail"
		_, allowed = gmailResetSenderAllowed(part, defaultGmailResetConfig())
		require.False(t, allowed)
	}
}

func TestGmailResetExtractsBodyWithoutScriptsOrAttachments(t *testing.T) {
	part := gmailResetPart{MIMEType: "text/html"}
	part.Body.Data = base64.RawURLEncoding.EncodeToString([]byte(`<html><head><title>ignore</title></head><body><script>evil()</script><p>The limits have been reset.</p></body></html>`))
	require.Equal(t, "The limits have been reset.", gmailResetBody(part, 0))
	part.Filename = "attachment.html"
	require.Empty(t, gmailResetBody(part, 0))
}

func TestGmailResetModelMustQuoteEvidenceAndReturnCompleteJSON(t *testing.T) {
	record := gmailResetMailRecord{Body: "The limits have been reset. All propagated."}
	_, err := parseGmailResetAnalysis(`{"kind":"direct_reset","confidence":0.99,"evidence":"The limits have been reset."}`, record)
	require.NoError(t, err)
	for _, output := range []string{
		`{"kind":"direct_reset","confidence":0.99,"evidence":"invented evidence"}`,
		`{"kind":"direct_reset","confidence":2,"evidence":"The limits have been reset."}`,
		`{"kind":"execute","confidence":1}`,
		`{"kind":"direct_reset"`,
	} {
		_, err := parseGmailResetAnalysis(output, record)
		require.Error(t, err)
	}
}

func TestGmailResetModelProtocols(t *testing.T) {
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
			cfg := defaultGmailResetConfig()
			cfg.ModelBaseURL, cfg.Model, cfg.APIFormat = server.URL+"/v1", "test-model", format
			analysis, err := gmailResetAnalyze(context.Background(), server.Client(), cfg, "model-secret", gmailResetMailRecord{Body: "99% probability alert"})
			require.NoError(t, err)
			require.Equal(t, "forecast", analysis.Kind)
		})
	}
}

func TestGmailResetQueryNeverBackfillsBeforeConnection(t *testing.T) {
	now := time.Now().UTC()
	stored := &gmailResetStored{Config: defaultGmailResetConfig(), Since: now.Add(-time.Hour)}
	query := gmailResetQuery(stored, now)
	require.Contains(t, query, "from:notification@willcodexreset.com")
	require.Contains(t, query, "from:alerts@codex-resets.com")
	require.Contains(t, query, "after:"+strconv.FormatInt(stored.Since.Unix(), 10))
}

func TestGmailResetOAuthUsesReadonlyPKCEAndPersistsSession(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
	t.Cleanup(func() { _ = client.Close() })
	svc := NewOnboardingService(client, nil, nil, nil, nil, onboardingTestEncryptor{}, nil)
	cfg := defaultGmailResetConfig()
	cfg.RedirectURI = "https://example.com/admin/service-center"
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	mock.ExpectQuery("SELECT config,credentials_encrypted").WillReturnRows(sqlmock.NewRows([]string{"config", "credentials_encrypted", "refresh", "key", "email", "since", "version"}).AddRow(raw, `{"web":{"client_id":"client-id","client_secret":"private-secret","redirect_uris":["https://example.com/admin/service-center"]}}`, "", "", "", time.Now(), 1))
	mock.ExpectExec("UPDATE service_pool_reset_gmail SET oauth_state_hash").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7), int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	link, err := svc.StartGmailResetOAuth(context.Background(), 7)
	require.NoError(t, err)
	parsed, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, gmailResetScope, parsed.Query().Get("scope"))
	require.Equal(t, "offline", parsed.Query().Get("access_type"))
	require.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	require.True(t, strings.HasPrefix(parsed.Query().Get("state"), "gmail-reset."))
	require.NotContains(t, link, "private-secret")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGmailResetDuplicateMessageIsPersistentlyRecognized(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
	t.Cleanup(func() { _ = client.Close() })
	svc := NewServicePoolResetService(NewOnboardingService(client, nil, nil, nil, nil, nil, nil))
	mock.ExpectQuery("SELECT attempts,outcome FROM service_pool_reset_mail").WithArgs("message-key").WillReturnRows(sqlmock.NewRows([]string{"attempts", "outcome"}).AddRow(1, "applied"))
	attempts, seen, err := svc.gmailResetProgress(context.Background(), "message-key")
	require.NoError(t, err)
	require.Equal(t, 1, attempts)
	require.True(t, seen)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGmailResetFailedAnalysisRetriesAcrossRestarts(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
	t.Cleanup(func() { _ = client.Close() })
	for _, scenario := range []struct {
		attempts int
		outcome  string
		seen     bool
	}{{1, "analysis_retry", false}, {2, "analysis_retry", false}, {3, "analysis_failed", true}} {
		svc := NewServicePoolResetService(NewOnboardingService(client, nil, nil, nil, nil, nil, nil))
		mock.ExpectQuery("SELECT attempts,outcome FROM service_pool_reset_mail").WithArgs("failed-key").WillReturnRows(sqlmock.NewRows([]string{"attempts", "outcome"}).AddRow(scenario.attempts, scenario.outcome))
		attempts, seen, err := svc.gmailResetProgress(context.Background(), "failed-key")
		require.NoError(t, err)
		require.Equal(t, scenario.attempts, attempts)
		require.Equal(t, scenario.seen, seen)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGmailResetSeparatePendingCampaignReplacesOlderBacklog(t *testing.T) {
	now := time.Now().UTC()
	cfg := defaultGmailResetConfig()
	state := servicePoolResetState{}
	first := gmailResetMailRecord{Key: "old", Kind: "direct_reset", Confidence: 1, ReceivedAt: now.Add(-4 * time.Hour)}
	acceptGmailResetMail(&state, &first, cfg, now)
	newer := gmailResetMailRecord{Key: "new", Kind: "banked_reset", Confidence: 1, ReceivedAt: now}
	acceptGmailResetMail(&state, &newer, cfg, now)
	require.Equal(t, "pending", newer.Outcome)
	require.Equal(t, "new", state.Gmail.Pending.Key)
	require.Equal(t, 1, state.Gmail.Pending.Count)
	acceptGmailResetMail(&state, &first, cfg, now)
	require.Equal(t, "superseded", first.Outcome)
	require.Equal(t, "new", state.Gmail.Pending.Key)
}

func TestGmailResetConfigRejectsUnsafeRetryAndRetentionRanges(t *testing.T) {
	cfg := defaultGmailResetConfig()
	require.NoError(t, validateGmailResetConfig(cfg))
	cfg.MaxModelAttempts = 0
	require.Error(t, validateGmailResetConfig(cfg))
	cfg.MaxModelAttempts = 3
	cfg.RetentionDays = 6
	require.Error(t, validateGmailResetConfig(cfg))
}

func TestGmailResetSecretsAreRedactedFromAudit(t *testing.T) {
	redacted := RedactAuditBody([]byte(`{"credentials_json":"private-client-secret","model_api_key":"private-model-key"}`), "application/json")
	require.NotContains(t, redacted, "private-client-secret")
	require.NotContains(t, redacted, "private-model-key")
}
