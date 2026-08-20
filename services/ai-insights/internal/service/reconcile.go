package service

import (
	"fmt"
	"math"
	"sort"
)

// Tolerances for the reconciliation guard.
//
// Money and quantities are NUMERIC(20,4) in Postgres and float64 in Go, so a
// derived total and a stored one can differ in the last bits without either
// being wrong. A cent for cash and 1e-4 for quantity are exactly
// NUMERIC(20,4)'s finest real distinctions -- tight enough that any genuine
// divergence (a dropped trade, a truncated calendar) is orders of magnitude
// larger, and loose enough that float64 accumulation never trips it.
const (
	cashTolerance     = 0.01
	quantityTolerance = 1e-4
)

// Reconcile checks the reconstruction against the live account before any
// section is computed (SPEC.md §2.12).
//
// The reconstruction is derived from the trade log; accounts.balance and
// positions are what trading-engine actually stores. They are two routes to
// the same facts, so they must agree -- and when they do not, the derived one
// is the one to distrust.
//
// This exists because §2.1's calendar is a flat intersection over every symbol
// the account has EVER held. A gap in the middle of one symbol's bars drops a
// single date, which is intended. A gap at the tail -- a symbol delisted,
// renamed, or missing from an ingest -- truncates the whole curve there, and
// Reconstruction.Holdings is then the position map as of that earlier date.
// Silently. That map is what ComputeRisk weighs, so without this guard a
// portfolio of 100 shares gets reported as 10 with no indication anything is
// wrong.
//
// It is a refusal, not a repair. Reconciling TOWARD the live account would
// mean inventing equity history for the dates the calendar could not cover,
// which is precisely the fabricated data this service exists not to produce.
//
// AMENDED (SPEC.md §2.12, Checkpoint C): the comparison is made against the
// derived state PROJECTED FORWARD through the trades executed after the last
// calendar date -- not against the derived state directly.
//
// The drafted guard compared directly, and that blanked the entire report
// after any trade placed since the last stored close. Verified end to end: an
// account with 39 trading days of history reported fully, then one 1-share buy
// placed the same day turned every section into insufficient_data. The cause
// is structural rather than a bug -- the reconstruction ends at the last
// stored bar and the live account is NOW, so any trade in between makes them
// differ, which is the normal case intraday.
//
// The root of it: "the curve is truncated" and "the user traded after the last
// close" are ARITHMETICALLY THE SAME EVENT, and a direct comparison cannot
// tell them apart. Projecting the post-calendar trades forward separates them.
// What remains after the projection is unexplained by anything this service
// knows about, and that is what a refusal should mean.
//
// So a truncated tail is no longer a refusal; it is an honest report whose
// as_of_date names the date its holdings actually describe. That is the half
// of this guard given back deliberately -- truncation was always disclosed by
// as_of_date, and what made it dangerous was being silent about being stale
// rather than being stale.
//
// What it still catches: a dropped or duplicated trade in GET /trading/trades,
// a mis-signed side, a wrong StartingBalance, and any divergence in
// Reconstruct's fold over the calendar -- which is the part with the calendar
// logic in it, and therefore the part worth guarding.
//
// An empty reconstruction reconciles trivially. There is no derived state to
// contradict the account, and the caller already routes an empty curve to
// insufficient_data on its own.
func Reconcile(r Reconstruction, trades []Trade, live LivePortfolio) error {
	if len(r.Dates) == 0 {
		return nil
	}

	asOf := r.Dates[len(r.Dates)-1]
	derivedCash, derivedHoldings := projectPastCalendar(r, trades)

	if math.Abs(derivedCash-live.Balance) > cashTolerance {
		return fmt.Errorf("%w: cash %.4f derived from the trade log through %s, account holds %.4f",
			ErrReconciliation, derivedCash, asOf.Format("2006-01-02"), live.Balance)
	}

	livePositions := make(map[string]float64, len(live.Positions))
	for _, p := range live.Positions {
		// trading-engine filters sold-out positions already; this guards
		// against a zero slipping through and reading as a real divergence
		// from a holding the reconstruction correctly dropped.
		if math.Abs(p.Quantity) > quantityTolerance {
			livePositions[p.Symbol] = p.Quantity
		}
	}

	// Symbols scanned in sorted order so a portfolio with several divergences
	// always names the same one, rather than whichever the map happened to
	// yield first -- the same determinism FetchHistories' error scan has.
	for _, symbol := range sortedUnion(derivedHoldings, livePositions) {
		derived, inDerived := derivedHoldings[symbol]
		actual, inLive := livePositions[symbol]

		switch {
		case !inLive:
			return fmt.Errorf("%w: %s reconstructed at %.4f but the account holds none",
				ErrReconciliation, symbol, derived)
		case !inDerived:
			return fmt.Errorf("%w: account holds %.4f %s but the reconstruction has none",
				ErrReconciliation, actual, symbol)
		case math.Abs(derived-actual) > quantityTolerance:
			return fmt.Errorf("%w: %s reconstructed at %.4f, account holds %.4f",
				ErrReconciliation, symbol, derived, actual)
		}
	}

	return nil
}

// projectPastCalendar rolls the reconstruction's final state forward through
// every trade the calendar could not cover, so the comparison is like for like
// -- a state as of NOW against an account as of NOW.
//
// Trades are applied in whatever order they arrive. Cash and holding deltas
// are additive, so any permutation reaches the same state; sorting here would
// be ceremony that suggests an ordering dependency that does not exist.
//
// The reconstruction's own maps are not mutated. Holdings is returned to the
// caller inside Reconstruction and is what ComputeRisk weighs -- folding
// today's trades into it here would make the risk section silently describe a
// composition its own as_of_date contradicts, which is the exact confusion
// this guard exists to prevent.
func projectPastCalendar(r Reconstruction, trades []Trade) (float64, map[string]float64) {
	asOf := r.Dates[len(r.Dates)-1]

	cash := r.Cash[len(r.Cash)-1]
	holdings := make(map[string]float64, len(r.Holdings))
	for symbol, qty := range r.Holdings {
		holdings[symbol] = qty
	}

	for _, t := range trades {
		if Day(t.ExecutedAt).After(asOf) {
			cash = applyTrade(t, cash, holdings)
		}
	}
	return cash, holdings
}

func sortedUnion(a, b map[string]float64) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
