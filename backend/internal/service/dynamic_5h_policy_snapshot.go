package service

import (
	"context"
	"fmt"
	"math"
	"time"
)

func normalizedDynamic5hPolicy(policy Dynamic5hUserPolicy) Dynamic5hUserPolicy {
	if policy.Multiplier <= 0 || math.IsNaN(policy.Multiplier) || math.IsInf(policy.Multiplier, 0) {
		policy.Multiplier = 1
	}
	return policy
}

func (s *Dynamic5hPressureService) policySnapshot(ctx context.Context) (map[int64]Dynamic5hUserPolicy, error) {
	if s.policyRepo == nil {
		return nil, nil
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	now := s.now().UTC()
	age := now.Sub(s.policyLoadedAt)
	if s.policies != nil && age >= 0 && age < 30*time.Second {
		return s.policies, nil
	}
	if now.Sub(s.policyAttemptAt) >= 5*time.Second || s.policyAttemptAt.IsZero() {
		s.policyAttemptAt = now
		policies, err := s.policyRepo.ListUserPolicies(ctx)
		if err == nil {
			if policies == nil {
				policies = make(map[int64]Dynamic5hUserPolicy)
			}
			s.policies = policies
			s.policyLoadedAt = now
			return policies, nil
		}
		s.metrics.policyRefreshFailures.Add(1)
	}
	if s.policies != nil && age >= 0 && age < 2*time.Minute {
		return s.policies, nil
	}
	return nil, fmt.Errorf("dynamic 5h policy snapshot unavailable")
}

func (s *Dynamic5hPressureService) usableStatus(status Dynamic5hPressureStatus) Dynamic5hPressureStatus {
	maxAge := 3 * time.Duration(s.cfg.RefreshIntervalSeconds) * time.Second
	if maxAge < 90*time.Second {
		maxAge = 90 * time.Second
	}
	age := s.now().UTC().Sub(status.EvaluatedAt)
	status.Stale = status.EvaluatedAt.IsZero() || age < -5*time.Second || age > maxAge
	status.Enabled = s.cfg.Enabled
	if !status.Enabled || status.Stale {
		status.State = Dynamic5hPressureNormal
		status.PeakActive = false
		status.DataAvailable = false
		status.CalibrationReady = false
	}
	return status
}
