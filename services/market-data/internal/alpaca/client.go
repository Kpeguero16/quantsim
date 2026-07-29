package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://data.alpaca.markets/v2"
	requestTimeout = 15 * time.Second
	// feed=iex is the only feed entitled under Alpaca's free market data plan.
	feed = "iex"
)

// StatusError is returned when Alpaca responds with a non-2xx status.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("alpaca: unexpected status %d: %s", e.StatusCode, e.Body)
}

// Client is a plain net/http wrapper around Alpaca's historical market data API.
type Client struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		baseURL:    defaultBaseURL,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// GetBars fetches all bars for symbol/timeframe in [start, end], following
// Alpaca's next_page_token pagination until fully drained. Bars are returned
// in ascending timestamp order.
func (c *Client) GetBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]Bar, error) {
	var bars []Bar
	pageToken := ""

	for {
		page, nextToken, err := c.fetchBarsPage(ctx, symbol, timeframe, start, end, pageToken)
		if err != nil {
			return nil, err
		}
		bars = append(bars, page...)

		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}

	return bars, nil
}

func (c *Client) fetchBarsPage(ctx context.Context, symbol, timeframe string, start, end time.Time, pageToken string) ([]Bar, string, error) {
	reqURL := fmt.Sprintf("%s/stocks/%s/bars", c.baseURL, url.PathEscape(symbol))

	q := url.Values{}
	q.Set("timeframe", timeframe)
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("end", end.UTC().Format(time.RFC3339))
	q.Set("feed", feed)
	q.Set("sort", "asc")
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("alpaca: build request: %w", err)
	}
	req.Header.Set("APCA-API-KEY-ID", c.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", c.apiSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("alpaca: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", &StatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var parsed barsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", fmt.Errorf("alpaca: decode response: %w", err)
	}

	bars := make([]Bar, 0, len(parsed.Bars))
	for _, dto := range parsed.Bars {
		ts, err := time.Parse(time.RFC3339Nano, dto.Timestamp)
		if err != nil {
			return nil, "", fmt.Errorf("alpaca: parse bar timestamp %q: %w", dto.Timestamp, err)
		}
		bars = append(bars, Bar{
			Timestamp: ts,
			Open:      dto.Open,
			High:      dto.High,
			Low:       dto.Low,
			Close:     dto.Close,
			Volume:    dto.Volume,
		})
	}

	next := ""
	if parsed.NextPageToken != nil {
		next = *parsed.NextPageToken
	}
	return bars, next, nil
}

// GetSnapshots fetches the latest snapshot for every symbol in one batched
// request (SPEC.md §2.2 -- one call per tick, not one per symbol).
func (c *Client) GetSnapshots(ctx context.Context, symbols []string) (map[string]Snapshot, error) {
	reqURL := c.baseURL + "/stocks/snapshots"

	q := url.Values{}
	q.Set("symbols", strings.Join(symbols, ","))
	q.Set("feed", feed)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("alpaca: build request: %w", err)
	}
	req.Header.Set("APCA-API-KEY-ID", c.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", c.apiSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alpaca: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var parsed snapshotsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("alpaca: decode response: %w", err)
	}

	snapshots := make(map[string]Snapshot, len(parsed))
	for symbol, dto := range parsed {
		snap := Snapshot{}
		if dto.LatestTrade != nil {
			ts, err := time.Parse(time.RFC3339Nano, dto.LatestTrade.Timestamp)
			if err != nil {
				return nil, fmt.Errorf("alpaca: parse latestTrade timestamp %q: %w", dto.LatestTrade.Timestamp, err)
			}
			snap.LatestTrade = &Trade{Price: dto.LatestTrade.Price, Timestamp: ts}
		}
		snapshots[symbol] = snap
	}

	return snapshots, nil
}
