//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type dynamic5hMemoryCache struct {
	status    Dynamic5hPressureStatus
	found     bool
	lockToken string
	active    map[int64]time.Time
	meter     map[int64]float64
	userMeter map[int64]map[int64]float64
}

func newDynamic5hMemoryCache() *dynamic5hMemoryCache {
	return &dynamic5hMemoryCache{
		active: make(map[int64]time.Time), meter: make(map[int64]float64),
		userMeter: make(map[int64]map[int64]float64),
	}
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

func (c *dynamic5hMemoryCache) TouchActiveUser(_ context.Context, userID int64, now, cutoff time.Time) error {
	c.active[userID] = now
	for id, seen := range c.active {
		if !seen.After(cutoff) {
			delete(c.active, id)
		}
	}
	return nil
}

func (c *dynamic5hMemoryCache) ActiveUserIDs(_ context.Context, cutoff time.Time) ([]string, error) {
	ids := make([]string, 0, len(c.active))
	for id, seen := range c.active {
		if seen.After(cutoff) {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
	}
	return ids, nil
}

func (c *dynamic5hMemoryCache) RecordUsage(
	ctx context.Context,
	userID, bucket int64,
	now, cutoff time.Time,
	amount float64,
	_ time.Duration,
) error {
	_ = c.TouchActiveUser(ctx, userID, now, cutoff)
	c.meter[bucket] += amount
	if c.userMeter[userID] == nil {
		c.userMeter[userID] = make(map[int64]float64)
	}
	c.userMeter[userID][bucket] += amount
	return nil
}

func (c *dynamic5hMemoryCache) MeterValues(_ context.Context, latestBucket int64, count int) ([]float64, error) {
	return dynamic5hMemoryValues(c.meter, latestBucket, count), nil
}

func (c *dynamic5hMemoryCache) UserMeterValues(_ context.Context, userID, latestBucket int64, count int) ([]float64, error) {
	return dynamic5hMemoryValues(c.userMeter[userID], latestBucket, count), nil
}

func dynamic5hMemoryValues(source map[int64]float64, latestBucket int64, count int) []float64 {
	values := make([]float64, count)
	for i := range values {
		values[i] = source[latestBucket-int64(count-1-i)]
	}
	return values
}

func dynamic5hTestConfig() config.Dynamic5hPressureConfig {
	return config.Dynamic5hPressureConfig{Enabled: true, PeakThreshold: 0.8, NormalThreshold: 0.7, EWMAAlpha: 1}
}

func newDynamic5hTestService(t *testing.T, now time.Time) (*Dynamic5hPressureService, context.Context) {
	t.Helper()
	svc := NewDynamic5hPressureService(nil, newDynamic5hMemoryCache(), &config.Config{Gateway: config.GatewayConfig{Dynamic5hPressure: dynamic5hTestConfig()}})
	svc.now = func() time.Time { return now }
	return svc, context.Background()
}

func storeDynamic5hTestStatus(svc *Dynamic5hPressureService, ctx context.Context, state Dynamic5hPressureState, capacity float64) {
	svc.storeStatus(ctx, Dynamic5hPressureStatus{
		Enabled: true, DataAvailable: true, CalibrationReady: capacity > 0,
		State: state, PeakActive: state == Dynamic5hPressurePeak, PoolCapacity: capacity,
	})
}

func TestCalculateDynamic5hPressureAccountsWithDifferentResetTimes(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	soon, _, _, _ := calculateDynamic5hPressure([]dynamic5hAccountWindow{{used: 0.95, resetAt: now.Add(10 * time.Minute)}}, now)
	far, _, _, _ := calculateDynamic5hPressure([]dynamic5hAccountWindow{{used: 0.95, resetAt: now.Add(4 * time.Hour)}}, now)
	require.Less(t, soon, far, "the same remaining quota must be safer when reset is near")
	require.Less(t, soon, 0.8)
	require.Greater(t, far, 1.0)
}

func TestNextDynamic5hPressureStateUsesHysteresis(t *testing.T) {
	cfg := dynamic5hTestConfig()
	require.Equal(t, Dynamic5hPressurePeak, nextDynamic5hPressureState(Dynamic5hPressureNormal, 0.85, true, cfg))
	require.Equal(t, Dynamic5hPressurePeak, nextDynamic5hPressureState(Dynamic5hPressurePeak, 0.75, true, cfg))
	require.Equal(t, Dynamic5hPressureNormal, nextDynamic5hPressureState(Dynamic5hPressurePeak, 0.69, true, cfg))
}

func TestDynamic5hFiveUsersShareFiveAccountCapacity(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, now)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressureNormal, 500)
	for userID := int64(1); userID <= 5; userID++ {
		svc.RecordUsage(ctx, userID, PlatformOpenAI, 80)
		require.NoError(t, svc.CheckUser(ctx, userID))
	}
	for userID := int64(1); userID <= 5; userID++ {
		require.InDelta(t, 80, svc.UserStatus(ctx, userID).UsagePercent, 0.01)
	}

	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 500)
	for userID := int64(1); userID <= 5; userID++ {
		require.NoError(t, svc.CheckUser(ctx, userID), "users below their equal dynamic share must continue during Peak")
	}
}

func TestDynamic5hFairShareBorrowingAndPeakReclamation(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, now)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressureNormal, 500)

	// With only two active users, each may borrow the idle capacity.
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 180)
	svc.RecordUsage(ctx, 2, PlatformOpenAI, 180)
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.NoError(t, svc.CheckUser(ctx, 2))
	require.InDelta(t, 72, svc.UserStatus(ctx, 1).UsagePercent, 0.01)

	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 500)
	// Late arrivals immediately join the protected population and retain a fair
	// chance. There is no generation snapshot and no zero-remaining penalty.
	for _, userID := range []int64{3, 4, 5} {
		require.NoError(t, svc.CheckUser(ctx, userID))
		svc.RecordUsage(ctx, userID, PlatformOpenAI, 10)
	}

	// Five users now share five effective account-windows: fair share = 100.
	require.ErrorIs(t, svc.CheckUser(ctx, 1), ErrDynamic5hPressureLimitExceeded)
	require.ErrorIs(t, svc.CheckUser(ctx, 2), ErrDynamic5hPressureLimitExceeded)
	for _, userID := range []int64{3, 4, 5} {
		require.NoError(t, svc.CheckUser(ctx, userID))
		status := svc.UserStatus(ctx, userID)
		require.False(t, status.CurrentlyLimited)
		require.InDelta(t, 10, status.UsagePercent, 0.01)
		require.InDelta(t, 90, status.RemainingPercent, 0.01)
	}
	overused := svc.UserStatus(ctx, 1)
	require.True(t, overused.CurrentlyLimited)
	require.InDelta(t, 180, overused.UsagePercent, 0.01)
	require.Zero(t, overused.RemainingPercent)
}

func TestDynamic5hAdminOverviewSortsActiveUsersByUsage(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, now)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 300)
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 180)
	svc.RecordUsage(ctx, 2, PlatformOpenAI, 30)
	svc.RecordUsage(ctx, 3, PlatformOpenAI, 90)

	overview := svc.AdminOverview(ctx)
	require.Equal(t, []int64{1, 3, 2}, []int64{
		overview.Users[0].UserID,
		overview.Users[1].UserID,
		overview.Users[2].UserID,
	})
	require.InDelta(t, 180, overview.Users[0].UsagePercent, 0.01)
	require.True(t, overview.Users[0].CurrentlyLimited)
	require.InDelta(t, 90, overview.Users[1].UsagePercent, 0.01)
	require.False(t, overview.Users[1].CurrentlyLimited)
}

func TestDynamic5hNewPeakUserGetsDynamicFairShare(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, now)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 200)
	svc.RecordUsage(ctx, 10, PlatformOpenAI, 150)

	require.NoError(t, svc.CheckUser(ctx, 20), "a user joining during Peak must not start at 0% remaining")
	late := svc.UserStatus(ctx, 20)
	require.True(t, late.LimitActive)
	require.False(t, late.CurrentlyLimited)
	require.Zero(t, late.UsagePercent)
	require.InDelta(t, 100, late.RemainingPercent, 0.01)

	// Adding the user changes both shares to 100. The earlier user loses borrowed
	// capacity but keeps already successful usage.
	require.ErrorIs(t, svc.CheckUser(ctx, 10), ErrDynamic5hPressureLimitExceeded)
}

func TestDynamic5hInactiveDemandExpiresWithoutErasingRollingUsage(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	current := now
	svc, ctx := newDynamic5hTestService(t, now)
	svc.now = func() time.Time { return current }
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 200)

	// Both users initially compete for the pool, so each fair share is 100.
	svc.RecordUsage(ctx, 1, PlatformOpenAI, 120)
	svc.RecordUsage(ctx, 2, PlatformOpenAI, 20)
	require.ErrorIs(t, svc.CheckUser(ctx, 1), ErrDynamic5hPressureLimitExceeded)

	// After the short demand lease expires, user 1 can return and borrow the
	// idle user's share. Their previous 120 units are still in the rolling 5h
	// meter, but the sole active user's current fair share is now 200.
	current = now.Add(dynamic5hActiveLease + time.Second)
	require.NoError(t, svc.CheckUser(ctx, 1))
	require.InDelta(t, 60, svc.UserStatus(ctx, 1).UsagePercent, 0.01)

	// User 2 rejoins immediately on a new request. The population returns to
	// two, user 1's old usage is not erased, and excess borrowing is reclaimed.
	require.NoError(t, svc.CheckUser(ctx, 2))
	require.ErrorIs(t, svc.CheckUser(ctx, 1), ErrDynamic5hPressureLimitExceeded)
	require.InDelta(t, 120, svc.UserStatus(ctx, 1).UsagePercent, 0.01)
}

func TestDynamic5hNormalImmediatelyReleasesPeakLimit(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	svc, ctx := newDynamic5hTestService(t, now)
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 100)
	svc.RecordUsage(ctx, 7, PlatformOpenAI, 120)
	require.ErrorIs(t, svc.CheckUser(ctx, 7), ErrDynamic5hPressureLimitExceeded)

	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressureNormal, 100)
	require.NoError(t, svc.CheckUser(ctx, 7))
	status := svc.UserStatus(ctx, 7)
	require.False(t, status.LimitActive)
	require.False(t, status.CurrentlyLimited)
	require.InDelta(t, 120, status.UsagePercent, 0.01)
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
