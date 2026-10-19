//go:build unit

package service

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestDecodeDiscordJSONResponseMapsUpstreamFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantCode   int
		wantReason string
	}{
		{name: "permission failure", statusCode: http.StatusForbidden, body: `{"message":"Missing Permissions"}`, wantCode: http.StatusBadGateway, wantReason: "DISCORD_API_ERROR"},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, body: `{"retry_after":1}`, wantCode: http.StatusServiceUnavailable, wantReason: "DISCORD_API_UNAVAILABLE"},
		{name: "discord outage", statusCode: http.StatusBadGateway, body: `{"message":"upstream unavailable"}`, wantCode: http.StatusServiceUnavailable, wantReason: "DISCORD_API_UNAVAILABLE"},
		{name: "invalid success payload", statusCode: http.StatusOK, body: `{`, wantCode: http.StatusServiceUnavailable, wantReason: "DISCORD_API_INVALID_RESPONSE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &http.Response{StatusCode: test.statusCode, Body: io.NopCloser(strings.NewReader(test.body))}
			err := decodeDiscordJSONResponse(response, &struct{}{}, "测试")
			require.Error(t, err)
			require.Equal(t, test.wantCode, infraerrors.Code(err))
			require.Equal(t, test.wantReason, infraerrors.Reason(err))
		})
	}
}

func TestDiscordNetworkErrorDoesNotExposeUpstreamDetails(t *testing.T) {
	err := discordNetworkError("读取用户资料", errors.New("secret proxy address"))
	require.Equal(t, http.StatusServiceUnavailable, infraerrors.Code(err))
	require.Equal(t, "DISCORD_API_UNAVAILABLE", infraerrors.Reason(err))
	require.NotContains(t, infraerrors.Message(err), "secret proxy address")
}
