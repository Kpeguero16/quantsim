package service

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"
	"sync"
	"time"

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

// DefaultTradeLimit and MaxTradeLimit bound GET /trading/trades, mirroring
// market-data's DefaultHistoryLimit/MaxHistoryLimit convention rather than
// inventing a second one.
//
// There is no principled number here. 10000 is comfortably above what any
// paper-trading account plausibly accumulates, so the cap exists to stop one
// request reading an unbounded table, not to paginate -- real pagination is
// deferred until an account can actually exceed it (Step 20 SPEC.md §3).
const (
	DefaultTradeLimit = 1000
	MaxTradeLimit     = 10000
)

// Trades returns the caller's own executions, oldest first.
//
// Scoped through AccountForUser like Orders, which is what makes reading
// someone else's trade log impossible to express rather than merely forbidden.
func (s *Service) Trades(ctx context.Context, userID uuid.UUID, limit int) ([]Trade, error) {
	account, err := s.accounts.AccountForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.trading.ListTrades(ctx, account.ID, normalizeTradeLimit(limit))
}

// normalizeTradeLimit clamps a requested limit into range.
//
// Anything non-positive means "the caller did not usefully ask" -- absent,
// zero, negative, or unparseable at the HTTP boundary -- and gets the default.
// Anything above the cap is clamped rather than rejected: a caller asking for
// more history than exists wants all of it, and a 400 would be pedantry.
//
// This lives in the service, not the handler, so the rule is one function with
// one set of tests rather than something only reachable through HTTP.
func normalizeTradeLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultTradeLimit
	case limit > MaxTradeLimit:
		return MaxTradeLimit
	default:
		return limit
	}
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

// PricingBudget bounds one whole pricing pass -- every holding together, not
// each lookup within it.
//
// It is deliberately equal to the per-lookup timeout in internal/client rather
// than a multiple of it. A single degraded symbol may spend the entire budget;
// what it may not do is add its timeout to another symbol's.
//
// It must stay comfortably under ai-insights' 5s upstream timeout, which is
// what turned a slow answer here into a 502 there.
const PricingBudget = 3 * time.Second

// price values each holding independently and concurrently, under one budget
// for the whole pass.
//
// Independently is the point: one symbol market-data cannot price must not
// blank the others. A single failed lookup that aborted the loop, or that set
// a shared "prices unavailable" flag, would turn one missing quote into a
// portfolio that reports every position as unvalued.
//
// Concurrently, and under one budget, is what keeps the endpoint's worst case
// flat in the number of holdings. This loop used to be sequential, and its
// per-symbol timeouts composed additively: N holdings that market-data could
// not price cost N x the client's 3s timeout, and three of them made
// GET /trading/portfolio take 8.7s against 5.8ms healthy. That tripped
// ai-insights' 5s upstream timeout and took GET /insights/portfolio down with
// a 502 -- for a report whose every figure comes from Postgres, during a Redis
// outage. The per-call timeout was correct throughout. Nothing bounded the
// loop, and a bound on each item is not a bound on the request.
//
// The budget is a context deadline rather than a cap on outstanding
// goroutines: the symbol count is bounded by the distinct symbols an account
// holds, which is bounded by the tradable watchlist, so there is no fan-out
// here worth throttling.
func (s *Service) price(ctx context.Context, holdings []Holding) []Position {
	// Pre-sized and written by index. Appending from the goroutines would put
	// results in completion order, so the same portfolio would come back in a
	// different order run to run -- and the positions endpoint's order is the
	// holdings' order.
	positions := make([]Position, len(holdings))
	for i, h := range holdings {
		positions[i] = Position{Symbol: h.Symbol, Quantity: h.Quantity, AvgCost: h.AvgCost}
	}
	if len(holdings) == 0 {
		return positions
	}

	ctx, cancel := context.WithTimeout(ctx, PricingBudget)
	defer cancel()

	var wg sync.WaitGroup
	for i := range holdings {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := holdings[i]

			latest, err := s.prices.LatestPrice(ctx, h.Symbol)
			if err != nil {
				// Logged rather than silent: a portfolio quietly reporting no
				// P/L is exactly the kind of degradation nobody notices until
				// someone asks why their numbers look wrong.
				log.Printf("trading-engine: pricing %s failed, returning it unpriced: %v", h.Symbol, err)
				return
			}

			// Each goroutine writes only its own index, and takes the address
			// of its own local, so every position points at its own price.
			price := latest
			positions[i].LatestPrice = &price
			positions[i].UnrealizedPL = (latest - h.AvgCost) * h.Quantity
		}(i)
	}
	wg.Wait()
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
