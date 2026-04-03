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
	sess := Session{Email: "alice@example.com", Scopes: []string{"admin", "chat"}}
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
	if len(got.Scopes) != 2 || got.Scopes[0] != "admin" {
		t.Errorf("scopes = %v, want [admin chat]", got.Scopes)
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
