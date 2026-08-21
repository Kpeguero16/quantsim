package narrative

import (
	"fmt"
	"regexp"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// safeName is what may appear inside a token name or be handed to the model as
// a symbol.
//
// Symbols and finding codes reach this package over HTTP from trading-engine,
// and they are interpolated into token names -- so a symbol containing a brace
// would produce a token like {risk.positions.X} {risk.max_drawdown_pct} that
// the renderer parses as two tokens, and a symbol containing a sentence would
// be injected verbatim into the prompt as data. Neither is reachable today,
// because a position only exists for a symbol market-data could price, but that
// constraint lives two services away and is not this package's to assume.
//
// Anything that does not match contributes no tokens at all. Failing closed
// costs a holding its name in the prose; failing open costs the guarantee.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// Placeholders is the whole token vocabulary for one report.
//
// Its result is used twice and built once (SPEC.md §2.3): rendered into the
// prompt as the list of tokens the model may use, and used as the substitution
// table when the draft comes back. One source is what makes the contract
// airtight -- a token the prompt advertises always renders, and a token that
// renders was always advertised, because there is no second list to drift from.
//
// Token names mirror the report's JSON paths so a raw draft can be checked
// against the response by eye.
//
// A section whose state is not "ok" contributes nothing. Such a section still
// carries zeros in its numeric fields -- they are rendered beside its state
// rather than omitted (service/risk.go:51) -- and offering those zeros as
// tokens would let the model phrase a measurement nobody took. The same rule
// applies one level down to the finding fields that carry omitempty, where a
// zero means "absent", not "measured as zero".
//
// An empty result means there is nothing to phrase at all, which is how the
// caller recognises the fully-degraded report it must not spend an API call on
// (SPEC.md §2.12).
func Placeholders(r service.PortfolioInsights) map[string]Value {
	out := make(map[string]Value)

	// The window belongs to the report rather than to any one section, but it
	// is only meaningful when at least one section was computed -- a report
	// with nothing in it must offer nothing.
	if r.Risk.State == service.StateOK || r.Benchmarking.State == service.StateOK || r.Behavior.State == service.StateOK {
		if r.AsOfDate != "" {
			out["as_of_date"] = text(KindDate, r.AsOfDate)
		}
		if r.Window.StartDate != "" {
			out["window.start_date"] = text(KindDate, r.Window.StartDate)
		}
		out["window.trading_days"] = num(KindCount, float64(r.Window.TradingDays))
	}

	if r.Risk.State == service.StateOK {
		out["risk.position_count"] = num(KindCount, float64(len(r.Risk.Positions)))
		out["risk.cash_weight_pct"] = num(KindPercent, r.Risk.CashWeightPct)
		out["risk.concentration_hhi"] = num(KindIndex, r.Risk.ConcentrationHHI)
		out["risk.largest_position_pct"] = num(KindPercent, r.Risk.LargestPositionPct)
		out["risk.annualized_volatility_pct"] = num(KindPercent, r.Risk.AnnualizedVolatilityPct)
		// Unsigned, even though a drawdown is a fall. pkg/portfoliomath
		// reports it "as a positive percentage" -- a 1.7% decline is 1.7,
		// not -1.7 -- so rendering it signed would print "+1.7%" for a loss
		// and read as a gain. The manual pass caught this; the unit fixture
		// had used a negative value no code path can produce.
		out["risk.max_drawdown_pct"] = num(KindPercent, r.Risk.MaxDrawdownPct)

		// Nothing is the largest of nothing. Offering this token for an
		// all-cash portfolio would invite the model to name a holding that
		// does not exist -- and unlike a wrong number, a wrong ticker reads
		// as perfectly confident.
		if largest, ok := largestPosition(r.Risk.Positions); ok && safeName.MatchString(largest.Symbol) {
			out["risk.largest_position_symbol"] = text(KindSymbol, largest.Symbol)
		}

		for _, p := range r.Risk.Positions {
			if !safeName.MatchString(p.Symbol) {
				continue
			}
			out[fmt.Sprintf("risk.positions.%s.weight_pct", p.Symbol)] = num(KindPercent, p.WeightPct)
			out[fmt.Sprintf("risk.positions.%s.market_value", p.Symbol)] = num(KindMoney, p.MarketValue)
			out[fmt.Sprintf("risk.positions.%s.quantity", p.Symbol)] = num(KindQuantity, p.Quantity)
		}
	}

	if r.Benchmarking.State == service.StateOK {
		out["benchmarking.portfolio_return_pct"] = num(KindSignedPercent, r.Benchmarking.PortfolioReturnPct)
		out["benchmarking.portfolio_sharpe"] = num(KindRatio, r.Benchmarking.PortfolioSharpe)
		// A count token so the section can refer to how many benchmarks there
		// are without spelling it. The manual pass had a draft rejected for
		// "all three", counting the portfolio and its two benchmarks, with no
		// token available to say it any other way.
		out["benchmarking.benchmark_count"] = num(KindCount, float64(len(r.Benchmarking.Benchmarks)))

		for _, b := range r.Benchmarking.Benchmarks {
			if !safeName.MatchString(b.Symbol) {
				continue
			}
			out[fmt.Sprintf("benchmarking.%s.return_pct", b.Symbol)] = num(KindSignedPercent, b.ReturnPct)
			out[fmt.Sprintf("benchmarking.%s.excess_return_pct", b.Symbol)] = num(KindSignedPercent, b.ExcessReturnPct)
			out[fmt.Sprintf("benchmarking.%s.sharpe", b.Symbol)] = num(KindRatio, b.Sharpe)
		}
	}

	if r.Behavior.State == service.StateOK {
		// Counts get tokens so that small numbers have somewhere to go. The
		// validator rejects the word "two" as readily as the digit 2, and
		// without these the model would have no way to say how many trades
		// there were except by breaking the contract.
		out["behavior.trade_count"] = num(KindCount, float64(r.Behavior.TradeCount))
		out["behavior.finding_count"] = num(KindCount, float64(len(r.Behavior.Findings)))

		for _, f := range r.Behavior.Findings {
			if !safeName.MatchString(f.Code) {
				continue
			}
			// omitempty on the wire means these two cannot distinguish absent
			// from zero, so this package treats zero as absent to match. A
			// rule that computed no turnover ratio must not appear to have
			// measured one of 0.00.
			if f.TurnoverRatio != 0 {
				out[fmt.Sprintf("behavior.findings.%s.turnover_ratio", f.Code)] = num(KindRatio, f.TurnoverRatio)
			}
			if f.Occurrences != 0 {
				out[fmt.Sprintf("behavior.findings.%s.occurrences", f.Code)] = num(KindCount, float64(f.Occurrences))
			}
		}
	}

	return out
}

// largestPosition returns the heaviest holding by weight.
//
// It reads the weights rather than trusting Positions to be sorted: risk.go
// orders them for presentation, and a presentation order is exactly the kind
// of thing that changes without anyone thinking about who depended on it.
func largestPosition(positions []service.PositionWeight) (service.PositionWeight, bool) {
	if len(positions) == 0 {
		return service.PositionWeight{}, false
	}
	largest := positions[0]
	for _, p := range positions[1:] {
		if p.WeightPct > largest.WeightPct {
			largest = p
		}
	}
	return largest, true
}
