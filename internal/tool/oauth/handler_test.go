//go:build mcp_go_client_oauth

package oauth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
	_ "modernc.org/sqlite"
)

func testHandlerDeps(t *testing.T) (*TokenStore, *PendingManager, *slog.Logger) {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTokenStore(db, testKey())
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	pending := NewPendingManager(logger)

	return store, pending, logger
}

func TestNewHandler_PreregisteredClient(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:     "test-tool",
		CallbackURL:  "http://localhost:8080/api/v1/tools/oauth/callback",
		ClientID:     "my-client-id",
		ClientSecret: "my-client-secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	if h.ToolName() != "test-tool" {
		t.Errorf("tool name: got %q", h.ToolName())
	}
	if h.HasToken() {
		t.Error("should not have token before auth")
	}
}

func TestNewHandler_DynamicRegistration(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:    "dcr-tool",
		CallbackURL: "http://localhost:8080/api/v1/tools/oauth/callback",
		Store:       store,
		Pending:     pending,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestNewHandler_MissingToolName(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	_, err := NewHandler(HandlerConfig{
		CallbackURL: "http://localhost:8080/callback",
		Store:       store,
		Pending:     pending,
		Logger:      logger,
	})
	if err == nil {
		t.Fatal("expected error for missing tool name")
	}
}

func TestNewHandler_MissingCallbackURL(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	_, err := NewHandler(HandlerConfig{
		ToolName: "test",
		Store:    store,
		Pending:  pending,
		Logger:   logger,
	})
	if err == nil {
		t.Fatal("expected error for missing callback URL")
	}
}

func TestNewHandler_MissingStore(t *testing.T) {
	_, pending, logger := testHandlerDeps(t)

	_, err := NewHandler(HandlerConfig{
		ToolName:    "test",
		CallbackURL: "http://localhost/callback",
		Pending:     pending,
		Logger:      logger,
	})
	if err == nil {
		t.Fatal("expected error for missing store")
	}
}

func TestHandler_TokenSource_NilWithoutCachedToken(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:     "test-tool",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "id",
		ClientSecret: "secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("token source: %v", err)
	}
	if ts != nil {
		t.Error("expected nil token source without cached token")
	}
}

func TestHandler_TokenSource_WithCachedToken(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	// Pre-store a token.
	expiry := time.Now().Add(time.Hour)
	if err := store.Put(&StoredToken{
		ToolName:    "cached-tool",
		AccessToken: "cached-access",
		TokenType:   "Bearer",
		Expiry:      &expiry,
	}); err != nil {
		t.Fatalf("pre-store: %v", err)
	}

	h, err := NewHandler(HandlerConfig{
		ToolName:     "cached-tool",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "id",
		ClientSecret: "secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if !h.HasToken() {
		t.Error("expected cached token")
	}

	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("token source: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil token source with cached token")
	}

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("getting token: %v", err)
	}
	if tok.AccessToken != "cached-access" {
		t.Errorf("access token: got %q, want %q", tok.AccessToken, "cached-access")
	}
}

func TestHandler_ClearToken(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	expiry := time.Now().Add(time.Hour)
	if err := store.Put(&StoredToken{
		ToolName:    "clear-tool",
		AccessToken: "tok",
		TokenType:   "Bearer",
		Expiry:      &expiry,
	}); err != nil {
		t.Fatalf("pre-store: %v", err)
	}

	h, err := NewHandler(HandlerConfig{
		ToolName:     "clear-tool",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "id",
		ClientSecret: "secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if !h.HasToken() {
		t.Error("expected token before clear")
	}

	if err := h.ClearToken(); err != nil {
		t.Fatalf("clear token: %v", err)
	}

	if h.HasToken() {
		t.Error("expected no token after clear")
	}

	got, err := store.Get("clear-tool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Error("expected token deleted from store")
	}
}

func TestHandler_ImplementsOAuthHandler(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:     "interface-test",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "id",
		ClientSecret: "secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	// This is the key compile-time check that our Handler satisfies OAuthHandler.
	var _ auth.OAuthHandler = h
}

// mockRoundTripper returns a fixed status code for any request.
type mockRoundTripper struct {
	status int
}

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: m.status,
		Status:     fmt.Sprintf("%d", m.status),
		Header:     http.Header{},
	}, nil
}

func TestDCRFixRoundTripper_Rewrites200To201(t *testing.T) {
	rt := &oauthRoundTripper{base: &mockRoundTripper{status: http.StatusOK}}

	req, _ := http.NewRequest(http.MethodPost, "https://example.com/register", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}

func TestDCRFixRoundTripper_PassesThrough201(t *testing.T) {
	rt := &oauthRoundTripper{base: &mockRoundTripper{status: http.StatusCreated}}

	req, _ := http.NewRequest(http.MethodPost, "https://example.com/register", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}

func TestDCRFixRoundTripper_IgnoresNonRegisterPaths(t *testing.T) {
	rt := &oauthRoundTripper{base: &mockRoundTripper{status: http.StatusOK}}

	req, _ := http.NewRequest(http.MethodPost, "https://example.com/token", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want %d (should not rewrite non-register paths)", resp.StatusCode, http.StatusOK)
	}
}

func TestDCRFixRoundTripper_IgnoresGETRequests(t *testing.T) {
	rt := &oauthRoundTripper{base: &mockRoundTripper{status: http.StatusOK}}

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/register", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want %d (should not rewrite GET)", resp.StatusCode, http.StatusOK)
	}
}

func TestPersistToken_SavesAllMetadata(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:     "metadata-tool",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "my-client-id",
		ClientSecret: "my-client-secret",
		Scopes:       []string{"read", "write"},
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	tok := &oauth2.Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
	}
	h.persistToken(tok, oauth2.StaticTokenSource(tok))

	got, err := store.Get("metadata-tool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected stored token")
	}
	if got.ClientID != "my-client-id" {
		t.Errorf("client id: got %q, want %q", got.ClientID, "my-client-id")
	}
	if got.ClientSecret != "my-client-secret" {
		t.Errorf("client secret: got %q, want %q", got.ClientSecret, "my-client-secret")
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "read" || got.Scopes[1] != "write" {
		t.Errorf("scopes: got %v, want [read write]", got.Scopes)
	}
}

func TestTokenSourceFromStored_WithRefreshConfig(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:     "refresh-tool",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "id",
		ClientSecret: "secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	expiry := time.Now().Add(time.Hour)
	st := &StoredToken{
		ToolName:     "refresh-tool",
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Expiry:       &expiry,
		TokenURL:     "https://example.com/token",
		ClientID:     "id",
		ClientSecret: "secret",
	}

	ts := h.tokenSourceFromStored(st)
	if ts == nil {
		t.Fatal("expected non-nil token source")
	}

	// Should be able to get the current (non-expired) token without error.
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("getting token: %v", err)
	}
	if tok.AccessToken != "access" {
		t.Errorf("access token: got %q, want %q", tok.AccessToken, "access")
	}
}

func TestTokenSourceFromStored_WithoutTokenURL(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:     "static-tool",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "id",
		ClientSecret: "secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	st := &StoredToken{
		ToolName:     "static-tool",
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		// No TokenURL — should fall back to static.
	}

	ts := h.tokenSourceFromStored(st)
	if ts == nil {
		t.Fatal("expected non-nil token source")
	}

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("getting token: %v", err)
	}
	if tok.AccessToken != "access" {
		t.Errorf("access token: got %q, want %q", tok.AccessToken, "access")
	}
}

func TestHandler_Close_CancelsContext(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:     "close-tool",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "id",
		ClientSecret: "secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	// Context should not be cancelled before Close.
	if h.ctx.Err() != nil {
		t.Error("expected context to not be cancelled before Close")
	}

	h.Close()

	if h.ctx.Err() == nil {
		t.Error("expected context to be cancelled after Close")
	}
}

// --- Round tripper behaviour tests ---

func TestOAuthRoundTripper_CapturesTokenURL(t *testing.T) {
	rt := &oauthRoundTripper{base: &mockRoundTripper{status: http.StatusOK}}

	req, _ := http.NewRequest(http.MethodPost, "https://auth.example.com/oauth/token", nil)
	_, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}

	url, _, _, _ := rt.captured()
	if url != "https://auth.example.com/oauth/token" {
		t.Errorf("captured token URL: got %q", url)
	}
}

func TestOAuthRoundTripper_CapturesBasicAuthCredentials(t *testing.T) {
	rt := &oauthRoundTripper{base: &mockRoundTripper{status: http.StatusOK}}

	req, _ := http.NewRequest(http.MethodPost, "https://auth.example.com/token", nil)
	req.SetBasicAuth("dcr-client-id", "dcr-client-secret")
	_, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}

	_, clientID, clientSecret, authStyle := rt.captured()
	if clientID != "dcr-client-id" {
		t.Errorf("client id: got %q, want %q", clientID, "dcr-client-id")
	}
	if clientSecret != "dcr-client-secret" {
		t.Errorf("client secret: got %q, want %q", clientSecret, "dcr-client-secret")
	}
	if authStyle != oauth2.AuthStyleInHeader {
		t.Errorf("auth style: got %d, want %d (AuthStyleInHeader)", authStyle, oauth2.AuthStyleInHeader)
	}
}

func TestOAuthRoundTripper_DoesNotCaptureFromRegisterPath(t *testing.T) {
	rt := &oauthRoundTripper{base: &mockRoundTripper{status: http.StatusOK}}

	// A POST to /register with "token" in another part should not capture.
	req, _ := http.NewRequest(http.MethodPost, "https://auth.example.com/register", nil)
	_, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}

	url, _, _, _ := rt.captured()
	if url != "" {
		t.Errorf("should not capture token URL from register path, got %q", url)
	}
}

// --- Todoist-specific behaviour tests ---

// TestTodoistBehavior_NonExpiringToken verifies that tokens with no expiry
// (like Todoist's expires_in=0) are handled correctly — they should be
// treated as valid indefinitely and not trigger NeedsReauth.
func TestTodoistBehavior_NonExpiringToken(t *testing.T) {
	store := testStore(t)

	// Simulate a Todoist-like token: no expiry, no refresh token.
	st := &StoredToken{
		ToolName:    "todoist-mcp",
		AccessToken: "test-fake-todoist-access-token-not-real",
		TokenType:   "Bearer",
		// Expiry: nil (Todoist returns expires_in=0 → zero-time → not stored)
		// RefreshToken: "" (Todoist doesn't return one)
	}
	if err := store.Put(st); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Get("todoist-mcp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	summary := got.Summary()
	if !summary.HasToken {
		t.Error("expected has_token=true")
	}
	if summary.NeedsReauth {
		t.Error("non-expiring token should not need reauth")
	}
	if summary.ExpiresAt != nil {
		t.Error("expected nil expiry for non-expiring token")
	}
}

// TestTodoistBehavior_StaticTokenSource verifies that tokens without a
// refresh token or token URL produce a static token source that returns
// the access token as-is (no refresh attempt).
func TestTodoistBehavior_StaticTokenSource(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	// Pre-store a Todoist-like token (no refresh, no expiry).
	if err := store.Put(&StoredToken{
		ToolName:    "todoist-static",
		AccessToken: "todoist-access-token",
		TokenType:   "Bearer",
	}); err != nil {
		t.Fatalf("pre-store: %v", err)
	}

	h, err := NewHandler(HandlerConfig{
		ToolName:    "todoist-static",
		CallbackURL: "http://localhost:8080/callback",
		// No ClientID/ClientSecret — DCR flow.
		Store:   store,
		Pending: pending,
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if !h.HasToken() {
		t.Error("expected cached token after loading stored token")
	}

	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("token source: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil token source")
	}

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("getting token: %v", err)
	}
	if tok.AccessToken != "todoist-access-token" {
		t.Errorf("access token: got %q, want %q", tok.AccessToken, "todoist-access-token")
	}
}

// TestRFCBehavior_RefreshableTokenSource verifies that tokens with a
// refresh token AND token URL produce a refreshable token source that
// can auto-refresh when the access token expires.
func TestRFCBehavior_RefreshableTokenSource(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	// Store a token with full refresh metadata (RFC-compliant provider).
	expiry := time.Now().Add(time.Hour)
	if err := store.Put(&StoredToken{
		ToolName:     "rfc-provider",
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		Expiry:       &expiry,
		ClientID:     "rfc-client-id",
		ClientSecret: "rfc-client-secret",
		TokenURL:     "https://auth.example.com/token",
		AuthStyle:    oauth2.AuthStyleInParams,
	}); err != nil {
		t.Fatalf("pre-store: %v", err)
	}

	h, err := NewHandler(HandlerConfig{
		ToolName:     "rfc-provider",
		CallbackURL:  "http://localhost:8080/callback",
		ClientID:     "rfc-client-id",
		ClientSecret: "rfc-client-secret",
		Store:        store,
		Pending:      pending,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if !h.HasToken() {
		t.Error("expected cached token")
	}

	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("token source: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil token source")
	}

	// The token is still valid, so Token() should return it directly.
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("getting token: %v", err)
	}
	if tok.AccessToken != "access-123" {
		t.Errorf("access token: got %q, want %q", tok.AccessToken, "access-123")
	}
}

// TestPersistToken_DCRCredentialsCaptured verifies that when using DCR
// (no pre-registered client), the client credentials discovered by the
// SDK are captured from the token exchange and persisted.
func TestPersistToken_DCRCredentialsCaptured(t *testing.T) {
	store, pending, logger := testHandlerDeps(t)

	h, err := NewHandler(HandlerConfig{
		ToolName:    "dcr-tool",
		CallbackURL: "http://localhost:8080/callback",
		// No ClientID/ClientSecret — DCR flow.
		Store:   store,
		Pending: pending,
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	// Simulate what the round tripper captures during token exchange.
	h.rt.mu.Lock()
	h.rt.tokenURL = "https://auth.todoist.net/token"
	h.rt.clientID = "dcr-assigned-id"
	h.rt.clientSecret = "dcr-assigned-secret"
	h.rt.authStyle = oauth2.AuthStyleInParams
	h.rt.mu.Unlock()

	tok := &oauth2.Token{
		AccessToken:  "dcr-access-token",
		RefreshToken: "dcr-refresh-token",
		TokenType:    "Bearer",
	}
	h.persistToken(tok, oauth2.StaticTokenSource(tok))

	got, err := store.Get("dcr-tool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected stored token")
	}

	if got.ClientID != "dcr-assigned-id" {
		t.Errorf("client id: got %q, want %q", got.ClientID, "dcr-assigned-id")
	}
	if got.ClientSecret != "dcr-assigned-secret" {
		t.Errorf("client secret: got %q, want %q", got.ClientSecret, "dcr-assigned-secret")
	}
	if got.TokenURL != "https://auth.todoist.net/token" {
		t.Errorf("token url: got %q, want %q", got.TokenURL, "https://auth.todoist.net/token")
	}
	if got.AuthStyle != oauth2.AuthStyleInParams {
		t.Errorf("auth style: got %d, want %d", got.AuthStyle, oauth2.AuthStyleInParams)
	}
}
