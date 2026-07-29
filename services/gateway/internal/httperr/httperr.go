// Package httperr holds the gateway's JSON error shape. It lives in its own
// package rather than in internal/handler (where auth and market-data keep
// theirs) because internal/proxy needs it too, and internal/handler imports
// internal/proxy to mount the reverse proxies -- putting it in handler would
// be an import cycle.
package httperr

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the standard JSON error shape for every 4xx/5xx response,
// matching the auth and market-data services.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Write(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorResponse{Code: code, Message: message})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
