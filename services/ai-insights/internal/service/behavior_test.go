package service_test

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// tradeAt is `trade` with an explicit ID and realized P/L, which the
// behavioural rules need and the reconstruction tests never did.
func tradeAt(id, symbol string, side service.Side, qty, price float64, d int, realizedPL *float64) service.Trade {
	return service.Trade{
		ID: id, Symbol: symbol, Side: side, Quantity: qty, Price: price,
		RealizedPL: realizedPL, ExecutedAt: day(d),
	}
}

func pl(v float64) *float64 { return &v }

func behavior(t *testing.T, trades []service.Trade, barsBySymbol map[string][]service.Bar) service.BehaviorSection {
	t.Helper()
	r := reconstruct(t, trades, barsBySymbol)
	risk, err := service.ComputeRisk(r, barsBySymbol)
	if err != nil {
		t.Fatalf("ComputeRisk: %v", err)
	}
	got, err := service.ComputeBehavior(trades, r, barsBySymbol, risk)
	if err != nil {
		t.Fatalf("ComputeBehavior: %v", err)
	}
	return got
}

func findingFor(section service.BehaviorSection, code string) (service.Finding, bool) {
	for _, f := range section.Findings {
		if f.Code == code {
			return f, true
		}
	}
	return service.Finding{}, false
}

func assertNoFinding(t *testing.T, section service.BehaviorSection, code string) {
	t.Helper()
	if f, ok := findingFor(section, code); ok {
		t.Errorf("%s fired when it should not have: %+v", code, f)
	}
}

func assertEvidence(t *testing.T, f service.Finding, want ...string) {
	t.Helper()
	if len(f.EvidenceTradeIDs) != len(want) {
		t.Fatalf("evidence: got %v, want %v", f.EvidenceTradeIDs, want)
	}
	for i, id := range want {
		if f.EvidenceTradeIDs[i] != id {
			t.Errorf("evidence %d: got %q, want %q", i, f.EvidenceTradeIDs[i], id)
		}
	}
}

// ---------------------------------------------------------------- overtrading

// THE ACCEPTANCE CRITERION: turnover, not count. Ten tiny trades and ten large
// ones differ only in value, and a count cannot tell them apart.
func TestBehavior_OvertradingMeasuresValueNotCount(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3}, []float64{100, 100, 100}),
		"SPY":  priced([]int{1, 2, 3}, []float64{100, 100, 100}),
	}

	tests := []struct {
		name      string
		qty       float64
		price     float64
		wantFires bool
	}{
		// Equity is ~100,000, so ten $100 round trips move almost nothing.
		{"ten small trades", 1, 100, false},
		// Ten $10,000 trades is 100,000 traded against ~100,000 of equity...
		{"ten large trades", 100, 100, false},
		// ...and thirty is comfortably past 2.0x.
		{"thirty large trades", 300, 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var trades []service.Trade
			for i := 0; i < 10; i++ {
				trades = append(trades,
					tradeAt(fmt.Sprintf("b%d", i), "AAPL", service.SideBuy, tt.qty, tt.price, 1, nil),
					tradeAt(fmt.Sprintf("s%d", i), "AAPL", service.SideSell, tt.qty, tt.price, 2, pl(0)))
			}

			got := behavior(t, trades, barsBySymbol)
			if _, fired := findingFor(got, service.FindingOvertrading); fired != tt.wantFires {
				f, _ := findingFor(got, service.FindingOvertrading)
				t.Fatalf("fired=%v, want %v (turnover %.2f)", fired, tt.wantFires, f.TurnoverRatio)
			}
			if got.TradeCount != 20 {
				t.Errorf("trade_count: got %d, want 20 (the whole log)", got.TradeCount)
			}
		})
	}
}

// The threshold from both sides and exactly at it. "Flagged ABOVE 2.0" means
// exactly 2.0 does not fire.
func TestBehavior_OvertradingThresholdBoundary(t *testing.T) {
	// A flat 100,000 curve: no holdings overnight, so mean equity is exactly
	// StartingBalance and turnover is (value traded)/100,000.
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2}, []float64{100, 100}),
		"SPY":  priced([]int{1, 2}, []float64{100, 100}),
	}

	tests := []struct {
		name      string
		traded    float64
		wantFires bool
	}{
		{"just under", 199_999, false},
		{"exactly at the threshold", 200_000, false},
		{"just over", 200_001, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// One buy and one matching sell on day 1, so day 2's equity is
			// pure cash and the curve stays flat at StartingBalance.
			half := tt.traded / 2
			trades := []service.Trade{
				tradeAt("b", "AAPL", service.SideBuy, half/100, 100, 1, nil),
				tradeAt("s", "AAPL", service.SideSell, half/100, 100, 1, pl(0)),
			}

			got := behavior(t, trades, barsBySymbol)
			f, fired := findingFor(got, service.FindingOvertrading)
			if fired != tt.wantFires {
				t.Fatalf("traded %.0f gave fired=%v (turnover %v), want %v",
					tt.traded, fired, f.TurnoverRatio, tt.wantFires)
			}
			if fired {
				assertEvidence(t, f, "b", "s")
			}
		})
	}
}

// Trades older than the window are outside the numerator. Without this the
// ratio only ever grows, and every long-lived account eventually reads as
// overtrading.
func TestBehavior_OvertradingIgnoresTradesOlderThanTheWindow(t *testing.T) {
	// Day 1 and day 40 of the same month-plus: 39 days apart, so day 1 falls
	// outside a 30-day window ending on day 40.
	days := []int{1, 40, 41}
	closes := []float64{100, 100, 100}
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced(days, closes),
		"SPY":  priced(days, closes),
	}
	trades := []service.Trade{
		tradeAt("old", "AAPL", service.SideBuy, 3000, 100, 1, nil),     // 300k, long ago
		tradeAt("new", "AAPL", service.SideSell, 3000, 100, 41, pl(0)), // in window
	}

	got := behavior(t, trades, barsBySymbol)
	f, fired := findingFor(got, service.FindingOvertrading)
	if !fired {
		t.Fatal("the in-window sell alone should still be well past 2.0x")
	}
	assertEvidence(t, f, "new")
}

// ------------------------------------------------------------- panic selling

// THE ACCEPTANCE CRITERION: all four combinations, only both-true fires.
func TestBehavior_PanicSellingRequiresBothConditions(t *testing.T) {
	// Day 2 is the "previous trading day" for a day-3 sell. A 10% fall from
	// day 1 to day 2 satisfies the drop; a flat day 2 does not.
	dropped := []float64{100, 90, 90}
	calm := []float64{100, 100, 100}

	tests := []struct {
		name       string
		closes     []float64
		realizedPL *float64
		wantFires  bool
	}{
		{"drop and loss", dropped, pl(-500), true},
		{"drop but a profit", dropped, pl(500), false},
		{"loss on a calm day", calm, pl(-500), false},
		{"neither", calm, pl(500), false},
		{"drop and exactly break even", dropped, pl(0), false},
		{"drop and no realized P/L recorded", dropped, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			barsBySymbol := map[string][]service.Bar{
				"AAPL": priced([]int{1, 2, 3}, tt.closes),
				"SPY":  priced([]int{1, 2, 3}, calm),
			}
			trades := []service.Trade{
				tradeAt("b", "AAPL", service.SideBuy, 10, 100, 1, nil),
				tradeAt("s", "AAPL", service.SideSell, 10, 90, 3, tt.realizedPL),
			}

			got := behavior(t, trades, barsBySymbol)
			// One sell out of one is 100% >= 30%, so a single qualifying sell
			// is enough to fire via the share arm.
			if _, fired := findingFor(got, service.FindingPanicSelling); fired != tt.wantFires {
				t.Fatalf("fired=%v, want %v", fired, tt.wantFires)
			}
		})
	}
}

// THE ACCEPTANCE CRITERION: "previous TRADING day", tested across a weekend.
//
// 2026-03-06 is a Friday and 2026-03-09 the Monday after. The calendar skips
// the weekend, as a real market calendar does. A Monday sell must be judged
// against Friday's move; date - 1 would land on Sunday, find no bar, and the
// rule would quietly never fire for any Monday.
func TestBehavior_PanicSellingUsesThePreviousTradingDayAcrossAWeekend(t *testing.T) {
	if day(6).Weekday() != time.Friday || day(9).Weekday() != time.Monday {
		t.Fatalf("fixture assumption broken: day 6 is %v, day 9 is %v", day(6).Weekday(), day(9).Weekday())
	}

	// Thursday 100 -> Friday 90 is a 10% fall. Monday is the sell.
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{5, 6, 9}, []float64{100, 90, 90}),
		"SPY":  priced([]int{5, 6, 9}, []float64{100, 100, 100}),
	}
	trades := []service.Trade{
		tradeAt("b", "AAPL", service.SideBuy, 10, 100, 5, nil),
		tradeAt("s", "AAPL", service.SideSell, 10, 90, 9, pl(-100)),
	}

	got := behavior(t, trades, barsBySymbol)
	f, fired := findingFor(got, service.FindingPanicSelling)
	if !fired {
		t.Fatal("a Monday sell after a 10% Friday fall did not fire; the rule is not " +
			"finding the previous TRADING day")
	}
	assertEvidence(t, f, "s")
}

// The drop threshold from both sides and exactly at it. "≥ 5% down" means
// exactly -5% counts.
func TestBehavior_PanicSellingDropThresholdBoundary(t *testing.T) {
	tests := []struct {
		name      string
		prevClose float64
		wantFires bool
	}{
		{"fell 4.9%", 95.1, false},
		{"fell exactly 5%", 95, true},
		{"fell 5.1%", 94.9, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			barsBySymbol := map[string][]service.Bar{
				"AAPL": priced([]int{1, 2, 3}, []float64{100, tt.prevClose, tt.prevClose}),
				"SPY":  priced([]int{1, 2, 3}, []float64{100, 100, 100}),
			}
			trades := []service.Trade{
				tradeAt("b", "AAPL", service.SideBuy, 10, 100, 1, nil),
				tradeAt("s", "AAPL", service.SideSell, 10, tt.prevClose, 3, pl(-100)),
			}

			got := behavior(t, trades, barsBySymbol)
			if _, fired := findingFor(got, service.FindingPanicSelling); fired != tt.wantFires {
				t.Fatalf("prev close %.2f: fired=%v, want %v", tt.prevClose, fired, tt.wantFires)
			}
		})
	}
}

// The two arms of the firing rule are alternatives. Six calm sells and two
// panics is 25% -- under the 30% share -- and two is under the three-occurrence
// count, so neither arm fires. A third panic trips the count arm alone.
func TestBehavior_PanicSellingCountAndShareAreAlternatives(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3}, []float64{100, 90, 90}), // day 2 fell 10%
		"MSFT": priced([]int{1, 2, 3}, []float64{100, 100, 100}),
		"SPY":  priced([]int{1, 2, 3}, []float64{100, 100, 100}),
	}

	build := func(panics int, calmSells int) []service.Trade {
		trades := []service.Trade{
			tradeAt("ba", "AAPL", service.SideBuy, 1000, 100, 1, nil),
			tradeAt("bm", "MSFT", service.SideBuy, 1000, 100, 1, nil),
		}
		for i := 0; i < panics; i++ {
			trades = append(trades,
				tradeAt(fmt.Sprintf("p%d", i), "AAPL", service.SideSell, 1, 90, 3, pl(-10)))
		}
		for i := 0; i < calmSells; i++ {
			// MSFT never fell, so these are sells but not panics.
			trades = append(trades,
				tradeAt(fmt.Sprintf("c%d", i), "MSFT", service.SideSell, 1, 100, 3, pl(-10)))
		}
		return trades
	}

	tests := []struct {
		name              string
		panics, calmSells int
		wantFires         bool
	}{
		{"two of eight is 25%, under both arms", 2, 6, false},
		{"three of nine trips the count arm", 3, 6, true},
		{"two of five is 40%, trips the share arm", 2, 3, true},
		// The count arm ALONE, at its boundary. Every case above leaves the
		// share arm able to fire too, which masks the count threshold
		// entirely: three of nine is 33%, over the 30% share, so raising the
		// count from 3 to 4 changed no verdict anywhere and the constant was
		// effectively untested. Fifteen sells puts the share out of reach
		// (2/15 = 13%, 3/15 = 20%) and leaves the count as the only arm that
		// can fire.
		{"two of fifteen: 13% share and under the count", 2, 13, false},
		{"three of fifteen: 20% share, count arm alone", 3, 12, true},
		{"no sells at all", 0, 0, false},
		{"sells but no panics", 0, 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := behavior(t, build(tt.panics, tt.calmSells), barsBySymbol)
			f, fired := findingFor(got, service.FindingPanicSelling)
			if fired != tt.wantFires {
				t.Fatalf("fired=%v (occurrences %d), want %v", fired, f.Occurrences, tt.wantFires)
			}
			if fired && f.Occurrences != tt.panics {
				t.Errorf("occurrences: got %d, want %d", f.Occurrences, tt.panics)
			}
		})
	}
}

// A sell in the window's first two sessions has no previous trading day whose
// own move can be measured, so it does not fire. An absence of evidence, not a
// default.
func TestBehavior_PanicSellingNeedsTwoPriorSessions(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2}, []float64{100, 50}),
		"SPY":  priced([]int{1, 2}, []float64{100, 100}),
	}
	// Sold on day 2: the only prior session is day 1, which has no day before
	// it in the window to have fallen from.
	trades := []service.Trade{
		tradeAt("b", "AAPL", service.SideBuy, 10, 100, 1, nil),
		tradeAt("s", "AAPL", service.SideSell, 10, 50, 2, pl(-500)),
	}

	assertNoFinding(t, behavior(t, trades, barsBySymbol), service.FindingPanicSelling)
}

// ------------------------------------------------------------- risk profile

func TestBehavior_RiskProfileBands(t *testing.T) {
	tests := []struct {
		name string
		vol  float64
		hhi  float64
		want string
	}{
		{"calm and diversified", 11.9, 0.24, service.ProfileConservative},
		{"volatility exactly at the conservative bound", 12, 0.24, service.ProfileModerate},
		{"concentration exactly at the conservative bound", 11.9, 0.25, service.ProfileModerate},
		{"between the bands", 20, 0.4, service.ProfileModerate},
		{"volatile alone is enough", 25.1, 0.1, service.ProfileAggressive},
		{"concentrated alone is enough", 5, 0.51, service.ProfileAggressive},
		{"volatility exactly at the aggressive bound", 25, 0.1, service.ProfileModerate},
		{"concentration exactly at the aggressive bound", 5, 0.5, service.ProfileModerate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			risk := service.RiskSection{
				State:                   service.StateOK,
				AnnualizedVolatilityPct: tt.vol,
				ConcentrationHHI:        tt.hhi,
			}
			got, err := service.ComputeBehavior(
				[]service.Trade{tradeAt("b", "AAPL", service.SideBuy, 1, 100, 1, nil)},
				service.Reconstruction{}, nil, risk)
			if err != nil {
				t.Fatalf("ComputeBehavior: %v", err)
			}
			if got.RiskProfile != tt.want {
				t.Errorf("vol %.1f hhi %.2f: got %q, want %q", tt.vol, tt.hhi, got.RiskProfile, tt.want)
			}
		})
	}
}

// No risk section, no band. Defaulting to conservative would be a claim about
// a portfolio nothing was measured on.
func TestBehavior_RiskProfileIsAbsentWhenRiskIsNot(t *testing.T) {
	got, err := service.ComputeBehavior(
		[]service.Trade{tradeAt("b", "AAPL", service.SideBuy, 1, 100, 1, nil)},
		service.Reconstruction{}, nil,
		service.RiskSection{State: service.StateInsufficientData})
	if err != nil {
		t.Fatalf("ComputeBehavior: %v", err)
	}
	if got.RiskProfile != "" {
		t.Errorf("risk_profile: got %q, want empty", got.RiskProfile)
	}
	if got.State != service.StateOK {
		t.Errorf("behavior itself needs only one trade, got state %q", got.State)
	}
}

// ------------------------------------------------------------------- section

// Behavior needs one trade, not two trading days -- so it can be populated
// beside two insufficient_data sections, which is a truthful description of
// what is known (SPEC.md §2.10).
func TestBehavior_NoTradesIsInsufficientDataWithAnEmptyFindingsArray(t *testing.T) {
	got, err := service.ComputeBehavior(nil, service.Reconstruction{}, nil, service.RiskSection{})
	if err != nil {
		t.Fatalf("ComputeBehavior: %v", err)
	}
	if got.State != service.StateInsufficientData {
		t.Errorf("state: got %q", got.State)
	}
	if got.Findings == nil {
		t.Error("findings is nil; it must marshal as [] rather than null")
	}
}

// A well-behaved account produces no findings, and that is an empty array
// rather than an absent one.
func TestBehavior_ACalmAccountHasNoFindings(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3}, []float64{100, 101, 102}),
		"SPY":  priced([]int{1, 2, 3}, []float64{100, 101, 102}),
	}
	trades := []service.Trade{tradeAt("b", "AAPL", service.SideBuy, 10, 100, 1, nil)}

	got := behavior(t, trades, barsBySymbol)
	if len(got.Findings) != 0 {
		t.Errorf("got %+v, want no findings", got.Findings)
	}
	if got.Findings == nil {
		t.Error("findings is nil; it must marshal as [] rather than null")
	}
	if math.Abs(float64(got.TradeCount)-1) > 0 {
		t.Errorf("trade_count: got %d, want 1", got.TradeCount)
	}
}

// The denominator is MEAN daily equity over the window, not the final equity.
//
// Every other overtrading fixture here holds a flat curve, where the two are
// the same number and the distinction is invisible -- which is exactly how a
// mutant swapping them survived. This curve triples over the window, so the
// two disagree about whether the same trading fired the rule:
//
//	mean   200,000 -> 550,000 traded is 2.75x  -- fires
//	final  300,000 -> 550,000 traded is 1.83x  -- does not
//
// Mean is right because the trades happened THROUGHOUT the window, and judging
// them against the equity that existed only at the end flatters any account
// whose portfolio grew.
func TestBehavior_OvertradingDividesByMeanEquityNotFinalEquity(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3}, []float64{100, 200, 300}),
		"SPY":  priced([]int{1, 2, 3}, []float64{100, 100, 100}),
	}
	trades := []service.Trade{
		tradeAt("b1", "AAPL", service.SideBuy, 1000, 100, 1, nil), // 100,000
		// A matched pair on the last day: 450,000 more traded, and no net
		// effect on cash or holdings, so the curve is untouched.
		tradeAt("b2", "AAPL", service.SideBuy, 750, 300, 3, nil),
		tradeAt("s2", "AAPL", service.SideSell, 750, 300, 3, pl(0)),
	}

	r := reconstruct(t, trades, barsBySymbol)
	// Precondition: the curve really does vary, or this proves nothing.
	if r.Equity[0] != 100_000 || r.Equity[len(r.Equity)-1] != 300_000 {
		t.Fatalf("fixture: equity runs %v, want 100000 -> 300000", r.Equity)
	}

	got := behavior(t, trades, barsBySymbol)
	f, fired := findingFor(got, service.FindingOvertrading)
	if !fired {
		t.Fatal("did not fire: 550,000 traded against a 200,000 mean is 2.75x")
	}
	if math.Abs(f.TurnoverRatio-2.75) > 1e-9 {
		t.Errorf("turnover: got %v, want 2.75 (550,000 / 200,000)", f.TurnoverRatio)
	}
}

// A sell placed after the last stored close has no "previous trading day" in
// the data at all. sort.Search over the calendar falls off the end for such a
// trade, and calendar[i-1]/calendar[i-2] then name the last two days of the
// window -- which may be weeks earlier and have nothing to do with the sell.
//
// This path was unreachable until SPEC.md §2.12 was amended: before the
// amendment a trade after the last close made Reconcile refuse and no section
// was computed. Making truncation survivable made it routine, and this rule
// was the one place that read the calendar without bounding itself by
// `as_of_date` the way `overtrading` already did.
//
// Found in T15's manual pass, where a real GOOGL sell sat 23 days past the
// last stored bar.
func TestBehavior_PanicSellingIgnoresTradesPastTheCalendar(t *testing.T) {
	// Days 1 and 2 are the whole calendar, and day 1 -> day 2 is a 50% fall.
	// The sell is on day 40, long after it.
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2}, []float64{100, 50}),
		"SPY":  priced([]int{1, 2}, []float64{100, 100}),
	}
	trades := []service.Trade{
		tradeAt("b", "AAPL", service.SideBuy, 10, 100, 1, nil),
		tradeAt("late", "AAPL", service.SideSell, 10, 40, 40, pl(-600)),
	}

	assertNoFinding(t, behavior(t, trades, barsBySymbol), service.FindingPanicSelling)
}

// A sell past the calendar is excluded from the DENOMINATOR too, not just
// from the drop check. Counting a sell the rule refused to judge would dilute
// the share test with a trade that never had a chance to qualify.
//
// The fixture isolates that choice: 2 qualifying sells is below
// PanicSellMinOccurrences, so the count arm cannot fire and only the share
// arm decides. 2/6 = 33% clears PanicSellMinShareOfSells; adding the excluded
// seventh sell back would make it 2/7 = 29% and silence the finding.
func TestBehavior_PanicSellingExcludesPastCalendarSellsFromTheShare(t *testing.T) {
	days := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	barsBySymbol := map[string][]service.Bar{
		// Falls on day 3 and again on day 8; flat everywhere else.
		"AAPL": priced(days, []float64{100, 100, 90, 90, 90, 90, 90, 80, 80, 80, 80, 80}),
		"SPY":  priced(days, []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100}),
	}
	trades := []service.Trade{
		tradeAt("b", "AAPL", service.SideBuy, 100, 100, 1, nil),
		tradeAt("p1", "AAPL", service.SideSell, 10, 90, 4, pl(-100)),  // after the day-3 fall
		tradeAt("n1", "AAPL", service.SideSell, 10, 90, 6, pl(-100)),  // flat behind it
		tradeAt("p2", "AAPL", service.SideSell, 10, 80, 9, pl(-200)),  // after the day-8 fall
		tradeAt("n2", "AAPL", service.SideSell, 10, 80, 10, pl(-200)), // flat behind it
		tradeAt("n3", "AAPL", service.SideSell, 10, 80, 11, pl(-200)),
		tradeAt("n4", "AAPL", service.SideSell, 10, 80, 12, pl(-200)),
		tradeAt("late", "AAPL", service.SideSell, 10, 80, 40, pl(-200)), // past the calendar
	}

	section := behavior(t, trades, barsBySymbol)
	f, ok := findingFor(section, service.FindingPanicSelling)
	if !ok {
		t.Fatalf("panic_selling did not fire; 2 of 6 in-window sells is above the %.0f%% share",
			service.PanicSellMinShareOfSells*100)
	}
	if f.Occurrences != 2 {
		t.Errorf("occurrences: got %d, want 2", f.Occurrences)
	}
	if want := "2 of 6 sells"; !strings.Contains(f.Detail, want) {
		t.Errorf("detail %q does not report %q -- the excluded sell leaked into the denominator", f.Detail, want)
	}
	assertEvidence(t, f, "p1", "p2")
}

// The sibling of the test above, at the OTHER end of the calendar.
//
// A sell in the window's first two sessions is unjudgeable for the same reason
// a post-calendar one is -- there is no pair of prior sessions to have fallen
// across -- so it must leave the share test's denominator by the same rule.
// It did not, until this test: `sells` was incremented for every in-window
// sell and only the drop test refused the early ones, so unjudgeable sells
// diluted the share and kept the finding quiet.
//
// One judgeable sell that IS a panic is 100% of what the rule could evaluate.
// Counting the three it could not drags that to 25%, below the 30% threshold.
func TestBehavior_PanicSellingExcludesEarlySellsFromTheShare(t *testing.T) {
	days := []int{1, 2, 3, 4, 5}
	barsBySymbol := map[string][]service.Bar{
		// Flat until the day-4 fall, so nothing in the first sessions could
		// have qualified even if it had been judged.
		"AAPL": priced(days, []float64{100, 100, 100, 90, 90}),
		"SPY":  priced(days, []float64{100, 100, 100, 100, 100}),
	}
	trades := []service.Trade{
		tradeAt("b", "AAPL", service.SideBuy, 100, 100, 1, nil),
		// Days 1 and 2 are the window's first two sessions: no prior pair.
		tradeAt("e1", "AAPL", service.SideSell, 1, 100, 1, pl(-1)),
		tradeAt("e2", "AAPL", service.SideSell, 1, 100, 2, pl(-1)),
		tradeAt("e3", "AAPL", service.SideSell, 1, 100, 2, pl(-1)),
		// Day 5, judged against the day-4 fall, at a loss: a panic sell.
		tradeAt("p1", "AAPL", service.SideSell, 1, 90, 5, pl(-10)),
	}

	section := behavior(t, trades, barsBySymbol)
	f, ok := findingFor(section, service.FindingPanicSelling)
	if !ok {
		t.Fatalf("panic_selling did not fire; the only judgeable sell was a panic, "+
			"which is 100%% and above the %.0f%% share", service.PanicSellMinShareOfSells*100)
	}
	if f.Occurrences != 1 {
		t.Errorf("occurrences: got %d, want 1", f.Occurrences)
	}
	if want := "1 of 1 sells"; !strings.Contains(f.Detail, want) {
		t.Errorf("detail %q does not report %q -- the three early sells leaked into the denominator", f.Detail, want)
	}
	assertEvidence(t, f, "p1")
}
