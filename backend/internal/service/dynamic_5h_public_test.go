//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dynamic5hPublicAccountRepository struct {
	AccountRepository
	accounts []Account
	err      error
}

func (r *dynamic5hPublicAccountRepository) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return r.accounts, r.err
}

func (r *dynamic5hPublicAccountRepository) ListActive(context.Context) ([]Account, error) {
	return r.accounts, r.err
}

func TestDynamic5hPublicPoolDoesNotExposePrivateAccountData(t *testing.T) {
	svc, ctx := newDynamic5hTestService(t, time.Now().UTC())
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressureNormal, 0)
	now := time.Now().UTC()
	svc.accountRepo = &dynamic5hPublicAccountRepository{accounts: []Account{
		{ID: 1, Name: "公开账号", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"email": "private@example.com", "access_token": "secret-token"}, Extra: map[string]any{"email": "private@example.com", "codex_5h_used_percent": 40, "codex_7d_used_percent": 20, "codex_5h_reset_at": now.Add(2 * time.Hour).Format(time.RFC3339), "codex_7d_reset_at": now.Add(2 * 24 * time.Hour).Format(time.RFC3339), "codex_usage_updated_at": now.Format(time.RFC3339)}},
		{ID: 2, Name: "第二账号", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Extra: map[string]any{"codex_5h_used_percent": 60, "codex_7d_used_percent": 40, "codex_5h_reset_at": now.Add(3 * time.Hour).Format(time.RFC3339), "codex_7d_reset_at": now.Add(3 * 24 * time.Hour).Format(time.RFC3339), "codex_usage_updated_at": now.Format(time.RFC3339)}},
		{ID: 3, Name: "不支持账号", Platform: "unsupported", Status: StatusActive, Schedulable: true},
	}}
	status, err := svc.PublicUserStatus(ctx, 7)
	require.NoError(t, err)
	require.NotNil(t, status.Pool)
	require.Equal(t, 50.0, *status.Pool.FiveHourUsedPercent)
	require.Equal(t, 30.0, *status.Pool.SevenDayUsedPercent)
	require.Equal(t, 2, status.Pool.FiveHourSampleCount)
	require.Equal(t, 2, status.Pool.SevenDaySampleCount)
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	for _, private := range []string{"private@example.com", "owner@example.com", "secret-token", "credentials", "extra", "pool_capacity", "audit_logs", "actor_email"} {
		require.NotContains(t, string(encoded), private)
	}
	require.NotContains(t, string(encoded), "公开账号")
}

func TestDynamic5hPublicPoolReportsAccountReadFailure(t *testing.T) {
	svc, ctx := newDynamic5hTestService(t, time.Now().UTC())
	svc.accountRepo = &dynamic5hPublicAccountRepository{err: errors.New("database unavailable")}
	_, err := svc.PublicUserStatus(ctx, 7)
	require.Error(t, err)
}

func TestDynamic5hAuditDoesNotExposeHistoricalReasons(t *testing.T) {
	encoded, err := json.Marshal(Dynamic5hAuditLog{Reason: "private historical reason", Action: "policy_update"})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "reason")
}
