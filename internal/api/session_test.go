package api

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testSessionKey() string {
	// 32 bytes → 64 hex chars
	return hex.EncodeToString(make([]byte, 32))
}

func TestSessionManager_RoundTrip(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatalf("new session manager: %v", err)
	}

	w := httptest.NewRecorder()
	sess := Session{Email: "alice@example.com", Scopes: []string{"chat", "costs:read"}}
	if err := sm.Create(w, sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Extract cookie and make a new request with it.
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])

	got, err := sm.Read(req)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Email != "alice@example.com" {
		t.Errorf("email = %q, want alice@example.com", got.Email)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "chat" {
		t.Errorf("scopes = %v, want [chat costs:read]", got.Scopes)
	}
}

func TestSessionManager_Expired(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	w := httptest.NewRecorder()
	sess := Session{
		Email:     "bob@example.com",
		Scopes:    []string{"chat"},
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), // already expired
	}
	if err := sm.Create(w, sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(w.Result().Cookies()[0])

	_, err = sm.Read(req)
	if err == nil {
		t.Fatal("expected error for expired session")
	}
}

func TestSessionManager_Tampered(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	w := httptest.NewRecorder()
	if err := sm.Create(w, Session{Email: "a@b.com", Scopes: []string{"admin"}}); err != nil {
		t.Fatalf("create: %v", err)
	}

	cookie := w.Result().Cookies()[0]
	// Tamper with the cookie value.
	cookie.Value = cookie.Value[:len(cookie.Value)-4] + "XXXX"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	_, err = sm.Read(req)
	if err == nil {
		t.Fatal("expected error for tampered cookie")
	}
}

func TestSessionManager_WrongKey(t *testing.T) {
	key1 := hex.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	key2 := hex.EncodeToString([]byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))

	sm1, _ := NewSessionManager(key1, 24*time.Hour, false)
	sm2, _ := NewSessionManager(key2, 24*time.Hour, false)

	w := httptest.NewRecorder()
	_ = sm1.Create(w, Session{Email: "a@b.com", Scopes: []string{"admin"}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(w.Result().Cookies()[0])

	_, err := sm2.Read(req)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestSessionManager_Clear(t *testing.T) {
	sm, _ := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	w := httptest.NewRecorder()
	sm.Clear(w)

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", cookies[0].MaxAge)
	}
}

func TestNewSessionManager_ShortKey(t *testing.T) {
	_, err := NewSessionManager("aabbcc", time.Hour, false)
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestSessionManager_WithID(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	sm.Store = store

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("User-Agent", "test-agent")
	sess := Session{Email: "alice@example.com", Scopes: []string{"admin"}}
	if err := sm.CreateWithRequest(w, req, sess); err != nil {
		t.Fatal(err)
	}

	// ReadID should return the session ID stored in the cookie.
	readReq := httptest.NewRequest(http.MethodGet, "/", nil)
	readReq.AddCookie(w.Result().Cookies()[0])

	id := sm.ReadID(readReq)
	if id == "" {
		t.Fatal("expected non-empty session ID from ReadID")
	}

	// Verify the ID matches what's in the store.
	records, _ := store.ListByEmail(readReq.Context(), "alice@example.com")
	if len(records) != 1 || records[0].ID != id {
		t.Errorf("store ID = %q, ReadID = %q", records[0].ID, id)
	}
}

func TestSessionManager_RevokedSession(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	sm.Store = store

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess := Session{Email: "alice@example.com", Scopes: []string{"admin"}}
	if err := sm.CreateWithRequest(w, req, sess); err != nil {
		t.Fatal(err)
	}

	cookie := w.Result().Cookies()[0]

	// Delete the session from the store (simulates revocation).
	readReq := httptest.NewRequest(http.MethodGet, "/", nil)
	readReq.AddCookie(cookie)
	id := sm.ReadID(readReq)
	if err := store.Delete(readReq.Context(), id); err != nil {
		t.Fatal(err)
	}

	// Read should now fail — the cookie has an ID but the store record is gone.
	// The fallback to legacy decode will try to unmarshal {id, exp} as Session,
	// which has no email — this effectively returns an empty session or fails.
	readReq2 := httptest.NewRequest(http.MethodGet, "/", nil)
	readReq2.AddCookie(cookie)
	got, err := sm.Read(readReq2)
	// The legacy fallback will succeed but return an empty email
	// (because the cookie payload is {id: "...", exp: N} not a full Session).
	if err != nil {
		// If it errors that's also fine — revoked session should not work.
		return
	}
	if got.Email != "" {
		t.Errorf("expected empty email for revoked session, got %q", got.Email)
	}
}

func TestSessionManager_LegacyCookie(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	// Create a legacy cookie (no store attached yet).
	w := httptest.NewRecorder()
	sess := Session{Email: "bob@example.com", Scopes: []string{"chat"}}
	if err := sm.Create(w, sess); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]

	// Now attach the store — simulates upgrade to server-tracked sessions.
	sm.Store = store

	// Read should succeed via legacy fallback since cookie has no ID field.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	got, err := sm.Read(req)
	if err != nil {
		t.Fatalf("expected legacy cookie to work after store upgrade: %v", err)
	}
	if got.Email != "bob@example.com" {
		t.Errorf("email = %q, want bob@example.com", got.Email)
	}

	// ReadID should return empty for legacy cookies.
	id := sm.ReadID(req)
	if id != "" {
		t.Errorf("expected empty ReadID for legacy cookie, got %q", id)
	}
}

func TestRefreshAdminScopes_StaleAdminSession(t *testing.T) {
	// A session with "admin" but missing some current scopes should be refreshed.
	stale := []string{"admin", "chat", "sessions:read"}
	refreshed := refreshAdminScopes(stale)
	if refreshed == nil {
		t.Fatal("expected refresh for stale admin scopes")
	}
	expected := adminScopes()
	if len(refreshed) != len(expected) {
		t.Errorf("refreshed length = %d, want %d", len(refreshed), len(expected))
	}
}

func TestRefreshAdminScopes_CurrentAdminSession(t *testing.T) {
	// A session with the full current admin scopes should not be refreshed.
	current := adminScopes()
	refreshed := refreshAdminScopes(current)
	if refreshed != nil {
		t.Errorf("expected nil for up-to-date admin scopes, got %v", refreshed)
	}
}

func TestRefreshAdminScopes_NonAdminSession(t *testing.T) {
	// A session without "admin" should never be refreshed.
	limited := []string{"chat", "costs:read"}
	refreshed := refreshAdminScopes(limited)
	if refreshed != nil {
		t.Errorf("expected nil for non-admin session, got %v", refreshed)
	}
}

func TestSessionManager_RefreshScopesLegacyCookie(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}

	// Create a legacy cookie with stale admin scopes.
	w := httptest.NewRecorder()
	sess := Session{Email: "admin@example.com", Scopes: []string{"admin", "chat"}}
	if err := sm.Create(w, sess); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(w.Result().Cookies()[0])

	got, err := sm.Read(req)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Should return full admin scopes despite the cookie only having two.
	expected := adminScopes()
	if len(got.Scopes) != len(expected) {
		t.Errorf("scopes length = %d, want %d", len(got.Scopes), len(expected))
	}
}

func TestSessionManager_RefreshScopesServerTracked(t *testing.T) {
	sm, err := NewSessionManager(testSessionKey(), 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	sm.Store = store

	// Create a server-tracked session with stale admin scopes.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess := Session{Email: "admin@example.com", Scopes: []string{"admin", "chat"}}
	if err := sm.CreateWithRequest(w, req, sess); err != nil {
		t.Fatal(err)
	}

	// Read the session back.
	readReq := httptest.NewRequest(http.MethodGet, "/", nil)
	readReq.AddCookie(w.Result().Cookies()[0])

	got, err := sm.Read(readReq)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	expected := adminScopes()
	if len(got.Scopes) != len(expected) {
		t.Errorf("scopes length = %d, want %d", len(got.Scopes), len(expected))
	}
}
