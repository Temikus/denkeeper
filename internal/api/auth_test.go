package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testPasswordHash(password string) string {
	h, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	return string(h)
}

func testServerWithAuth(t *testing.T, passwordHash string) *Server {
	t.Helper()
	key := hex.EncodeToString(make([]byte, 32))
	sm, err := NewSessionManager(key, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		cfg:          testConfig(),
		deps:         Deps{},
		logger:       testLogger(),
		limiters:     make(map[string]*rateLimiter),
		sessions:     sm,
		passwordHash: passwordHash,
		loginLimiter: newLoginRateLimiter(5, 15*time.Minute),
	}
}

func TestHandleAuthConfig_NothingEnabled(t *testing.T) {
	s := testServerWithAuth(t, "")
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodGet, "/auth/config", nil)
	rec := httptest.NewRecorder()
	s.handleAuthConfig(rec, req)

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["password_enabled"] {
		t.Error("expected password_enabled=false")
	}
	if resp["oidc_enabled"] {
		t.Error("expected oidc_enabled=false")
	}
}

func TestHandleAuthConfig_PasswordEnabled(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("secret"))
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodGet, "/auth/config", nil)
	rec := httptest.NewRecorder()
	s.handleAuthConfig(rec, req)

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["password_enabled"] {
		t.Error("expected password_enabled=true")
	}
}

func TestHandlePasswordLogin_Success(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("mypassword"))
	defer s.loginLimiter.stop()

	body := `{"password":"mypassword"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePasswordLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Should have a session cookie.
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected session cookie to be set")
	}
}

func TestHandlePasswordLogin_WrongPassword(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("correct"))
	defer s.loginLimiter.stop()

	body := `{"password":"wrong"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePasswordLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandlePasswordLogin_RateLimit(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	// Exhaust the rate limit (5 attempts).
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "1.2.3.4:1234"
		rec := httptest.NewRecorder()
		s.handlePasswordLogin(rec, req)
	}

	// 6th attempt should be rate limited.
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"pw"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	s.handlePasswordLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestHandlePasswordLogin_NotConfigured(t *testing.T) {
	s := testServerWithAuth(t, "") // no password hash
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePasswordLogin(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleLogout(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	rec := httptest.NewRecorder()
	s.handleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		if c.Name == sessionCookieName && c.MaxAge != -1 {
			t.Error("expected session cookie to be cleared")
		}
	}
}

func TestHandleSessionCheck_NoSession(t *testing.T) {
	s := testServerWithAuth(t, "")
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	rec := httptest.NewRecorder()
	s.handleSessionCheck(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["authenticated"] != false {
		t.Error("expected authenticated=false")
	}
}

func TestHandleSessionCheck_ValidSession(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	// First, log in.
	loginBody := `{"password":"pw"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	s.handlePasswordLogin(loginRec, loginReq)

	// Now check session with the cookie from login.
	checkReq := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	for _, c := range loginRec.Result().Cookies() {
		checkReq.AddCookie(c)
	}
	checkRec := httptest.NewRecorder()
	s.handleSessionCheck(checkRec, checkReq)

	var resp map[string]any
	_ = json.NewDecoder(checkRec.Body).Decode(&resp)
	if resp["authenticated"] != true {
		t.Error("expected authenticated=true")
	}
}

func TestClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	if got := clientIP(req); got != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %q", got)
	}
}

func TestClientIP_XForwardedForMultiple(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientIP(req); got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %q", got)
	}
}

func TestClientIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-Ip", "9.8.7.6")
	if got := clientIP(req); got != "9.8.7.6" {
		t.Errorf("expected 9.8.7.6, got %q", got)
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	if got := clientIP(req); got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %q", got)
	}
}

func TestIsValidOrigin_Wildcard(t *testing.T) {
	s := testServerWithAuth(t, "")
	defer s.loginLimiter.stop()
	s.cfg.CORSOrigins = []string{"*"}

	if !s.isValidOrigin("https://anything.example.com") {
		t.Error("expected wildcard CORS to allow any origin")
	}
}

func TestIsValidOrigin_ExactMatch(t *testing.T) {
	s := testServerWithAuth(t, "")
	defer s.loginLimiter.stop()
	s.cfg.CORSOrigins = []string{"https://example.com"}

	if !s.isValidOrigin("https://example.com") {
		t.Error("expected exact match to return true")
	}
	if s.isValidOrigin("https://other.com") {
		t.Error("expected non-matching origin to return false")
	}
}

func TestIsValidOrigin_NoCORSConfigured(t *testing.T) {
	s := testServerWithAuth(t, "")
	defer s.loginLimiter.stop()
	s.cfg.CORSOrigins = nil

	if !s.isValidOrigin("https://whatever.com") {
		t.Error("expected empty CORS origins to allow any origin (same-origin default)")
	}
}

func TestAuthenticate_SessionCookie(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	// Log in to get a session cookie.
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"pw"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	s.handlePasswordLogin(loginRec, loginReq)

	// Use the session cookie for authentication.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	for _, c := range loginRec.Result().Cookies() {
		req.AddCookie(c)
	}

	name, ok := s.authenticate(req.Context(), req, "admin")
	if !ok {
		t.Fatal("expected authentication to succeed")
	}
	if name != "admin" {
		t.Errorf("expected name=admin, got %q", name)
	}
}
