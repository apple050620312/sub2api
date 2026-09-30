package service

import "math"

func dynamic5hPriceMeter(cost *CostBreakdown, usageLogs ...*UsageLog) float64 {
	if len(usageLogs) > 0 && usageLogs[0] != nil && usageLogs[0].AccountStatsCost != nil {
		price := *usageLogs[0].AccountStatsCost
		if price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0) {
			return price
		}
		return 0
	}
	if cost == nil || cost.TotalCost <= 0 || math.IsNaN(cost.TotalCost) || math.IsInf(cost.TotalCost, 0) {
		return 0
	}
	return cost.TotalCost
}
