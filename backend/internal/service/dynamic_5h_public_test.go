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
func (r *dynamic5hPublicAccountRepository) ListActive(context.Context) ([]Account, error) { return r.accounts, r.err }

func TestDynamic5hPublicPoolDoesNotExposePrivateAccountData(t *testing.T) {
	svc, ctx := newDynamic5hTestService(t, time.Now().UTC())
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressureNormal, 0)
	svc.accountRepo = &dynamic5hPublicAccountRepository{accounts: []Account{
		{ID: 1, Name: "公开账号", Platform: PlatformOpenAI, Credentials: map[string]any{"email": "private@example.com", "access_token": "secret-token"}, Extra: map[string]any{"email": "private@example.com", "codex_5h_used_percent": 40}},
		{ID: 2, Name: "owner@example.com", Platform: PlatformAnthropic},
		{ID: 3, Name: "停用账号", Platform: "unsupported"},
	}}
	status, err := svc.PublicUserStatus(ctx, 7)
	require.NoError(t, err)
	require.Len(t, status.Pool.Accounts, 3)
	require.Equal(t, "账号 #2", status.Pool.Accounts[1].Name)
	require.Equal(t, float64(40), *status.Pool.Accounts[0].FiveHourUsed)
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	for _, private := range []string{"private@example.com", "owner@example.com", "secret-token", "credentials", "extra", "pool_capacity", "audit_logs", "actor_email"} {
		require.NotContains(t, string(encoded), private)
	}
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
