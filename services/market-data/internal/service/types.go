package service

import "time"

// Bar is a single OHLCV bar in this service's own representation, distinct
// from Alpaca's wire format (internal/alpaca.Bar) -- it carries Symbol and
// Timeframe since it's what the store persists.
type Bar struct {
	Symbol    string
	Timeframe string
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
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
