package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
)

func testKeyStore(t *testing.T) *KeyStore {
	t.Helper()
	ks, err := NewInMemoryKeyStore()
	if err != nil {
		t.Fatalf("NewInMemoryKeyStore: %v", err)
	}
	return ks
}

func testAdminConfig() config.APIConfig {
	return testConfig(config.APIKeyConfig{
		Name:   "admin-key",
		Key:    "admin-token",
		Scopes: []string{"admin"},
	})
}

// ---------------------------------------------------------------------------
// KeyStore unit tests
// ---------------------------------------------------------------------------

func TestKeyStore_Create(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, plaintext, err := ks.Create(ctx, "test-key", []string{"chat", "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.ID == "" {
		t.Error("expected non-empty ID")
	}
	if rec.Name != "test-key" {
		t.Errorf("name = %q, want %q", rec.Name, "test-key")
	}
	if len(rec.Scopes) != 2 {
		t.Errorf("scopes len = %d, want 2", len(rec.Scopes))
	}
	if !strings.HasPrefix(plaintext, "dk_") {
		t.Errorf("key %q does not start with 'dk_'", plaintext)
	}
	if len(plaintext) < 20 {
		t.Errorf("key %q is suspiciously short", plaintext)
	}
	if rec.Revoked {
		t.Error("newly created key should not be revoked")
	}
}

func TestKeyStore_List(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	_, _, _ = ks.Create(ctx, "key-a", []string{"chat"})
	_, _, _ = ks.Create(ctx, "key-b", []string{"admin"})

	recs, err := ks.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("List len = %d, want 2", len(recs))
	}
}

func TestKeyStore_FindActiveByHash(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	_, plaintext, _ := ks.Create(ctx, "findme", []string{"chat"})

	sk, err := ks.FindActiveByHash(ctx, hashToken(plaintext))
	if err != nil {
		t.Fatalf("FindActiveByHash error: %v", err)
	}
	if sk == nil {
		t.Fatal("expected to find key, got nil")
	}
	if sk.Name != "findme" {
		t.Errorf("name = %q, want %q", sk.Name, "findme")
	}
}

func TestKeyStore_FindActiveByHash_NotFound(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	sk, err := ks.FindActiveByHash(ctx, hashToken("dk_nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sk != nil {
		t.Errorf("expected nil, got %+v", sk)
	}
}

func TestKeyStore_Revoke(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, plaintext, _ := ks.Create(ctx, "revokeme", []string{"chat"})

	if err := ks.Revoke(ctx, rec.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Should not be findable anymore.
	sk, _ := ks.FindActiveByHash(ctx, hashToken(plaintext))
	if sk != nil {
		t.Error("revoked key should not be found by FindActiveByHash")
	}

	// Should appear as revoked in List.
	recs, _ := ks.List(ctx)
	if len(recs) == 0 || !recs[0].Revoked {
		t.Error("revoked key should appear as revoked in List")
	}
}

func TestKeyStore_Revoke_AlreadyRevoked(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, _, _ := ks.Create(ctx, "double-revoke", []string{"chat"})
	_ = ks.Revoke(ctx, rec.ID)

	if err := ks.Revoke(ctx, rec.ID); err == nil {
		t.Error("expected error revoking an already-revoked key")
	}
}

func TestKeyStore_Rotate(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, oldPlaintext, _ := ks.Create(ctx, "rotateme", []string{"chat"})

	newRec, newPlaintext, err := ks.Rotate(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newRec.ID == rec.ID {
		t.Error("rotated key should have a new ID")
	}
	if newPlaintext == oldPlaintext {
		t.Error("rotated key plaintext should differ from old key")
	}
	if newRec.Name != "rotateme" {
		t.Errorf("rotated key name = %q, want %q", newRec.Name, "rotateme")
	}

	// Old key should be revoked.
	sk, _ := ks.FindActiveByHash(ctx, hashToken(oldPlaintext))
	if sk != nil {
		t.Error("old key should be revoked after rotation")
	}

	// New key should be active.
	sk, _ = ks.FindActiveByHash(ctx, hashToken(newPlaintext))
	if sk == nil {
		t.Error("new key should be active after rotation")
	}
}

func TestKeyStore_TouchLastUsed(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, _, _ := ks.Create(ctx, "touch", []string{"chat"})
	ks.TouchLastUsed(ctx, rec.ID)

	recs, _ := ks.List(ctx)
	if len(recs) == 0 {
		t.Fatal("expected at least one key")
	}
	if recs[0].LastUsedAt == nil {
		t.Error("expected last_used_at to be set after TouchLastUsed")
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	h1 := hashToken("dk_somekey")
	h2 := hashToken("dk_somekey")
	if h1 != h2 {
		t.Errorf("hashToken not deterministic: %q != %q", h1, h2)
	}
}

// ---------------------------------------------------------------------------
// HTTP handler tests for API key endpoints
// ---------------------------------------------------------------------------

func TestHandleListKeys_Empty(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	req := httptest.NewRequest("GET", "/api/v1/keys", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body []any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected empty list, got %d items", len(body))
	}
}

func TestHandleCreateKey(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	body := `{"name":"mykey","scopes":["chat"]}`
	req := httptest.NewRequest("POST", "/api/v1/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key, _ := resp["key"].(string)
	if !strings.HasPrefix(key, "dk_") {
		t.Errorf("key %q does not start with dk_", key)
	}
}

func TestHandleCreateKey_MissingName(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	body := `{"scopes":["chat"]}`
	req := httptest.NewRequest("POST", "/api/v1/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleRevokeKey(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	ctx := context.Background()
	rec, _, _ := ks.Create(ctx, "todelete", []string{"chat"})

	req := httptest.NewRequest("DELETE", "/api/v1/keys/"+rec.ID, nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestHandleRevokeKey_NotFound(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	req := httptest.NewRequest("DELETE", "/api/v1/keys/nonexistent-id", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleRotateKey(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	ctx := context.Background()
	rec, _, _ := ks.Create(ctx, "torotate", []string{"chat"})

	req := httptest.NewRequest("POST", "/api/v1/keys/"+rec.ID+"/rotate", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	newKey, _ := resp["key"].(string)
	if !strings.HasPrefix(newKey, "dk_") {
		t.Errorf("rotated key %q does not start with dk_", newKey)
	}
}

func TestAuthenticate_SQLiteKey(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	// No TOML keys — only SQLite keys should work.
	srv := New(testConfig(), deps, testLogger())

	ctx := context.Background()
	_, plaintext, _ := ks.Create(ctx, "sqlite-key", []string{"chat"})

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	_, ok := srv.authenticate(req.Context(), req, "chat")
	if !ok {
		t.Error("SQLite key should authenticate successfully")
	}
}

func TestAuthenticate_RevokedSQLiteKey(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testConfig(), deps, testLogger())

	ctx := context.Background()
	rec, plaintext, _ := ks.Create(ctx, "revoke-test", []string{"chat"})
	_ = ks.Revoke(ctx, rec.ID)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	_, ok := srv.authenticate(req.Context(), req, "chat")
	if ok {
		t.Error("revoked SQLite key should not authenticate")
	}
}

func TestAuthenticate_TOMLKeyFallback(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	cfg := testConfig(config.APIKeyConfig{Name: "toml-key", Key: "toml-token", Scopes: []string{"admin"}})
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer toml-token")
	_, ok := srv.authenticate(req.Context(), req, "admin")
	if !ok {
		t.Error("TOML key should authenticate when SQLite key not found")
	}
}

func TestHandleKeys_NoKeyStore_Returns503(t *testing.T) {
	deps := testDeps()
	// KeyStore is nil.
	cfg := testConfig(config.APIKeyConfig{Name: "admin", Key: "admin-token", Scopes: []string{"admin"}})
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest("GET", "/api/v1/keys", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestAuthenticate_UpdatesLastUsedAt(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testConfig(), deps, testLogger())

	ctx := context.Background()
	rec, plaintext, _ := ks.Create(ctx, "usage-track", []string{"chat"})

	// Verify last_used_at is nil before any auth.
	recs, _ := ks.List(ctx)
	if len(recs) == 0 || recs[0].LastUsedAt != nil {
		t.Error("last_used_at should be nil before any auth")
	}

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	srv.authenticate(req.Context(), req, "chat")

	// Allow the goroutine to complete.
	time.Sleep(50 * time.Millisecond)

	recs, _ = ks.List(ctx)
	for _, r := range recs {
		if r.ID == rec.ID && r.LastUsedAt != nil {
			return // success
		}
	}
	t.Error("last_used_at should be set after successful auth")
}
