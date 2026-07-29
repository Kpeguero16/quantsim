package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kpeguero/quantsim/services/auth/internal/handler"
	"github.com/kpeguero/quantsim/services/auth/internal/service"
	"github.com/kpeguero/quantsim/services/auth/internal/service/mock"
)

var testSecret = []byte("test-secret")

func newTestRouter() http.Handler {
	users := mock.NewUserStore()
	accounts := &mock.AccountStore{}
	svc := service.NewService(users, accounts, testSecret)
	return handler.NewRouter(handler.NewAuthHandler(svc))
}

func doRequest(t *testing.T, r http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRegisterHandler_Success(t *testing.T) {
	r := newTestRouter()
	body, _ := json.Marshal(map[string]string{
		"email":    "a@b.com",
		"username": "alice",
		"password": "pw12345678",
	})

	rec := doRequest(t, r, http.MethodPost, "/auth/register", body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var tokens service.TokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &tokens); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("expected non-empty tokens in response")
	}
}

func TestRegisterHandler_DuplicateEmail(t *testing.T) {
	r := newTestRouter()
	body, _ := json.Marshal(map[string]string{
		"email":    "a@b.com",
		"username": "alice",
		"password": "pw12345678",
	})

	if rec := doRequest(t, r, http.MethodPost, "/auth/register", body); rec.Code != http.StatusCreated {
		t.Fatalf("expected first register to succeed with 201, got %d", rec.Code)
	}

	rec := doRequest(t, r, http.MethodPost, "/auth/register", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var errResp handler.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Code != "duplicate_user" {
		t.Fatalf("expected code duplicate_user, got %q", errResp.Code)
	}
}

func TestRegisterHandler_MalformedJSON(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, http.MethodPost, "/auth/register", []byte("not-json"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var errResp handler.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Code == "" {
		t.Fatal("expected a non-empty error code")
	}
}

func TestHealthz(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
