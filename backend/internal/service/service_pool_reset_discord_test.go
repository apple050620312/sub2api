//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServicePoolResetDiscordPreservesMessageAndSuppressesMentions(t *testing.T) {
	onboarding := &OnboardingService{httpClient: &http.Client{Transport: onboardingDiscordTransport(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "/api/v10/channels/123456/messages", request.URL.Path)
		require.Equal(t, "Bot test-token", request.Header.Get("Authorization"))
		var payload struct {
			Content         string `json:"content"`
			Nonce           string `json:"nonce"`
			EnforceNonce    bool   `json:"enforce_nonce"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		require.Equal(t, "配额已恢复\n@everyone", payload.Content)
		require.Equal(t, "fixed-event-nonce", payload.Nonce)
		require.True(t, payload.EnforceNonce)
		require.Empty(t, payload.AllowedMentions.Parse)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"message-id"}`))}, nil
	})}}
	svc := &ServicePoolResetService{onboarding: onboarding}
	stored := &onboardingStoredConfig{OnboardingConfig: OnboardingConfig{Discord: OnboardingDiscordConfig{BotToken: "test-token"}}}
	state, id, code := svc.sendResetNotification(context.Background(), stored, "fixed-event-nonce", "123456", "配额已恢复\n@everyone")
	require.Equal(t, "sent", state)
	require.Equal(t, "message-id", id)
	require.Empty(t, code)
}

func TestServicePoolResetDiscordOnlyRetriesDefiniteRateLimit(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
		state  string
	}{
		{"rate_limited", 429, "pending"}, {"forbidden", 403, "failed"}, {"server_error", 502, "uncertain"}, {"transport_timeout", 0, "uncertain"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			onboarding := &OnboardingService{httpClient: &http.Client{Transport: onboardingDiscordTransport(func(*http.Request) (*http.Response, error) {
				if scenario.status == 0 {
					return nil, errors.New("timeout")
				}
				return &http.Response{StatusCode: scenario.status, Body: io.NopCloser(strings.NewReader(`{"token":"must-not-leak"}`))}, nil
			})}}
			svc := &ServicePoolResetService{onboarding: onboarding}
			stored := &onboardingStoredConfig{OnboardingConfig: OnboardingConfig{Discord: OnboardingDiscordConfig{BotToken: "test-token"}}}
			state, id, code := svc.sendResetNotification(context.Background(), stored, "nonce", "123456", "配额已恢复")
			require.Equal(t, scenario.state, state)
			require.Empty(t, id)
			require.NotContains(t, code, "must-not-leak")
		})
	}
}
