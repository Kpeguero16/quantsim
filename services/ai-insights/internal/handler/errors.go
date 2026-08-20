package handler

import (
	"encoding/json"
	"net/http"
)

// This file is a deliberate copy of the same two helpers in every other
// QuantSim service, not an oversight -- see trading-engine's own copy for
// the reasoning: the JSON error shape is a contract between each service and
// the gateway, and every service owning its own encoder is what keeps that
// contract from becoming a shared package every service must be redeployed
// to change.

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorResponse{Code: code, Message: message})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
