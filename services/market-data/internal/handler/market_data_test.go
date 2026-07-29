package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	r := NewRouter()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSymbolsHandler(t *testing.T) {
	r := NewRouter()

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body SymbolsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	want := []string{"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA", "SPY", "QQQ"}
	if len(body.Symbols) != len(want) {
		t.Fatalf("expected %d symbols, got %d: %v", len(want), len(body.Symbols), body.Symbols)
	}
	for i, s := range want {
		if body.Symbols[i] != s {
			t.Errorf("symbol[%d] = %q, want %q", i, body.Symbols[i], s)
		}
	}
}
