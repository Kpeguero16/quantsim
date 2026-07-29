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
