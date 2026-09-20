package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type Dynamic5hPressureState string

const (
	Dynamic5hPressureNormal Dynamic5hPressureState = "normal"
	Dynamic5hPressurePeak   Dynamic5hPressureState = "peak"

	dynamic5hWindow      = 5 * time.Hour
	dynamic5hMeterBucket = 5 * time.Minute
	dynamic5hRedisTTL    = 6 * time.Hour
	// Usage remains rolling for five hours, while the protected population
	// tracks only users with current demand. Each request renews this lease.
	dynamic5hActiveLease = 15 * time.Minute
)

var ErrDynamic5hPressureLimitExceeded = infraerrors.TooManyRequests(
	"DYNAMIC_5H_PRESSURE_LIMIT_EXCEEDED",
	"Your temporary rolling five-hour usage limit has been reached. Please retry after it recovers.",
)

// Dynamic5hPressureStatus is the administrator view. RemainingCapacity and
// demand fields use normalized account-window units; PoolCapacity is the
// calibrated internal meter capacity and is never returned by the user API.
type Dynamic5hPressureStatus struct {
	Enabled                    bool                   `json:"enabled"`
	DataAvailable              bool                   `json:"data_available"`
	CalibrationReady           bool                   `json:"calibration_ready"`
	Pressure                   float64                `json:"pressure"`
	RawPressure                float64                `json:"raw_pressure"`
	State                      Dynamic5hPressureState `json:"state"`
	PeakActive                 bool                   `json:"peak_active"`
	AccountCount               int                    `json:"account_count"`
	ActiveUserCount            int64                  `json:"active_user_count"`
	RemainingCapacity          float64                `json:"remaining_capacity"`
	BurnRatePerHour            float64                `json:"burn_rate_per_hour"`
	ProjectedDemand            float64                `json:"projected_demand"`
	PoolCapacity               float64                `json:"pool_capacity"`
	Episode                    int64                  `json:"episode"`
	EvaluatedAt                time.Time              `json:"evaluated_at"`
	CapacityRecoveringNextHour float64                `json:"capacity_recovering_next_hour"`
	ExcludedAccountCount       int                    `json:"excluded_account_count"`
	NextResetAt                *time.Time             `json:"next_reset_at,omitempty"`
	PeakThreshold              float64                `json:"peak_threshold"`
	NormalThreshold            float64                `json:"normal_threshold"`
}

// Dynamic5hUserStatus deliberately exposes only OpenAI-style percentages and
// timing. Meter values and pool capacity units never cross the user API.
type Dynamic5hUserStatus struct {
	Enabled          bool                   `json:"enabled"`
	State            Dynamic5hPressureState `json:"state"`
	LimitActive      bool                   `json:"limit_active"`
	CurrentlyLimited bool                   `json:"currently_limited"`
	UsagePercent     float64                `json:"usage_percent"`
	RemainingPercent float64                `json:"remaining_percent"`
	WindowStartedAt  *time.Time             `json:"window_started_at,omitempty"`
	RecoverAt        *time.Time             `json:"recover_at,omitempty"`
	Multiplier       float64                `json:"multiplier"`
	Exempt           bool                   `json:"exempt"`
	WarningLevel     string                 `json:"warning_level"`
	PendingPercent   float64                `json:"pending_percent"`
}

type Dynamic5hAdminUserStatus struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Active bool   `json:"active"`
	Dynamic5hUserStatus
}

type Dynamic5hUserPolicy struct {
	Exempt     bool    `json:"exempt"`
	Multiplier float64 `json:"multiplier"`
}

type Dynamic5hAuditLog struct {
	ID          int64                `json:"id"`
	UserID      int64                `json:"user_id"`
	ActorUserID *int64               `json:"actor_user_id,omitempty"`
	ActorEmail  string               `json:"actor_email"`
	Action      string               `json:"action"`
	Reason      string               `json:"reason"`
	BeforeUsage *float64             `json:"before_usage,omitempty"`
	AfterUsage  *float64             `json:"after_usage,omitempty"`
	OldPolicy   *Dynamic5hUserPolicy `json:"old_policy,omitempty"`
	NewPolicy   *Dynamic5hUserPolicy `json:"new_policy,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
}

type Dynamic5hPolicyRepository interface {
	GetUserPolicy(context.Context, int64) (Dynamic5hUserPolicy, bool, error)
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

type Dynamic5hPolicyCache interface {
	LoadUserPolicy(context.Context, int64) (Dynamic5hUserPolicy, error)
	StoreUserPolicy(context.Context, int64, Dynamic5hUserPolicy) error
	ResetUserUsage(context.Context, int64) error
}

type Dynamic5hAdminOverview struct {
	Pool                    Dynamic5hPressureStatus      `json:"pool"`
	Users                   []Dynamic5hAdminUserStatus   `json:"users"`
	Accounts                []Dynamic5hAccountDiagnostic `json:"accounts"`
	AuditLogs               []Dynamic5hAuditLog          `json:"audit_logs"`
	GuaranteedCapacityRatio float64                      `json:"guaranteed_capacity_ratio"`
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
	TouchActiveUser(ctx context.Context, userID int64, now, cutoff time.Time) error
	ActiveUserIDs(ctx context.Context, cutoff time.Time) ([]string, error)
	RollingUserIDs(ctx context.Context, cutoff time.Time) ([]string, error)
	RecordUsage(ctx context.Context, userID, bucket int64, now, cutoff time.Time, amount float64, ttl time.Duration) error
	MeterValues(ctx context.Context, latestBucket int64, count int) ([]float64, error)
	UserMeterValues(ctx context.Context, userID, latestBucket int64, count int) ([]float64, error)
	ReserveUserUsage(ctx context.Context, userID int64, token string, observed, limit, amount float64, now time.Time, ttl time.Duration) (bool, float64, error)
	SettleUserReservation(ctx context.Context, userID int64, token string) error
	PendingUserUsage(ctx context.Context, userID int64) (float64, error)
	ResetUserUsage(ctx context.Context, userID int64) error
}

type Dynamic5hPressureService struct {
	accountRepo AccountRepository
	userRepo    UserRepository
	policyRepo  Dynamic5hPolicyRepository
	cache       Dynamic5hPressureCache
	cfg         config.Dynamic5hPressureConfig

	mu          sync.RWMutex
	status      Dynamic5hPressureStatus
	refreshing  bool
	lastAttempt time.Time
	now         func() time.Time
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
	return svc
}

func normalizeDynamic5hPressureConfig(c config.Dynamic5hPressureConfig) config.Dynamic5hPressureConfig {
	if c.RefreshIntervalSeconds <= 0 {
		c.RefreshIntervalSeconds = 30
	}
	if c.PeakThreshold <= 0 {
		c.PeakThreshold = 0.8
	}
	if c.NormalThreshold <= 0 || c.NormalThreshold >= c.PeakThreshold {
		c.NormalThreshold = 0.7
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
	episode := previous.Episode
	if previous.State != Dynamic5hPressurePeak && state == Dynamic5hPressurePeak {
		episode = now.UnixNano()
	}
	activeUserIDs := s.activeUserIDs(ctx, now)
	activeUsers := int64(len(activeUserIDs))
	meterRate := s.recentMeterRate(ctx, now)
	poolMeterUnits := 0.0
	if burnPerHour > 0 && meterRate > 0 {
		poolMeterUnits = float64(len(windows)) * meterRate / burnPerHour
	}
	status := Dynamic5hPressureStatus{
		Enabled: true, DataAvailable: len(windows) > 0, CalibrationReady: poolMeterUnits > 0,
		Pressure: pressure, RawPressure: raw, State: state, PeakActive: state == Dynamic5hPressurePeak,
		AccountCount: len(windows), ActiveUserCount: activeUsers, RemainingCapacity: remaining,
		BurnRatePerHour: burnPerHour, ProjectedDemand: projected, PoolCapacity: poolMeterUnits,
		Episode: episode, EvaluatedAt: now,
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

// dynamic5hAccountAvailable mirrors the account-level runtime gates used by
// scheduling and also rejects a still-exhausted long window. A 5h reset must
// never make an account contribute capacity while its 7d window is exhausted.
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
			return status
		}
	} else {
		s.MaybeRefresh()
	}
	return s.loadStatus(ctx)
}

func (s *Dynamic5hPressureService) UserStatus(ctx context.Context, userID int64) Dynamic5hUserStatus {
	status := s.AdminStatus(ctx, false)
	return s.userStatus(ctx, userID, status, s.now().UTC())
}

func (s *Dynamic5hPressureService) userStatus(ctx context.Context, userID int64, status Dynamic5hPressureStatus, now time.Time) Dynamic5hUserStatus {
	result := Dynamic5hUserStatus{Enabled: status.Enabled, State: status.State}
	if !status.CalibrationReady || userID <= 0 || s.cache == nil {
		return result
	}
	fairShare, ok := s.currentFairShareForUser(ctx, status, now, userID)
	if !ok {
		return result
	}
	return s.userStatusWithFairShare(ctx, userID, status, now, fairShare)
}

func (s *Dynamic5hPressureService) userStatusWithFairShare(ctx context.Context, userID int64, status Dynamic5hPressureStatus, now time.Time, fairShare float64) Dynamic5hUserStatus {
	result := Dynamic5hUserStatus{Enabled: status.Enabled, State: status.State}
	policy := s.userPolicy(ctx, userID)
	share := fairShare * policy.Multiplier
	usage, recoverAt := s.rollingUserUsage(ctx, userID, now, share)
	pending := s.pendingUserUsage(ctx, userID)
	result.LimitActive = status.State == Dynamic5hPressurePeak && !policy.Exempt
	result.CurrentlyLimited = result.LimitActive && usage+pending >= share
	result.UsagePercent = math.Max(0, 100*usage/share)
	result.RemainingPercent = math.Max(0, 100-result.UsagePercent)
	result.Multiplier, result.Exempt = policy.Multiplier, policy.Exempt
	result.PendingPercent = math.Max(0, 100*pending/share)
	switch {
	case result.CurrentlyLimited:
		result.WarningLevel = "limited"
	case result.UsagePercent >= 100:
		result.WarningLevel = "borrowed"
	case result.UsagePercent >= 80:
		result.WarningLevel = "warning"
	default:
		result.WarningLevel = "normal"
	}
	started := now.Add(-dynamic5hWindow)
	result.WindowStartedAt = &started
	if !recoverAt.IsZero() {
		result.RecoverAt = &recoverAt
	}
	return result
}

func (s *Dynamic5hPressureService) AdminOverview(ctx context.Context) Dynamic5hAdminOverview {
	status := s.AdminStatus(ctx, true)
	overview := Dynamic5hAdminOverview{Pool: status, Users: []Dynamic5hAdminUserStatus{}, Accounts: []Dynamic5hAccountDiagnostic{}, AuditLogs: []Dynamic5hAuditLog{}}
	if s.accountRepo != nil {
		if accounts, err := s.accountRepo.ListAllWithFilters(ctx, "", "", "", "", 0, ""); err == nil {
			for i := range accounts {
				overview.Accounts = append(overview.Accounts, dynamic5hAccountDiagnostic(&accounts[i], s.now().UTC()))
			}
		}
	}
	if s.policyRepo != nil {
		if logs, err := s.policyRepo.ListAuditLogs(ctx, 50); err == nil {
			overview.AuditLogs = logs
		}
	}
	if !status.Enabled || !status.CalibrationReady || s.cache == nil {
		return overview
	}

	now := s.now().UTC()
	activeUserIDs := s.activeUserIDs(ctx, now)
	rollingUserIDs := s.rollingUserIDs(ctx, now)
	if len(rollingUserIDs) == 0 || status.PoolCapacity <= 0 {
		return overview
	}
	active := make(map[string]bool, len(activeUserIDs))
	for _, rawID := range activeUserIDs {
		active[rawID] = true
	}
	protectedUsers := 0
	guaranteed := 0.0
	for _, rawID := range activeUserIDs {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			continue
		}
		policy := s.userPolicy(ctx, id)
		if policy.Exempt {
			continue
		}
		protectedUsers++
		guaranteed += policy.Multiplier
	}
	if protectedUsers == 0 {
		protectedUsers = 1
	}
	fairShare := status.PoolCapacity / float64(protectedUsers)
	overview.GuaranteedCapacityRatio = guaranteed / float64(protectedUsers)
	for _, rawID := range rollingUserIDs {
		userID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || userID <= 0 {
			continue
		}
		entry := Dynamic5hAdminUserStatus{UserID: userID, Active: active[rawID], Dynamic5hUserStatus: s.userStatusWithFairShare(ctx, userID, status, now, fairShare)}
		if s.userRepo != nil {
			if user, err := s.userRepo.GetByID(ctx, userID); err == nil && user != nil {
				entry.Email = user.Email
			}
		}
		overview.Users = append(overview.Users, entry)
	}
	sort.Slice(overview.Users, func(i, j int) bool {
		if overview.Users[i].UsagePercent == overview.Users[j].UsagePercent {
			return overview.Users[i].UserID < overview.Users[j].UserID
		}
		return overview.Users[i].UsagePercent > overview.Users[j].UsagePercent
	})
	return overview
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

func (s *Dynamic5hPressureService) CheckUser(ctx context.Context, userID int64, platform ...string) error {
	if len(platform) > 0 && platform[0] != "" && !dynamic5hPlatformSupported(platform[0]) {
		return nil
	}
	status := s.AdminStatus(ctx, false)
	if status.State != Dynamic5hPressurePeak || !status.CalibrationReady || userID <= 0 || s.cache == nil {
		return nil
	}
	now := s.now().UTC()
	policy := s.userPolicy(ctx, userID)
	if policy.Exempt {
		return nil
	}
	fairShare, ok := s.currentFairShareForUser(ctx, status, now, userID)
	if !ok {
		return nil
	}
	fairShare *= policy.Multiplier
	usage, recoverAt := s.rollingUserUsage(ctx, userID, now, fairShare)
	token, _ := ctx.Value(ctxkey.RequestID).(string)
	reservation := math.Max(1, fairShare*0.01)
	if token != "" {
		allowed, _, err := s.cache.ReserveUserUsage(ctx, userID, token, usage, fairShare, reservation, now, 2*time.Minute)
		if err != nil {
			return nil
		}
		if allowed {
			s.releaseReservationWhenRequestEnds(ctx, userID, token)
			return nil
		}
	} else if usage+s.pendingUserUsage(ctx, userID) < fairShare {
		return nil
	}
	return ErrDynamic5hPressureLimitExceeded.WithMetadata(map[string]string{
		"usage_percent":     fmt.Sprintf("%.2f", 100*usage/fairShare),
		"remaining_percent": "0.00",
		"recover_at":        recoverAt.Format(time.RFC3339),
	})
}

func (s *Dynamic5hPressureService) releaseReservationWhenRequestEnds(ctx context.Context, userID int64, token string) {
	if ctx == nil || ctx.Done() == nil || token == "" {
		return
	}
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.cache.SettleUserReservation(releaseCtx, userID, token)
	}()
}

func (s *Dynamic5hPressureService) userPolicy(ctx context.Context, userID int64) Dynamic5hUserPolicy {
	p := Dynamic5hUserPolicy{Multiplier: 1}
	if s.policyRepo != nil {
		if loaded, found, err := s.policyRepo.GetUserPolicy(ctx, userID); err == nil && found {
			p = loaded
		}
	} else if c, ok := s.cache.(Dynamic5hPolicyCache); ok {
		if loaded, err := c.LoadUserPolicy(ctx, userID); err == nil {
			p = loaded
		}
	}
	if p.Multiplier <= 0 || math.IsNaN(p.Multiplier) || math.IsInf(p.Multiplier, 0) {
		p.Multiplier = 1
	}
	return p
}

func (s *Dynamic5hPressureService) SetUserPolicy(ctx context.Context, userID int64, policy Dynamic5hUserPolicy, actorAndReason ...any) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user id")
	}
	if policy.Multiplier <= 0 || math.IsNaN(policy.Multiplier) || math.IsInf(policy.Multiplier, 0) {
		return fmt.Errorf("multiplier must be a positive finite number")
	}
	actorID, reason := int64(0), ""
	if len(actorAndReason) > 0 {
		actorID, _ = actorAndReason[0].(int64)
	}
	if len(actorAndReason) > 1 {
		reason, _ = actorAndReason[1].(string)
	}
	if s.policyRepo != nil {
		return s.policyRepo.UpsertUserPolicy(ctx, userID, policy, actorID, strings.TrimSpace(reason))
	}
	c, ok := s.cache.(Dynamic5hPolicyCache)
	if !ok {
		return fmt.Errorf("dynamic 5h policy cache unavailable")
	}
	return c.StoreUserPolicy(ctx, userID, policy)
}

func (s *Dynamic5hPressureService) ResetUserUsage(ctx context.Context, userID int64, actorAndReason ...any) error {
	c, ok := s.cache.(Dynamic5hPolicyCache)
	if !ok {
		return fmt.Errorf("dynamic 5h policy cache unavailable")
	}
	now := s.now().UTC()
	before, _ := s.rollingUserUsage(ctx, userID, now, math.MaxFloat64)
	if err := c.ResetUserUsage(ctx, userID); err != nil {
		return err
	}
	actorID, reason := int64(0), ""
	if len(actorAndReason) > 0 {
		actorID, _ = actorAndReason[0].(int64)
	}
	if len(actorAndReason) > 1 {
		reason, _ = actorAndReason[1].(string)
	}
	if s.policyRepo != nil {
		return s.policyRepo.RecordUsageReset(ctx, userID, actorID, strings.TrimSpace(reason), before, 0)
	}
	return nil
}

// RecordUsage maintains calibration, active population, and each user's rolling
// usage in every state. Normal usage must remain visible if the pool enters Peak.
func (s *Dynamic5hPressureService) RecordUsage(ctx context.Context, userID int64, platform string, meterUnits float64) {
	if s == nil || s.cache == nil || userID <= 0 || meterUnits <= 0 || !dynamic5hPlatformSupported(platform) {
		return
	}
	now := s.now().UTC()
	if token, _ := ctx.Value(ctxkey.RequestID).(string); token != "" {
		_ = s.cache.SettleUserReservation(ctx, userID, token)
	}
	bucket := now.Unix() / int64(dynamic5hMeterBucket/time.Second)
	if err := s.cache.RecordUsage(ctx, userID, bucket, now, now.Add(-dynamic5hActiveLease), meterUnits, dynamic5hRedisTTL); err != nil {
		slog.Warn("dynamic 5h pressure calibration update failed; failing open", "error", err)
		return
	}
}

func dynamic5hPlatformSupported(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI, PlatformAnthropic, PlatformKimi, PlatformZhipu, PlatformMiniMax, PlatformOpenCodeGo, PlatformComposite:
		return true
	default:
		return false
	}
}

func (s *Dynamic5hPressureService) activeUserIDs(ctx context.Context, now time.Time) []string {
	if s.cache == nil {
		return nil
	}
	ids, err := s.cache.ActiveUserIDs(ctx, now.Add(-dynamic5hActiveLease))
	if err != nil {
		return nil
	}
	return ids
}

func (s *Dynamic5hPressureService) rollingUserIDs(ctx context.Context, now time.Time) []string {
	if s.cache == nil {
		return nil
	}
	ids, err := s.cache.RollingUserIDs(ctx, now.Add(-dynamic5hWindow))
	if err != nil {
		return nil
	}
	return ids
}

func (s *Dynamic5hPressureService) recentMeterRate(ctx context.Context, now time.Time) float64 {
	if s.cache == nil {
		return 0
	}
	bucketSeconds := int64(dynamic5hMeterBucket / time.Second)
	latest := now.Unix() / bucketSeconds
	count := int(dynamic5hWindow/dynamic5hMeterBucket) + 1
	values, err := s.cache.MeterValues(ctx, latest, count)
	if err != nil {
		return 0
	}
	var total float64
	first := -1
	for i, value := range values {
		if value <= 0 {
			continue
		}
		if first < 0 {
			first = i
		}
		total += value
	}
	if first < 0 || total <= 0 {
		return 0
	}
	firstBucket := latest - int64(count-1-first)
	observed := now.Sub(time.Unix(firstBucket*bucketSeconds, 0))
	if observed < dynamic5hMeterBucket {
		observed = dynamic5hMeterBucket
	}
	if observed > dynamic5hWindow {
		observed = dynamic5hWindow
	}
	return total / observed.Hours()
}

func (s *Dynamic5hPressureService) currentFairShareForUser(ctx context.Context, status Dynamic5hPressureStatus, now time.Time, candidateID int64) (float64, bool) {
	if s.cache == nil || !status.CalibrationReady || status.PoolCapacity <= 0 {
		return 0, false
	}
	users := s.activeUserIDs(ctx, now)
	count, found := 0, false
	for _, rawID := range users {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			continue
		}
		if id == candidateID {
			found = true
		}
		if !s.userPolicy(ctx, id).Exempt {
			count++
		}
	}
	if candidateID > 0 && !found && !s.userPolicy(ctx, candidateID).Exempt {
		count++
	}
	if count == 0 {
		count = 1
	}
	return status.PoolCapacity / float64(count), true
}

func (s *Dynamic5hPressureService) pendingUserUsage(ctx context.Context, userID int64) float64 {
	if s.cache == nil {
		return 0
	}
	value, err := s.cache.PendingUserUsage(ctx, userID)
	if err != nil {
		return 0
	}
	return math.Max(0, value)
}

// rollingUserUsage returns the last-five-hour usage and the first bucket expiry
// at which usage falls below the current fair share. The estimate naturally
// changes when pool capacity or the protected population changes.
func (s *Dynamic5hPressureService) rollingUserUsage(ctx context.Context, userID int64, now time.Time, fairShare float64) (float64, time.Time) {
	bucketSeconds := int64(dynamic5hMeterBucket / time.Second)
	latest := now.Unix() / bucketSeconds
	count := int(dynamic5hWindow/dynamic5hMeterBucket) + 1
	values, err := s.cache.UserMeterValues(ctx, userID, latest, count)
	if err != nil {
		return 0, time.Time{}
	}
	amounts := make([]float64, len(values))
	var total float64
	for i, value := range values {
		amounts[i] = math.Max(0, value)
		total += amounts[i]
	}
	remaining := total
	for i, amount := range amounts {
		if amount <= 0 {
			continue
		}
		remaining -= amount
		bucket := latest - int64(count-1-i)
		recover := time.Unix((bucket+1)*bucketSeconds, 0).Add(dynamic5hWindow).UTC()
		if total < fairShare || remaining < fairShare {
			return total, recover
		}
	}
	return total, time.Time{}
}

func (s *Dynamic5hPressureService) cachedStatus() Dynamic5hPressureStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Dynamic5hPressureService) loadStatus(ctx context.Context) Dynamic5hPressureStatus {
	if s.cache != nil {
		if status, found, err := s.cache.LoadStatus(ctx); err == nil && found {
			s.mu.Lock()
			s.status = status
			s.mu.Unlock()
			return status
		}
	}
	return s.cachedStatus()
}

func (s *Dynamic5hPressureService) storeStatus(ctx context.Context, status Dynamic5hPressureStatus) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	if s.cache != nil {
		if err := s.cache.StoreStatus(ctx, status); err != nil {
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
