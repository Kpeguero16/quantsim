package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

var _ service.TradingClient = (*TradingClient)(nil)

// tradeLimit requests trading-engine's MaxTradeLimit. The reconstruction
// replays the log from the first trade forward, so it needs the whole thing;
// a truncated response would still be a complete prefix and therefore
// correct-but-shorter, but there is no reason to accept a shorter one.
const tradeLimit = 10000

// TradingClient reads the caller's own trading data from trading-engine.
//
// Called directly rather than through the gateway, but NOT anonymously:
// /trading/* revalidates the JWT at trading-engine itself, so the caller's
// Authorization header is forwarded on every call (SPEC.md §6.5). That also
// keeps account scoping where it belongs -- trading-engine resolves the
// account from the token, so this client never handles an account id.
type TradingClient struct {
	baseURL string
	http    *http.Client
}

func NewTradingClient(baseURL string) *TradingClient {
	return &TradingClient{baseURL: baseURL, http: &http.Client{Timeout: requestTimeout}}
}

type tradesResponse struct {
	Trades []service.Trade `json:"trades"`
}

func (c *TradingClient) Trades(ctx context.Context, authorization string) ([]service.Trade, error) {
	var body tradesResponse
	endpoint := fmt.Sprintf("%s/trading/trades?limit=%d", c.baseURL, tradeLimit)
	if err := c.get(ctx, endpoint, authorization, &body); err != nil {
		return nil, err
	}
	// Non-nil so downstream reads an empty history as "never traded" rather
	// than having to distinguish nil from empty.
	if body.Trades == nil {
		return []service.Trade{}, nil
	}
	return body.Trades, nil
}

func (c *TradingClient) Portfolio(ctx context.Context, authorization string) (service.LivePortfolio, error) {
	var body service.LivePortfolio
	if err := c.get(ctx, c.baseURL+"/trading/portfolio", authorization, &body); err != nil {
		return service.LivePortfolio{}, err
	}
	if body.Positions == nil {
		body.Positions = []service.LivePosition{}
	}
	return body, nil
}

// get performs one authenticated GET and decodes into out.
//
// 401 and 403 become ErrUnauthorized rather than ErrUpstreamUnavailable: the
// caller's credential was refused, which is not an outage and must not be
// reported as one. Everything else non-200 is an upstream failure.
func (c *TradingClient) get(ctx context.Context, endpoint, authorization string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: building request: %v", service.ErrUpstreamUnavailable, err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", service.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: trading-engine returned %d", service.ErrUnauthorized, resp.StatusCode)
	default:
		return fmt.Errorf("%w: trading-engine returned %d", service.ErrUpstreamUnavailable, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: decoding response: %v", service.ErrUpstreamUnavailable, err)
	}
	return nil
}
