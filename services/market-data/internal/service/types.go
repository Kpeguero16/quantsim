package service

import "time"

// Bar is a single OHLCV bar in this service's own representation, distinct
// from Alpaca's wire format (internal/alpaca.Bar) -- it carries Symbol and
// Timeframe since it's what the store persists. Symbol/Timeframe are omitted
// from JSON since HistoryResponse already carries them once at the top level.
type Bar struct {
	Symbol    string    `json:"-"`
	Timeframe string    `json:"-"`
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    int64     `json:"volume"`
}

// IngestRequest is the input to Ingest. Symbols defaults to DefaultWatchlist
// when empty; Start/End default per SPEC.md §2.2 when empty. Start/End are
// raw strings (not time.Time) so callers can pass either an RFC-3339
// timestamp or a plain YYYY-MM-DD date.
type IngestRequest struct {
	Symbols []string `json:"symbols"`
	Start   string   `json:"start"`
	End     string   `json:"end"`
}

// IngestResult reports the outcome for a single symbol -- Ingest processes
// symbols independently (SPEC.md §2.5), so one bad symbol doesn't abort the
// batch and its failure is reported here rather than as a request-level error.
type IngestResult struct {
	Symbol       string `json:"symbol"`
	BarsIngested int    `json:"bars_ingested"`
	Error        string `json:"error,omitempty"`
}

// HistoryResponse is History's return shape. Bars is always a non-nil
// (possibly empty) slice -- an unfetched symbol is a valid, empty result,
// not an error (SPEC.md §2.6).
type HistoryResponse struct {
	Symbol    string `json:"symbol"`
	Timeframe string `json:"timeframe"`
	Bars      []Bar  `json:"bars"`
}
