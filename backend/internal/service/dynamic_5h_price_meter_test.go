//go:build unit

package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"

	"github.com/stretchr/testify/require"
)

func TestDynamic5hMeterUsesModelPriceRatherThanTokensOrUserMultiplier(t *testing.T) {
	usage := &UsageLog{InputTokens: 1000, OutputTokens: 1000}
	params := &postUsageBillingParams{User: &User{ID: 7}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 1, Platform: PlatformOpenAI}, Cost: &CostBreakdown{TotalCost: 0.04, ActualCost: 0.4}}
	cheap := buildUsageBillingCommand("cheap", usage, params)
	params.Cost = &CostBreakdown{TotalCost: 0.4, ActualCost: 0.004}
	expensive := buildUsageBillingCommand("expensive", usage, params)
	require.Equal(t, cheap.InputTokens, expensive.InputTokens)
	require.Equal(t, 0.04, cheap.Dynamic5hMeterUnits)
	require.Equal(t, 0.4, expensive.Dynamic5hMeterUnits)
	params.Cost.ActualCost = 0
	require.Equal(t, 0.4, buildUsageBillingCommand("free-user", usage, params).Dynamic5hMeterUnits)
}

func TestDynamic5hPriceMeterRejectsMissingOrInvalidPrices(t *testing.T) {
	require.Zero(t, dynamic5hPriceMeter(nil))
	for _, price := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		require.Zero(t, dynamic5hPriceMeter(&CostBreakdown{TotalCost: price}))
	}
}

func TestDynamic5hPriceMeterPrefersUpstreamModelCost(t *testing.T) {
	upstreamCost := 0.4
	accountMultiplier := 20.0
	usage := &UsageLog{AccountStatsCost: &upstreamCost, AccountRateMultiplier: &accountMultiplier}
	cost := &CostBreakdown{TotalCost: 0.04, ActualCost: 0.004}
	require.Equal(t, 0.4, dynamic5hPriceMeter(cost, usage))
	params := &postUsageBillingParams{User: &User{ID: 7}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 1, Platform: PlatformOpenAI}, Cost: cost}
	require.Equal(t, 0.4, buildUsageBillingCommand("mapped-upstream", usage, params).Dynamic5hMeterUnits)
	usage.AccountStatsCost = nil
	require.Equal(t, 0.04, dynamic5hPriceMeter(cost, usage))
	for _, invalid := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		usage.AccountStatsCost = &invalid
		require.Zero(t, dynamic5hPriceMeter(cost, usage))
	}
}

func TestDynamic5hPriceDenominatedSmallShareCanAdmit(t *testing.T) {
	svc, ctx := newDynamic5hTestService(t, time.Now())
	storeDynamic5hTestStatus(svc, ctx, Dynamic5hPressurePeak, 0.001)
	ctx = context.WithValue(ctx, ctxkey.RequestID, "small-price-request")
	require.NoError(t, svc.CheckUser(ctx, 1))
}
