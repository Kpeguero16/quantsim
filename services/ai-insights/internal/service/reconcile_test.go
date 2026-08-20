package service_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

func live(balance float64, positions ...service.LivePosition) service.LivePortfolio {
	return service.LivePortfolio{Balance: balance, Positions: positions}
}

func pos(symbol string, qty float64) service.LivePosition {
	return service.LivePosition{Symbol: symbol, Quantity: qty}
}

// The happy path: the trade log and the account are two routes to the same
// facts, and normally they agree exactly.
func TestReconcile_AgreementPasses(t *testing.T) {
	trades := []service.Trade{buy("AAPL", 10, 100, 1)}
	r := reconstruct(t, trades,
		map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)})

	if err := service.Reconcile(r, trades, live(99000, pos("AAPL", 10))); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

// Both sides of the cash threshold. A cent is float64 noise; two cents is a
// real disagreement.
func TestReconcile_CashToleranceBoundaries(t *testing.T) {
	trades := []service.Trade{buy("AAPL", 10, 100, 1)}
	r := reconstruct(t, trades,
		map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)})

	tests := []struct {
		name      string
		balance   float64
		wantError bool
	}{
		{"exact", 99000, false},
		{"one cent under", 98999.99, false},
		{"one cent over", 99000.01, false},
		{"two cents under", 98999.98, true},
		{"two cents over", 99000.02, true},
		{"wildly different", 90000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.Reconcile(r, trades, live(tt.balance, pos("AAPL", 10)))
			if gotError := err != nil; gotError != tt.wantError {
				t.Fatalf("balance %.2f gave err=%v, wantError=%v", tt.balance, err, tt.wantError)
			}
			if tt.wantError && !errors.Is(err, service.ErrReconciliation) {
				t.Errorf("got %v, want ErrReconciliation", err)
			}
		})
	}
}

func TestReconcile_HoldingDisagreements(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": bars(1, 2), "MSFT": bars(1, 2), "SPY": bars(1, 2),
	}
	trades := []service.Trade{buy("AAPL", 10, 100, 1)}
	r := reconstruct(t, trades, barsBySymbol)

	// wantPhrase pins WHICH branch fired, not merely that something did.
	// Asserting only "the message names AAPL" cannot tell the three cases
	// apart -- the quantity-mismatch branch catches a missing position too,
	// via its zero-valued quantity, so deleting either explicit branch would
	// leave every test green while the diagnostic silently got worse.
	tests := []struct {
		name       string
		live       service.LivePortfolio
		wantError  bool
		wantPhrase string
	}{
		{"identical", live(99000, pos("AAPL", 10)), false, ""},
		{"within quantity tolerance", live(99000, pos("AAPL", 10.00005)), false, ""},
		{"outside quantity tolerance", live(99000, pos("AAPL", 10.001)), true,
			"AAPL reconstructed at 10.0000, account holds 10.0010"},
		{"account holds none of a reconstructed symbol", live(99000), true,
			"AAPL reconstructed at 10.0000 but the account holds none"},
		{"account holds a symbol the reconstruction lacks",
			live(99000, pos("AAPL", 10), pos("MSFT", 5)), true,
			"account holds 5.0000 MSFT but the reconstruction has none"},
		{"a sold-out live position does not count as a divergence",
			live(99000, pos("AAPL", 10), pos("MSFT", 0)), false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.Reconcile(r, trades, tt.live)
			if gotError := err != nil; gotError != tt.wantError {
				t.Fatalf("got err=%v, wantError=%v", err, tt.wantError)
			}
			if tt.wantError && !strings.Contains(err.Error(), tt.wantPhrase) {
				t.Errorf("error %q\n does not contain %q", err, tt.wantPhrase)
			}
		})
	}
}

// Several divergences at once must always name the same one, rather than
// whichever the map happened to yield first.
func TestReconcile_NamesTheSameSymbolEveryTime(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": bars(1, 2), "MSFT": bars(1, 2), "TSLA": bars(1, 2), "SPY": bars(1, 2),
	}
	trades := []service.Trade{
		buy("AAPL", 10, 100, 1), buy("MSFT", 10, 100, 1), buy("TSLA", 10, 100, 1),
	}
	r := reconstruct(t, trades, barsBySymbol)

	// All three disagree; AAPL sorts first.
	l := live(97000, pos("AAPL", 1), pos("MSFT", 1), pos("TSLA", 1))
	for i := 0; i < 50; i++ {
		err := service.Reconcile(r, trades, l)
		if err == nil || !strings.Contains(err.Error(), "AAPL") {
			t.Fatalf("run %d: got %v, want the alphabetically first divergence (AAPL)", i, err)
		}
	}
}

// An empty reconstruction has no derived state to contradict the account, and
// the caller already routes it to insufficient_data.
func TestReconcile_EmptyReconstructionPasses(t *testing.T) {
	r := reconstruct(t, nil, map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)})

	if err := service.Reconcile(r, nil, live(100000)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

// The truncated calendar, revisited under SPEC §2.12 as amended.
//
// TSLA's bars stop at day 2, so the calendar stops there too -- even though
// AAPL and SPY are priced through day 4. The day-4 buy therefore never reaches
// Holdings, which holds 10 AAPL for an account really holding 100.
//
// The drafted guard refused this. It no longer does, and that is the
// deliberate half given back: the day-4 buy is a trade after the last calendar
// date, which is arithmetically indistinguishable from a user who simply
// traded today. Refusing both would blank the report for anyone with recent
// activity -- the failure Checkpoint C found against the live stack.
//
// What makes it safe is disclosure rather than detection: as_of_date names the
// date the holdings describe, so a section reporting 10 AAPL is labelled "as
// of day 2", which is TRUE. The projection is also proof the two states are
// consistent -- 10 AAPL on day 2 plus a 90-share buy on day 4 IS 100 AAPL.
func TestReconcile_ATruncatedTailIsExplainedByItsOwnTrades(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": bars(1, 2, 3, 4),
		"SPY":  bars(1, 2, 3, 4),
		"TSLA": bars(1, 2), // stops early: delisted, renamed, or a missing ingest
	}
	trades := []service.Trade{
		buy("TSLA", 1, 300, 1),
		sell("TSLA", 1, 310, 2),
		buy("AAPL", 10, 100, 1),
		buy("AAPL", 90, 100, 4), // after the calendar ends
	}
	r := reconstruct(t, trades, barsBySymbol)

	// Precondition: the truncation really happened, so this test is exercising
	// what it claims to. If Calendar is ever fixed to qualify each date by the
	// symbols held on it, this fails loudly rather than passing for a new
	// reason.
	if got := r.Holdings["AAPL"]; got != 10 {
		t.Fatalf("fixture no longer truncates: AAPL reconstructed at %v, want 10", got)
	}

	wantCash, wantHoldings := naiveFinalState(trades)
	if err := service.Reconcile(r, trades, live(wantCash, pos("AAPL", wantHoldings["AAPL"]))); err != nil {
		t.Fatalf("Reconcile: %v -- the truncation is fully explained by the day-4 buy", err)
	}
}

// The regression test for what Checkpoint C found against the live stack: an
// account with real history, one trade placed after the last stored close, and
// the entire report going insufficient_data.
//
// Here the calendar ends at day 2 for every symbol -- the ordinary case where
// market-data's bars simply do not cover today yet -- and the account traded
// on day 4.
func TestReconcile_ATradeAfterTheLastCloseIsNotADivergence(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)}
	trades := []service.Trade{
		buy("AAPL", 50, 100, 1),
		buy("AAPL", 1, 316.67, 4), // today; no bar exists for it yet
	}
	r := reconstruct(t, trades, barsBySymbol)

	if got := len(r.Dates); got != 2 {
		t.Fatalf("fixture: want a 2-day curve, got %d", got)
	}

	wantCash, wantHoldings := naiveFinalState(trades)
	if err := service.Reconcile(r, trades, live(wantCash, pos("AAPL", wantHoldings["AAPL"]))); err != nil {
		t.Fatalf("Reconcile: %v -- one trade since the last close must not blank the report", err)
	}
}

// What the guard is still FOR. The projection replays the trades this service
// was given, so a trade the account made but GET /trading/trades never
// returned is still unexplained -- and that is a derivation this service
// cannot be trusted to have got right.
func TestReconcile_ADroppedTradeIsStillCaught(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)}
	all := []service.Trade{
		buy("AAPL", 10, 100, 1),
		buy("AAPL", 5, 100, 4), // happened, and reflected in the live account
	}
	// What this service actually received: the day-4 trade is missing.
	seen := all[:1]

	r := reconstruct(t, seen, barsBySymbol)
	wantCash, wantHoldings := naiveFinalState(all)

	err := service.Reconcile(r, seen, live(wantCash, pos("AAPL", wantHoldings["AAPL"])))
	if !errors.Is(err, service.ErrReconciliation) {
		t.Fatalf("got %v, want ErrReconciliation -- a trade missing from the log is "+
			"exactly the derivation error this guard is for", err)
	}
}

// A mis-signed sell is still caught, because the pre-calendar trades reach the
// live account only through Reconstruct's fold -- the part with the calendar
// logic in it.
func TestReconcile_AWrongDerivedStateIsStillCaught(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)}
	trades := []service.Trade{buy("AAPL", 10, 100, 1)}
	r := reconstruct(t, trades, barsBySymbol)

	// The account the trade log describes is 99000/10 AAPL. This one is not.
	err := service.Reconcile(r, trades, live(99000, pos("AAPL", 4)))
	if !errors.Is(err, service.ErrReconciliation) {
		t.Fatalf("got %v, want ErrReconciliation", err)
	}
}

// The projection must not write into the reconstruction.
//
// Holdings is what ComputeRisk weighs and what as_of_date labels. Folding
// today's trades into it here would make the risk section describe a
// composition its own as_of_date contradicts -- the exact confusion this guard
// exists to prevent, reintroduced by the fix for it.
func TestReconcile_DoesNotMutateTheReconstruction(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)}
	trades := []service.Trade{
		buy("AAPL", 10, 100, 1),
		buy("AAPL", 90, 100, 4), // after the calendar
	}
	r := reconstruct(t, trades, barsBySymbol)

	beforeCash := r.Cash[len(r.Cash)-1]
	wantCash, wantHoldings := naiveFinalState(trades)
	if err := service.Reconcile(r, trades, live(wantCash, pos("AAPL", wantHoldings["AAPL"]))); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := r.Holdings["AAPL"]; got != 10 {
		t.Errorf("Holdings mutated: AAPL is %v, want the as-of-date value 10", got)
	}
	if got := r.Cash[len(r.Cash)-1]; got != beforeCash {
		t.Errorf("Cash mutated: got %v, want %v", got, beforeCash)
	}
}
