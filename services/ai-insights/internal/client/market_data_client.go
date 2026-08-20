// Package client holds the AI insights service's outbound calls to other
// QuantSim services.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

var _ service.HistoryClient = (*MarketDataClient)(nil)

// requestTimeout bounds one upstream call. GET /insights/portfolio is
// user-facing and synchronous, so a hung dependency must fail the request
// promptly rather than hold it open -- the same value and the same reasoning
// as backtesting's client.
const requestTimeout = 5 * time.Second

// historyLimit requests market-data's MaxHistoryLimit, comfortably above the
// ~501 bars any watchlist symbol has today, so the reconstruction always sees
// a symbol's full stored history rather than a truncated tail.
const historyLimit = 2000

// MarketDataClient reads a symbol's stored history from market-data over HTTP.
//
// Called directly rather than through the gateway, and with no credential:
// /market-data/* has no RequireAuth on it, unlike /trading/*.
type MarketDataClient struct {
	baseURL string
	http    *http.Client
}

func NewMarketDataClient(baseURL string) *MarketDataClient {
	return &MarketDataClient{baseURL: baseURL, http: &http.Client{Timeout: requestTimeout}}
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
// market-data's History endpoint never 404s -- an unfetched symbol is a valid
// 200 with an empty Bars slice there -- so "this symbol has no data" is
// detected from an empty body rather than from a status code market-data will
// never send.
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
		return nil, fmt.Errorf("%w: market-data returned %d for %s",
			service.ErrUpstreamUnavailable, resp.StatusCode, symbol)
	}

	var body historyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: decoding history for %s: %v",
			service.ErrUpstreamUnavailable, symbol, err)
	}

	if len(body.Bars) == 0 {
		return nil, service.SymbolUnavailable(symbol)
	}

	bars := make([]service.Bar, len(body.Bars))
	for i, b := range body.Bars {
		bars[i] = service.Bar{
			Timestamp: b.Timestamp, Open: b.Open, High: b.High,
			Low: b.Low, Close: b.Close, Volume: b.Volume,
		}
	}
	return bars, nil
}
