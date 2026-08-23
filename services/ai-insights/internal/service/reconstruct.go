package service

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/kpeguero/quantsim/pkg/portfoliomath"
)

// dustQuantity is the threshold below which a position is treated as closed.
//
// Quantities are NUMERIC(20,4) in Postgres, so four decimals is the finest
// distinction that can exist in the data; 1e-9 is far below that and can only
// ever catch float64 residue from a sequence of adds and subtracts that should
// have cancelled exactly. Without it, a position bought and fully sold can
// linger at ~1e-17 shares and be reported as an open holding with a market
// value of nine zeroes and a rounding error.
const dustQuantity = 1e-9

// sqrtTradingDays annualizes a daily standard deviation. Its square is
// portfoliomath.TradingDaysPerYear, so the two can never disagree.
var sqrtTradingDays = math.Sqrt(portfoliomath.TradingDaysPerYear)

// missingCloseError is the one place ErrMissingClose is formatted, so the
// message reads identically wherever the inconsistency is noticed.
func missingCloseError(symbol string, date time.Time) error {
	return fmt.Errorf("%w: %s on %s", ErrMissingClose, symbol, date.Format("2006-01-02"))
}

// Reconstruct derives a portfolio's daily equity curve from its trade log and
// stored bars (SPEC.md §2.1).
//
// QuantSim stores no portfolio history -- accounts.balance and positions are a
// single mutable snapshot of now -- but the history is exactly derivable, and
// "exactly" is load-bearing rather than a boast:
//
//   - Cash is exact. Every account opens at StartingBalance and there are no
//     deposits, withdrawals, fees or interest anywhere in the system, so every
//     cash movement that can ever occur is a trade in this log.
//   - Holdings are exact, being the signed sum of those same trades.
//   - Valuation is stored: close(symbol, date) is a lookup, not a model.
//
// calendar comes from Calendar and must have been built from every symbol the
// account has ever held (plus any benchmark the caller intends to compare
// against). Passing a narrower set is the one way to reach ErrMissingClose.
//
// It must also be ASCENDING, which Calendar guarantees. This is a precondition
// rather than something checked here: a descending calendar would produce a
// reversed curve with no error, so if anything other than Calendar ever feeds
// this, it owes the same ordering guarantee.
//
// Note what the calendar being a flat intersection costs at its TAIL: one
// symbol whose bars stop early truncates the whole curve there, and Holdings
// is then the position map as of that earlier date rather than today. That is
// silent here by design -- Reconstruct reports what the calendar supports --
// and it is disclosed downstream by as_of_date, which names the date the
// holdings actually describe (SPEC.md §2.12 as amended). The guard does not
// refuse it, because "the curve is truncated" and "the user traded after the
// last close" are the same arithmetic and refusing both would blank the report
// for anyone who traded today.
//
// The window starts at the first trade. Calendar dates before it are dropped:
// the portfolio was a flat StartingBalance then, carrying no information, and
// including that stretch would drag every return, volatility and drawdown
// figure toward zero in proportion to how long the account sat idle before it
// was used -- reporting the age of the account rather than the behaviour of
// the portfolio.
//
// An account with no trades reconstructs to an empty Reconstruction and no
// error. "Nothing has happened yet" is an answer; the caller decides whether
// that is an insufficient_data section (SPEC.md §2.10).
func Reconstruct(trades []Trade, barsBySymbol map[string][]Bar, calendar []time.Time) (Reconstruction, error) {
	if len(trades) == 0 || len(calendar) == 0 {
		return Reconstruction{Holdings: map[string]float64{}}, nil
	}

	// Sorted by DAY, not by timestamp, and stably.
	//
	// Within-day order does not change the state the fold reaches to any
	// precision anyone reads: cash and holding deltas are additive, so any
	// permutation of one day's trades lands on the same figures. Sorting by
	// day makes that explicit instead of relying on it, and it is what makes
	// the curve insensitive to the arbitrary order two trades sharing an
	// executed_at come back in, which GET /trading/trades cannot determine
	// (its ORDER BY breaks such ties on a random UUID; see trading-engine's
	// ListTrades).
	//
	// "Same state" is a statement about figures, NOT about bits. Float64
	// addition is not associative, so a different within-day order shifts
	// cash in its last bits, exactly as map iteration order used to shift
	// equity (see sortedSymbols). It is not a source of hash drift only
	// because those tie-breaking UUIDs are fixed per row, so the same trades
	// come back in the same arbitrary order every time. That makes the
	// guarantee BORROWED from trading-engine's query rather than intrinsic
	// here. Sorting the fold by trade ID would make it intrinsic; it would
	// also change every existing report hash, so it waits for a defect that
	// actually bites (SPEC.md Step 23 §4.3).
	ordered := make([]Trade, len(trades))
	copy(ordered, trades)
	sort.SliceStable(ordered, func(i, j int) bool {
		return Day(ordered[i].ExecutedAt).Before(Day(ordered[j].ExecutedAt))
	})

	closes := closesBySymbolAndDay(barsBySymbol)
	firstTradeDay := Day(ordered[0].ExecutedAt)

	var (
		cash     = StartingBalance
		holdings = map[string]float64{}
		next     int // index into ordered: the first trade not yet applied
		out      = Reconstruction{Holdings: holdings}
	)

	for _, date := range calendar {
		if date.Before(firstTradeDay) {
			continue
		}

		// Apply every trade up to and including this date -- "including",
		// so a trade's own day already reflects it.
		//
		// The <= comparison rather than == matters: a trade can fall on a date
		// the calendar does not contain, because some other symbol had no bar
		// that day. That trade still happened and still moved cash. Matching
		// on equality would silently drop it, and the error would surface much
		// later as an equity curve that disagrees with the live balance.
		for next < len(ordered) && !Day(ordered[next].ExecutedAt).After(date) {
			cash = applyTrade(ordered[next], cash, holdings)
			next++
		}

		equity := cash
		for _, symbol := range sortedSymbols(holdings) {
			px, ok := closes[symbol][date]
			if !ok {
				return Reconstruction{}, missingCloseError(symbol, date)
			}
			equity += holdings[symbol] * px
		}

		out.Dates = append(out.Dates, date)
		out.Cash = append(out.Cash, cash)
		out.Equity = append(out.Equity, equity)
	}

	return out, nil
}

// sortedSymbols returns m's keys in ascending order.
//
// Summing a portfolio in map iteration order is the one thing that made
// ReportHash useless. Go randomizes that order per pass, and float64 addition
// is NOT associative, so the same holdings at the same closes sum to results
// that differ in the last bits depending on where the iteration happened to
// start. Every displayed figure rounds that difference away and every test
// with a tolerance passes; the hash is defined on the exact bytes and sees all
// of it. Measured before the fix: 199 distinct equity curves over 200
// reconstructions of identical input (Step 23).
//
// Note this is a DIFFERENT concern from the sorts in Calendar and ComputeRisk,
// which order a result a reader sees. This one orders an accumulation nobody
// sees, and it is load-bearing for exactly that reason.
func sortedSymbols(m map[string]float64) []string {
	dst := make([]string, 0, len(m))
	for symbol := range m {
		dst = append(dst, symbol)
	}
	sort.Strings(dst)
	return dst
}

// applyTrade folds one execution into a running cash balance and holdings
// map, returning the new cash. holdings is mutated in place.
//
// Extracted so Reconstruct and Reconcile cannot disagree about what a trade
// does. That matters more than it looks: the guard projects post-calendar
// trades forward and compares the result against the live account
// (SPEC.md §2.12), so if the two folds ever drifted -- a sign, a dust rule --
// the guard would refuse every request and blame the account.
//
// A non-buy side is treated as a buy rather than rejected. Side comes off a
// trading-engine response and is "buy" or "sell"; there is no third value to
// have an opinion about, and inventing an error path for one would be an
// untestable branch guarding an impossible input.
func applyTrade(t Trade, cash float64, holdings map[string]float64) float64 {
	switch t.Side {
	case SideSell:
		cash += t.Quantity * t.Price
		holdings[t.Symbol] -= t.Quantity
	default:
		cash -= t.Quantity * t.Price
		holdings[t.Symbol] += t.Quantity
	}
	// A position bought and fully sold can linger at ~1e-17 shares and be
	// reported as an open holding with a market value of nine zeroes and a
	// rounding error.
	if q := holdings[t.Symbol]; q > -dustQuantity && q < dustQuantity {
		delete(holdings, t.Symbol)
	}
	return cash
}

// closesBySymbolAndDay indexes closing prices for lookup by (symbol, date).
//
// A later bar for a date wins. market-data ingests only "1Day" bars, so no
// date has two -- but if that ever changes, the day's final close is the one a
// mark-to-market wants, and picking it deliberately beats whichever the range
// happened to reach last.
func closesBySymbolAndDay(barsBySymbol map[string][]Bar) map[string]map[time.Time]float64 {
	closes := make(map[string]map[time.Time]float64, len(barsBySymbol))
	latest := make(map[string]map[time.Time]time.Time, len(barsBySymbol))

	for symbol, bars := range barsBySymbol {
		closes[symbol] = make(map[time.Time]float64, len(bars))
		latest[symbol] = make(map[time.Time]time.Time, len(bars))
		for _, b := range bars {
			day := Day(b.Timestamp)
			if seen, ok := latest[symbol][day]; ok && b.Timestamp.Before(seen) {
				continue
			}
			latest[symbol][day] = b.Timestamp
			closes[symbol][day] = b.Close
		}
	}
	return closes
}
