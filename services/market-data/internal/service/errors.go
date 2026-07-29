package service

import "errors"

// ErrInvalidSymbol is returned per-symbol by Ingest when a symbol fails basic
// format validation. Never a request-level error -- see IngestResult.
var ErrInvalidSymbol = errors.New("invalid symbol")

// ErrUpstreamUnavailable is Ingest's per-symbol outcome when the Alpaca
// client fails (non-2xx, network error). It escalates to a request-level
// error only when every requested symbol fails this way -- see Ingest.
// Handlers map the request-level case to 502.
var ErrUpstreamUnavailable = errors.New("market data upstream unavailable")

// ErrInvalidDateRange is a request-level error: a malformed start/end value
// affects the whole request the same way, unlike a per-symbol problem.
// Handlers map this to 400.
var ErrInvalidDateRange = errors.New("invalid start/end date")

// ErrPriceNotCached is LatestPrice's error when a symbol has no cached price
// -- never polled yet, TTL expired, or not in the watchlist (SPEC.md §2.8).
// Handlers map this to 404.
var ErrPriceNotCached = errors.New("no cached price for symbol")
