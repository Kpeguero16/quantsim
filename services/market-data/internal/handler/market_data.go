package handler

import "net/http"

var watchlist = []string{"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA", "SPY", "QQQ"}

type SymbolsResponse struct {
	Symbols []string `json:"symbols"`
}

func SymbolsHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, SymbolsResponse{Symbols: watchlist})
}
