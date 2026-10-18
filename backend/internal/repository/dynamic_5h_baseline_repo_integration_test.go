//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDynamic5hPlusBaselineSamplesRealAccountWindows(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	_, err := integrationDB.ExecContext(ctx, "TRUNCATE dynamic_5h_plus_baseline, dynamic_5h_plus_samples")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "TRUNCATE dynamic_5h_plus_baseline, dynamic_5h_plus_samples")
	})
	repo := &dynamic5hPolicyRepository{db: integrationDB}
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-" + uuid.NewString(), Name: "baseline"})
	account := mustCreateAccount(t, client, &service.Account{Name: "baseline-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(ctx, "DELETE FROM usage_logs WHERE account_id=$1", account.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id=$1", key.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", account.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
		require.NoError(t, cleanupErr)
	})
	usageRepo := NewUsageLogRepository(client, integrationDB)
	origin := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	for index, capacity := range []float64{100, 120, 110} {
		start := origin.Add(time.Duration(index) * 6 * time.Hour)
		end := start.Add(4 * time.Hour)
		reset := start.Add(5 * time.Hour)
		baseline, samples, err := repo.ObservePlusBaseline(ctx, []service.Dynamic5hPlusObservation{{AccountID: account.ID, ResetAt: reset, ObservedAt: start, UsedPercent: 2}}, start)
		require.NoError(t, err)
		require.Zero(t, baseline)
		require.Equal(t, index, samples)
		amount := capacity * 0.94
		_, err = usageRepo.Create(ctx, &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: uuid.NewString(), Model: "sample", TotalCost: 999, AccountStatsCost: &amount, CreatedAt: start.Add(time.Hour)})
		require.NoError(t, err)
		baseline, _, err = repo.ObservePlusBaseline(ctx, []service.Dynamic5hPlusObservation{{AccountID: account.ID, ResetAt: reset, ObservedAt: end, UsedPercent: 96}}, end)
		require.NoError(t, err)
		require.Zero(t, baseline, "allow asynchronous logs to settle before sampling")
		baseline, samples, err = repo.ObservePlusBaseline(ctx, nil, end.Add(3*time.Minute))
		require.NoError(t, err)
		require.Equal(t, index+1, samples)
		if index < 2 {
			require.Zero(t, baseline)
		} else {
			require.InDelta(t, 110, baseline, 1e-6)
		}
	}
	baseline, samples, err := repo.ObservePlusBaseline(ctx, []service.Dynamic5hPlusObservation{{AccountID: account.ID, ResetAt: origin.Add(30 * time.Hour), ObservedAt: origin.Add(25 * time.Hour), UsedPercent: 0}}, origin.Add(25*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 3, samples)
	require.InDelta(t, 110, baseline, 1e-6, "new traffic cannot shrink the persisted reference")
}
