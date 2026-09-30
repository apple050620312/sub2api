package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type Dynamic5hReplayUser struct {
	ID     int64               `json:"id"`
	Active bool                `json:"active"`
	Policy Dynamic5hUserPolicy `json:"policy"`
}

type Dynamic5hReplayEvent struct {
	AtSeconds           float64                `json:"at_seconds"`
	UserID              int64                  `json:"user_id"`
	Platform            string                 `json:"platform"`
	Units               float64                `json:"units"`
	DurationSeconds     float64                `json:"duration_seconds"`
	State               Dynamic5hPressureState `json:"state,omitempty"`
	PoolCapacity        float64                `json:"pool_capacity,omitempty"`
	ResourcePool        string                 `json:"resource_pool,omitempty"`
	ResourceCostPerUnit float64                `json:"resource_cost_per_unit,omitempty"`
}

type Dynamic5hReplayTrace struct {
	Name               string                 `json:"name"`
	State              Dynamic5hPressureState `json:"state"`
	PoolCapacity       float64                `json:"pool_capacity"`
	Users              []Dynamic5hReplayUser  `json:"users"`
	Events             []Dynamic5hReplayEvent `json:"events"`
	ResourceCapacities map[string]float64     `json:"resource_capacities,omitempty"`
}

type Dynamic5hReplayUserResult struct {
	UserID      int64   `json:"user_id"`
	Requests    int     `json:"requests"`
	Admitted    int     `json:"admitted"`
	Denied      int     `json:"denied"`
	DemandUnits float64 `json:"demand_units"`
	ServedUnits float64 `json:"served_units"`
}

type Dynamic5hReplayReport struct {
	MeterUnit           string                      `json:"meter_unit"`
	Name                string                      `json:"name"`
	Users               []Dynamic5hReplayUserResult `json:"users"`
	JainServedUnits     float64                     `json:"jain_served_units"`
	ResourceUsage       map[string]float64          `json:"resource_usage"`
	ResourceUtilization map[string]float64          `json:"resource_utilization"`
	Metrics             Dynamic5hMetricsSnapshot    `json:"metrics"`
}

func ReplayDynamic5hTraffic(ctx context.Context, cache Dynamic5hPressureCache, trace Dynamic5hReplayTrace, advance func(time.Duration)) (Dynamic5hReplayReport, error) {
	report := Dynamic5hReplayReport{Name: trace.Name, Users: []Dynamic5hReplayUserResult{}, ResourceUsage: map[string]float64{}, ResourceUtilization: map[string]float64{}}
	if cache == nil || trace.PoolCapacity <= 0 || !finiteReplayNumber(trace.PoolCapacity) {
		report.MeterUnit = "model_price_cost"
		return report, fmt.Errorf("replay requires a cache and positive finite capacity")
	}
	if trace.State != Dynamic5hPressureNormal && trace.State != Dynamic5hPressurePeak {
		return report, fmt.Errorf("invalid initial pressure state")
	}
	report.MeterUnit = "model_price_cost"
	current := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	origin := current
	svc := NewDynamic5hPressureService(nil, cache, &config.Config{Gateway: config.GatewayConfig{Dynamic5hPressure: config.Dynamic5hPressureConfig{Enabled: true}}})
	svc.now = func() time.Time { return current }
	policyCache, ok := cache.(Dynamic5hPolicyCache)
	if !ok {
		return report, fmt.Errorf("replay cache must support policies")
	}
	for _, user := range trace.Users {
		if user.ID <= 0 {
			return report, fmt.Errorf("invalid replay user")
		}
		if err := policyCache.StoreUserPolicy(ctx, user.ID, normalizedDynamic5hPolicy(user.Policy)); err != nil {
			return report, err
		}
		if user.Active {
			if err := cache.TouchActiveUser(ctx, user.ID, current, current.Add(-dynamic5hActiveLease)); err != nil {
				return report, err
			}
		}
	}
	type scheduledEvent struct {
		event    Dynamic5hReplayEvent
		index    int
		complete bool
	}
	scheduled := make([]scheduledEvent, 0, len(trace.Events)*2)
	for index, event := range trace.Events {
		if !finiteReplayNumber(event.AtSeconds) || !finiteReplayNumber(event.DurationSeconds) || !finiteReplayNumber(event.Units) || !finiteReplayNumber(event.ResourceCostPerUnit) || event.AtSeconds < 0 || event.DurationSeconds < 0 || event.Units < 0 || event.ResourceCostPerUnit < 0 {
			return report, fmt.Errorf("invalid replay event %d", index)
		}
		if event.State != "" && event.State != Dynamic5hPressureNormal && event.State != Dynamic5hPressurePeak {
			return report, fmt.Errorf("invalid pressure state at event %d", index)
		}
		if event.UserID > 0 && !dynamic5hPlatformSupported(event.Platform) {
			return report, fmt.Errorf("unsupported platform at event %d", index)
		}
		scheduled = append(scheduled, scheduledEvent{event: event, index: index})
	}
	users := map[int64]*Dynamic5hReplayUserResult{}
	admitted := map[int]bool{}
	state, capacity := trace.State, trace.PoolCapacity
	for len(scheduled) > 0 {
		sort.SliceStable(scheduled, func(first, second int) bool {
			if scheduled[first].event.AtSeconds != scheduled[second].event.AtSeconds {
				return scheduled[first].event.AtSeconds < scheduled[second].event.AtSeconds
			}
			return scheduled[first].complete && !scheduled[second].complete
		})
		next := scheduled[0]
		scheduled = scheduled[1:]
		event := next.event
		at := origin.Add(time.Duration(event.AtSeconds * float64(time.Second)))
		if advance != nil {
			advance(at.Sub(current))
		}
		current = at
		if !next.complete && event.State != "" {
			state = event.State
		}
		if !next.complete && event.PoolCapacity > 0 {
			capacity = event.PoolCapacity
		}
		svc.storeStatus(ctx, Dynamic5hPressureStatus{Enabled: true, DataAvailable: true, CalibrationReady: true, State: state, PeakActive: state == Dynamic5hPressurePeak, PoolCapacity: capacity, EvaluatedAt: current})
		if event.UserID <= 0 {
			continue
		}
		token := fmt.Sprintf("replay-%d", next.index)
		requestCtx := context.WithValue(ctx, ctxkey.RequestID, token)
		if next.complete {
			if admitted[next.index] {
				svc.RecordUsage(requestCtx, event.UserID, event.Platform, event.Units)
				users[event.UserID].ServedUnits += event.Units
				if event.ResourcePool != "" {
					report.ResourceUsage[event.ResourcePool] += event.Units * event.ResourceCostPerUnit
				}
			}
			continue
		}
		if users[event.UserID] == nil {
			users[event.UserID] = &Dynamic5hReplayUserResult{UserID: event.UserID}
		}
		user := users[event.UserID]
		user.Requests++
		user.DemandUnits += event.Units
		if err := svc.CheckUser(requestCtx, event.UserID, event.Platform); err != nil {
			user.Denied++
			continue
		}
		user.Admitted++
		admitted[next.index] = true
		event.AtSeconds += event.DurationSeconds
		scheduled = append(scheduled, scheduledEvent{event: event, index: next.index, complete: true})
	}
	var sum, squared float64
	for _, user := range users {
		report.Users = append(report.Users, *user)
		sum += user.ServedUnits
		squared += user.ServedUnits * user.ServedUnits
	}
	sort.Slice(report.Users, func(first, second int) bool { return report.Users[first].UserID < report.Users[second].UserID })
	if squared > 0 {
		report.JainServedUnits = sum * sum / (float64(len(users)) * squared)
	}
	for pool, usage := range report.ResourceUsage {
		if budget := trace.ResourceCapacities[pool]; budget > 0 {
			report.ResourceUtilization[pool] = usage / budget
		}
	}
	report.Metrics = svc.Metrics()
	return report, nil
}

func finiteReplayNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
