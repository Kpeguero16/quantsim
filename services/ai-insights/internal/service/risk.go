package service

import (
	"sort"

	"github.com/kpeguero/quantsim/pkg/portfoliomath"
)

// Section states. A section that cannot be computed says so explicitly rather
// than returning zeros: a 0.0 volatility is a VALUE, and a new user would read
// it as a measurement of a portfolio that has never traded (SPEC.md §2.10).
const (
	StateOK               = "ok"
	StateInsufficientData = "insufficient_data"
)

// minTradingDays is what risk and benchmarking need: two dates give one
// return, which is the least that volatility and drawdown can be computed
// from at all.
const minTradingDays = 2

// PositionWeight is one holding's share of the portfolio.
type PositionWeight struct {
	Symbol      string  `json:"symbol"`
	Quantity    float64 `json:"quantity"`
	MarketValue float64 `json:"market_value"`
	WeightPct   float64 `json:"weight_pct"`
}

// RiskSection is SPEC.md §2.5.
//
// Positions are the holdings as of the report's as_of_date -- the last stored
// close -- not as of now. A position opened after that date is not in them.
// That is part of the contract rather than an accident of it (SPEC.md §2.12 as
// amended): the alternative was refusing the whole report for anyone who had
// traded since the last close.
//
// Sectors are deliberately absent. agents.md §4 names sector exposure, but
// there is no sector data anywhere in this repo and the tradable universe is
// five names plus two ETFs -- a hardcoded map would be a fixture impersonating
// data. Concentration measures the thing sector exposure is a proxy FOR,
// undiversified risk, from data that is exact.
// Only Reason carries omitempty. The figures deliberately do not, because
// every one of them has a meaningful zero that omitempty would delete from a
// perfectly good report: concentration_hhi is 0 for an all-cash portfolio --
// which is the whole point of excluding cash from it -- cash_weight_pct is 0
// when fully invested, and max_drawdown_pct is 0 for a curve that only went
// up. An omitted field reads as "not computed", so dropping those would
// misreport a measurement as an absence.
//
// The cost is that an insufficient_data section renders zeros beside its
// state. That is the safer direction: State is the discriminator a caller
// must read anyway to know whether to render the block at all, and Reason
// says why -- whereas a silently missing figure has nothing pointing at it.
type RiskSection struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`

	Positions               []PositionWeight `json:"positions"`
	CashWeightPct           float64          `json:"cash_weight_pct"`
	ConcentrationHHI        float64          `json:"concentration_hhi"`
	LargestPositionPct      float64          `json:"largest_position_pct"`
	AnnualizedVolatilityPct float64          `json:"annualized_volatility_pct"`
	MaxDrawdownPct          float64          `json:"max_drawdown_pct"`
}

// ComputeRisk derives §2.5's figures from the reconstructed curve.
//
// Callers must have run Reconcile first: every weight here is computed from
// Reconstruction.Holdings, which a truncated calendar can silently make wrong
// (SPEC.md §2.12).
func ComputeRisk(r Reconstruction, barsBySymbol map[string][]Bar) (RiskSection, error) {
	if len(r.Dates) < minTradingDays {
		return RiskSection{
			State:     StateInsufficientData,
			Reason:    "fewer than two trading days of history",
			Positions: []PositionWeight{},
		}, nil
	}

	asOf := r.Dates[len(r.Dates)-1]
	closes := closesBySymbolAndDay(barsBySymbol)

	positions := make([]PositionWeight, 0, len(r.Holdings))
	invested := 0.0
	for symbol, qty := range r.Holdings {
		px, ok := closes[symbol][asOf]
		if !ok {
			return RiskSection{}, missingCloseError(symbol, asOf)
		}
		value := qty * px
		invested += value
		positions = append(positions, PositionWeight{Symbol: symbol, Quantity: qty, MarketValue: value})
	}

	equity := r.Equity[len(r.Equity)-1]
	cash := r.Cash[len(r.Cash)-1]

	// Weights are shares of TOTAL equity, so positions and cash sum to 100.
	largest := 0.0
	for i := range positions {
		if equity != 0 {
			positions[i].WeightPct = positions[i].MarketValue / equity * 100
		}
		if positions[i].WeightPct > largest {
			largest = positions[i].WeightPct
		}
	}

	// Largest first, symbol as tiebreak: deterministic, and the order a reader
	// wants. Map iteration order would otherwise vary per run.
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].MarketValue != positions[j].MarketValue {
			return positions[i].MarketValue > positions[j].MarketValue
		}
		return positions[i].Symbol < positions[j].Symbol
	})

	section := RiskSection{
		State:              StateOK,
		Positions:          positions,
		ConcentrationHHI:   concentrationHHI(positions, invested),
		LargestPositionPct: largest,
		MaxDrawdownPct:     portfoliomath.MaxDrawdownPct(r.Equity),
	}
	if equity != 0 {
		section.CashWeightPct = cash / equity * 100
	}

	returns := portfoliomath.DailyReturns(r.Equity)
	if len(returns) > 0 {
		stdev := portfoliomath.StdevPopulation(returns, portfoliomath.Mean(returns))
		section.AnnualizedVolatilityPct = stdev * sqrtTradingDays * 100
	}

	return section, nil
}

// concentrationHHI is the Herfindahl-Hirschman index over INVESTED positions,
// with weights renormalized to the invested total: 1.0 for a single holding,
// 1/n for n equal ones.
//
// Cash is excluded and reported separately as CashWeightPct. Folding it in
// would make an all-cash portfolio report perfect concentration and perfect
// safety from the same number. For the same reason an all-cash portfolio gets
// 0 here, not 1.0 -- there is no concentrated risk in holding nothing, and
// "undefined" is better said as zero-with-a-visible-cash-weight than as a 1.0
// that reads like maximum danger.
func concentrationHHI(positions []PositionWeight, invested float64) float64 {
	if invested == 0 {
		return 0
	}
	hhi := 0.0
	for _, p := range positions {
		w := p.MarketValue / invested
		hhi += w * w
	}
	return hhi
}
