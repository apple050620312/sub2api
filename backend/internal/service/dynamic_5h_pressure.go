package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/redis/go-redis/v9"
)

type Dynamic5hPressureState string

const (
	Dynamic5hPressureNormal Dynamic5hPressureState = "normal"
	Dynamic5hPressurePeak   Dynamic5hPressureState = "peak"

	dynamic5hStatusKey       = "dynamic_5h_pressure:status"
	dynamic5hRefreshLockKey  = "dynamic_5h_pressure:refresh_lock"
	dynamic5hActiveUsersKey  = "dynamic_5h_pressure:active_users"
	dynamic5hMeterBucketBase = "dynamic_5h_pressure:meter:"
	dynamic5hUserMeterBase   = "dynamic_5h_pressure:user_meter:"
	dynamic5hWindow          = 5 * time.Hour
	dynamic5hMeterBucket     = 5 * time.Minute
	dynamic5hRedisTTL        = 6 * time.Hour
)

var ErrDynamic5hPressureLimitExceeded = infraerrors.TooManyRequests(
	"DYNAMIC_5H_PRESSURE_LIMIT_EXCEEDED",
	"Your temporary rolling five-hour usage limit has been reached. Please retry after it recovers.",
)

// Dynamic5hPressureStatus is the administrator view. RemainingCapacity and
// demand fields use normalized account-window units; PoolCapacity is the
// calibrated internal meter capacity and is never returned by the user API.
type Dynamic5hPressureStatus struct {
	Enabled           bool                   `json:"enabled"`
	DataAvailable     bool                   `json:"data_available"`
	CalibrationReady  bool                   `json:"calibration_ready"`
	Pressure          float64                `json:"pressure"`
	RawPressure       float64                `json:"raw_pressure"`
	State             Dynamic5hPressureState `json:"state"`
	PeakActive        bool                   `json:"peak_active"`
	AccountCount      int                    `json:"account_count"`
	ActiveUserCount   int64                  `json:"active_user_count"`
	RemainingCapacity float64                `json:"remaining_capacity"`
	BurnRatePerHour   float64                `json:"burn_rate_per_hour"`
	ProjectedDemand   float64                `json:"projected_demand"`
	PoolCapacity      float64                `json:"pool_capacity"`
	Episode           int64                  `json:"episode"`
	EvaluatedAt       time.Time              `json:"evaluated_at"`
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
}

type dynamic5hAccountWindow struct {
	used    float64
	resetAt time.Time
}

type Dynamic5hPressureService struct {
	accountRepo AccountRepository
	rdb         *redis.Client
	cfg         config.Dynamic5hPressureConfig

	mu          sync.RWMutex
	status      Dynamic5hPressureStatus
	refreshing  bool
	lastAttempt time.Time
	now         func() time.Time
}

func NewDynamic5hPressureService(accountRepo AccountRepository, rdb *redis.Client, cfg *config.Config) *Dynamic5hPressureService {
	pressureCfg := config.Dynamic5hPressureConfig{}
	if cfg != nil {
		pressureCfg = cfg.Gateway.Dynamic5hPressure
	}
	pressureCfg = normalizeDynamic5hPressureConfig(pressureCfg)
	s := &Dynamic5hPressureService{accountRepo: accountRepo, rdb: rdb, cfg: pressureCfg, now: time.Now}
	s.status = Dynamic5hPressureStatus{Enabled: pressureCfg.Enabled, State: Dynamic5hPressureNormal}
	return s
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
	for i := range accounts {
		if w, ok := dynamic5hWindowForAccount(&accounts[i], now); ok {
			windows = append(windows, w)
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
	}
	s.storeStatus(ctx, status)
	return status, nil
}

func calculateDynamic5hPressure(windows []dynamic5hAccountWindow, now time.Time) (pressure, remaining, burnPerHour, projected float64) {
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
		projected += accountBurn * remainingHours
	}
	if remaining <= 0 {
		if projected > 0 {
			return 100, remaining, burnPerHour, projected
		}
		return 0, remaining, burnPerHour, projected
	}
	return projected / remaining, remaining, burnPerHour, projected
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
		reset := parseSchedulingResetAt(a.Extra["codex_5h_reset_at"])
		if !ok || reset == nil || !reset.After(now) {
			return dynamic5hAccountWindow{}, false
		}
		return dynamic5hAccountWindow{used: used / 100, resetAt: *reset}, true
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
	result := Dynamic5hUserStatus{Enabled: status.Enabled, State: status.State}
	if !status.CalibrationReady || userID <= 0 || s.rdb == nil {
		return result
	}
	now := s.now().UTC()
	fairShare, ok := s.currentFairShare(ctx, status, now)
	if !ok {
		return result
	}
	usage, recoverAt := s.rollingUserUsage(ctx, userID, now, fairShare)
	result.LimitActive = status.State == Dynamic5hPressurePeak
	result.CurrentlyLimited = result.LimitActive && usage >= fairShare
	result.UsagePercent = math.Max(0, 100*usage/fairShare)
	result.RemainingPercent = math.Max(0, 100-result.UsagePercent)
	started := now.Add(-dynamic5hWindow)
	result.WindowStartedAt = &started
	if !recoverAt.IsZero() {
		result.RecoverAt = &recoverAt
	}
	return result
}

func (s *Dynamic5hPressureService) CheckUser(ctx context.Context, userID int64, platform ...string) error {
	if len(platform) > 0 && platform[0] != "" && !dynamic5hPlatformSupported(platform[0]) {
		return nil
	}
	status := s.AdminStatus(ctx, false)
	if status.State != Dynamic5hPressurePeak || !status.CalibrationReady || userID <= 0 || s.rdb == nil {
		return nil
	}
	now := s.now().UTC()
	s.touchActiveUser(ctx, userID, now)
	fairShare, ok := s.currentFairShare(ctx, status, now)
	if !ok {
		return nil
	}
	usage, recoverAt := s.rollingUserUsage(ctx, userID, now, fairShare)
	if usage < fairShare {
		return nil
	}
	return ErrDynamic5hPressureLimitExceeded.WithMetadata(map[string]string{
		"usage_percent":     fmt.Sprintf("%.2f", 100*usage/fairShare),
		"remaining_percent": "0.00",
		"recover_at":        recoverAt.Format(time.RFC3339),
	})
}

// RecordUsage maintains calibration, active population, and each user's rolling
// usage in every state. Normal usage must remain visible if the pool enters Peak.
func (s *Dynamic5hPressureService) RecordUsage(ctx context.Context, userID int64, platform string, meterUnits float64) {
	if s == nil || s.rdb == nil || userID <= 0 || meterUnits <= 0 || !dynamic5hPlatformSupported(platform) {
		return
	}
	now := s.now().UTC()
	bucket := now.Unix() / int64(dynamic5hMeterBucket/time.Second)
	pipe := s.rdb.TxPipeline()
	pipe.ZAdd(ctx, dynamic5hActiveUsersKey, redis.Z{Score: float64(now.Unix()), Member: strconv.FormatInt(userID, 10)})
	pipe.ZRemRangeByScore(ctx, dynamic5hActiveUsersKey, "-inf", strconv.FormatInt(now.Add(-dynamic5hWindow).Unix(), 10))
	key := fmt.Sprintf("%s%d", dynamic5hMeterBucketBase, bucket)
	pipe.IncrByFloat(ctx, key, meterUnits)
	pipe.Expire(ctx, key, dynamic5hRedisTTL)
	userKey := dynamic5hUserMeterKey(userID, bucket)
	pipe.IncrByFloat(ctx, userKey, meterUnits)
	pipe.Expire(ctx, userKey, dynamic5hRedisTTL)
	if _, err := pipe.Exec(ctx); err != nil {
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

func (s *Dynamic5hPressureService) touchActiveUser(ctx context.Context, userID int64, now time.Time) {
	pipe := s.rdb.TxPipeline()
	pipe.ZAdd(ctx, dynamic5hActiveUsersKey, redis.Z{Score: float64(now.Unix()), Member: strconv.FormatInt(userID, 10)})
	pipe.ZRemRangeByScore(ctx, dynamic5hActiveUsersKey, "-inf", strconv.FormatInt(now.Add(-dynamic5hWindow).Unix(), 10))
	_, _ = pipe.Exec(ctx)
}

func (s *Dynamic5hPressureService) activeUserIDs(ctx context.Context, now time.Time) []string {
	if s.rdb == nil {
		return nil
	}
	ids, err := s.rdb.ZRangeByScore(ctx, dynamic5hActiveUsersKey, &redis.ZRangeBy{
		Min: strconv.FormatInt(now.Add(-dynamic5hWindow).Unix(), 10), Max: "+inf",
	}).Result()
	if err != nil {
		return nil
	}
	return ids
}

func (s *Dynamic5hPressureService) recentMeterRate(ctx context.Context, now time.Time) float64 {
	if s.rdb == nil {
		return 0
	}
	bucketSeconds := int64(dynamic5hMeterBucket / time.Second)
	latest := now.Unix() / bucketSeconds
	count := int(dynamic5hWindow/dynamic5hMeterBucket) + 1
	keys := make([]string, 0, count)
	for i := count - 1; i >= 0; i-- {
		keys = append(keys, fmt.Sprintf("%s%d", dynamic5hMeterBucketBase, latest-int64(i)))
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return 0
	}
	var total float64
	first := -1
	for i, value := range values {
		if value == nil {
			continue
		}
		v, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		if err != nil || v <= 0 {
			continue
		}
		if first < 0 {
			first = i
		}
		total += v
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

func dynamic5hUserMeterKey(userID, bucket int64) string {
	return fmt.Sprintf("%s%d:%d", dynamic5hUserMeterBase, userID, bucket)
}

func (s *Dynamic5hPressureService) currentFairShare(ctx context.Context, status Dynamic5hPressureStatus, now time.Time) (float64, bool) {
	if s.rdb == nil || !status.CalibrationReady || status.PoolCapacity <= 0 {
		return 0, false
	}
	users := s.activeUserIDs(ctx, now)
	if len(users) == 0 {
		return 0, false
	}
	return status.PoolCapacity / float64(len(users)), true
}

// rollingUserUsage returns the last-five-hour usage and the first bucket expiry
// at which usage falls below the current fair share. The estimate naturally
// changes when pool capacity or the protected population changes.
func (s *Dynamic5hPressureService) rollingUserUsage(ctx context.Context, userID int64, now time.Time, fairShare float64) (float64, time.Time) {
	bucketSeconds := int64(dynamic5hMeterBucket / time.Second)
	latest := now.Unix() / bucketSeconds
	count := int(dynamic5hWindow/dynamic5hMeterBucket) + 1
	keys := make([]string, 0, count)
	for i := count - 1; i >= 0; i-- {
		keys = append(keys, dynamic5hUserMeterKey(userID, latest-int64(i)))
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return 0, time.Time{}
	}
	amounts := make([]float64, len(values))
	var total float64
	for i, value := range values {
		if value == nil {
			continue
		}
		amounts[i], _ = strconv.ParseFloat(fmt.Sprint(value), 64)
		total += math.Max(0, amounts[i])
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
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, dynamic5hStatusKey).Bytes(); err == nil {
			var status Dynamic5hPressureStatus
			if json.Unmarshal(raw, &status) == nil {
				s.mu.Lock()
				s.status = status
				s.mu.Unlock()
				return status
			}
		}
	}
	return s.cachedStatus()
}

func (s *Dynamic5hPressureService) storeStatus(ctx context.Context, status Dynamic5hPressureStatus) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	if s.rdb != nil {
		if raw, err := json.Marshal(status); err == nil {
			if err := s.rdb.Set(ctx, dynamic5hStatusKey, raw, 0).Err(); err != nil {
				slog.Warn("dynamic 5h pressure status persistence failed", "error", err)
			}
		}
	}
}

func (s *Dynamic5hPressureService) acquireRefreshLock(ctx context.Context) (string, bool) {
	if s.rdb == nil {
		return "", true
	}
	token := fmt.Sprintf("%d", s.now().UnixNano())
	ok, err := s.rdb.SetNX(ctx, dynamic5hRefreshLockKey, token, 15*time.Second).Result()
	return token, err == nil && ok
}

func (s *Dynamic5hPressureService) releaseRefreshLock(ctx context.Context, token string) {
	if s.rdb == nil || token == "" {
		return
	}
	_, _ = releaseDynamic5hRefreshLockScript.Run(ctx, s.rdb, []string{dynamic5hRefreshLockKey}, token).Result()
}

var releaseDynamic5hRefreshLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
