package service

import (
	"errors"
	"fmt"
)

// ErrMissingClose means a symbol the account held has no bar on a date the
// calendar contains.
//
// This is an internal inconsistency, not a user-facing condition: Calendar
// intersects over the symbols it is given, so if every held symbol was passed
// to it, every held symbol has a bar on every date it returned. Reaching this
// means the caller built the calendar from a different symbol set than it
// reconstructed with. It is an explicit error rather than a skipped term
// because valuing a held position at zero would understate equity by exactly
// the amount of that position, and look like a bad day rather than a bug.
var ErrMissingClose = errors.New("no close for a held symbol on a calendar date")

// ErrSymbolUnavailable means market-data has no stored history at all for a
// symbol the account holds, or for a benchmark. Handlers map this to 404.
//
// The request fails rather than degrading: a curve computed without one of the
// account's positions, or a benchmarks array quietly one entry short, is a
// different answer to a different question wearing the right shape
// (SPEC.md §2.1, §2.6).
var ErrSymbolUnavailable = errors.New("no historical data available for symbol")

// ErrUpstreamUnavailable means trading-engine or market-data could not be
// reached or did not answer usefully. Handlers map this to 502.
var ErrUpstreamUnavailable = errors.New("upstream service unavailable")

// ErrUnauthorized means an upstream rejected the caller's forwarded token.
//
// Distinct from ErrUpstreamUnavailable because it is not an outage and must
// not read as one: the caller's own credential was refused, so this maps to
// 401 rather than 502. Reaching it usually means the token expired between
// this service validating it and the onward hop (SPEC.md §6.5).
var ErrUnauthorized = errors.New("upstream rejected the caller's credentials")

// ErrReconciliation means the reconstruction disagrees with the live account
// (SPEC.md §2.12). Every section becomes insufficient_data, because the
// disagreement means the derived history is not trustworthy and there is no
// way to tell which parts of it are still right.
var ErrReconciliation = errors.New("reconstruction disagrees with the live account")

// SymbolUnavailableError is ErrSymbolUnavailable with the offending symbol
// attached, so a handler can NAME it (SPEC.md §2.10) rather than saying "one
// of the portfolio's symbols" and leaving the user to guess which.
//
// A typed error rather than a formatted one because the handler needs the
// symbol as a VALUE. fmt.Errorf("%w: %s", ...) puts it in a string that can
// only be recovered by parsing the message back apart, which turns the
// message's wording into a wire contract -- rewording it would silently break
// the response.
//
// errors.Is(err, ErrSymbolUnavailable) still matches, via Unwrap.
type SymbolUnavailableError struct {
	Symbol string
}

func (e *SymbolUnavailableError) Error() string {
	return fmt.Sprintf("%s: %s", ErrSymbolUnavailable, e.Symbol)
}

func (e *SymbolUnavailableError) Unwrap() error { return ErrSymbolUnavailable }

// SymbolUnavailable builds the error for one symbol.
func SymbolUnavailable(symbol string) error {
	return &SymbolUnavailableError{Symbol: symbol}
}
