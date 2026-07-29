package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/gateway/internal/middleware"
)

var testSecret = []byte("test-secret-at-least-32-bytes-long!!")

// backendSpy records the X-User-ID a backend would actually observe.
func backendSpy(seen *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get(middleware.UserIDHeader)
	})
}

// TestStripUserIDRemovesSpoofedHeader is the spoofing regression test and the
// most important one in this package. A caller sets X-User-ID by hand with no
// token at all; it must not survive to the backend.
func TestStripUserIDRemovesSpoofedHeader(t *testing.T) {
	var seen string
	h := middleware.StripUserID()(backendSpy(&seen))

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set(middleware.UserIDHeader, "00000000-0000-0000-0000-victimuser")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen != "" {
		t.Fatalf("backend saw X-User-ID %q; a client-supplied identity header must never survive", seen)
	}
}

// TestStripUserIDAppliesToPublicRoutes: /auth/* is public and proxied, which
// is exactly why the strip cannot be scoped to authenticated routes.
func TestStripUserIDAppliesToPublicRoutes(t *testing.T) {
	var seen string
	h := middleware.StripUserID()(backendSpy(&seen))

	req := httptest.NewRequest(http.MethodPost, "/auth/register", nil)
	req.Header.Set(middleware.UserIDHeader, "attacker-chosen")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen != "" {
		t.Fatalf("backend saw X-User-ID %q on a public route", seen)
	}
}

// TestInjectUserIDSetsHeaderFromClaims covers the happy path through the real
// chain: RequireAuth validates a genuine token, InjectUserID forwards it.
func TestInjectUserIDSetsHeaderFromClaims(t *testing.T) {
	const userID = "11111111-2222-3333-4444-555555555555"

	token, err := pkgauth.GenerateToken(testSecret, userID, pkgauth.TokenTypeAccess, time.Minute)
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}

	var seen string
	h := middleware.StripUserID()(
		pkgauth.RequireAuth(testSecret)(
			middleware.InjectUserID()(backendSpy(&seen)),
		),
	)

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	// The client also tries to spoof; the validated claims must win.
	req.Header.Set(middleware.UserIDHeader, "attacker-chosen")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seen != userID {
		t.Errorf("backend saw X-User-ID %q, want the token subject %q", seen, userID)
	}
}

// TestInjectUserIDFailsClosed: with no user ID on the context the chain is
// misordered and the request was never authenticated, so it must be refused
// outright. Forwarding it minus the header would let a deployment mistake
// quietly serve unauthenticated traffic.
func TestInjectUserIDFailsClosed(t *testing.T) {
	var called bool
	h := middleware.InjectUserID()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set(middleware.UserIDHeader, "attacker-chosen")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Error("request was forwarded with no authenticated user on the context")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestRefreshTokenIsRejected confirms the chain relies on RequireAuth's
// access-vs-refresh distinction: a refresh token must not open a data route.
func TestRefreshTokenIsRejected(t *testing.T) {
	token, err := pkgauth.GenerateToken(testSecret, "some-user", pkgauth.TokenTypeRefresh, time.Minute)
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}

	var called bool
	h := pkgauth.RequireAuth(testSecret)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Error("a refresh token reached a protected route")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
