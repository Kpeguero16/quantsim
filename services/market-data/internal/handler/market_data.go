package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kpeguero/quantsim/services/market-data/internal/service"
)

type SymbolsResponse struct {
	Symbols []string `json:"symbols"`
}

func SymbolsHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, SymbolsResponse{Symbols: service.DefaultWatchlist})
}

type MarketDataHandler struct {
	service *service.Service
}

func NewMarketDataHandler(svc *service.Service) *MarketDataHandler {
	return &MarketDataHandler{service: svc}
}

type IngestResponse struct {
	Results []service.IngestResult `json:"results"`
}

func (h *MarketDataHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	var req service.IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}

	results, err := h.service.Ingest(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidDateRange):
			WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		case errors.Is(err, service.ErrUpstreamUnavailable):
			WriteError(w, http.StatusBadGateway, "upstream_unavailable", "market data upstream unavailable")
		default:
			WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
		}
		return
	}

	WriteJSON(w, http.StatusOK, IngestResponse{Results: results})
}

func (h *MarketDataHandler) History(w http.ResponseWriter, r *http.Request) {
	symbol := chi.URLParam(r, "symbol")

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
			return
		}
		limit = n
	}

	resp, err := h.service.History(r.Context(), symbol, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
		return
	}

	WriteJSON(w, http.StatusOK, resp)
}
