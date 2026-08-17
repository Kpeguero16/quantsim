package service

import "errors"

// ErrInvalidOrder is a malformed order the engine can reject without asking
// anything else: an unknown side, a non-positive quantity, an empty symbol.
// Nothing is persisted for one of these -- it never became an order.
// Handlers map this to 400 invalid_request.
var ErrInvalidOrder = errors.New("invalid order")

// ErrInsufficientBalance means the account could not afford the buy. It can
// only be produced inside the transaction holding the account row lock: a
// balance read outside that lock is a number that may already be wrong by the
// time it is compared (SPEC.md §2.6).
//
// The rejected order IS persisted before this is returned. Handlers map this
// to 400 insufficient_balance.
var ErrInsufficientBalance = errors.New("insufficient balance")

// ErrInsufficientPosition means the sell asked for more than the position
// holds, or there is no position at all. Long-only: this is never a short
// (SPEC.md §2.8). Like ErrInsufficientBalance it comes from inside the lock,
// and the rejected order is persisted. Handlers map this to 400
// insufficient_position.
var ErrInsufficientPosition = errors.New("insufficient position")

// ErrSymbolUnavailable means market-data has no cached price for the symbol
// -- never polled, TTL expired, or not a real symbol. There is deliberately
// no separate whitelist to check against; market-data's own 404 is the
// rejection (SPEC.md §2.7). Handlers map this to 404 symbol_unavailable.
var ErrSymbolUnavailable = errors.New("no price available for symbol")

// ErrUpstreamUnavailable means market-data could not be reached or did not
// answer usefully.
//
// This is the fail-closed case, and it is the whole reason the write path is
// shaped the way it is: with no price there is no fill, because filling at a
// guessed or stale price is an integrity violation in a system whose premise
// is realistic financial data (SPEC.md §2.3). The rejected order is persisted
// so the attempt is visible. Handlers map this to 502 upstream_unavailable.
//
// The read path does NOT use this. A position that cannot be priced is
// returned with a null price rather than failing the request -- see
// Service.Positions and SPEC.md §2.9.
var ErrUpstreamUnavailable = errors.New("market data upstream unavailable")

// ErrAccountNotFound means an authenticated user has no account row. Every
// user gets one at registration, so this is a broken invariant rather than
// anything the caller did, and handlers map it to 500 internal_error. A 4xx
// here would put a code in the contract that should never be reachable.
var ErrAccountNotFound = errors.New("no account for user")
