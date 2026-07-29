package alpaca

import "time"

// Bar is a single OHLCV bar as returned by Alpaca's historical bars endpoint.
type Bar struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
}

// barDTO mirrors Alpaca's wire format for a single bar.
type barDTO struct {
	Timestamp string  `json:"t"`
	Open      float64 `json:"o"`
	High      float64 `json:"h"`
	Low       float64 `json:"l"`
	Close     float64 `json:"c"`
	Volume    int64   `json:"v"`
}

// barsResponse mirrors GET /v2/stocks/{symbol}/bars's response shape.
type barsResponse struct {
	Symbol        string   `json:"symbol"`
	Bars          []barDTO `json:"bars"`
	NextPageToken *string  `json:"next_page_token"`
}

// Trade is the fields of Alpaca's latestTrade this service actually uses
// (SPEC.md §2.4 -- latestTrade.p only, no quote/bar fallback).
type Trade struct {
	Price     float64
	Timestamp time.Time
}

// Snapshot is a single symbol's entry from GET /v2/stocks/snapshots.
// LatestTrade is nil when Alpaca hasn't recorded a trade for the symbol yet
// (e.g. pre-market on a thin feed) -- SPEC.md §2.4 treats that as "skip this
// symbol for this tick," not a zero-value trade at price 0.
type Snapshot struct {
	LatestTrade *Trade
}

// snapshotTradeDTO mirrors Alpaca's latestTrade wire format.
type snapshotTradeDTO struct {
	Price     float64 `json:"p"`
	Timestamp string  `json:"t"`
}

// snapshotDTO mirrors one symbol's entry in the snapshots response. Only
// latestTrade is decoded -- latestQuote/minuteBar/dailyBar/prevDailyBar are
// present in Alpaca's payload but unused by this service (SPEC.md §2.4).
type snapshotDTO struct {
	LatestTrade *snapshotTradeDTO `json:"latestTrade"`
}

// snapshotsResponse mirrors GET /v2/stocks/snapshots's response shape: a map
// keyed by symbol, not a list.
type snapshotsResponse map[string]snapshotDTO
