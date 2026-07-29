package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
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

// Me is mounted behind pkg/auth.RequireAuth, which is the sole JWT
// gatekeeper for this route -- by the time this runs, the token has already
// been validated and its subject placed on the context.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	idStr, ok := pkgauth.UserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}
	userID, err := uuid.Parse(idStr)
	if err != nil {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	profile, err := h.service.Me(r.Context(), userID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
			return
		}
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
		return
	}

	WriteJSON(w, http.StatusOK, profile)
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req service.RefreshTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.RefreshToken == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}

	tokens, err := h.service.Refresh(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrTokenInvalid) {
			WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
			return
		}
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
		return
	}

	WriteJSON(w, http.StatusOK, tokens)
}
