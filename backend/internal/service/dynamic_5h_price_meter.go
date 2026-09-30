package service

import "math"

func dynamic5hPriceMeter(cost *CostBreakdown) float64 {
	if cost == nil || cost.TotalCost <= 0 || math.IsNaN(cost.TotalCost) || math.IsInf(cost.TotalCost, 0) {
		return 0
	}
	return cost.TotalCost
}
