package service

import "sync/atomic"

type dynamic5hMetrics struct {
	statusReadFailures     atomic.Uint64
	statusWriteFailures    atomic.Uint64
	refreshFailures        atomic.Uint64
	policyRefreshFailures  atomic.Uint64
	staleFailOpen          atomic.Uint64
	policyFailOpen         atomic.Uint64
	reservationFailOpen    atomic.Uint64
	settlementFailures     atomic.Uint64
	meterReadFailures      atomic.Uint64
	meterWriteFailures     atomic.Uint64
	denials                atomic.Uint64
	reconciliationFailures atomic.Uint64
	repairedMeterEvents    atomic.Uint64
	expiredMeterEvents     atomic.Uint64
}

type Dynamic5hMetricsSnapshot struct {
	StatusReadFailures     uint64 `json:"status_read_failures_total"`
	StatusWriteFailures    uint64 `json:"status_write_failures_total"`
	RefreshFailures        uint64 `json:"refresh_failures_total"`
	PolicyRefreshFailures  uint64 `json:"policy_refresh_failures_total"`
	StaleFailOpen          uint64 `json:"stale_fail_open_total"`
	PolicyFailOpen         uint64 `json:"policy_fail_open_total"`
	ReservationFailOpen    uint64 `json:"reservation_fail_open_total"`
	SettlementFailures     uint64 `json:"settlement_failures_total"`
	MeterReadFailures      uint64 `json:"meter_read_failures_total"`
	MeterWriteFailures     uint64 `json:"meter_write_failures_total"`
	Denials                uint64 `json:"denials_total"`
	ReconciliationFailures uint64 `json:"reconciliation_failures_total"`
	RepairedMeterEvents    uint64 `json:"repaired_meter_events_total"`
	ExpiredMeterEvents     uint64 `json:"expired_meter_events_total"`
}

func (s *Dynamic5hPressureService) Metrics() Dynamic5hMetricsSnapshot {
	return Dynamic5hMetricsSnapshot{
		StatusReadFailures:     s.metrics.statusReadFailures.Load(),
		StatusWriteFailures:    s.metrics.statusWriteFailures.Load(),
		RefreshFailures:        s.metrics.refreshFailures.Load(),
		PolicyRefreshFailures:  s.metrics.policyRefreshFailures.Load(),
		StaleFailOpen:          s.metrics.staleFailOpen.Load(),
		PolicyFailOpen:         s.metrics.policyFailOpen.Load(),
		ReservationFailOpen:    s.metrics.reservationFailOpen.Load(),
		SettlementFailures:     s.metrics.settlementFailures.Load(),
		MeterReadFailures:      s.metrics.meterReadFailures.Load(),
		MeterWriteFailures:     s.metrics.meterWriteFailures.Load(),
		Denials:                s.metrics.denials.Load(),
		ReconciliationFailures: s.metrics.reconciliationFailures.Load(),
		RepairedMeterEvents:    s.metrics.repairedMeterEvents.Load(),
		ExpiredMeterEvents:     s.metrics.expiredMeterEvents.Load(),
	}
}
