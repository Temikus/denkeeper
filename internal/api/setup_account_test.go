package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// testServerWithPIN creates a server with a setup PIN set and no auth configured.
func testServerWithPIN(t *testing.T, pin string) *Server {
	t.Helper()

	// Create a temp config file for the test.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("[api]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return &Server{
		cfg:          testConfig(),
		deps:         Deps{ConfigPath: cfgPath},
		logger:       testLogger(),
		limiters:     make(map[string]*rateLimiter),
		loginLimiter: newLoginRateLimiter(5, 15*time.Minute),
		setupPIN:     pin,
		bcryptCost:   bcrypt.MinCost,
	}
}

func TestSetupAccount_Success(t *testing.T) {
	s := testServerWithPIN(t, "123456")
	defer s.loginLimiter.stop()

	body := `{"pin":"123456","password":"strongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSetupAccount(rec, req)

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

	// PIN should be cleared after use.
	if s.setupPIN != "" {
		t.Error("expected setupPIN to be cleared after successful setup")
	}

	// Password hash and sessions should be set.
	if s.passwordHash == "" {
		t.Error("expected passwordHash to be set")
	}
	if s.sessions == nil {
		t.Error("expected sessions manager to be created")
	}

	// Response should indicate authenticated.
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["authenticated"] != true {
		t.Error("expected authenticated=true in response")
	}

	// TOML config should have been persisted with password_hash and session_secret.
	cfgData, err := os.ReadFile(s.deps.ConfigPath)
	if err != nil {
		t.Fatalf("reading config file: %v", err)
	}
	cfgStr := string(cfgData)
	if !strings.Contains(cfgStr, "password_hash") {
		t.Error("config file should contain password_hash")
	}
	if !strings.Contains(cfgStr, "session_secret") {
		t.Error("config file should contain session_secret")
	}
}

func TestSetupAccount_WrongPIN(t *testing.T) {
	s := testServerWithPIN(t, "123456")
	defer s.loginLimiter.stop()

	body := `{"pin":"999999","password":"strongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSetupAccount(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	// PIN should NOT be cleared.
	if s.setupPIN == "" {
		t.Error("expected setupPIN to still be set after wrong PIN")
	}
}

func TestSetupAccount_AlreadyComplete(t *testing.T) {
	s := testServerWithPIN(t, "") // empty PIN = already used
	defer s.loginLimiter.stop()

	body := `{"pin":"123456","password":"strongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSetupAccount(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupAccount_WeakPassword(t *testing.T) {
	s := testServerWithPIN(t, "123456")
	defer s.loginLimiter.stop()

	body := `{"pin":"123456","password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSetupAccount(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// PIN should NOT be cleared.
	if s.setupPIN == "" {
		t.Error("expected setupPIN to still be set after weak password")
	}
}

func TestSetupAccount_MissingPIN(t *testing.T) {
	s := testServerWithPIN(t, "123456")
	defer s.loginLimiter.stop()

	body := `{"pin":"","password":"strongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSetupAccount(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupAccount_InvalidJSON(t *testing.T) {
	s := testServerWithPIN(t, "123456")
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSetupAccount(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupAccount_RateLimited(t *testing.T) {
	s := testServerWithPIN(t, "123456")
	defer s.loginLimiter.stop()

	// Exhaust the rate limit (5 attempts).
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(`{"pin":"999999","password":"strongpassword"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "5.6.7.8:1234"
		rec := httptest.NewRecorder()
		s.handleSetupAccount(rec, req)
	}

	// 6th attempt should be rate limited (even with correct PIN).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/account", strings.NewReader(`{"pin":"123456","password":"strongpassword"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "5.6.7.8:1234"
	rec := httptest.NewRecorder()
	s.handleSetupAccount(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestSetupStatus_IncludesAccountAvailable(t *testing.T) {
	s := testServerWithPIN(t, "123456")
	defer s.loginLimiter.stop()

	// Need a key store for handleSetupStatus — use in-memory SQLite.
	ks, err := NewKeyStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	s.deps.KeyStore = ks

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup", nil)
	rec := httptest.NewRecorder()
	s.handleSetupStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["account_setup_available"] != true {
		t.Error("expected account_setup_available=true when PIN is set")
	}
	if resp["setup_required"] != true {
		t.Error("expected setup_required=true when no keys or password exist")
	}
}

func TestSetupRequired_FalseWhenPasswordConfigured(t *testing.T) {
	s := testServerWithPIN(t, "")
	defer s.loginLimiter.stop()
	s.passwordHash = "$2a$04$somehashvalue" // non-empty = password configured

	ks, err := NewKeyStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	s.deps.KeyStore = ks

	required, err := s.setupRequired(httptest.NewRequest(http.MethodGet, "/", nil).Context())
	if err != nil {
		t.Fatal(err)
	}
	if required {
		t.Error("expected setupRequired=false when passwordHash is set")
	}
}
