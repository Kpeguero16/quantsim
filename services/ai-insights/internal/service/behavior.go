package service

import (
	"fmt"
	"sort"
	"time"
)

// Finding codes and severities. Codes are stable identifiers a UI can switch
// on; Detail is prose for a human and must not be parsed.
const (
	FindingOvertrading  = "overtrading"
	FindingPanicSelling = "panic_selling"

	SeverityInfo = "info"
	SeverityWarn = "warn"
)

// Risk-profile bands (SPEC.md §2.7).
const (
	ProfileAggressive   = "aggressive"
	ProfileModerate     = "moderate"
	ProfileConservative = "conservative"
)

// Finding is one behavioural rule that fired.
//
// EvidenceTradeIDs is a requirement rather than a nicety (SPEC.md §2.7): it is
// what lets the deferred LLM step cite specifics without inventing them. The
// model receives the trades that fired the rule and can describe only those.
//
// TurnoverRatio and Occurrences carry omitempty, unlike RiskSection's figures
// where it was a bug. The difference is that neither zero is REACHABLE in a
// finding that exists: overtrading fires only above OvertradingTurnoverRatio,
// and panic selling only at one occurrence or more. A field omitted here was
// never computed for this finding's kind, which is exactly what its absence
// means.
type Finding struct {
	Code             string   `json:"code"`
	Severity         string   `json:"severity"`
	Detail           string   `json:"detail"`
	TurnoverRatio    float64  `json:"turnover_ratio,omitempty"`
	Occurrences      int      `json:"occurrences,omitempty"`
	EvidenceTradeIDs []string `json:"evidence_trade_ids"`
}

// BehaviorSection is SPEC.md §2.7.
//
// TradeCount is the account's whole trade log, not the overtrading window --
// it is a fact about the account, and the overtrading finding states its own
// window count in Detail.
//
// RiskProfile is absent when the risk section could not be computed, because
// its two inputs come from there. That is the one place in this response where
// an omitted key is right: the value genuinely was not computed, rather than
// computed and found to be zero. Emitting the empty string would be worse --
// a band nobody defined -- and defaulting to "conservative" would be a claim
// about a portfolio nothing was measured on.
type BehaviorSection struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`

	TradeCount  int       `json:"trade_count"`
	Findings    []Finding `json:"findings"`
	RiskProfile string    `json:"risk_profile,omitempty"`
}

// ComputeBehavior applies §2.7's three rules to the trade log.
//
// It needs the reconstruction for the equity denominator and the calendar, the
// bars for the previous trading day's move, and the risk section for the
// profile bands -- so it runs last, after both other sections.
//
// Unlike risk and benchmarking, this section needs only ONE trade rather than
// two trading days (SPEC.md §2.10). A portfolio that traded yesterday can
// legitimately return a populated behavior section next to two
// insufficient_data ones, and that is a truthful description of what is known.
func ComputeBehavior(trades []Trade, r Reconstruction, barsBySymbol map[string][]Bar, risk RiskSection) (BehaviorSection, error) {
	if len(trades) == 0 {
		return BehaviorSection{
			State:    StateInsufficientData,
			Reason:   "no trades yet",
			Findings: []Finding{},
		}, nil
	}

	findings := make([]Finding, 0, 2)

	if f, ok := overtrading(trades, r); ok {
		findings = append(findings, f)
	}

	panicFinding, ok, err := panicSelling(trades, r, barsBySymbol)
	if err != nil {
		return BehaviorSection{}, err
	}
	if ok {
		findings = append(findings, panicFinding)
	}

	return BehaviorSection{
		State:       StateOK,
		TradeCount:  len(trades),
		Findings:    findings,
		RiskProfile: riskProfile(risk),
	}, nil
}

// overtrading measures value traded against the equity that supported it, over
// a trailing OvertradingWindowDays ending at the report's as-of date.
//
// Anchored to the last calendar date rather than to time.Now(), so numerator
// and denominator cover the same span: mean equity can only be averaged over
// dates the curve has. Anchoring to now would divide a window's worth of
// trades by an average that stops earlier, inflating the ratio by exactly how
// stale the bars are.
func overtrading(trades []Trade, r Reconstruction) (Finding, bool) {
	if len(r.Dates) == 0 {
		return Finding{}, false
	}
	asOf := r.Dates[len(r.Dates)-1]
	windowStart := asOf.AddDate(0, 0, -OvertradingWindowDays)

	traded := 0.0
	var ids []string
	for _, t := range trades {
		d := Day(t.ExecutedAt)
		if d.After(windowStart) && !d.After(asOf) {
			traded += t.Quantity * t.Price
			ids = append(ids, t.ID)
		}
	}
	if len(ids) == 0 {
		return Finding{}, false
	}

	sum, days := 0.0, 0
	for i, date := range r.Dates {
		if date.After(windowStart) {
			sum += r.Equity[i]
			days++
		}
	}
	// No equity to divide by is not a turnover of infinity; it is a ratio that
	// cannot be formed, and a rule that cannot be evaluated does not fire.
	if days == 0 || sum == 0 {
		return Finding{}, false
	}

	turnover := traded / (sum / float64(days))
	if turnover <= OvertradingTurnoverRatio {
		return Finding{}, false
	}

	return Finding{
		Code:     FindingOvertrading,
		Severity: SeverityWarn,
		Detail: fmt.Sprintf("%d trades in the last %d days, %.1fx average equity traded",
			len(ids), OvertradingWindowDays, turnover),
		TurnoverRatio:    turnover,
		EvidenceTradeIDs: ids,
	}, true
}

// panicSelling finds sells that are BOTH into a drop and at a loss.
//
// Both conditions are required and neither is sufficient: selling into a drop
// at a profit is taking a gain, and selling at a loss on a calm day is
// ordinary rebalancing. Only the pair describes capitulation.
func panicSelling(trades []Trade, r Reconstruction, barsBySymbol map[string][]Bar) (Finding, bool, error) {
	if len(r.Dates) == 0 {
		return Finding{}, false, nil
	}
	asOf := r.Dates[len(r.Dates)-1]
	closes := closesBySymbolAndDay(barsBySymbol)

	sells, occurrences := 0, 0
	var ids []string

	for _, t := range trades {
		if t.Side != SideSell {
			continue
		}
		// A sell this rule cannot be evaluated for is excluded from the
		// DENOMINATOR as well as the numerator: counting a sell we refused to
		// judge would dilute the share test with trades that never had a
		// chance to qualify. `overtrading` bounds its window the same way.
		//
		// There are two ways a sell is unjudgeable and they are the same
		// case -- no pair of prior sessions to have fallen across:
		//
		//   - It is after the last stored close. sort.Search would then run
		//     off the end of the calendar and calendar[i-1]/[i-2] would name
		//     the window's last two days, which may be weeks stale, so the
		//     sell would be judged on a price move that had nothing to do
		//     with it (SPEC.md §2.12).
		//   - It is in the window's first two sessions, so there are fewer
		//     than two dates before it.
		//
		// The first needs its own check: past the end of the calendar
		// sort.Search returns len(calendar), which passes priorSessions' own
		// bound and is exactly the stale judgement described above.
		if Day(t.ExecutedAt).After(asOf) {
			continue
		}
		prev, before, judgeable := priorSessions(Day(t.ExecutedAt), r.Dates)
		if !judgeable {
			continue
		}
		sells++

		// (ii) first: it needs no lookup, and a sell at a profit is not a
		// panic no matter what the market did. It stays in the denominator --
		// the rule evaluated it and it failed a condition, which is not the
		// same as never having been evaluable.
		if t.RealizedPL == nil || *t.RealizedPL >= 0 {
			continue
		}

		dropped, err := droppedAcross(t.Symbol, prev, before, closes)
		if err != nil {
			return Finding{}, false, err
		}
		if dropped {
			occurrences++
			ids = append(ids, t.ID)
		}
	}

	// Zero occurrences must not fire. Without this the share test below reads
	// 0 >= 0.30*0 as true for an account that has never sold at all.
	if occurrences == 0 {
		return Finding{}, false, nil
	}

	share := float64(occurrences) / float64(sells)
	if occurrences < PanicSellMinOccurrences && share < PanicSellMinShareOfSells {
		return Finding{}, false, nil
	}

	return Finding{
		Code:     FindingPanicSelling,
		Severity: SeverityInfo,
		Detail: fmt.Sprintf("%d of %d sells were into a drop of %.0f%% or more and booked a loss",
			occurrences, sells, PanicSellDropPct),
		Occurrences:      occurrences,
		EvidenceTradeIDs: ids,
	}, true, nil
}

// priorSessions returns the two calendar dates a sell on tradeDay is judged
// across -- the previous trading day and the one before it -- and whether the
// calendar holds such a pair at all.
//
// "Previous trading day" is the last CALENDAR date strictly before the trade,
// not date - 1. A Monday sell is judged against Friday's move, and a sell on a
// date the calendar does not contain still finds the last real session before
// it. Using date - 1 would silently evaluate most Monday sells against a
// Sunday that has no bar, and quietly never fire.
//
// Two prior dates are needed, since the previous day's own move is measured
// against the day before it. A trade in the window's first two sessions has no
// such pair.
//
// Whether the pair EXISTS is returned separately from what it shows, because
// they are different facts and the caller acts differently on each: no pair
// means the rule could not be evaluated and the sell leaves the share test's
// denominator entirely, whereas a pair that did not drop means the rule was
// evaluated and the sell did not qualify. Collapsing both into a bare false --
// which this did until an unjudgeable sell was found diluting the denominator
// -- is the same "two paths to one outcome" that hid four mutation survivors
// in this step.
//
// It does NOT bound tradeDay above: past the end of the calendar sort.Search
// returns len(calendar), which satisfies the i >= 2 test and would name the
// window's last two days. The caller excludes post-as-of sells before asking.
func priorSessions(tradeDay time.Time, calendar []time.Time) (prev, before time.Time, ok bool) {
	// The first index at or after the trade's day; everything before it is
	// strictly earlier. sort.Search over an ascending calendar.
	i := sort.Search(len(calendar), func(i int) bool { return !calendar[i].Before(tradeDay) })
	if i < 2 {
		return time.Time{}, time.Time{}, false
	}
	return calendar[i-1], calendar[i-2], true
}

// droppedAcross reports whether symbol closed PanicSellDropPct or more down
// on prev relative to before -- the pair priorSessions selected.
func droppedAcross(symbol string, prev, before time.Time, closes map[string]map[time.Time]float64) (bool, error) {
	prevClose, ok := closes[symbol][prev]
	if !ok {
		return false, missingCloseError(symbol, prev)
	}
	beforeClose, ok := closes[symbol][before]
	if !ok || beforeClose == 0 {
		return false, missingCloseError(symbol, before)
	}

	// Compared as PRICES against a threshold price, not as a percentage
	// against -PanicSellDropPct. The two are algebraically identical and are
	// not identical in float64, which matters at exactly the boundary the
	// threshold names:
	//
	//	(95.0/100.0 - 1) * 100  ==  -5.000000000000004   -- never equals -5
	//	100.0 * (1 - 5.0/100)   ==   95.0                -- exact
	//
	// 0.95 has no exact binary representation, so the percentage form reports
	// every clean 5% fall as slightly MORE than 5% and can never land on the
	// boundary at all. "≥ 5% down" would then be untestable at 5% -- <= and <
	// would be indistinguishable -- and a test claiming to pin the boundary
	// would be pinning nothing. Found by a surviving mutant that flipped
	// exactly that comparison.
	return prevClose <= beforeClose*(1-PanicSellDropPct/100), nil
}

// riskProfile bands §2.5's two figures (SPEC.md §2.7).
//
// An empty string when risk could not be computed: the inputs do not exist, so
// there is no band. Defaulting to conservative would be a claim about a
// portfolio nothing was measured on.
func riskProfile(risk RiskSection) string {
	if risk.State != StateOK {
		return ""
	}
	switch {
	case risk.AnnualizedVolatilityPct > AggressiveVolatilityPct || risk.ConcentrationHHI > AggressiveHHI:
		return ProfileAggressive
	case risk.AnnualizedVolatilityPct < ConservativeVolatilityPct && risk.ConcentrationHHI < ConservativeHHI:
		return ProfileConservative
	default:
		return ProfileModerate
	}
}
