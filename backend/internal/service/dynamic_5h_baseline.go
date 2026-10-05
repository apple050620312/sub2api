package service

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"
)

type Dynamic5hPlusObservation struct {
	AccountID   int64
	ResetAt     time.Time
	ObservedAt  time.Time
	UsedPercent float64
}

type Dynamic5hBaselineRepository interface {
	ObservePlusBaseline(context.Context, []Dynamic5hPlusObservation, time.Time) (float64, int, error)
}

func dynamic5hPlusObservation(account *Account, now time.Time) (Dynamic5hPlusObservation, bool) {
	if account.Platform != PlatformOpenAI || strings.ToLower(strings.TrimSpace(account.GetCredential("plan_type"))) != "plus" || !openAICodexSnapshotIdentityTrusted(account) {
		return Dynamic5hPlusObservation{}, false
	}
	used, ok := resolveAccountExtraNumber(account.Extra, "codex_5h_used_percent")
	reset, hasReset := openAICodexWindowResetAt(account.Extra, "5h")
	observed := parseSchedulingResetAt(account.Extra["codex_usage_updated_at"])
	if !ok || !hasReset || observed == nil || used < 0 || used > 100 || math.IsNaN(used) || math.IsInf(used, 0) || observed.After(now) || now.Sub(*observed) > 15*time.Minute || !reset.After(*observed) || observed.Before(reset.Add(-dynamic5hWindow)) {
		return Dynamic5hPlusObservation{}, false
	}
	return Dynamic5hPlusObservation{AccountID: account.ID, ResetAt: reset, ObservedAt: *observed, UsedPercent: used}, true
}

func (s *Dynamic5hPressureService) plusBaseline(ctx context.Context, accounts []Account, now time.Time) (float64, int) {
	repo, ok := s.policyRepo.(Dynamic5hBaselineRepository)
	if !ok {
		return 0, 0
	}
	observations := make([]Dynamic5hPlusObservation, 0, len(accounts))
	for index := range accounts {
		if observation, valid := dynamic5hPlusObservation(&accounts[index], now); valid {
			observations = append(observations, observation)
		}
	}
	baseline, samples, err := repo.ObservePlusBaseline(ctx, observations, now)
	if err != nil {
		slog.Warn("dynamic 5h Plus calibration failed; additional user limits disabled", "error", err)
		return 0, 0
	}
	if baseline < 0 || math.IsNaN(baseline) || math.IsInf(baseline, 0) {
		return 0, 0
	}
	return baseline, samples
}
