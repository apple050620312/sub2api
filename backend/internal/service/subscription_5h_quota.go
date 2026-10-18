package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

const subscription5hWeeklyRatio = 0.15

type Subscription5hQuota struct {
	WeeklyLimitUSD float64
	UsageUSD       float64
	RecoverAt      *time.Time
	LastUsedAt     *time.Time
}

type Subscription5hQuotaRepository interface {
	ReadSubscription5hQuota(context.Context, int64, time.Time) (Subscription5hQuota, error)
	ListSubscription5hUserIDs(context.Context, time.Time) ([]int64, error)
}

func (s *Dynamic5hPressureService) subscriptionQuotaUserStatus(ctx context.Context, userID int64) (Dynamic5hUserStatus, error) {
	pressure := s.AdminStatus(ctx, false)
	result := Dynamic5hUserStatus{Enabled: s.cfg.Enabled, State: pressure.State, WarningLevel: "normal"}
	if userID <= 0 || s.quotaRepo == nil {
		return result, nil
	}
	policies, err := s.policySnapshot(ctx)
	if err != nil {
		s.metrics.policyFailOpen.Add(1)
		return result, err
	}
	policy := policies[userID]
	result.Exempt = policy.Exempt
	quota, err := s.quotaRepo.ReadSubscription5hQuota(ctx, userID, s.now().UTC())
	if err != nil {
		return result, err
	}
	result.SevenDayLimitUSD = quota.WeeklyLimitUSD
	result.LimitUSD = quota.WeeklyLimitUSD * subscription5hWeeklyRatio
	if result.LimitUSD <= 0 || math.IsNaN(result.LimitUSD) || math.IsInf(result.LimitUSD, 0) {
		return result, nil
	}
	result.QuotaAvailable = true
	result.UsageUSD = math.Max(0, quota.UsageUSD)
	result.LastUsedAt = quota.LastUsedAt
	result.RemainingUSD = math.Max(0, result.LimitUSD-result.UsageUSD)
	result.UsagePercent = 100 * result.UsageUSD / result.LimitUSD
	result.RemainingPercent = math.Max(0, 100-result.UsagePercent)
	result.LimitActive = result.Enabled && pressure.DataAvailable && !pressure.Stale && pressure.State == Dynamic5hPressurePeak && !policy.Exempt
	result.CurrentlyLimited = result.LimitActive && result.UsageUSD >= result.LimitUSD
	started := s.now().UTC().Add(-dynamic5hWindow)
	result.WindowStartedAt, result.RecoverAt = &started, quota.RecoverAt
	if result.CurrentlyLimited {
		result.WarningLevel = "limited"
	} else if result.UsagePercent >= 100 {
		result.WarningLevel = "borrowed"
	} else if result.UsagePercent >= 80 {
		result.WarningLevel = "warning"
	}
	return result, nil
}

func (s *Dynamic5hPressureService) checkSubscriptionQuota(ctx context.Context, userID int64) error {
	if !s.cfg.Enabled || userID <= 0 {
		return nil
	}
	pressure := s.AdminStatus(ctx, false)
	if !pressure.DataAvailable || pressure.Stale || pressure.State != Dynamic5hPressurePeak {
		if pressure.Stale {
			s.metrics.staleFailOpen.Add(1)
		}
		return nil
	}
	status, err := s.subscriptionQuotaUserStatus(ctx, userID)
	if err != nil {
		s.metrics.meterReadFailures.Add(1)
		return nil
	}
	if !status.CurrentlyLimited {
		return nil
	}
	s.metrics.denials.Add(1)
	metadata := map[string]string{
		"usage_percent":     fmt.Sprintf("%.2f", status.UsagePercent),
		"remaining_percent": "0.00",
	}
	if status.RecoverAt != nil {
		metadata["recover_at"] = status.RecoverAt.Format(time.RFC3339)
	}
	return ErrDynamic5hPressureLimitExceeded.WithMetadata(metadata)
}

func (s *Dynamic5hPressureService) subscriptionQuotaAdminOverview(ctx context.Context) Dynamic5hAdminOverview {
	overview := Dynamic5hAdminOverview{Pool: s.AdminStatus(ctx, true), Users: []Dynamic5hAdminUserStatus{}, Accounts: []Dynamic5hAccountDiagnostic{}, AuditLogs: []Dynamic5hAuditLog{}, Metrics: s.Metrics()}
	overview.Pool.ActiveUserCount = 0
	if logs, err := s.policyRepo.ListAuditLogs(ctx, 50); err == nil {
		overview.AuditLogs = logs
	}
	userIDs, err := s.quotaRepo.ListSubscription5hUserIDs(ctx, s.now().UTC())
	if err != nil {
		overview.Pool.DataAvailable, overview.Pool.Stale = false, true
		return overview
	}
	for _, userID := range userIDs {
		status, err := s.subscriptionQuotaUserStatus(ctx, userID)
		if err != nil {
			overview.Pool.DataAvailable, overview.Pool.Stale = false, true
			continue
		}
		entry := Dynamic5hAdminUserStatus{UserID: userID, Dynamic5hUserStatus: status}
		if s.userRepo != nil {
			if user, err := s.userRepo.GetByID(ctx, userID); err == nil && user != nil {
				entry.Email = user.Email
			}
		}
		entry.Active = status.LastUsedAt != nil && status.LastUsedAt.After(s.now().UTC().Add(-dynamic5hActiveLease))
		overview.Users = append(overview.Users, entry)
		if entry.Active {
			overview.Pool.ActiveUserCount++
		}
	}
	sort.Slice(overview.Users, func(first, second int) bool {
		if overview.Users[first].UsagePercent == overview.Users[second].UsagePercent {
			return overview.Users[first].UserID < overview.Users[second].UserID
		}
		return overview.Users[first].UsagePercent > overview.Users[second].UsagePercent
	})
	return overview
}

func (s *Dynamic5hPressureService) resetSubscriptionQuota(ctx context.Context, userID int64, actorAndReason ...any) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user id")
	}
	status, err := s.subscriptionQuotaUserStatus(ctx, userID)
	if err != nil {
		return err
	}
	var actorID int64
	if len(actorAndReason) > 0 {
		actorID, _ = actorAndReason[0].(int64)
	}
	return s.policyRepo.RecordUsageReset(ctx, userID, actorID, "", status.UsageUSD, 0)
}
