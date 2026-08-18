package service

import "errors"

// ErrInvalidRequest covers every validation failure in §2.8 that is
// discoverable before any history is fetched: window bounds, an inverted or
// unbounded date range, a non-positive starting capital. Handlers map this
// to 400 invalid_request.
var ErrInvalidRequest = errors.New("invalid backtest request")

// ErrSymbolUnavailable means market-data has no history at all for the
// requested symbol -- a 400, not a 502: this is the caller asking for
// something that does not exist, discoverable before any simulation starts
// (SPEC.md §2.8), unlike trading-engine's identically-named case which is a
// live price call that can fail transiently.
var ErrSymbolUnavailable = errors.New("no historical data available for symbol")

// ErrDateRangeUnavailable means the symbol has history, but not covering the
// requested [start_date, end_date] -- a 400: running on fewer bars than
// asked for would silently answer a different question than the one asked.
var ErrDateRangeUnavailable = errors.New("no historical data in the requested date range")

// ErrUpstreamUnavailable means market-data could not be reached or did not
// answer usefully. Handlers map this to 502 upstream_unavailable.
var ErrUpstreamUnavailable = errors.New("market data upstream unavailable")

// ErrNotFound means the backtest id does not exist, or exists but belongs to
// a different user -- deliberately the same outcome for both (SPEC.md §2.7:
// 404, not 403, so a non-owner learns nothing about whether the id exists).
var ErrNotFound = errors.New("backtest not found")
