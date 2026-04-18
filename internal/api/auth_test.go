package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Temikus/denkeeper/internal/config"
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
		bcryptCost:   bcrypt.MinCost,
	}
}

func TestHandleAuthConfig_NothingEnabled(t *testing.T) {
	s := testServerWithAuth(t, "")
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodGet, "/auth/config", nil)
	rec := httptest.NewRecorder()
	s.handleAuthConfig(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["password_enabled"] != false {
		t.Error("expected password_enabled=false")
	}
	if resp["oidc_enabled"] != false {
		t.Error("expected oidc_enabled=false")
	}
	if resp["preferred_login_method"] != "auto" {
		t.Errorf("expected preferred_login_method=auto, got %v", resp["preferred_login_method"])
	}
}

func TestHandleAuthConfig_PasswordEnabled(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("secret"))
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodGet, "/auth/config", nil)
	rec := httptest.NewRecorder()
	s.handleAuthConfig(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["password_enabled"] != true {
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

// testServerWithConfig creates a test server with a writable TOML config file.
func testServerWithConfig(t *testing.T, passwordHash string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("[api]\n[api.auth]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := hex.EncodeToString(make([]byte, 32))
	sm, err := NewSessionManager(key, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.API.Auth.PreferredLoginMethod = "auto"
	return &Server{
		cfg:          testConfig(),
		deps:         Deps{ConfigPath: cfgPath, Config: cfg},
		logger:       testLogger(),
		limiters:     make(map[string]*rateLimiter),
		sessions:     sm,
		passwordHash: passwordHash,
		loginLimiter: newLoginRateLimiter(5, 15*time.Minute),
		bcryptCost:   bcrypt.MinCost,
	}, cfgPath
}

func TestHandlePasswordChange_Success(t *testing.T) {
	s, _ := testServerWithConfig(t, testPasswordHash("oldpass"))
	defer s.loginLimiter.stop()

	body := `{"current_password":"oldpass","new_password":"newpass1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePasswordChange(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify hash was updated and authenticates with new password.
	if err := bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte("newpass1234")); err != nil {
		t.Error("new password hash does not verify")
	}
}

func TestHandlePasswordChange_WrongCurrent(t *testing.T) {
	s, _ := testServerWithConfig(t, testPasswordHash("correct"))
	defer s.loginLimiter.stop()

	body := `{"current_password":"wrong","new_password":"newpass1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePasswordChange(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandlePasswordChange_WeakNew(t *testing.T) {
	s, _ := testServerWithConfig(t, testPasswordHash("correct"))
	defer s.loginLimiter.stop()

	body := `{"current_password":"correct","new_password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePasswordChange(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePasswordChange_NotConfigured(t *testing.T) {
	s, _ := testServerWithConfig(t, "")
	defer s.loginLimiter.stop()

	body := `{"current_password":"x","new_password":"longpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePasswordChange(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlePasswordChange_RateLimited(t *testing.T) {
	s, _ := testServerWithConfig(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	// Exhaust rate limit.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"wrong","new_password":"whatever1"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "5.5.5.5:1234"
		rec := httptest.NewRecorder()
		s.handlePasswordChange(rec, req)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"pw","new_password":"newpass1234"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "5.5.5.5:1234"
	rec := httptest.NewRecorder()
	s.handlePasswordChange(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestHandleAuthPreferences_Valid(t *testing.T) {
	s, _ := testServerWithConfig(t, "")
	defer s.loginLimiter.stop()

	body := `{"preferred_login_method":"password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleAuthPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if s.deps.Config.API.Auth.PreferredLoginMethod != "password" {
		t.Errorf("expected in-memory update to 'password', got %q", s.deps.Config.API.Auth.PreferredLoginMethod)
	}
}

func TestHandleAuthPreferences_Invalid(t *testing.T) {
	s, _ := testServerWithConfig(t, "")
	defer s.loginLimiter.stop()

	body := `{"preferred_login_method":"magic"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleAuthPreferences(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleOIDCTest_NoOIDC(t *testing.T) {
	s, _ := testServerWithConfig(t, "")
	defer s.loginLimiter.stop()
	// s.oidcProvider is nil by default.

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/test", nil)
	rec := httptest.NewRecorder()
	s.handleOIDCTest(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleOIDCTest_Reachable(t *testing.T) {
	// Spin up a minimal OIDC discovery mock.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]any{
			"issuer":                                "",
			"authorization_endpoint":                "/authorize",
			"token_endpoint":                        "/token",
			"userinfo_endpoint":                     "/userinfo",
			"jwks_uri":                              "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		// Set issuer to the request's host so go-oidc issuer validation passes.
		doc["issuer"] = "http://" + r.Host
		doc["authorization_endpoint"] = "http://" + r.Host + "/authorize"
		doc["token_endpoint"] = "http://" + r.Host + "/token"
		doc["userinfo_endpoint"] = "http://" + r.Host + "/userinfo"
		doc["jwks_uri"] = "http://" + r.Host + "/jwks"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc) //nolint:errcheck
	}))
	defer mock.Close()

	s, _ := testServerWithConfig(t, "")
	defer s.loginLimiter.stop()
	// Set up a non-nil oidcProvider so the guard passes.
	s.oidcProvider = &OIDCProvider{}
	s.deps.Config.API.Auth.OIDC.Issuer = mock.URL

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/test", nil)
	rec := httptest.NewRecorder()
	s.handleOIDCTest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Error("expected ok=true")
	}
	endpoints, _ := resp["endpoints"].(map[string]any)
	if endpoints["authorization"] == "" {
		t.Error("expected authorization endpoint")
	}
	if endpoints["userinfo"] == "" {
		t.Error("expected userinfo endpoint")
	}
}

func TestHandleAuthStatus_NoAuth(t *testing.T) {
	s := testServerWithAuth(t, "")
	defer s.loginLimiter.stop()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	rec := httptest.NewRecorder()
	s.handleAuthStatus(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp["password_enabled"] != false {
		t.Error("expected password_enabled=false")
	}
	if resp["oidc_enabled"] != false {
		t.Error("expected oidc_enabled=false")
	}
	if resp["sessions_trackable"] != false {
		t.Error("expected sessions_trackable=false")
	}
}

func TestHandleListSessions_Success(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	s.sessions.Store = store

	// Create a session via the manager (simulates login).
	loginW := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodGet, "/", nil)
	loginReq.Header.Set("User-Agent", "test-browser")
	if err := s.sessions.CreateWithRequest(loginW, loginReq, Session{
		Email: "admin@example.com", Scopes: []string{"admin"},
	}); err != nil {
		t.Fatal(err)
	}
	cookie := loginW.Result().Cookies()[0]

	// Create a second session directly in the store.
	_, _ = store.Create(loginReq.Context(), "admin@example.com", []string{"admin"}, "curl/8.0", "10.0.0.1", time.Now().Add(24*time.Hour))

	// List sessions with the cookie.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Sessions         []SessionRecord `json:"sessions"`
		CurrentSessionID string          `json:"current_session_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(resp.Sessions))
	}
	if resp.CurrentSessionID == "" {
		t.Error("expected non-empty current_session_id")
	}
	// Verify the current session ID matches one of the records.
	found := false
	for _, s := range resp.Sessions {
		if s.ID == resp.CurrentSessionID {
			found = true
		}
	}
	if !found {
		t.Error("current_session_id does not match any session record")
	}
}

func TestHandleListSessions_NoStore(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()
	// Store is nil by default from testServerWithAuth.

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
	rec := httptest.NewRecorder()
	s.handleListSessions(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleRevokeSession_Success(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	s.sessions.Store = store

	ctx := httptest.NewRequest(http.MethodGet, "/", nil).Context()
	id, _ := store.Create(ctx, "admin@example.com", []string{"admin"}, "ua", "127.0.0.1", time.Now().Add(24*time.Hour))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleRevokeSession(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	// Verify session is gone.
	_, lookupErr := store.Get(ctx, id)
	if lookupErr == nil {
		t.Error("expected session to be deleted from store")
	}
}

func TestHandleRevokeSession_MissingID(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	s.sessions.Store = store

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/", nil)
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	s.handleRevokeSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRevokeAllSessions_Success(t *testing.T) {
	s := testServerWithAuth(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()

	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	s.sessions.Store = store

	// Create the "current" session via the manager.
	loginW := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := s.sessions.CreateWithRequest(loginW, loginReq, Session{
		Email: "admin@example.com", Scopes: []string{"admin"},
	}); err != nil {
		t.Fatal(err)
	}
	cookie := loginW.Result().Cookies()[0]

	// Create 2 more sessions.
	ctx := loginReq.Context()
	_, _ = store.Create(ctx, "admin@example.com", []string{"admin"}, "ua1", "1.2.3.4", time.Now().Add(24*time.Hour))
	_, _ = store.Create(ctx, "admin@example.com", []string{"admin"}, "ua2", "5.6.7.8", time.Now().Add(24*time.Hour))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleRevokeAllSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	revoked, ok := resp["revoked"].(float64)
	if !ok || revoked != 3 {
		t.Errorf("expected revoked=3, got %v", resp["revoked"])
	}
}

func TestHandleAuthStatus_Enriched(t *testing.T) {
	s, _ := testServerWithConfig(t, testPasswordHash("pw"))
	defer s.loginLimiter.stop()
	s.deps.Config.API.Auth.OIDC.Issuer = "https://accounts.example.com"
	s.deps.Config.API.Auth.OIDC.AllowedEmails = []string{"user@example.com"}
	s.deps.Config.API.Auth.PreferredLoginMethod = "password"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	rec := httptest.NewRecorder()
	s.handleAuthStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp["password_enabled"] != true {
		t.Error("expected password_enabled=true")
	}
	if resp["oidc_issuer"] != "https://accounts.example.com" {
		t.Errorf("expected oidc_issuer, got %v", resp["oidc_issuer"])
	}
	if resp["preferred_login_method"] != "password" {
		t.Errorf("expected preferred_login_method=password, got %v", resp["preferred_login_method"])
	}
}
