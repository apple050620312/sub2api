//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type dynamic5hMemoryCache struct {
	status    Dynamic5hPressureStatus
	found     bool
	lockToken string
}

func (c *dynamic5hMemoryCache) LoadStatus(context.Context) (Dynamic5hPressureStatus, bool, error) {
	return c.status, c.found, nil
}

func (c *dynamic5hMemoryCache) StoreStatus(_ context.Context, status Dynamic5hPressureStatus) error {
	c.status, c.found = status, true
	return nil
}

func (c *dynamic5hMemoryCache) AcquireRefreshLock(_ context.Context, token string, _ time.Duration) (bool, error) {
	if c.lockToken != "" {
		return false, nil
	}
	c.lockToken = token
	return true, nil
}

func (c *dynamic5hMemoryCache) ReleaseRefreshLock(_ context.Context, token string) error {
	if c.lockToken == token {
		c.lockToken = ""
	}
	return nil
}

func dynamic5hTestConfig() config.Dynamic5hPressureConfig {
	return config.Dynamic5hPressureConfig{Enabled: true, PeakThreshold: 0.8, NormalThreshold: 0.7, EWMAAlpha: 1}
}

func newDynamic5hTestService(t *testing.T, now time.Time) (*Dynamic5hPressureService, context.Context) {
	t.Helper()
	svc := NewDynamic5hPressureService(nil, &dynamic5hMemoryCache{}, &config.Config{Gateway: config.GatewayConfig{Dynamic5hPressure: dynamic5hTestConfig()}})
	svc.quotaRepo = &subscription5hTestRepository{quotas: map[int64]Subscription5hQuota{}}
	svc.now = func() time.Time { return now }
	return svc, context.Background()
}

func storeDynamic5hTestStatus(svc *Dynamic5hPressureService, ctx context.Context, state Dynamic5hPressureState, capacity float64) {
	svc.storeStatus(ctx, Dynamic5hPressureStatus{
		Enabled: true, DataAvailable: true,
		State: state, PeakActive: state == Dynamic5hPressurePeak,
		EvaluatedAt: svc.now().UTC(),
	})
}

func TestCalculateDynamic5hPressureAccountsWithDifferentResetTimes(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	soon, _, _, _ := calculateDynamic5hPressure([]dynamic5hAccountWindow{{used: 0.95, resetAt: now.Add(10 * time.Minute)}}, now)
	far, _, _, _ := calculateDynamic5hPressure([]dynamic5hAccountWindow{{used: 0.95, resetAt: now.Add(4 * time.Hour)}}, now)
	require.Less(t, soon, far, "the same remaining quota must be safer when reset is near")
	require.Less(t, soon, 1.1)
	require.Greater(t, far, 1.0)
}

func TestCalculateDynamic5hPressureCreditsCapacityAfterNearReset(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	pressure, remaining, _, _ := calculateDynamic5hPressure([]dynamic5hAccountWindow{{used: 1, resetAt: now.Add(30 * time.Minute)}}, now)
	require.Equal(t, 0.0, remaining, "the account is currently exhausted")
	require.Less(t, pressure, 1.2, "a full account resetting soon should not keep the pool at extreme pressure")
}

func TestNextDynamic5hPressureStateUsesHysteresis(t *testing.T) {
	cfg := dynamic5hTestConfig()
	require.Equal(t, Dynamic5hPressurePeak, nextDynamic5hPressureState(Dynamic5hPressureNormal, 0.85, true, cfg))
	require.Equal(t, Dynamic5hPressurePeak, nextDynamic5hPressureState(Dynamic5hPressurePeak, 0.75, true, cfg))
	require.Equal(t, Dynamic5hPressureNormal, nextDynamic5hPressureState(Dynamic5hPressurePeak, 0.69, true, cfg))
}

func TestDynamic5hSevenDayExhaustionRemovesCapacityUntilRecovery(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"codex_5h_used_percent": 25.0,
		"codex_5h_reset_at":     now.Add(time.Hour).Format(time.RFC3339),
		"codex_7d_used_percent": 100.0,
		"codex_7d_reset_at":     now.Add(6 * 24 * time.Hour).Format(time.RFC3339),
	}}

	_, ok := dynamic5hWindowForAccount(account, now)
	require.False(t, ok, "an exhausted 7d account contributes zero effective 5h capacity")

	account.Extra["codex_5h_used_percent"] = 0.0
	account.Extra["codex_5h_reset_at"] = now.Add(5 * time.Hour).Format(time.RFC3339)
	_, ok = dynamic5hWindowForAccount(account, now.Add(time.Hour))
	require.False(t, ok, "a 5h reset must not restore capacity while 7d is still exhausted")

	account.Extra["codex_7d_used_percent"] = 0.0
	account.Extra["codex_7d_reset_at"] = now.Add(-time.Minute).Format(time.RFC3339)
	_, ok = dynamic5hWindowForAccount(account, now.Add(time.Hour))
	require.True(t, ok, "capacity returns only after 7d recovery and schedulability")

	account.Schedulable = false
	_, ok = dynamic5hWindowForAccount(account, now.Add(time.Hour))
	require.False(t, ok, "a recovered but unschedulable account still contributes zero")
}

func TestDynamic5hExpiredOpenAIWindowReturnsAsFreshIdleCapacity(t *testing.T) {
	now := time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)
	account := &Account{ID: 42, Name: "idle after reset", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"codex_5h_used_percent": 82.0,
		"codex_5h_reset_at":     now.Add(-time.Minute).Format(time.RFC3339),
		"codex_7d_used_percent": 30.0,
		"codex_7d_reset_at":     now.Add(4 * 24 * time.Hour).Format(time.RFC3339),
	}}

	window, ok := dynamic5hWindowForAccount(account, now)
	require.True(t, ok)
	require.Zero(t, window.used)
	require.Equal(t, now.Add(dynamic5hWindow), window.resetAt)

	diagnostic := dynamic5hAccountDiagnostic(account, now)
	require.True(t, diagnostic.Included)
	require.Equal(t, "included", diagnostic.Reason)
	require.NotNil(t, diagnostic.FiveHourUsed)
	require.Zero(t, *diagnostic.FiveHourUsed)
	require.Nil(t, diagnostic.FiveHourReset)
}

func TestDynamic5hExpiredOpenAIWindowStaysExcludedWhileSevenDayIsExhausted(t *testing.T) {
	now := time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"codex_5h_used_percent": 100.0,
		"codex_5h_reset_at":     now.Add(-time.Minute).Format(time.RFC3339),
		"codex_7d_used_percent": 100.0,
		"codex_7d_reset_at":     now.Add(24 * time.Hour).Format(time.RFC3339),
	}}

	_, ok := dynamic5hWindowForAccount(account, now)
	require.False(t, ok)
	diagnostic := dynamic5hAccountDiagnostic(account, now)
	require.False(t, diagnostic.Included)
	require.Equal(t, "seven_day_exhausted", diagnostic.Reason)
}

func TestDynamic5hRuntimeUnavailableAccountsDoNotContribute(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	base := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"codex_5h_used_percent": 20.0, "codex_5h_reset_at": now.Add(4 * time.Hour).Format(time.RFC3339),
	}}
	require.True(t, dynamic5hAccountAvailable(&base, now))

	until := now.Add(time.Hour)
	for name, mutate := range map[string]func(*Account){
		"disabled":                func(a *Account) { a.Schedulable = false },
		"error":                   func(a *Account) { a.Status = StatusError },
		"rate limited":            func(a *Account) { a.RateLimitResetAt = &until },
		"overloaded":              func(a *Account) { a.OverloadUntil = &until },
		"temporarily unavailable": func(a *Account) { a.TempUnschedulableUntil = &until },
	} {
		t.Run(name, func(t *testing.T) {
			account := base
			mutate(&account)
			require.False(t, dynamic5hAccountAvailable(&account, now))
		})
	}
}

func TestDynamic5hWindowForAccountSupportsOpenAIAndAnthropic(t *testing.T) {
	now := time.Now().UTC()
	openAI := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"codex_5h_used_percent": 35.0, "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339),
	}}
	w, ok := dynamic5hWindowForAccount(openAI, now)
	require.True(t, ok)
	require.InDelta(t, 0.35, w.used, 1e-9)

	reset := now.Add(2 * time.Hour)
	anthropic := &Account{Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, SessionWindowEnd: &reset, Extra: map[string]any{"session_window_utilization": 0.4}}
	w, ok = dynamic5hWindowForAccount(anthropic, now)
	require.True(t, ok)
	require.InDelta(t, 0.4, w.used, 1e-9)
}
