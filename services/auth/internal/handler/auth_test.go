package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/auth/internal/handler"
	"github.com/kpeguero/quantsim/services/auth/internal/service"
	"github.com/kpeguero/quantsim/services/auth/internal/service/mock"
)

var testSecret = []byte("test-secret")

func newTestRouter() http.Handler {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)
	return handler.NewRouter(handler.NewAuthHandler(svc), testSecret)
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
		"password": testPassword,
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
		"password": testPassword,
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

func registerUser(t *testing.T, r http.Handler, email, username, password string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email": email, "username": username, "password": password,
	})
	if rec := doRequest(t, r, http.MethodPost, "/auth/register", body); rec.Code != http.StatusCreated {
		t.Fatalf("setup: expected register to succeed with 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginHandler_Success(t *testing.T) {
	r := newTestRouter()
	registerUser(t, r, "a@b.com", "alice", testPassword)

	body, _ := json.Marshal(map[string]string{"email": "a@b.com", "password": testPassword})
	rec := doRequest(t, r, http.MethodPost, "/auth/login", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var tokens service.TokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &tokens); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("expected non-empty tokens in response")
	}
}

func TestLoginHandler_WrongPasswordAndUnknownEmail_IdenticalResponse(t *testing.T) {
	r := newTestRouter()
	registerUser(t, r, "a@b.com", "alice", testPassword)

	wrongPwBody, _ := json.Marshal(map[string]string{"email": "a@b.com", "password": "wrong-password"})
	wrongPwRec := doRequest(t, r, http.MethodPost, "/auth/login", wrongPwBody)

	unknownEmailBody, _ := json.Marshal(map[string]string{"email": "nouser@x.com", "password": "whatever123"})
	unknownEmailRec := doRequest(t, r, http.MethodPost, "/auth/login", unknownEmailBody)

	if wrongPwRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d: %s", wrongPwRec.Code, wrongPwRec.Body.String())
	}
	if unknownEmailRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown email, got %d: %s", unknownEmailRec.Code, unknownEmailRec.Body.String())
	}
	if wrongPwRec.Body.String() != unknownEmailRec.Body.String() {
		t.Fatalf("expected identical bodies (no enumeration), got %q vs %q",
			wrongPwRec.Body.String(), unknownEmailRec.Body.String())
	}
}

func TestRefreshHandler_Success(t *testing.T) {
	r := newTestRouter()
	registerBody, _ := json.Marshal(map[string]string{
		"email": "a@b.com", "username": "alice", "password": testPassword,
	})
	registerRec := doRequest(t, r, http.MethodPost, "/auth/register", registerBody)
	var registerTokens service.TokenPair
	if err := json.Unmarshal(registerRec.Body.Bytes(), &registerTokens); err != nil {
		t.Fatalf("failed to decode register response: %v", err)
	}

	refreshBody, _ := json.Marshal(map[string]string{"refresh_token": registerTokens.RefreshToken})
	rec := doRequest(t, r, http.MethodPost, "/auth/refresh", refreshBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var newTokens service.TokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &newTokens); err != nil {
		t.Fatalf("failed to decode refresh response: %v", err)
	}
	if newTokens.AccessToken == "" || newTokens.RefreshToken == "" {
		t.Fatal("expected non-empty tokens in refresh response")
	}
}

func TestRefreshHandler_AccessTokenRejected(t *testing.T) {
	r := newTestRouter()
	registerBody, _ := json.Marshal(map[string]string{
		"email": "a@b.com", "username": "alice", "password": testPassword,
	})
	registerRec := doRequest(t, r, http.MethodPost, "/auth/register", registerBody)
	var registerTokens service.TokenPair
	if err := json.Unmarshal(registerRec.Body.Bytes(), &registerTokens); err != nil {
		t.Fatalf("failed to decode register response: %v", err)
	}

	refreshBody, _ := json.Marshal(map[string]string{"refresh_token": registerTokens.AccessToken})
	rec := doRequest(t, r, http.MethodPost, "/auth/refresh", refreshBody)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for access token used as refresh, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRefreshHandler_GarbageToken(t *testing.T) {
	r := newTestRouter()
	body, _ := json.Marshal(map[string]string{"refresh_token": "not-a-real-token"})
	rec := doRequest(t, r, http.MethodPost, "/auth/refresh", body)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func registerAndDecode(t *testing.T, r http.Handler, email, username, password string) service.TokenPair {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email": email, "username": username, "password": password,
	})
	rec := doRequest(t, r, http.MethodPost, "/auth/register", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: expected register to succeed with 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var tokens service.TokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &tokens); err != nil {
		t.Fatalf("failed to decode register response: %v", err)
	}
	return tokens
}

func doMeRequest(t *testing.T, r http.Handler, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMeHandler_Success(t *testing.T) {
	r := newTestRouter()
	tokens := registerAndDecode(t, r, "a@b.com", "alice", testPassword)

	rec := doMeRequest(t, r, "Bearer "+tokens.AccessToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var profile service.MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &profile); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if profile.Email != "a@b.com" || profile.Username != "alice" {
		t.Fatalf("expected profile for a@b.com/alice, got %+v", profile)
	}
}

func TestMeHandler_NoHeader(t *testing.T) {
	r := newTestRouter()
	registerAndDecode(t, r, "a@b.com", "alice", testPassword)

	rec := doMeRequest(t, r, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMeHandler_MalformedHeader(t *testing.T) {
	r := newTestRouter()
	registerAndDecode(t, r, "a@b.com", "alice", testPassword)

	rec := doMeRequest(t, r, "Basic sometoken")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMeHandler_RefreshTokenRejected(t *testing.T) {
	r := newTestRouter()
	tokens := registerAndDecode(t, r, "a@b.com", "alice", testPassword)

	rec := doMeRequest(t, r, "Bearer "+tokens.RefreshToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for refresh token used at /auth/me, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMeHandler_GarbageToken(t *testing.T) {
	r := newTestRouter()
	rec := doMeRequest(t, r, "Bearer garbage")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// --- Step 9: validation surfaces as 400, bodies are capped (SPEC.md 2.2, 2.11) ---

// testPassword satisfies the registration rules: 15+ runes, under 72 bytes,
// not on the blocklist, and containing neither "alice" nor a service term.
const testPassword = "quiet-harbor-lantern-9"

func TestRegisterHandler_ValidationFailuresAre400(t *testing.T) {
	cases := []struct {
		name, email, username, password string
	}{
		{"password one under the minimum", "a@b.com", "alice", "fourteen-chars"},
		{"password over 72 bytes", "a@b.com", "alice", strings.Repeat("z", 80)},
		{"password on the blocklist", "a@b.com", "alice", "aaaaaaaaaaaaaaaa"},
		{"malformed email", "x", "alice", testPassword},
		{"username of 500 characters", "a@b.com", strings.Repeat("u", 500), testPassword},
		{"empty password", "a@b.com", "alice", ""},
		{"empty email", "", "alice", testPassword},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter()
			body, _ := json.Marshal(map[string]string{
				"email": tc.email, "username": tc.username, "password": tc.password,
			})

			rec := doRequest(t, r, http.MethodPost, "/auth/register", body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}

			var errResp handler.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}
			if errResp.Code != "invalid_request" {
				t.Fatalf("expected code invalid_request, got %q", errResp.Code)
			}
			if errResp.Message == "" {
				t.Fatal("expected a non-empty message -- the frontend renders it verbatim")
			}
			// The message is shown to the user as-is, so the internal
			// sentinel text must not be part of it.
			if strings.Contains(errResp.Message, "invalid input") {
				t.Fatalf("sentinel text leaked into the user-facing message: %q", errResp.Message)
			}
		})
	}
}

// TestRegisterHandler_OversizedBodyRejected covers SPEC.md 2.11: length
// validation runs after decoding, so only a cap on the reader can stop a
// multi-megabyte body from being buffered in the first place.
func TestRegisterHandler_OversizedBodyRejected(t *testing.T) {
	r := newTestRouter()
	body, _ := json.Marshal(map[string]string{
		"email": "a@b.com", "username": "alice", "password": strings.Repeat("a", 128<<10),
	})
	if len(body) <= 64<<10 {
		t.Fatalf("test body is %d bytes, which does not exceed the 64 KiB cap", len(body))
	}

	rec := doRequest(t, r, http.MethodPost, "/auth/register", body)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRegisterHandler_ValidBodyUnderTheCapStillSucceeds(t *testing.T) {
	r := newTestRouter()
	registerUser(t, r, "a@b.com", "alice", testPassword)
}

func TestLoginHandler_DifferentCapitalizationSucceeds(t *testing.T) {
	r := newTestRouter()
	registerUser(t, r, "case@b.test", "casey", testPassword)

	body, _ := json.Marshal(map[string]string{"email": "CASE@B.TEST", "password": testPassword})
	rec := doRequest(t, r, http.MethodPost, "/auth/login", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 logging in with different capitalization, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestLoginHandler_MissingFieldsAreUniform401 pins a deliberate behaviour
// change in Step 9. The handler used to answer 400 "email and password are
// required" for missing fields; now they take the same path as any other
// failed login and come back 401.
//
// This is the point of SPEC.md 2.12: absent, malformed, and simply wrong
// credentials must be indistinguishable. A 400 here tells an attacker which
// half of the pair the server objected to, which is the same class of leak
// the uniform ErrInvalidCredentials and the dummy-hash timing defence exist
// to close.
func TestLoginHandler_MissingFieldsAreUniform401(t *testing.T) {
	registered := func() http.Handler {
		r := newTestRouter()
		registerUser(t, r, "a@b.com", "alice", testPassword)
		return r
	}

	bodies := map[string]string{
		"empty object":     `{}`,
		"missing password": `{"email":"a@b.com"}`,
		"missing email":    `{"password":"` + testPassword + `"}`,
		"both empty":       `{"email":"","password":""}`,
		"wrong password":   `{"email":"a@b.com","password":"definitely-wrong-pw"}`,
	}

	var seen []string
	for name, body := range bodies {
		rec := doRequest(t, registered(), http.MethodPost, "/auth/login", []byte(body))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d: %s", name, rec.Code, rec.Body.String())
		}
		seen = append(seen, rec.Body.String())
	}

	// Identical bodies, not merely identical statuses.
	for i, body := range seen {
		if body != seen[0] {
			t.Fatalf("login responses differ, which distinguishes failure modes: %q vs %q", seen[i], seen[0])
		}
	}
}
