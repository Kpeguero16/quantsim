package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// maxBodyBytes caps every request body the auth service decodes. Nothing
// bounded body size before: json.Decode reads whatever is sent, so a
// multi-megabyte body was buffered in full before any validation ran -- the
// length rules in the service layer can't help, because they only see the
// decoded value. 64 KiB is far above any legitimate credential payload and
// far below anything that matters for memory.
//
// This is a transport concern, so it lives in the handler. That's consistent
// with keeping domain rules in the service, not an exception to it.
const maxBodyBytes = 64 << 10

type AuthHandler struct {
	service *service.Service
}

func NewAuthHandler(svc *service.Service) *AuthHandler {
	return &AuthHandler{service: svc}
}

// decodeJSON caps the body, decodes it, and writes the appropriate error
// response itself, reporting whether the caller should continue. Every route
// that reads a body goes through here so the cap can't be forgotten on one
// of them.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, "invalid_request", "request body is too large")
			return false
		}
		WriteError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return false
	}

	return true
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req service.RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// No non-empty checks here: every rule, including "is this present at
	// all", lives in service.ValidateRegistration. Two layers enforcing one
	// rule is how they drift apart.
	tokens, err := h.service.Register(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			WriteError(w, http.StatusBadRequest, "invalid_request", service.InvalidInputMessage(err))
			return
		}
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
	if !decodeJSON(w, r, &req) {
		return
	}

	// Deliberately no checks here, and no ErrInvalidInput mapping below:
	// every login failure -- absent, malformed, or simply wrong credentials
	// -- returns the same 401. Missing fields are just credentials that
	// don't match, and answering them differently would tell an attacker
	// which half of the pair was the problem.
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
	if !decodeJSON(w, r, &req) {
		return
	}
	// This check stays: it isn't duplicating a service rule, and a missing
	// refresh_token is a malformed request rather than a rejected token.
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
