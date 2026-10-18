package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type Dynamic5hPressureState string

const (
	Dynamic5hPressureNormal Dynamic5hPressureState = "normal"
	Dynamic5hPressurePeak   Dynamic5hPressureState = "peak"

	dynamic5hWindow      = 5 * time.Hour
	dynamic5hActiveLease = 15 * time.Minute
)

var ErrDynamic5hPressureLimitExceeded = infraerrors.TooManyRequests(
	"DYNAMIC_5H_PRESSURE_LIMIT_EXCEEDED",
	"Your temporary rolling five-hour usage limit has been reached. Please retry after it recovers.",
)

type Dynamic5hPressureStatus struct {
	Enabled                    bool                   `json:"enabled"`
	DataAvailable              bool                   `json:"data_available"`
	Pressure                   float64                `json:"pressure"`
	RawPressure                float64                `json:"raw_pressure"`
	State                      Dynamic5hPressureState `json:"state"`
	PeakActive                 bool                   `json:"peak_active"`
	AccountCount               int                    `json:"account_count"`
	ActiveUserCount            int64                  `json:"active_user_count"`
	RemainingCapacity          float64                `json:"remaining_capacity"`
	BurnRatePerHour            float64                `json:"burn_rate_per_hour"`
	ProjectedDemand            float64                `json:"projected_demand"`
	EvaluatedAt                time.Time              `json:"evaluated_at"`
	Stale                      bool                   `json:"stale"`
	CapacityRecoveringNextHour float64                `json:"capacity_recovering_next_hour"`
	ExcludedAccountCount       int                    `json:"excluded_account_count"`
	NextResetAt                *time.Time             `json:"next_reset_at,omitempty"`
	PeakThreshold              float64                `json:"peak_threshold"`
	NormalThreshold            float64                `json:"normal_threshold"`
}

type Dynamic5hUserStatus struct {
	Enabled          bool                   `json:"enabled"`
	State            Dynamic5hPressureState `json:"state"`
	LimitActive      bool                   `json:"limit_active"`
	CurrentlyLimited bool                   `json:"currently_limited"`
	UsagePercent     float64                `json:"usage_percent"`
	RemainingPercent float64                `json:"remaining_percent"`
	WindowStartedAt  *time.Time             `json:"window_started_at,omitempty"`
	RecoverAt        *time.Time             `json:"recover_at,omitempty"`
	Exempt           bool                   `json:"exempt"`
	WarningLevel     string                 `json:"warning_level"`
	QuotaAvailable   bool                   `json:"quota_available"`
	SevenDayLimitUSD float64                `json:"seven_day_limit_usd"`
	LimitUSD         float64                `json:"limit_usd"`
	UsageUSD         float64                `json:"usage_usd"`
	RemainingUSD     float64                `json:"remaining_usd"`
	LastUsedAt       *time.Time             `json:"-"`
	Pool             *Dynamic5hPublicPool   `json:"pool,omitempty"`
}

type Dynamic5hPublicPool struct {
	FiveHourUsedPercent *float64   `json:"five_hour_used_percent,omitempty"`
	FiveHourSampleCount int        `json:"five_hour_sample_count"`
	SevenDayUsedPercent *float64   `json:"seven_day_used_percent,omitempty"`
	SevenDaySampleCount int        `json:"seven_day_sample_count"`
	Stale               bool       `json:"stale"`
	EvaluatedAt         time.Time  `json:"evaluated_at"`
	NextResetAt         *time.Time `json:"next_reset_at,omitempty"`
}

type Dynamic5hAdminUserStatus struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Active bool   `json:"active"`
	Dynamic5hUserStatus
}

type Dynamic5hUserPolicy struct {
	Exempt bool `json:"exempt"`
}

type Dynamic5hAuditLog struct {
	ID          int64                `json:"id"`
	UserID      int64                `json:"user_id"`
	ActorUserID *int64               `json:"actor_user_id,omitempty"`
	ActorEmail  string               `json:"actor_email"`
	UserEmail   string               `json:"user_email"`
	Action      string               `json:"action"`
	Reason      string               `json:"-"`
	BeforeUsage *float64             `json:"before_usage,omitempty"`
	AfterUsage  *float64             `json:"after_usage,omitempty"`
	OldPolicy   *Dynamic5hUserPolicy `json:"old_policy,omitempty"`
	NewPolicy   *Dynamic5hUserPolicy `json:"new_policy,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
}

type Dynamic5hPolicyRepository interface {
	GetUserPolicy(context.Context, int64) (Dynamic5hUserPolicy, bool, error)
	ListUserPolicies(context.Context) (map[int64]Dynamic5hUserPolicy, error)
	UpsertUserPolicy(context.Context, int64, Dynamic5hUserPolicy, int64, string) error
	RecordUsageReset(context.Context, int64, int64, string, float64, float64) error
	ListAuditLogs(context.Context, int) ([]Dynamic5hAuditLog, error)
}

type Dynamic5hAccountDiagnostic struct {
	AccountID     int64      `json:"account_id"`
	Name          string     `json:"name"`
	Platform      string     `json:"platform"`
	Included      bool       `json:"included"`
	Reason        string     `json:"reason"`
	FiveHourUsed  *float64   `json:"five_hour_used_percent,omitempty"`
	FiveHourReset *time.Time `json:"five_hour_reset_at,omitempty"`
	SevenDayUsed  *float64   `json:"seven_day_used_percent,omitempty"`
	SevenDayReset *time.Time `json:"seven_day_reset_at,omitempty"`
	RejoinAt      *time.Time `json:"rejoin_at,omitempty"`
}

type Dynamic5hAdminOverview struct {
	Pool      Dynamic5hPressureStatus      `json:"pool"`
	Users     []Dynamic5hAdminUserStatus   `json:"users"`
	Accounts  []Dynamic5hAccountDiagnostic `json:"accounts"`
	AuditLogs []Dynamic5hAuditLog          `json:"audit_logs"`
	Metrics   Dynamic5hMetricsSnapshot     `json:"metrics"`
}

type dynamic5hAccountWindow struct {
	used    float64
	resetAt time.Time
}

// Dynamic5hPressureCache keeps cache technology and key layout outside the
// service layer. Cache errors always result in fail-open enforcement.
type Dynamic5hPressureCache interface {
	LoadStatus(ctx context.Context) (Dynamic5hPressureStatus, bool, error)
	StoreStatus(ctx context.Context, status Dynamic5hPressureStatus) error
	AcquireRefreshLock(ctx context.Context, token string, ttl time.Duration) (bool, error)
	ReleaseRefreshLock(ctx context.Context, token string) error
}

type Dynamic5hPressureService struct {
	accountRepo AccountRepository
	userRepo    UserRepository
	policyRepo  Dynamic5hPolicyRepository
	quotaRepo   Subscription5hQuotaRepository
	cache       Dynamic5hPressureCache
	cfg         config.Dynamic5hPressureConfig

	mu              sync.RWMutex
	status          Dynamic5hPressureStatus
	refreshing      bool
	lastAttempt     time.Time
	now             func() time.Time
	policyMu        sync.Mutex
	policies        map[int64]Dynamic5hUserPolicy
	policyLoadedAt  time.Time
	policyAttemptAt time.Time
	metrics         dynamic5hMetrics
}

func NewDynamic5hPressureService(accountRepo AccountRepository, cache Dynamic5hPressureCache, cfg *config.Config, userRepo ...UserRepository) *Dynamic5hPressureService {
	pressureCfg := config.Dynamic5hPressureConfig{}
	if cfg != nil {
		pressureCfg = cfg.Gateway.Dynamic5hPressure
	}
	pressureCfg = normalizeDynamic5hPressureConfig(pressureCfg)
	s := &Dynamic5hPressureService{accountRepo: accountRepo, cache: cache, cfg: pressureCfg, now: time.Now}
	if len(userRepo) > 0 {
		s.userRepo = userRepo[0]
	}
	s.status = Dynamic5hPressureStatus{Enabled: pressureCfg.Enabled, State: Dynamic5hPressureNormal}
	return s
}

func ProvideDynamic5hPressureService(accountRepo AccountRepository, cache Dynamic5hPressureCache, cfg *config.Config, userRepo UserRepository, policyRepo Dynamic5hPolicyRepository) *Dynamic5hPressureService {
	svc := NewDynamic5hPressureService(accountRepo, cache, cfg, userRepo)
	svc.policyRepo = policyRepo
	svc.quotaRepo, _ = policyRepo.(Subscription5hQuotaRepository)
	return svc
}

func normalizeDynamic5hPressureConfig(c config.Dynamic5hPressureConfig) config.Dynamic5hPressureConfig {
	if c.RefreshIntervalSeconds <= 0 {
		c.RefreshIntervalSeconds = 30
	}
	if c.PeakThreshold <= 0 {
		c.PeakThreshold = 1.0
	}
	if c.NormalThreshold <= 0 || c.NormalThreshold > c.PeakThreshold {
		c.NormalThreshold = math.Min(0.9, c.PeakThreshold*0.9)
	}
	if c.EWMAAlpha <= 0 || c.EWMAAlpha > 1 {
		c.EWMAAlpha = 0.35
	}
	return c
}

func (s *Dynamic5hPressureService) MaybeRefresh() {
	if s == nil || !s.cfg.Enabled || s.accountRepo == nil {
		return
	}
	now := s.now()
	s.mu.Lock()
	if s.refreshing || now.Sub(s.lastAttempt) < time.Duration(s.cfg.RefreshIntervalSeconds)*time.Second {
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	s.lastAttempt = now
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := s.Refresh(ctx); err != nil {
			slog.Warn("dynamic 5h pressure refresh failed; retaining last-good state", "error", err)
		}
		s.mu.Lock()
		s.refreshing = false
		s.mu.Unlock()
	}()
}

func (s *Dynamic5hPressureService) Refresh(ctx context.Context) (Dynamic5hPressureStatus, error) {
	if s == nil {
		return Dynamic5hPressureStatus{State: Dynamic5hPressureNormal}, nil
	}
	if !s.cfg.Enabled {
		status := Dynamic5hPressureStatus{State: Dynamic5hPressureNormal, EvaluatedAt: s.now().UTC()}
		s.storeStatus(ctx, status)
		return status, nil
	}
	if s.accountRepo == nil {
		return s.cachedStatus(), nil
	}
	lockToken, acquired := s.acquireRefreshLock(ctx)
	if !acquired {
		return s.loadStatus(ctx), nil
	}
	defer s.releaseRefreshLock(context.Background(), lockToken)
	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		s.metrics.refreshFailures.Add(1)
		return s.cachedStatus(), err
	}
	now := s.now().UTC()
	windows := make([]dynamic5hAccountWindow, 0, len(accounts))
	var recoveringNextHour float64
	var nextResetAt *time.Time
	for i := range accounts {
		if w, ok := dynamic5hWindowForAccount(&accounts[i], now); ok {
			windows = append(windows, w)
			if w.resetAt.Sub(now) <= time.Hour {
				recoveringNextHour += w.used
			}
			if nextResetAt == nil || w.resetAt.Before(*nextResetAt) {
				reset := w.resetAt
				nextResetAt = &reset
			}
		}
	}
	raw, remaining, burnPerHour, projected := calculateDynamic5hPressure(windows, now)
	previous := s.loadStatus(ctx)
	pressure := raw
	if previous.DataAvailable {
		pressure = s.cfg.EWMAAlpha*raw + (1-s.cfg.EWMAAlpha)*previous.Pressure
	}
	if math.IsNaN(pressure) || math.IsInf(pressure, 0) {
		pressure = 0
	}
	pressure = math.Min(100, pressure)
	state := nextDynamic5hPressureState(previous.State, pressure, len(windows) > 0, s.cfg)
	status := Dynamic5hPressureStatus{
		Enabled: true, DataAvailable: len(windows) > 0,
		Pressure: pressure, RawPressure: raw, State: state, PeakActive: state == Dynamic5hPressurePeak,
		AccountCount: len(windows), RemainingCapacity: remaining,
		BurnRatePerHour: burnPerHour, ProjectedDemand: projected,
		EvaluatedAt:                now,
		CapacityRecoveringNextHour: recoveringNextHour, ExcludedAccountCount: len(accounts) - len(windows), NextResetAt: nextResetAt,
		PeakThreshold: s.cfg.PeakThreshold, NormalThreshold: s.cfg.NormalThreshold,
	}
	s.storeStatus(ctx, status)
	return status, nil
}

func calculateDynamic5hPressure(windows []dynamic5hAccountWindow, now time.Time) (pressure, remaining, burnPerHour, projected float64) {
	type resetEvent struct{ afterHours, restored float64 }
	events := make([]resetEvent, 0, len(windows))
	for _, w := range windows {
		u := math.Max(0, math.Min(1, w.used))
		remainingHours := math.Min(dynamic5hWindow.Hours(), w.resetAt.Sub(now).Hours())
		if remainingHours <= 0 {
			continue
		}
		elapsed := math.Max(5.0/60.0, dynamic5hWindow.Hours()-remainingHours)
		accountBurn := u / elapsed
		remaining += 1 - u
		burnPerHour += accountBurn
		events = append(events, resetEvent{afterHours: remainingHours, restored: u})
	}
	projected = burnPerHour * dynamic5hWindow.Hours()
	effectiveRemaining := remaining
	// Process reset events in time order. A reset restores the consumed part of
	// that account, but only capacity that forecast demand can actually use in
	// the remaining horizon is credited. This avoids both extremes: treating a
	// near reset as zero capacity, or counting a full fresh window that arrives
	// moments before the forecast ends.
	sort.Slice(events, func(i, j int) bool { return events[i].afterHours > events[j].afterHours })
	cumulativeRestored, usefulRecovery := 0.0, 0.0
	for _, event := range events {
		cumulativeRestored += event.restored
		futureDemand := burnPerHour * math.Max(0, dynamic5hWindow.Hours()-event.afterHours)
		usefulRecovery = math.Min(cumulativeRestored, futureDemand)
	}
	effectiveRemaining += usefulRecovery
	if effectiveRemaining <= 0 {
		if projected > 0 {
			return 100, remaining, burnPerHour, projected
		}
		return 0, remaining, burnPerHour, projected
	}
	return projected / effectiveRemaining, remaining, burnPerHour, projected
}

func nextDynamic5hPressureState(previous Dynamic5hPressureState, pressure float64, available bool, c config.Dynamic5hPressureConfig) Dynamic5hPressureState {
	if !available {
		return Dynamic5hPressureNormal
	}
	if previous == Dynamic5hPressurePeak {
		if pressure < c.NormalThreshold {
			return Dynamic5hPressureNormal
		}
		return Dynamic5hPressurePeak
	}
	if pressure >= c.PeakThreshold {
		return Dynamic5hPressurePeak
	}
	return Dynamic5hPressureNormal
}

func dynamic5hWindowForAccount(a *Account, now time.Time) (dynamic5hAccountWindow, bool) {
	if !dynamic5hAccountAvailable(a, now) {
		return dynamic5hAccountWindow{}, false
	}
	platform := strings.ToLower(strings.TrimSpace(a.Platform))
	switch platform {
	case PlatformOpenAI:
		if !openAICodexSnapshotIdentityTrusted(a) {
			return dynamic5hAccountWindow{}, false
		}
		used, ok := resolveAccountExtraNumber(a.Extra, "codex_5h_used_percent")
		resetAt, hasReset := openAICodexWindowResetAt(a.Extra, "5h")
		if !ok || !hasReset {
			return dynamic5hAccountWindow{}, false
		}
		if !resetAt.After(now) {
			// A rolling window that reset before the account was used again is a
			// fresh idle window. The synthetic deadline is internal forecasting
			// state; the real next reset starts with the account's next use.
			return dynamic5hAccountWindow{used: 0, resetAt: now.Add(dynamic5hWindow)}, true
		}
		return dynamic5hAccountWindow{used: used / 100, resetAt: resetAt}, true
	case PlatformAnthropic:
		raw, ok := a.Extra["session_window_utilization"]
		if !ok || a.SessionWindowEnd == nil || !a.SessionWindowEnd.After(now) {
			return dynamic5hAccountWindow{}, false
		}
		return dynamic5hAccountWindow{used: utilizationAsPercent(raw) / 100, resetAt: *a.SessionWindowEnd}, true
	case PlatformKimi, PlatformZhipu, PlatformMiniMax, PlatformOpenCodeGo:
		used, ok := resolveAccountExtraNumber(a.Extra, platform+"_5h_used_percent")
		reset := parseSchedulingResetAt(a.Extra[platform+"_5h_reset_at"])
		if !ok || reset == nil || !reset.After(now) {
			return dynamic5hAccountWindow{}, false
		}
		return dynamic5hAccountWindow{used: used / 100, resetAt: *reset}, true
	default:
		return dynamic5hAccountWindow{}, false
	}
}

func dynamic5hAccountAvailable(a *Account, now time.Time) bool {
	if a == nil || !a.Schedulable || !a.IsActive() {
		return false
	}
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt) {
		return false
	}
	if (a.OverloadUntil != nil && now.Before(*a.OverloadUntil)) ||
		(a.RateLimitResetAt != nil && now.Before(*a.RateLimitResetAt)) ||
		(a.TempUnschedulableUntil != nil && now.Before(*a.TempUnschedulableUntil)) ||
		(a.IsAPIKeyOrBedrock() && a.IsQuotaExceeded()) {
		return false
	}
	platform := strings.ToLower(strings.TrimSpace(a.Platform))
	var usedKeys, resetKeys []string
	switch platform {
	case PlatformOpenAI:
		usedKeys = []string{"codex_7d_used_percent"}
		resetKeys = []string{"codex_7d_reset_at"}
	case PlatformAnthropic:
		usedKeys = []string{"passive_usage_7d_utilization"}
		resetKeys = []string{"passive_usage_7d_reset"}
	case PlatformKimi, PlatformZhipu, PlatformMiniMax, PlatformOpenCodeGo:
		usedKeys = []string{platform + "_7d_used_percent"}
		resetKeys = []string{platform + "_7d_reset_at"}
	}
	for i, key := range usedKeys {
		used, ok := resolveAccountExtraNumber(a.Extra, key)
		if !ok {
			continue
		}
		if platform == PlatformAnthropic && used <= 1 {
			used *= 100
		}
		reset := parseSchedulingResetAt(a.Extra[resetKeys[i]])
		if used >= 100 && (reset == nil || reset.After(now)) {
			return false
		}
	}
	return true
}

func (s *Dynamic5hPressureService) AdminStatus(ctx context.Context, refresh bool) Dynamic5hPressureStatus {
	if s == nil {
		return Dynamic5hPressureStatus{State: Dynamic5hPressureNormal}
	}
	if refresh {
		if status, err := s.Refresh(ctx); err == nil {
			return s.usableStatus(status)
		}
	} else {
		s.MaybeRefresh()
	}
	return s.loadStatus(ctx)
}

func (s *Dynamic5hPressureService) PublicUserStatus(ctx context.Context, userID int64) (Dynamic5hUserStatus, error) {
	result, err := s.subscriptionQuotaUserStatus(ctx, userID)
	if err != nil {
		return result, err
	}
	pressure := s.AdminStatus(ctx, false)
	result.Pool = &Dynamic5hPublicPool{Stale: pressure.Stale, EvaluatedAt: pressure.EvaluatedAt, NextResetAt: pressure.NextResetAt}
	if s.accountRepo != nil {
		accounts, err := s.accountRepo.ListAllWithFilters(ctx, "", "", "", "", 0, "")
		if err != nil {
			return result, err
		}
		result.Pool.FiveHourUsedPercent, result.Pool.FiveHourSampleCount, result.Pool.SevenDayUsedPercent, result.Pool.SevenDaySampleCount = dynamic5hPublicPoolUsage(accounts, s.now().UTC())
	}
	return result, nil
}

func dynamic5hPublicPoolUsage(accounts []Account, now time.Time) (fiveHour *float64, fiveHourCount int, sevenDay *float64, sevenDayCount int) {
	var fiveHourTotal, sevenDayTotal float64
	for index := range accounts {
		account := &accounts[index]
		if !account.IsActive() || !account.Schedulable {
			continue
		}
		if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
			continue
		}
		diagnostic := dynamic5hAccountDiagnostic(account, now)
		if diagnostic.FiveHourUsed != nil {
			fiveHourTotal += math.Max(0, math.Min(100, *diagnostic.FiveHourUsed))
			fiveHourCount++
		}
		if diagnostic.SevenDayUsed != nil {
			sevenDayTotal += math.Max(0, math.Min(100, *diagnostic.SevenDayUsed))
			sevenDayCount++
		}
	}
	if fiveHourCount > 0 {
		value := fiveHourTotal / float64(fiveHourCount)
		fiveHour = &value
	}
	if sevenDayCount > 0 {
		value := sevenDayTotal / float64(sevenDayCount)
		sevenDay = &value
	}
	return fiveHour, fiveHourCount, sevenDay, sevenDayCount
}

func (s *Dynamic5hPressureService) AdminOverview(ctx context.Context) Dynamic5hAdminOverview {
	return s.subscriptionQuotaAdminOverview(ctx)
}

func dynamic5hAccountDiagnostic(a *Account, now time.Time) Dynamic5hAccountDiagnostic {
	d := Dynamic5hAccountDiagnostic{AccountID: a.ID, Name: a.Name, Platform: a.Platform}
	platform := strings.ToLower(strings.TrimSpace(a.Platform))
	var fiveUsedKey, fiveResetKey, sevenUsedKey, sevenResetKey string
	openAIFiveHourReset := false
	switch platform {
	case PlatformOpenAI:
		fiveUsedKey, fiveResetKey, sevenUsedKey, sevenResetKey = "codex_5h_used_percent", "codex_5h_reset_at", "codex_7d_used_percent", "codex_7d_reset_at"
	case PlatformAnthropic:
		fiveUsedKey, sevenUsedKey, sevenResetKey = "session_window_utilization", "passive_usage_7d_utilization", "passive_usage_7d_reset"
	case PlatformKimi, PlatformZhipu, PlatformMiniMax, PlatformOpenCodeGo:
		fiveUsedKey, fiveResetKey, sevenUsedKey, sevenResetKey = platform+"_5h_used_percent", platform+"_5h_reset_at", platform+"_7d_used_percent", platform+"_7d_reset_at"
	default:
		d.Reason = "unsupported_platform"
		return d
	}
	if value, ok := resolveAccountExtraNumber(a.Extra, fiveUsedKey); ok {
		if platform == PlatformAnthropic && value <= 1 {
			value *= 100
		}
		d.FiveHourUsed = &value
	}
	switch platform {
	case PlatformAnthropic:
		d.FiveHourReset = a.SessionWindowEnd
	case PlatformOpenAI:
		if resetAt, ok := openAICodexWindowResetAt(a.Extra, "5h"); ok {
			d.FiveHourReset = &resetAt
			openAIFiveHourReset = !resetAt.After(now)
		}
	default:
		d.FiveHourReset = parseSchedulingResetAt(a.Extra[fiveResetKey])
	}
	if openAIFiveHourReset && d.FiveHourUsed != nil {
		zero := 0.0
		d.FiveHourUsed = &zero
		// Until the account is used again there is no real rolling-window
		// deadline to present to an administrator.
		d.FiveHourReset = nil
	}
	if value, ok := resolveAccountExtraNumber(a.Extra, sevenUsedKey); ok {
		if platform == PlatformAnthropic && value <= 1 {
			value *= 100
		}
		d.SevenDayUsed = &value
	}
	d.SevenDayReset = parseSchedulingResetAt(a.Extra[sevenResetKey])
	switch {
	case !a.IsActive():
		d.Reason = "disabled"
	case !a.Schedulable:
		d.Reason = "unschedulable"
	case a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt):
		d.Reason = "credential_expired"
	case a.RateLimitResetAt != nil && now.Before(*a.RateLimitResetAt):
		d.Reason = "rate_limited"
		d.RejoinAt = a.RateLimitResetAt
	case a.OverloadUntil != nil && now.Before(*a.OverloadUntil):
		d.Reason = "overloaded"
		d.RejoinAt = a.OverloadUntil
	case a.TempUnschedulableUntil != nil && now.Before(*a.TempUnschedulableUntil):
		d.Reason = "temporarily_unavailable"
		d.RejoinAt = a.TempUnschedulableUntil
	case d.SevenDayUsed != nil && *d.SevenDayUsed >= 100 && (d.SevenDayReset == nil || d.SevenDayReset.After(now)):
		d.Reason = "seven_day_exhausted"
		d.RejoinAt = d.SevenDayReset
	case platform == PlatformOpenAI && !openAICodexSnapshotIdentityTrusted(a):
		d.Reason = "identity_mismatch"
	case openAIFiveHourReset && d.FiveHourUsed != nil:
		d.Included = true
		d.Reason = "included"
	case d.FiveHourUsed == nil || d.FiveHourReset == nil:
		d.Reason = "snapshot_missing"
	case !d.FiveHourReset.After(now):
		d.Reason = "snapshot_expired"
		d.RejoinAt = d.FiveHourReset
	default:
		d.Included = true
		d.Reason = "included"
	}
	return d
}

func (s *Dynamic5hPressureService) CheckUser(ctx context.Context, userID int64, _ ...string) error {
	if s == nil || s.quotaRepo == nil {
		return nil
	}
	return s.checkSubscriptionQuota(ctx, userID)
}

func (s *Dynamic5hPressureService) SetUserPolicy(ctx context.Context, userID int64, policy Dynamic5hUserPolicy, actorIDs ...int64) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user id")
	}
	if s.policyRepo == nil {
		return fmt.Errorf("five-hour policy repository unavailable")
	}
	var actorID int64
	if len(actorIDs) > 0 {
		actorID = actorIDs[0]
	}
	if err := s.policyRepo.UpsertUserPolicy(ctx, userID, policy, actorID, ""); err != nil {
		return err
	}
	s.policyMu.Lock()
	s.policyLoadedAt, s.policyAttemptAt, s.policies = time.Time{}, time.Time{}, nil
	s.policyMu.Unlock()
	return nil
}

func (s *Dynamic5hPressureService) ResetUserUsage(ctx context.Context, userID int64, actorIDs ...int64) error {
	var actorID int64
	if len(actorIDs) > 0 {
		actorID = actorIDs[0]
	}
	return s.resetSubscriptionQuota(ctx, userID, actorID)
}

func (s *Dynamic5hPressureService) cachedStatus() Dynamic5hPressureStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Dynamic5hPressureService) loadStatus(ctx context.Context) Dynamic5hPressureStatus {
	if s.cache != nil {
		status, found, err := s.cache.LoadStatus(ctx)
		if err != nil {
			s.metrics.statusReadFailures.Add(1)
		}
		if err == nil && found {
			s.mu.Lock()
			s.status = status
			s.mu.Unlock()
			return s.usableStatus(status)
		}
	}
	return s.usableStatus(s.cachedStatus())
}

func (s *Dynamic5hPressureService) storeStatus(ctx context.Context, status Dynamic5hPressureStatus) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	if s.cache != nil {
		if err := s.cache.StoreStatus(ctx, status); err != nil {
			s.metrics.statusWriteFailures.Add(1)
			slog.Warn("dynamic 5h pressure status persistence failed", "error", err)
		}
	}
}

func (s *Dynamic5hPressureService) acquireRefreshLock(ctx context.Context) (string, bool) {
	if s.cache == nil {
		return "", true
	}
	token := fmt.Sprintf("%d", s.now().UnixNano())
	ok, err := s.cache.AcquireRefreshLock(ctx, token, 15*time.Second)
	return token, err == nil && ok
}

func (s *Dynamic5hPressureService) releaseRefreshLock(ctx context.Context, token string) {
	if s.cache == nil || token == "" {
		return
	}
	_ = s.cache.ReleaseRefreshLock(ctx, token)
}
