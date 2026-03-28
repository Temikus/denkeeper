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

func TestKeyStore_Delete(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, _, _ := ks.Create(ctx, "deleteme", []string{"chat"})
	_ = ks.Revoke(ctx, rec.ID)

	if err := ks.Delete(ctx, rec.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should be completely gone from List.
	recs, _ := ks.List(ctx)
	for _, r := range recs {
		if r.ID == rec.ID {
			t.Error("deleted key should not appear in List")
		}
	}
}

func TestKeyStore_Delete_ActiveKey(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, _, _ := ks.Create(ctx, "still-active", []string{"chat"})

	if err := ks.Delete(ctx, rec.ID); err == nil {
		t.Error("expected error deleting an active (non-revoked) key")
	}
}

func TestKeyStore_Delete_NotFound(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	if err := ks.Delete(ctx, "nonexistent"); err == nil {
		t.Error("expected error deleting a nonexistent key")
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

func TestHandleDeleteKey(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	ctx := context.Background()
	rec, _, _ := ks.Create(ctx, "todelete", []string{"chat"})
	_ = ks.Revoke(ctx, rec.ID) // must be revoked first

	req := httptest.NewRequest("DELETE", "/api/v1/keys/"+rec.ID+"/permanent", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	// Verify it's gone from the list.
	keys, _ := ks.List(ctx)
	for _, k := range keys {
		if k.ID == rec.ID {
			t.Fatal("deleted key still present in list")
		}
	}
}

func TestHandleDeleteKey_ActiveKeyRejected(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	ctx := context.Background()
	rec, _, _ := ks.Create(ctx, "active", []string{"chat"})

	req := httptest.NewRequest("DELETE", "/api/v1/keys/"+rec.ID+"/permanent", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for active key", w.Code)
	}
}

func TestHandleDeleteKey_NotFound(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	req := httptest.NewRequest("DELETE", "/api/v1/keys/nonexistent/permanent", nil)
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

// ---------------------------------------------------------------------------
// KeyStore.HasActiveKey
// ---------------------------------------------------------------------------

func TestKeyStore_HasActiveKey_Empty(t *testing.T) {
	ks := testKeyStore(t)
	has, err := ks.HasActiveKey(context.Background())
	if err != nil {
		t.Fatalf("HasActiveKey: %v", err)
	}
	if has {
		t.Error("expected false on empty store")
	}
}

func TestKeyStore_HasActiveKey_WithActiveKey(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	_, _, _ = ks.Create(ctx, "active", []string{"admin"})
	has, err := ks.HasActiveKey(ctx)
	if err != nil {
		t.Fatalf("HasActiveKey: %v", err)
	}
	if !has {
		t.Error("expected true with one active key")
	}
}

func TestKeyStore_HasActiveKey_OnlyRevoked(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()

	rec, _, _ := ks.Create(ctx, "revokeme", []string{"admin"})
	_ = ks.Revoke(ctx, rec.ID)

	has, err := ks.HasActiveKey(ctx)
	if err != nil {
		t.Fatalf("HasActiveKey: %v", err)
	}
	if has {
		t.Error("expected false when all keys are revoked")
	}
}

// ---------------------------------------------------------------------------
// Setup endpoint handlers
// ---------------------------------------------------------------------------

func testSetupServer(t *testing.T, ks *KeyStore, tomlKeys ...config.APIKeyConfig) *Server {
	t.Helper()
	deps := testDeps()
	deps.KeyStore = ks
	return New(testConfig(tomlKeys...), deps, testLogger())
}

func TestHandleSetupStatus_SetupRequired(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks)

	req := httptest.NewRequest("GET", "/api/v1/setup", nil)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body["setup_required"] {
		t.Error("expected setup_required=true when no keys exist")
	}
}

func TestHandleSetupStatus_NotRequired_SQLiteKey(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()
	_, _, _ = ks.Create(ctx, "existing", []string{"admin"})
	srv := testSetupServer(t, ks)

	req := httptest.NewRequest("GET", "/api/v1/setup", nil)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]bool
	json.NewDecoder(w.Body).Decode(&body) //nolint:errcheck
	if body["setup_required"] {
		t.Error("expected setup_required=false when SQLite key exists")
	}
}

func TestHandleSetupStatus_NotRequired_TOMLKey(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks, config.APIKeyConfig{Name: "toml", Key: "toml-secret", Scopes: []string{"admin"}})

	req := httptest.NewRequest("GET", "/api/v1/setup", nil)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]bool
	json.NewDecoder(w.Body).Decode(&body) //nolint:errcheck
	if body["setup_required"] {
		t.Error("expected setup_required=false when TOML key exists")
	}
}

func TestHandleSetupInit_CreatesKey(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks)

	body := `{"name":"myadmin","scopes":["admin","chat"]}`
	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(body))
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
		t.Errorf("key %q should start with dk_", key)
	}
	if resp["name"] != "myadmin" {
		t.Errorf("name = %v, want myadmin", resp["name"])
	}
}

func TestHandleSetupInit_DefaultsWhenBodyEmpty(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks)

	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if resp["name"] != "admin" {
		t.Errorf("name = %v, want admin (default)", resp["name"])
	}
}

func TestHandleSetupInit_ConflictWhenKeyExists(t *testing.T) {
	ks := testKeyStore(t)
	ctx := context.Background()
	_, _, _ = ks.Create(ctx, "existing", []string{"admin"})
	srv := testSetupServer(t, ks)

	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{"name":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestHandleSetupInit_InvalidScope(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks)

	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{"name":"admin","scopes":["notascope"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown scope", w.Code)
	}
}

func TestHandleSetupInit_NameTooLong(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks)

	longName := strings.Repeat("a", maxKeyNameLen+1)
	body := `{"name":"` + longName + `","scopes":["admin"]}`
	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for oversized name", w.Code)
	}
}

func TestHandleSetupInit_Concurrent_OnlyOneSucceeds(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks)

	const goroutines = 10
	results := make([]int, goroutines)
	done := make(chan struct{})

	for i := range goroutines {
		go func(i int) {
			req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{"name":"admin","scopes":["admin"]}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(w, req)
			results[i] = w.Code
			done <- struct{}{}
		}(i)
	}
	for range goroutines {
		<-done
	}

	created := 0
	for _, code := range results {
		if code == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Errorf("expected exactly 1 successful setup, got %d (codes: %v)", created, results)
	}
}

func TestHandleCreateKey_InvalidScope(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	body := `{"name":"mykey","scopes":["notascope"]}`
	req := httptest.NewRequest("POST", "/api/v1/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown scope", w.Code)
	}
}

func TestHandleCreateKey_NameTooLong(t *testing.T) {
	ks := testKeyStore(t)
	deps := testDeps()
	deps.KeyStore = ks
	srv := New(testAdminConfig(), deps, testLogger())

	longName := strings.Repeat("a", maxKeyNameLen+1)
	body := `{"name":"` + longName + `","scopes":["admin"]}`
	req := httptest.NewRequest("POST", "/api/v1/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for oversized name", w.Code)
	}
}

func TestValidateKeyInput(t *testing.T) {
	if err := ValidateKeyInput("admin", []string{"admin", "chat"}); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}
	if err := ValidateKeyInput("admin", []string{"notascope"}); err == nil {
		t.Error("expected error for unknown scope")
	}
	if err := ValidateKeyInput(strings.Repeat("x", maxKeyNameLen+1), []string{"admin"}); err == nil {
		t.Error("expected error for name exceeding max length")
	}
	if err := ValidateKeyInput(strings.Repeat("x", maxKeyNameLen), []string{"admin"}); err != nil {
		t.Errorf("name exactly at limit should be valid: %v", err)
	}
}

func TestHandleSetupInit_NoKeyStore_Returns503(t *testing.T) {
	deps := testDeps()
	// KeyStore is nil.
	srv := New(testConfig(), deps, testLogger())

	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestHandleSetupStatus_NoKeyStore_Returns503(t *testing.T) {
	deps := testDeps()
	// KeyStore is nil.
	srv := New(testConfig(), deps, testLogger())

	req := httptest.NewRequest("GET", "/api/v1/setup", nil)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestHandleSetupInit_InvalidJSON(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks)

	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed JSON", w.Code)
	}
}

func TestHandleSetupInit_ConflictWhenTOMLKeyExists(t *testing.T) {
	ks := testKeyStore(t)
	srv := testSetupServer(t, ks, config.APIKeyConfig{Name: "toml", Key: "toml-secret", Scopes: []string{"admin"}})

	req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{"name":"admin","scopes":["admin"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 when TOML key satisfies setup", w.Code)
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
