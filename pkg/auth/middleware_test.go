package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/pkg/auth"
)

var testSecret = []byte("test-secret")

func newProtectedHandler() http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := auth.UserIDFromContext(r.Context())
		if !ok {
			http.Error(w, "no user id in context", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(userID))
	})
	return auth.RequireAuth(testSecret)(inner)
}

func doAuthedRequest(t *testing.T, header string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	newProtectedHandler().ServeHTTP(rec, req)
	return rec
}

func TestRequireAuth_ValidAccessToken(t *testing.T) {
	token, err := auth.GenerateToken(testSecret, "user-123", auth.TokenTypeAccess, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	rec := doAuthedRequest(t, "Bearer "+token)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "user-123" {
		t.Fatalf("expected context to carry user-123, got %q", rec.Body.String())
	}
}

func TestRequireAuth_MissingHeader(t *testing.T) {
	rec := doAuthedRequest(t, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuth_MalformedHeader(t *testing.T) {
	rec := doAuthedRequest(t, "Basic sometoken")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-Bearer header, got %d", rec.Code)
	}
}

func TestRequireAuth_ExpiredToken(t *testing.T) {
	token, err := auth.GenerateToken(testSecret, "user-123", auth.TokenTypeAccess, -1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	rec := doAuthedRequest(t, "Bearer "+token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", rec.Code)
	}
}

func TestRequireAuth_RefreshTokenRejected(t *testing.T) {
	token, err := auth.GenerateToken(testSecret, "user-123", auth.TokenTypeRefresh, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	rec := doAuthedRequest(t, "Bearer "+token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for refresh token used as access, got %d", rec.Code)
	}
}
