package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Timeframe is the only granularity this service ingests/queries in Phase 1.
const Timeframe = "1Day"

// DefaultWatchlist is ingested/returned when a caller doesn't specify
// symbols explicitly.
var DefaultWatchlist = []string{"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA", "SPY", "QQQ"}

// defaultLookbackYears is how far back Ingest fetches when Start is omitted
// (SPEC.md §2.2).
const defaultLookbackYears = 2

// DefaultHistoryLimit and MaxHistoryLimit bound History's `limit` (SPEC.md §2.6).
const (
	DefaultHistoryLimit = 500
	MaxHistoryLimit     = 2000
)

var symbolPattern = regexp.MustCompile(`^[A-Z.]{1,10}$`)

type Service struct {
	alpaca AlpacaClient
	store  HistoricalPriceStore
	cache  PriceCache
}

func NewService(alpacaClient AlpacaClient, store HistoricalPriceStore, cache PriceCache) *Service {
	return &Service{alpaca: alpacaClient, store: store, cache: cache}
}

// Ingest fetches and upserts daily bars for each requested symbol
// independently -- one symbol's failure (bad ticker, upstream error) doesn't
// abort the batch (SPEC.md §2.5); its outcome is reported in that symbol's
// IngestResult instead. If every symbol fails with an upstream error, that's
// escalated to a request-level ErrUpstreamUnavailable rather than returning
// an all-failed result set as if it were a success.
func (s *Service) Ingest(ctx context.Context, req IngestRequest) ([]IngestResult, error) {
	symbols := req.Symbols
	if len(symbols) == 0 {
		symbols = DefaultWatchlist
	}

	start, end, err := resolveDateRange(req)
	if err != nil {
		return nil, err
	}

	results := make([]IngestResult, 0, len(symbols))
	upstreamFailures := 0

	for _, raw := range symbols {
		symbol := strings.ToUpper(strings.TrimSpace(raw))

		if !symbolPattern.MatchString(symbol) {
			results = append(results, IngestResult{Symbol: symbol, Error: ErrInvalidSymbol.Error()})
			continue
		}

		bars, err := s.alpaca.GetBars(ctx, symbol, Timeframe, start, end)
		if err != nil {
			upstreamFailures++
			results = append(results, IngestResult{Symbol: symbol, Error: ErrUpstreamUnavailable.Error()})
			continue
		}

		storeBars := make([]Bar, 0, len(bars))
		for _, b := range bars {
			storeBars = append(storeBars, Bar{
				Symbol:    symbol,
				Timeframe: Timeframe,
				Timestamp: b.Timestamp,
				Open:      b.Open,
				High:      b.High,
				Low:       b.Low,
				Close:     b.Close,
				Volume:    b.Volume,
			})
		}

		if err := s.store.UpsertBars(ctx, storeBars); err != nil {
			results = append(results, IngestResult{Symbol: symbol, Error: "failed to store bars"})
			continue
		}

		results = append(results, IngestResult{Symbol: symbol, BarsIngested: len(storeBars)})
	}

	if len(symbols) > 0 && upstreamFailures == len(symbols) {
		return results, ErrUpstreamUnavailable
	}

	return results, nil
}

// resolveDateRange applies SPEC.md §2.2's defaults: end = start of today
// (so "through yesterday", since today's daily bar may not exist yet),
// start = end - 2 years. Either can be overridden by the request.
func resolveDateRange(req IngestRequest) (start, end time.Time, err error) {
	now := time.Now().UTC()
	end = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start = end.AddDate(-defaultLookbackYears, 0, 0)

	if req.Start != "" {
		t, perr := parseDate(req.Start)
		if perr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: start %q", ErrInvalidDateRange, req.Start)
		}
		start = t
	}
	if req.End != "" {
		t, perr := parseDate(req.End)
		if perr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: end %q", ErrInvalidDateRange, req.End)
		}
		end = t
	}
	return start, end, nil
}

func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

// History returns stored bars for symbol, most recent `limit` bars in
// ascending timestamp order. limit <= 0 uses DefaultHistoryLimit; anything
// above MaxHistoryLimit is clamped. A symbol with no ingested data returns an
// empty (not nil) Bars slice and no error (SPEC.md §2.6) -- it isn't the
// caller's fault that ingestion hasn't run for that symbol yet.
func (s *Service) History(ctx context.Context, symbol string, limit int) (*HistoryResponse, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))

	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		limit = MaxHistoryLimit
	}

	bars, err := s.store.GetHistory(ctx, symbol, Timeframe, limit)
	if err != nil {
		return nil, err
	}
	if bars == nil {
		bars = []Bar{}
	}

	return &HistoryResponse{Symbol: symbol, Timeframe: Timeframe, Bars: bars}, nil
}

// LatestPrice returns the cached latest price for symbol. Any cache error --
// a genuine miss, an expired TTL, or a Redis-layer problem -- surfaces as
// ErrPriceNotCached; SPEC.md §2.8 treats all of those as "not available right
// now" rather than distinguishing a cache miss from an infrastructure error.
func (s *Service) LatestPrice(ctx context.Context, symbol string) (Price, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))

	price, err := s.cache.GetPrice(ctx, symbol)
	if err != nil {
		return Price{}, fmt.Errorf("%w: %v", ErrPriceNotCached, err)
	}
	return price, nil
}
