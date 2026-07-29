package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

type AuthHandler struct {
	service *service.Service
}

func NewAuthHandler(svc *service.Service) *AuthHandler {
	return &AuthHandler{service: svc}
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req service.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.Email == "" || req.Username == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "email, username, and password are required")
		return
	}

	tokens, err := h.service.Register(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrDuplicateUser) {
			WriteError(w, http.StatusConflict, "duplicate_user", "email or username already exists")
			return
		}
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
		return
	}

	WriteJSON(w, http.StatusCreated, tokens)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req service.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.Email == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "email and password are required")
		return
	}

	tokens, err := h.service.Login(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
		return
	}

	WriteJSON(w, http.StatusOK, tokens)
}
