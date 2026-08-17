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

// quantityScale and minQuantity are maxQuantity's mirror at the bottom of the
// range, and they exist because Step 14's adversarial review found the gap.
//
// NUMERIC(20,4) stores four decimal places. A quantity of 0.00001 passes a
// `> 0` check, is charged for in full against the balance, and then rounds to
// 0.0000 shares on its way into positions and trades. The cash leg lands and
// the share leg vanishes: a buy destroys money, and a sell -- the direction an
// attacker picks -- mints it, at roughly a third of a cent per request with no
// bound on how often it can be repeated. Thirty dust sells against a 300-share
// AAPL position moved the balance +0.0930 and left the position untouched.
//
// So the ledger's own tick is the floor: anything the database cannot store is
// refused rather than half-executed, and anything finer than the tick is
// rounded to it up front, so the number that moves the balance is the same
// number that reaches positions and trades.
const (
	quantityScale = 1e4
	minQuantity   = 1 / quantityScale
)

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

// Orders returns the caller's own order history, newest first, with rejected
// orders included -- an audit trail missing its failures would be a worse
// record than none, because it would look complete (SPEC.md §2.5).
//
// Scoping runs through AccountForUser rather than taking an account id from
// the request, which is what makes reading someone else's history impossible
// to express rather than merely forbidden.
func (s *Service) Orders(ctx context.Context, userID uuid.UUID) ([]Order, error) {
	account, err := s.accounts.AccountForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.trading.ListOrders(ctx, account.ID)
}

// Positions returns the caller's open holdings priced at the current market.
//
// This is the fail-OPEN path, and it is deliberately the opposite posture from
// PlaceOrder (SPEC.md §2.9). If market-data cannot price a symbol, the position
// is still returned with latest_price null and unrealized_pl 0 -- the caller
// learns what they hold and at what cost, and only loses the live valuation.
//
// The two postures are not inconsistent: a write at a guessed price moves money
// against a number nobody verified, while a read that degrades to "no live P/L"
// is a smaller answer, not a wrong one. Reversing them is the single easiest
// way to violate this spec's intent.
func (s *Service) Positions(ctx context.Context, userID uuid.UUID) ([]Position, error) {
	account, err := s.accounts.AccountForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	return s.positionsFor(ctx, account.ID)
}

// Portfolio is the whole account in one response: cash, positions, and what
// the two are worth together (SPEC.md §2.9).
//
// It goes through the same positionsFor path GET /trading/positions does,
// rather than a second query of its own. A divergent copy is how the two
// endpoints would eventually disagree about the same account -- and the one
// that disagreed would still look right on its own.
func (s *Service) Portfolio(ctx context.Context, userID uuid.UUID) (PortfolioResponse, error) {
	account, err := s.accounts.AccountForUser(ctx, userID)
	if err != nil {
		return PortfolioResponse{}, err
	}

	positions, err := s.positionsFor(ctx, account.ID)
	if err != nil {
		return PortfolioResponse{}, err
	}

	portfolio := PortfolioResponse{Balance: account.Balance, Positions: positions, TotalEquity: account.Balance}
	for _, p := range positions {
		// An unpriceable position is valued at cost -- never dropped from the
		// total, never counted as zero. Dropping it would silently shrink the
		// portfolio, and zeroing it would report the account as having lost
		// everything it holds, both because one HTTP call failed.
		value := p.AvgCost * p.Quantity
		if p.LatestPrice != nil {
			value = *p.LatestPrice * p.Quantity
		}
		portfolio.TotalEquity += value
		portfolio.TotalUnrealizedPL += p.UnrealizedPL
	}
	return portfolio, nil
}

// positionsFor is the one path from an account to its priced positions, shared
// by both endpoints that need it.
func (s *Service) positionsFor(ctx context.Context, accountID uuid.UUID) ([]Position, error) {
	holdings, err := s.trading.ListHoldings(ctx, accountID)
	if err != nil {
		// The store failing is not the same as market-data failing. There is
		// no degraded answer here: without the holdings there is nothing to
		// return, so this one does propagate.
		return nil, err
	}
	return s.price(ctx, holdings), nil
}

// price values each holding independently.
//
// Independently is the point: one symbol market-data cannot price must not
// blank the others. A single failed lookup that aborted the loop, or that set
// a shared "prices unavailable" flag, would turn one missing quote into a
// portfolio that reports every position as unvalued.
func (s *Service) price(ctx context.Context, holdings []Holding) []Position {
	positions := make([]Position, 0, len(holdings))
	for _, h := range holdings {
		p := Position{Symbol: h.Symbol, Quantity: h.Quantity, AvgCost: h.AvgCost}

		latest, err := s.prices.LatestPrice(ctx, h.Symbol)
		if err != nil {
			// Logged rather than silent: a portfolio quietly reporting no P/L
			// is exactly the kind of degradation nobody notices until someone
			// asks why their numbers look wrong.
			log.Printf("trading-engine: pricing %s failed, returning it unpriced: %v", h.Symbol, err)
			positions = append(positions, p)
			continue
		}

		// Taking the address of the loop-local copy, so every position points
		// at its own price.
		price := latest
		p.LatestPrice = &price
		p.UnrealizedPL = (latest - h.AvgCost) * h.Quantity
		positions = append(positions, p)
	}
	return positions
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
	// to reject rather than to accept: `!(q >= minQuantity)` catches NaN where
	// `q < minQuantity` would let it through.
	if !(req.Quantity >= minQuantity) || math.IsInf(req.Quantity, 0) || req.Quantity > maxQuantity {
		return req, ErrInvalidOrder
	}
	// Snap to the tick the ledger stores, so the quantity that is charged for
	// is the quantity that is recorded. Rounding after the bounds check, not
	// before: a rejected order should be rejected for what was asked, not for
	// what it would have become.
	req.Quantity = math.Round(req.Quantity*quantityScale) / quantityScale
	return req, nil
}
