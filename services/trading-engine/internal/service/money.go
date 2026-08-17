package service

// The two pieces of arithmetic this engine gets wrong most expensively, kept
// as pure functions for one reason: they are called from inside the store's
// transaction, where nothing can be unit-tested, and expressing them as SQL
// instead would put the formulas somewhere no test can reach without a
// database. One implementation, exercised directly here and end-to-end by the
// store's integration tests.

// WeightedAvgCost is the cost basis after adding fillQty shares at fillPrice
// to a position of oldQty shares averaging oldAvg.
//
// The zero case is not defensive padding: a position sold down to quantity 0
// keeps its row, so the next buy against it arrives with oldQty == 0 and must
// come out at exactly fillPrice rather than dividing by zero. The general
// formula already produces that, so the guard only covers a fill of zero
// shares -- which validation rejects before it can reach here.
func WeightedAvgCost(oldQty, oldAvg, fillQty, fillPrice float64) float64 {
	total := oldQty + fillQty
	if total == 0 {
		return 0
	}
	return (oldAvg*oldQty + fillPrice*fillQty) / total
}

// RealizedPL is the profit or loss booked by selling quantity shares at
// fillPrice against a cost basis of avgCost.
//
// avgCost is passed in, never looked up, because the only correct value is
// the one in effect at the moment of the sell. A later buy moves the average,
// so a P/L recomputed afterwards is a different number that still looks
// plausible -- see migration 006.
func RealizedPL(avgCost, fillPrice, quantity float64) float64 {
	return (fillPrice - avgCost) * quantity
}
