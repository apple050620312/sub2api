package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
)

type Dynamic5hMeterEvent struct {
	ID         int64
	SourceKey  string
	UserID     int64
	RequestID  string
	Amount     float64
	UserAmount float64
	OccurredAt time.Time
}

type Dynamic5hMeterRepository interface {
	EnqueueMeterEvent(context.Context, Dynamic5hMeterEvent) error
	ListMeterEvents(context.Context, int64, bool, time.Time, int) ([]Dynamic5hMeterEvent, error)
	AcknowledgeMeterEvent(context.Context, int64) error
	PruneMeterEvents(context.Context, time.Time) (int64, error)
}

type Dynamic5hMeterCache interface {
	ApplyMeterEvent(context.Context, Dynamic5hMeterEvent, time.Time) (bool, error)
}

type Dynamic5hReconciliationResult struct {
	Processed int   `json:"processed"`
	Repaired  int   `json:"repaired"`
	Expired   int   `json:"expired"`
	NextID    int64 `json:"next_id"`
	More      bool  `json:"more"`
}

func (s *Dynamic5hPressureService) StartMeterReconciliation() {
	repo, ok := s.policyRepo.(Dynamic5hMeterRepository)
	if !ok {
		return
	}
	s.meterStart.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.meterCancel = cancel
		s.meterDone = make(chan struct{})
		s.meterWake = make(chan struct{}, 1)
		go func() {
			defer close(s.meterDone)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			lastPrune := time.Time{}
			for {
				workCtx, finish := context.WithTimeout(ctx, 10*time.Second)
				for batch := 0; batch < 16; batch++ {
					result, err := s.ReconcileMeters(workCtx, false, 0)
					if err != nil || !result.More {
						break
					}
				}
				if s.now().Sub(lastPrune) > time.Hour {
					for workCtx.Err() == nil {
						deleted, err := repo.PruneMeterEvents(workCtx, s.now().Add(-7*24*time.Hour))
						if err != nil {
							s.metrics.meterPruneFailures.Add(1)
							break
						}
						if deleted < 1000 {
							lastPrune = s.now()
							break
						}
					}
				}
				finish()
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				case <-s.meterWake:
				}
			}
		}()
	})
}

func (s *Dynamic5hPressureService) StopMeterReconciliation() {
	if s.meterCancel != nil {
		s.meterCancel()
		<-s.meterDone
	}
}

func (s *Dynamic5hPressureService) NotifyMeterReconciliation() {
	if s.meterWake == nil {
		return
	}
	select {
	case s.meterWake <- struct{}{}:
	default:
	}
}

func (s *Dynamic5hPressureService) enqueueStandaloneMeter(ctx context.Context, repo Dynamic5hMeterRepository, userID int64, amount float64) error {
	requestID, _ := ctx.Value(ctxkey.RequestID).(string)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	err := repo.EnqueueMeterEvent(ctx, Dynamic5hMeterEvent{SourceKey: fmt.Sprintf("standalone:%d:%s", userID, requestID), UserID: userID, RequestID: requestID, Amount: amount, OccurredAt: s.now().UTC()})
	if err == nil {
		s.NotifyMeterReconciliation()
	}
	return err
}

func (s *Dynamic5hPressureService) ReconcileMeters(ctx context.Context, replay bool, afterID int64) (result Dynamic5hReconciliationResult, err error) {
	defer func() {
		if err != nil {
			s.metrics.reconciliationFailures.Add(1)
		}
	}()
	repo, ok := s.policyRepo.(Dynamic5hMeterRepository)
	cache, cacheOK := s.cache.(Dynamic5hMeterCache)
	if !ok || !cacheOK {
		return result, fmt.Errorf("durable dynamic 5h metering unavailable")
	}
	now := s.now().UTC()
	cutoff := time.Time{}
	if replay {
		cutoff = now.Add(-dynamic5hWindow - dynamic5hMeterBucket)
	}
	events, err := repo.ListMeterEvents(ctx, afterID, !replay, cutoff, 250)
	if err != nil {
		return result, err
	}
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if event.OccurredAt.Before(now.Add(-dynamic5hWindow - dynamic5hMeterBucket)) {
			result.Expired++
			s.metrics.expiredMeterEvents.Add(1)
		} else {
			applied, err := cache.ApplyMeterEvent(ctx, event, now)
			if err != nil {
				return result, err
			}
			if applied {
				result.Repaired++
				s.metrics.repairedMeterEvents.Add(1)
			}
		}
		if err := repo.AcknowledgeMeterEvent(ctx, event.ID); err != nil {
			return result, err
		}
		if event.RequestID != "" {
			if err := s.cache.SettleUserReservation(ctx, event.UserID, event.RequestID); err != nil {
				s.metrics.settlementFailures.Add(1)
			}
		}
		result.Processed++
		result.NextID = event.ID
	}
	result.More = len(events) == 250
	return result, nil
}

func (s *BillingCacheService) HasDurableDynamic5hMetering() bool {
	if s == nil || s.dynamic5hPressure == nil {
		return false
	}
	_, ok := s.dynamic5hPressure.policyRepo.(Dynamic5hMeterRepository)
	return ok
}

func (s *BillingCacheService) NotifyDynamic5hMetering() {
	if s != nil && s.dynamic5hPressure != nil {
		s.dynamic5hPressure.NotifyMeterReconciliation()
	}
}

func (s *BillingCacheService) ApplyCommittedDynamic5hMeter(ctx context.Context, event *Dynamic5hMeterEvent) {
	if event == nil || !s.HasDurableDynamic5hMetering() {
		return
	}
	pressure := s.dynamic5hPressure
	repo, repoOK := pressure.policyRepo.(Dynamic5hMeterRepository)
	cache, ok := pressure.cache.(Dynamic5hMeterCache)
	if !ok || !repoOK {
		return
	}
	if _, err := cache.ApplyMeterEvent(ctx, *event, pressure.now().UTC()); err != nil {
		pressure.metrics.meterWriteFailures.Add(1)
		return
	}
	if err := repo.AcknowledgeMeterEvent(ctx, event.ID); err != nil {
		pressure.metrics.reconciliationFailures.Add(1)
	}
	if err := pressure.cache.SettleUserReservation(ctx, event.UserID, event.RequestID); err != nil {
		pressure.metrics.settlementFailures.Add(1)
	}
}
