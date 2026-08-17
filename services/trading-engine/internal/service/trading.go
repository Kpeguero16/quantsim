package service

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"

	"github.com/google/uuid"
)

// maxSymbolLength bounds what reaches market-data's URL and the symbol column.
// Not a whitelist -- SPEC.md §2.7 deliberately has none, and an unknown but
// plausible ticker still gets its rejection from market-data's 404. This only
// stops a caller writing arbitrary junk into the order history, which a
// rejected order would otherwise persist verbatim.
const maxSymbolLength = 16

// maxQuantity is an engineering bound, not a business rule.
//
// Postgres holds quantity, price and their product as NUMERIC(20,4), which
// overflows above ~10^16. Without a bound here, a quantity of 1e300 is
// rejected for insufficient balance -- correct -- and then fails again while
// persisting that rejection, turning a 400 into a 500 on the way out. A
// billion shares is orders of magnitude past any real order and far short of
// the column's limit.
const maxQuantity = 1e9

type Service struct {
	accounts AccountStore
	trading  TradingStore
	prices   PriceClient
}

func NewService(accounts AccountStore, trading TradingStore, prices PriceClient) *Service {
	return &Service{accounts: accounts, trading: trading, prices: prices}
}

// PlaceOrder executes a market order synchronously and returns the fill.
//
// The order of operations is load-bearing (SPEC.md §2.6): validate, resolve
// the account, fetch the price OUTSIDE any transaction because it is a network
// call, and only then hand everything to the store, which opens one
// transaction and holds the account row locked for its whole duration.
//
// It fails closed. If market-data cannot be reached there is no price, and
// without a price there is no fill -- the order is recorded as rejected rather
// than filled at the last number anyone happened to have. This is the opposite
// posture from the read path (see Positions), and reversing the two is the
// single easiest way to violate this spec's intent: a read that degrades to
// "no live P/L" is a worse answer, while a write at a guessed price is a wrong
// one that money moves against.
func (s *Service) PlaceOrder(ctx context.Context, userID uuid.UUID, req PlaceOrderRequest) (PlaceOrderResult, error) {
	req, err := validateOrder(req)
	if err != nil {
		// Nothing is persisted: this never became an order.
		return PlaceOrderResult{}, err
	}

	account, err := s.accounts.AccountForUser(ctx, userID)
	if err != nil {
		return PlaceOrderResult{}, err
	}

	price, err := s.prices.LatestPrice(ctx, req.Symbol)
	if err != nil {
		s.recordRejection(ctx, account.ID, req, rejectionReason(err))
		return PlaceOrderResult{}, err
	}

	return s.trading.ExecuteOrder(ctx, ExecuteOrderParams{
		AccountID: account.ID,
		Symbol:    req.Symbol,
		Side:      req.Side,
		Quantity:  req.Quantity,
		Price:     price,
	})
}

// recordRejection persists an order that failed before any transaction opened.
//
// Its own failure is logged and swallowed on purpose. The caller is already
// being told the order did not fill, which is the part that matters; replacing
// that specific answer with "could not write the audit row" would report the
// wrong problem for an outcome that is the same either way.
func (s *Service) recordRejection(ctx context.Context, accountID uuid.UUID, req PlaceOrderRequest, reason string) {
	if _, err := s.trading.RecordRejectedOrder(ctx, accountID, req.Symbol, req.Side, req.Quantity, reason); err != nil {
		log.Printf("trading-engine: failed to persist rejected order (account=%s symbol=%s reason=%s): %v",
			accountID, req.Symbol, reason, err)
	}
}

// rejectionReason is the string stored in orders.rejection_reason. It reuses
// the same vocabulary the API returns as an error code, so the history reads
// the same way the response that produced it did.
func rejectionReason(err error) string {
	switch {
	case errors.Is(err, ErrSymbolUnavailable):
		return "symbol_unavailable"
	case errors.Is(err, ErrUpstreamUnavailable):
		return "upstream_unavailable"
	case errors.Is(err, ErrInsufficientBalance):
		return "insufficient_balance"
	case errors.Is(err, ErrInsufficientPosition):
		return "insufficient_position"
	default:
		return "internal_error"
	}
}

// validateOrder returns the request with its symbol normalised, or
// ErrInvalidOrder.
//
// Normalising here rather than trusting the caller is the same rule auth
// applies to email addresses. market-data already uppercases before its cache
// lookup, so "aapl" would price correctly -- but this service stores the
// symbol in its own positions table under a UNIQUE (account_id, symbol)
// constraint, so accepting both forms would silently split one holding into
// two rows that never merge.
func validateOrder(req PlaceOrderRequest) (PlaceOrderRequest, error) {
	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	if req.Symbol == "" || len(req.Symbol) > maxSymbolLength {
		return req, ErrInvalidOrder
	}
	if req.Side != SideBuy && req.Side != SideSell {
		return req, ErrInvalidOrder
	}
	// NaN fails every comparison including this one, so the check is written
	// to reject rather than to accept: `!(q > 0)` catches NaN where `q <= 0`
	// would let it through.
	if !(req.Quantity > 0) || math.IsInf(req.Quantity, 0) || req.Quantity > maxQuantity {
		return req, ErrInvalidOrder
	}
	return req, nil
}
