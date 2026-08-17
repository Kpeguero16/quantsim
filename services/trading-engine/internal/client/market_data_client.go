// Package client holds the trading engine's outbound calls to other QuantSim
// services.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

var _ service.PriceClient = (*MarketDataClient)(nil)

// requestTimeout bounds one price lookup.
//
// This is not a tidiness default. The lookup sits on the synchronous order
// path, so an http.Client with no timeout turns a market-data that accepts
// connections and then hangs into orders that hang with it -- each one holding
// a connection while the caller waits for a fill that is never coming. Three
// seconds is generous for a Redis-backed read on the same host and short
// enough that failing closed happens promptly.
const requestTimeout = 3 * time.Second

// MarketDataClient reads the latest cached price from market-data over HTTP.
//
// It calls the service directly rather than going through the gateway: the
// gateway requires an end-user JWT, which has no meaning for one internal
// service calling another (SPEC.md §2.2). Talking to market-data's API rather
// than reading its Redis keys keeps that cache format a private implementation
// detail, and keeps Redis out of this service's dependencies entirely.
type MarketDataClient struct {
	baseURL string
	http    *http.Client
}

func NewMarketDataClient(baseURL string) *MarketDataClient {
	return &MarketDataClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// priceResponse mirrors market-data's service.Price. Only the price is read;
// symbol and timestamp are echoed back by that endpoint and add nothing here.
type priceResponse struct {
	Price float64 `json:"price"`
}

// LatestPrice returns the current cached price for symbol.
//
// The two error cases are kept apart deliberately. A 404 means market-data
// has no price for this symbol -- never polled, TTL expired, or not a real
// ticker -- which is the caller asking for something that does not exist, and
// becomes a 404 symbol_unavailable. Everything else means this service cannot
// currently tell, and becomes a 502. Collapsing them would report an outage as
// a bad ticker, sending whoever is debugging it to the wrong service.
func (c *MarketDataClient) LatestPrice(ctx context.Context, symbol string) (float64, error) {
	endpoint := c.baseURL + "/market-data/prices/" + url.PathEscape(symbol)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("%w: building request: %v", service.ErrUpstreamUnavailable, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", service.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("%w: market-data has no cached price for %s", service.ErrSymbolUnavailable, symbol)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%w: market-data returned %d", service.ErrUpstreamUnavailable, resp.StatusCode)
	}

	var body priceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("%w: decoding price: %v", service.ErrUpstreamUnavailable, err)
	}

	// A 200 carrying a price of zero, a negative, or NaN is treated as an
	// upstream failure rather than a price. Failing closed on nonsense is the
	// whole point of §2.3: the alternative is an order that fills at 0.00 and
	// looks, in the history, exactly like a legitimate trade.
	if body.Price <= 0 || math.IsNaN(body.Price) || math.IsInf(body.Price, 0) {
		return 0, fmt.Errorf("%w: market-data returned an unusable price (%v) for %s",
			service.ErrUpstreamUnavailable, body.Price, symbol)
	}

	return body.Price, nil
}
