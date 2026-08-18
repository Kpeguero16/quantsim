// Package client holds the backtesting service's outbound calls to other
// QuantSim services.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
)

var _ service.HistoryClient = (*MarketDataClient)(nil)

// requestTimeout bounds one history fetch. A backtest is user-facing and
// synchronous (SPEC.md §1 Non-goals), so a hung market-data call must fail
// the request promptly rather than hold it open indefinitely.
const requestTimeout = 5 * time.Second

// historyLimit requests market-data's MaxHistoryLimit -- comfortably above
// the ~501 bars any watchlist symbol has today -- so the date-range slicing
// in service.sliceRange always sees the symbol's full stored history rather
// than a truncated tail (SPEC.md §2.2).
const historyLimit = 2000

// MarketDataClient reads a symbol's stored history from market-data over
// HTTP, the same service-to-service pattern trading-engine's own
// MarketDataClient established in Step 14: called directly, bypassing the
// gateway, since a gateway JWT has no meaning between two internal services.
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

type barResponse struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    int64     `json:"volume"`
}

type historyResponse struct {
	Bars []barResponse `json:"bars"`
}

// History fetches symbol's stored daily bars.
//
// market-data's own History endpoint never 404s -- an unfetched symbol is a
// valid 200 with an empty Bars slice there (its own SPEC.md §2.6) -- so the
// "this symbol does not exist" case is detected here, from an empty body,
// rather than from a status code that market-data will never send.
func (c *MarketDataClient) History(ctx context.Context, symbol string) ([]service.Bar, error) {
	endpoint := fmt.Sprintf("%s/market-data/history/%s?limit=%d",
		c.baseURL, url.PathEscape(symbol), historyLimit)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: building request: %v", service.ErrUpstreamUnavailable, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", service.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: market-data returned %d", service.ErrUpstreamUnavailable, resp.StatusCode)
	}

	var body historyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: decoding history: %v", service.ErrUpstreamUnavailable, err)
	}

	if len(body.Bars) == 0 {
		return nil, fmt.Errorf("%w: %s", service.ErrSymbolUnavailable, symbol)
	}

	bars := make([]service.Bar, len(body.Bars))
	for i, b := range body.Bars {
		bars[i] = service.Bar{
			Timestamp: b.Timestamp,
			Open:      b.Open,
			High:      b.High,
			Low:       b.Low,
			Close:     b.Close,
			Volume:    b.Volume,
		}
	}
	return bars, nil
}
