package service

import "sync/atomic"

type dynamic5hMetrics struct {
	statusReadFailures    atomic.Uint64
	statusWriteFailures   atomic.Uint64
	refreshFailures       atomic.Uint64
	policyRefreshFailures atomic.Uint64
	staleFailOpen         atomic.Uint64
	policyFailOpen        atomic.Uint64
	meterReadFailures     atomic.Uint64
	denials               atomic.Uint64
}

type Dynamic5hMetricsSnapshot struct {
	StatusReadFailures    uint64 `json:"status_read_failures_total"`
	StatusWriteFailures   uint64 `json:"status_write_failures_total"`
	RefreshFailures       uint64 `json:"refresh_failures_total"`
	PolicyRefreshFailures uint64 `json:"policy_refresh_failures_total"`
	StaleFailOpen         uint64 `json:"stale_fail_open_total"`
	PolicyFailOpen        uint64 `json:"policy_fail_open_total"`
	MeterReadFailures     uint64 `json:"meter_read_failures_total"`
	Denials               uint64 `json:"denials_total"`
}

func (s *Dynamic5hPressureService) Metrics() Dynamic5hMetricsSnapshot {
	return Dynamic5hMetricsSnapshot{
		StatusReadFailures:    s.metrics.statusReadFailures.Load(),
		StatusWriteFailures:   s.metrics.statusWriteFailures.Load(),
		RefreshFailures:       s.metrics.refreshFailures.Load(),
		PolicyRefreshFailures: s.metrics.policyRefreshFailures.Load(),
		StaleFailOpen:         s.metrics.staleFailOpen.Load(),
		PolicyFailOpen:        s.metrics.policyFailOpen.Load(),
		MeterReadFailures:     s.metrics.meterReadFailures.Load(),
		Denials:               s.metrics.denials.Load(),
	}
}
