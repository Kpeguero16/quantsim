package service

import (
	"encoding/json"
	"fmt"
)

// StrategyKind identifies which named strategy a backtest run used --
// persisted as `strategy` and echoed on the wire (SPEC.md Step 18 §2.1).
type StrategyKind string

const (
	StrategyMACrossover StrategyKind = "ma_crossover"
	StrategyRSI         StrategyKind = "rsi"
	StrategyMACD        StrategyKind = "macd"
)

// Strategy is one configured, validated strategy instance -- built once by
// NewStrategy from a request's {strategy, params}, then consumed by
// RunBacktest's pipeline. These four methods are the only strategy-dependent
// things the pipeline needs (SPEC.md Step 18 §2.1); everything downstream of
// []Signal -- Simulate, ComputeMetrics, backtest_trades -- has no idea which
// of these produced its input, and none of them change in this step.
type Strategy interface {
	// Kind identifies which strategy this is, for persistence and display.
	Kind() StrategyKind

	// Params is the canonical, validated encoding of this strategy's
	// parameters -- what gets persisted and echoed back, never the raw
	// bytes a caller sent (SPEC.md §2.6). Pre-marshaled at construction, so
	// a caller of this method can never hit a marshal error.
	Params() json.RawMessage

	// WarmupBars is the number of leading bars this strategy's indicator
	// needs before it has its first value -- not the number needed for a
	// signal to be possible, which is one bar more (SPEC.md §2.4). A
	// request whose ranged bars fall short of this is ErrInvalidRequest,
	// preserving Step 16's exact `long_window > len(ranged)` rejection.
	WarmupBars() int

	// GenerateSignals computes this strategy's buy/sell/none signal for
	// every bar, decided from bars[0..i] only -- the same lookahead-safety
	// contract every strategy shares (SPEC.md §2.4). A GenerateSignals call
	// with fewer bars than WarmupBars requires returns all SignalNone
	// rather than panicking -- callers are expected to check WarmupBars
	// first (RunBacktest does), but this stays defensive rather than
	// trusting that every caller will.
	GenerateSignals(bars []Bar) []Signal
}

// maxWarmupBars bounds every strategy's WarmupBars, checked once here after
// construction rather than by each constructor separately (SPEC.md §2.4).
// This generalizes Step 16's maxLongWindow=500 (which bounded only
// long_window, on the unstated assumption that long_window alone was the
// whole warm-up cost) to whatever WarmupBars each strategy actually
// reports -- the case this exists to catch is a MACD whose slow+signal-1
// exceeds 500 while slow alone does not, which a per-field bound on
// slow_period could never see. 500 matches Step 16's original bound: the
// ~501 bars any symbol has ingested today.
//
// This checks the sum, not the addends -- but newRSIStrategy and
// newMACDStrategy also reject an individual period/slow_period/
// signal_period above maxWarmupBars before this ever runs, so those
// addends can never be large enough to overflow int when summed here.
// That per-addend check isn't redundant with this one: without it, a
// pathological single period (e.g. near math.MaxInt) overflows the sum to
// a negative number, which would pass this ">" check.
const maxWarmupBars = 500

// NewStrategy decodes and validates one strategy's parameters -- the single
// place an unknown kind, a malformed params object, an out-of-bounds
// parameter, or a warm-up longer than any available range all surface as
// ErrInvalidRequest (SPEC.md §2.1), instead of being scattered across three
// constructors.
func NewStrategy(kind StrategyKind, raw json.RawMessage) (Strategy, error) {
	strategy, err := newStrategyByKind(kind, raw)
	if err != nil {
		return nil, err
	}
	if strategy.WarmupBars() > maxWarmupBars {
		return nil, fmt.Errorf("%w: this strategy's parameters need %d warm-up bars, more than the %d maximum",
			ErrInvalidRequest, strategy.WarmupBars(), maxWarmupBars)
	}
	return strategy, nil
}

func newStrategyByKind(kind StrategyKind, raw json.RawMessage) (Strategy, error) {
	switch kind {
	case StrategyMACrossover:
		var p maCrossoverParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%w: params: %v", ErrInvalidRequest, err)
		}
		return newMACrossover(p.ShortWindow, p.LongWindow)
	case StrategyRSI:
		var p rsiParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%w: params: %v", ErrInvalidRequest, err)
		}
		return newRSIStrategy(p.Period, p.Oversold, p.Overbought)
	case StrategyMACD:
		var p macdParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%w: params: %v", ErrInvalidRequest, err)
		}
		return newMACDStrategy(p.FastPeriod, p.SlowPeriod, p.SignalPeriod)
	default:
		return nil, fmt.Errorf("%w: unknown strategy %q", ErrInvalidRequest, kind)
	}
}

// minShortWindow bounds maCrossover's short_window lower edge (SPEC.md
// Step 16 §2.8). Its upper edge, long_window, has no per-field bound of its
// own -- maxWarmupBars covers it generically via WarmupBars() instead.
const minShortWindow = 2

// maCrossoverParams is the wire and persisted shape of a moving-average
// crossover strategy's parameters (SPEC.md Step 16 §2.3).
type maCrossoverParams struct {
	ShortWindow int `json:"short_window"`
	LongWindow  int `json:"long_window"`
}

// maCrossover implements Strategy for the moving-average crossover rule
// Step 16 built: buy on a golden cross, sell on a death cross, edge-only.
type maCrossover struct {
	params maCrossoverParams
	raw    json.RawMessage
}

// newMACrossover validates and constructs a maCrossover directly from typed
// parameters -- the entry point NewStrategy uses once its JSON has been
// decoded, and the one this package's tests use to build a strategy without
// a JSON round trip.
func newMACrossover(shortWindow, longWindow int) (*maCrossover, error) {
	if shortWindow < minShortWindow {
		return nil, fmt.Errorf("%w: short_window must be at least %d", ErrInvalidRequest, minShortWindow)
	}
	if longWindow <= shortWindow {
		return nil, fmt.Errorf("%w: long_window must be greater than short_window", ErrInvalidRequest)
	}

	p := maCrossoverParams{ShortWindow: shortWindow, LongWindow: longWindow}
	raw, err := json.Marshal(p)
	if err != nil {
		// Unreachable: p is two ints, which always marshal successfully.
		return nil, fmt.Errorf("marshaling ma_crossover params: %w", err)
	}
	return &maCrossover{params: p, raw: raw}, nil
}

func (m *maCrossover) Kind() StrategyKind      { return StrategyMACrossover }
func (m *maCrossover) Params() json.RawMessage { return m.raw }
func (m *maCrossover) WarmupBars() int         { return m.params.LongWindow }

// GenerateSignals is Step 16's original signal logic, unchanged in
// substance: two moving averages, edge-only crossing detection via
// crossoverSignals (extracted below so macdStrategy can share it -- SPEC.md
// Step 18 §2.3, both rules are "fire on the sign change of two series'
// difference").
func (m *maCrossover) GenerateSignals(bars []Bar) []Signal {
	if m.params.LongWindow > len(bars) {
		return make([]Signal, len(bars))
	}
	shortMA := movingAverages(bars, m.params.ShortWindow)
	longMA := movingAverages(bars, m.params.LongWindow)
	return crossoverSignals(shortMA, longMA, m.params.LongWindow-1)
}

// crossoverSignals fires SignalBuy where fast crosses above slow and
// SignalSell where it crosses below, starting at bar index `from` -- the
// first index both series have a real value at. Edge-only: a signal fires
// only on the crossing bar itself, never on every subsequent bar the series
// spend on the same side (Step 16's strategy.go originally argued why: the
// alternative would force Simulate to guess which repeats to ignore). Shared
// by maCrossover and macdStrategy, whose buy/sell rules are structurally
// identical -- two series, cross on the sign change of their difference.
func crossoverSignals(fast, slow []float64, from int) []Signal {
	signals := make([]Signal, len(fast))
	var wasAbove, haveState bool
	for i := from; i < len(fast); i++ {
		above := fast[i] > slow[i]
		if haveState {
			switch {
			case above && !wasAbove:
				signals[i] = SignalBuy
			case !above && wasAbove:
				signals[i] = SignalSell
			}
		}
		wasAbove = above
		haveState = true
	}
	return signals
}

// rsiParams is the wire and persisted shape of an RSI-threshold strategy's
// parameters (SPEC.md Step 18 §2.2).
type rsiParams struct {
	Period     int     `json:"period"`
	Oversold   float64 `json:"oversold"`
	Overbought float64 `json:"overbought"`
}

// rsiStrategy implements Strategy for Wilder's RSI, signaling on the exit
// from a threshold zone rather than the entry into one (SPEC.md §2.2): the
// entry-triggered alternative buys into an ongoing decline the moment RSI
// first drops below oversold, the classic way a mean-reversion backtest
// catches a falling knife. Waiting for the cross back out is also the same
// edge-triggered shape maCrossover and macdStrategy already share.
type rsiStrategy struct {
	params rsiParams
	raw    json.RawMessage
}

// newRSIStrategy validates and constructs an rsiStrategy directly from
// typed parameters -- NewStrategy's entry point once its JSON has been
// decoded, and this package's tests' entry point to build one without a
// JSON round trip.
func newRSIStrategy(period int, oversold, overbought float64) (*rsiStrategy, error) {
	if period < 2 {
		return nil, fmt.Errorf("%w: period must be at least 2", ErrInvalidRequest)
	}
	// Bounded here, not just by NewStrategy's WarmupBars() > maxWarmupBars
	// check below: WarmupBars is period+1, and an unbounded period near
	// math.MaxInt overflows that addition, wrapping to a negative number
	// that then passes ">  maxWarmupBars" as false. Rejecting an
	// individually-too-large period first keeps that later sum from ever
	// overflowing.
	if period > maxWarmupBars {
		return nil, fmt.Errorf("%w: period must be at most %d", ErrInvalidRequest, maxWarmupBars)
	}
	if !(oversold > 0 && oversold < overbought && overbought < 100) {
		return nil, fmt.Errorf("%w: oversold/overbought must satisfy 0 < oversold < overbought < 100", ErrInvalidRequest)
	}

	p := rsiParams{Period: period, Oversold: oversold, Overbought: overbought}
	raw, err := json.Marshal(p)
	if err != nil {
		// Unreachable: p is an int and two float64s, which always marshal
		// successfully.
		return nil, fmt.Errorf("marshaling rsi params: %w", err)
	}
	return &rsiStrategy{params: p, raw: raw}, nil
}

func (r *rsiStrategy) Kind() StrategyKind      { return StrategyRSI }
func (r *rsiStrategy) Params() json.RawMessage { return r.raw }

// WarmupBars is period+1, not period: wilderRSI's first value needs
// `period` deltas and exists at bar index `period` (SPEC.md §2.4's table),
// but a zone-exit crossing also needs a previous RSI value to compare
// against, which pushes the earliest possible signal one bar later.
func (r *rsiStrategy) WarmupBars() int { return r.params.Period + 1 }

func (r *rsiStrategy) GenerateSignals(bars []Bar) []Signal {
	if r.WarmupBars() > len(bars) {
		return make([]Signal, len(bars))
	}
	rsi := wilderRSI(closesOf(bars), r.params.Period)
	return rsiZoneExitSignals(rsi, r.params.Oversold, r.params.Overbought, r.params.Period+1)
}

// rsiZoneExitSignals fires SignalBuy the bar RSI crosses from at-or-below
// oversold to above it, and SignalSell the bar it crosses from at-or-above
// overbought to below it -- edge-only, starting at `from` (the first bar
// index with both a current and a previous RSI value). oversold < overbought
// is enforced at construction, so the two conditions can never both be true
// on the same bar.
func rsiZoneExitSignals(rsi []float64, oversold, overbought float64, from int) []Signal {
	signals := make([]Signal, len(rsi))
	for i := from; i < len(rsi); i++ {
		prev, cur := rsi[i-1], rsi[i]
		switch {
		case prev <= oversold && cur > oversold:
			signals[i] = SignalBuy
		case prev >= overbought && cur < overbought:
			signals[i] = SignalSell
		}
	}
	return signals
}

// macdParams is the wire and persisted shape of a MACD strategy's
// parameters (SPEC.md Step 18 §2.3).
type macdParams struct {
	FastPeriod   int `json:"fast_period"`
	SlowPeriod   int `json:"slow_period"`
	SignalPeriod int `json:"signal_period"`
}

// macdStrategy implements Strategy for MACD: buy where the MACD line
// (fast EMA minus slow EMA) crosses above its own signal line (an EMA of
// the MACD line), sell where it crosses below -- structurally the same
// "cross on the sign change of two series' difference" rule maCrossover
// uses, sharing crossoverSignals with it (SPEC.md §2.3).
type macdStrategy struct {
	params macdParams
	raw    json.RawMessage
}

// newMACDStrategy validates and constructs a macdStrategy directly from
// typed parameters -- NewStrategy's entry point once its JSON has been
// decoded, and this package's tests' entry point to build one without a
// JSON round trip.
func newMACDStrategy(fastPeriod, slowPeriod, signalPeriod int) (*macdStrategy, error) {
	if fastPeriod < 2 {
		return nil, fmt.Errorf("%w: fast_period must be at least 2", ErrInvalidRequest)
	}
	if slowPeriod <= fastPeriod {
		return nil, fmt.Errorf("%w: slow_period must be greater than fast_period", ErrInvalidRequest)
	}
	if signalPeriod < 2 {
		return nil, fmt.Errorf("%w: signal_period must be at least 2", ErrInvalidRequest)
	}
	// Bounded individually, not just by NewStrategy's WarmupBars() >
	// maxWarmupBars check below: WarmupBars is slowPeriod+signalPeriod-1,
	// and unbounded addends near math.MaxInt overflow that sum, wrapping to
	// a negative number that then passes "> maxWarmupBars" as false.
	// Rejecting either addend when it alone already exceeds the bound keeps
	// the later sum from ever overflowing.
	if slowPeriod > maxWarmupBars {
		return nil, fmt.Errorf("%w: slow_period must be at most %d", ErrInvalidRequest, maxWarmupBars)
	}
	if signalPeriod > maxWarmupBars {
		return nil, fmt.Errorf("%w: signal_period must be at most %d", ErrInvalidRequest, maxWarmupBars)
	}

	p := macdParams{FastPeriod: fastPeriod, SlowPeriod: slowPeriod, SignalPeriod: signalPeriod}
	raw, err := json.Marshal(p)
	if err != nil {
		// Unreachable: p is three ints, which always marshal successfully.
		return nil, fmt.Errorf("marshaling macd params: %w", err)
	}
	return &macdStrategy{params: p, raw: raw}, nil
}

func (m *macdStrategy) Kind() StrategyKind      { return StrategyMACD }
func (m *macdStrategy) Params() json.RawMessage { return m.raw }

// WarmupBars is slow+signal-1: the MACD line is first defined at bar index
// slow-1 (SlowPeriod's own EMA seed), and the signal line -- an EMA of the
// MACD line over `signal` values, itself needing `signal` MACD-line values
// to seed -- is first defined at (slow-1)+(signal-1) = slow+signal-2. One
// bar more than that is the earliest a crossing can fire, matching every
// other strategy's "first-defined-index plus one" convention.
func (m *macdStrategy) WarmupBars() int {
	return m.params.SlowPeriod + m.params.SignalPeriod - 1
}

func (m *macdStrategy) GenerateSignals(bars []Bar) []Signal {
	if m.WarmupBars() > len(bars) {
		return make([]Signal, len(bars))
	}

	closes := closesOf(bars)
	fastEMA := ema(closes, m.params.FastPeriod)
	slowEMA := ema(closes, m.params.SlowPeriod)

	macdLine := make([]float64, len(bars))
	for i := m.params.SlowPeriod - 1; i < len(bars); i++ {
		macdLine[i] = fastEMA[i] - slowEMA[i]
	}

	// The signal line is an EMA of the MACD line, but only the portion of
	// macdLine from slow-1 onward is real -- everything before that is an
	// unwritten 0, not a genuine MACD value. Feeding the whole slice to ema
	// would seed it from those leading zeros instead of the first real
	// value, so ema runs on the defined sub-slice and the result is copied
	// back at the matching offset.
	signalSub := ema(macdLine[m.params.SlowPeriod-1:], m.params.SignalPeriod)
	signalLine := make([]float64, len(bars))
	for i, v := range signalSub {
		signalLine[m.params.SlowPeriod-1+i] = v
	}

	from := m.params.SlowPeriod + m.params.SignalPeriod - 2
	return crossoverSignals(macdLine, signalLine, from)
}

// closesOf extracts each bar's Close, the only field wilderRSI and ema
// operate on -- both take a plain []float64 so they stay reusable by any
// future indicator without depending on the Bar shape.
func closesOf(bars []Bar) []float64 {
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}
	return closes
}

// movingAverages returns the trailing `window`-bar mean of Close, indexed
// the same as bars. Indices before window-1 are 0 and must not be read by
// the caller -- GenerateSignals never does, since it only starts at
// window-1.
//
// A running sum keeps this one pass over bars rather than one pass per bar,
// which matters at MaxHistoryLimit-sized inputs (2000 bars) more than it
// would at the ~500 this project's watchlist actually has today.
func movingAverages(bars []Bar, window int) []float64 {
	result := make([]float64, len(bars))
	sum := 0.0
	for i, b := range bars {
		sum += b.Close
		if i >= window {
			sum -= bars[i-window].Close
		}
		if i >= window-1 {
			result[i] = sum / float64(window)
		}
	}
	return result
}
