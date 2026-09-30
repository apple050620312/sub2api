//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func replayDynamic5h(t *testing.T, trace service.Dynamic5hReplayTrace) service.Dynamic5hReplayReport {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	report, err := service.ReplayDynamic5hTraffic(context.Background(), NewDynamic5hPressureCache(client), trace, server.FastForward)
	require.NoError(t, err)
	return report
}

func TestDynamic5hTrafficReplayEqualDemandAndHeterogeneousResources(t *testing.T) {
	trace := service.Dynamic5hReplayTrace{Name: "equal", State: service.Dynamic5hPressurePeak, PoolCapacity: 500}
	for userID := int64(1); userID <= 5; userID++ {
		trace.Users = append(trace.Users, service.Dynamic5hReplayUser{ID: userID, Active: true})
	}
	for round := 0; round < 8; round++ {
		for userID := int64(1); userID <= 5; userID++ {
			trace.Events = append(trace.Events, service.Dynamic5hReplayEvent{AtSeconds: float64(round), UserID: userID, Platform: service.PlatformOpenAI, Units: 20})
		}
	}
	report := replayDynamic5h(t, trace)
	require.InDelta(t, 1, report.JainServedUnits, 1e-9)
	for _, user := range report.Users {
		require.Equal(t, 5, user.Admitted)
		require.Equal(t, 3, user.Denied)
		require.EqualValues(t, 100, user.ServedUnits)
	}
	trace.ResourceCapacities = map[string]float64{"small": 100, "large": 100}
	for index := range trace.Events {
		if trace.Events[index].UserID == 1 {
			trace.Events[index].ResourcePool = "small"
			trace.Events[index].ResourceCostPerUnit = 2
		} else {
			trace.Events[index].ResourcePool = "large"
			trace.Events[index].ResourceCostPerUnit = 0.1
		}
	}
	heterogeneous := replayDynamic5h(t, trace)
	require.InDelta(t, 2, heterogeneous.ResourceUtilization["small"], 1e-9)
	require.InDelta(t, 0.4, heterogeneous.ResourceUtilization["large"], 1e-9)
}

func TestDynamic5hTrafficReplayBorrowingAndLateArrival(t *testing.T) {
	trace := service.Dynamic5hReplayTrace{Name: "borrowing", State: service.Dynamic5hPressureNormal, PoolCapacity: 200,
		Users: []service.Dynamic5hReplayUser{{ID: 1, Active: true}},
		Events: []service.Dynamic5hReplayEvent{
			{UserID: 1, Platform: service.PlatformOpenAI, Units: 150},
			{AtSeconds: 1, State: service.Dynamic5hPressurePeak},
			{AtSeconds: 2, UserID: 2, Platform: service.PlatformOpenAI, Units: 20},
			{AtSeconds: 3, UserID: 1, Platform: service.PlatformOpenAI, Units: 20},
		}}
	report := replayDynamic5h(t, trace)
	require.Equal(t, 1, report.Users[0].Denied)
	require.Equal(t, 1, report.Users[1].Admitted)
	require.EqualValues(t, 150, report.Users[0].ServedUnits)
}

func TestDynamic5hTrafficReplayWeightsDifferentModelPrices(t *testing.T) {
	trace := service.Dynamic5hReplayTrace{Name: "model-price-weighting", State: service.Dynamic5hPressurePeak, PoolCapacity: 200, Users: []service.Dynamic5hReplayUser{{ID: 1, Active: true}, {ID: 2, Active: true}}}
	for request := 0; request < 60; request++ {
		trace.Events = append(trace.Events,
			service.Dynamic5hReplayEvent{AtSeconds: float64(request), UserID: 1, Platform: service.PlatformOpenAI, Units: 20},
			service.Dynamic5hReplayEvent{AtSeconds: float64(request), UserID: 2, Platform: service.PlatformOpenAI, Units: 2})
	}
	report := replayDynamic5h(t, trace)
	require.Equal(t, 5, report.Users[0].Admitted)
	require.Equal(t, 50, report.Users[1].Admitted)
	require.EqualValues(t, 100, report.Users[0].ServedUnits)
	require.EqualValues(t, 100, report.Users[1].ServedUnits)
}

func TestDynamic5hTrafficReplayReservationsDoNotGuaranteeActualCapacity(t *testing.T) {
	trace := service.Dynamic5hReplayTrace{Name: "burst", State: service.Dynamic5hPressurePeak, PoolCapacity: 100,
		Users: []service.Dynamic5hReplayUser{{ID: 1, Active: true}}}
	for request := 0; request < 10; request++ {
		trace.Events = append(trace.Events, service.Dynamic5hReplayEvent{UserID: 1, Platform: service.PlatformOpenAI, Units: 20, DurationSeconds: 180})
	}
	report := replayDynamic5h(t, trace)
	require.Equal(t, 10, report.Users[0].Admitted)
	require.EqualValues(t, 200, report.Users[0].ServedUnits)
}
