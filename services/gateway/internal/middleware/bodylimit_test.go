package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
	"github.com/kpeguero/quantsim/services/gateway/internal/middleware"
)

const testLimit = 1024

// limited wraps a handler that records whether it ran and how much of the body
// it could read, which is the only way to tell "capped" from "rejected".
func limited(t *testing.T, reached *bool, read *int) http.Handler {
	t.Helper()
	return middleware.LimitBody(testLimit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		b, err := io.ReadAll(r.Body)
		*read = len(b)
		if err != nil {
			// A truncated read is the expected outcome for a chunked
			// over-limit body; the point is that it did not arrive whole.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func TestLimitBody_OversizedDeclaredBodyIs413(t *testing.T) {
	var reached bool
	var read int
	h := limited(t, &reached, &read)

	body := strings.Repeat("x", testLimit+1)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", rec.Code)
	}
	if reached {
		t.Error("an oversized body reached the handler; it must be refused before it is proxied")
	}

	var errBody httperr.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("413 body is not the JSON error shape: %v", err)
	}
	if errBody.Code != "payload_too_large" {
		t.Errorf("got code %q, want payload_too_large", errBody.Code)
	}
}

// The case the Content-Length check cannot see. A chunked body declares no
// length, so nothing can be rejected up front -- MaxBytesReader is what stops
// it, and without that wrapper this route would be uncapped.
func TestLimitBody_ChunkedOversizedBodyIsTruncated(t *testing.T) {
	var reached bool
	var read int
	h := limited(t, &reached, &read)

	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(strings.Repeat("x", testLimit*4)))
	// -1 is how Go models "chunked, length unknown".
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if read > testLimit {
		t.Errorf("handler read %d bytes past a %d cap -- the body was not truncated", read, testLimit)
	}
}

func TestLimitBody_BodyAtTheLimitIsAllowed(t *testing.T) {
	var reached bool
	var read int
	h := limited(t, &reached, &read)

	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(strings.Repeat("x", testLimit)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 -- a body of exactly the limit is not over it", rec.Code)
	}
	if read != testLimit {
		t.Errorf("handler read %d bytes, want all %d", read, testLimit)
	}
}

func TestLimitBody_BodylessRequestsAreUnaffected(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			var reached bool
			var read int
			h := limited(t, &reached, &read)

			req := httptest.NewRequest(method, "/market-data/symbols", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if !reached {
				t.Fatalf("a %s with no body was blocked", method)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("got %d, want 200", rec.Code)
			}
		})
	}
}

// 64 KiB, matching auth's own per-request cap. The gateway must not be
// stricter than the service behind it, or it becomes a second validation
// layer that disagrees with the real one.
func TestMaxBodyBytesMatchesTheServiceCap(t *testing.T) {
	if middleware.MaxBodyBytes != 64<<10 {
		t.Errorf("MaxBodyBytes = %d, want 65536 (what auth applies per request)", middleware.MaxBodyBytes)
	}
}
