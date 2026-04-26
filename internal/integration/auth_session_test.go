//go:build integration

package integration

import (
	"net/http"
	"testing"
)

func TestAuth_PasswordLogin(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "secretpw"),
	})

	rec := h.Do(h.CookieRequest(http.MethodPost, "/auth/login",
		map[string]string{"password": "secretpw"}, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	DecodeJSON(t, rec, &body)
	if body["authenticated"] != true {
		t.Errorf("authenticated = %v, want true", body["authenticated"])
	}
	if body["email"] != "admin" {
		t.Errorf("email = %v, want admin", body["email"])
	}

	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "dk_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("dk_session cookie not set")
	}
	if sessionCookie.Value == "" {
		t.Error("dk_session cookie value is empty")
	}
}

func TestAuth_PasswordLoginBadPassword(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "secretpw"),
	})

	rec := h.Do(h.CookieRequest(http.MethodPost, "/auth/login",
		map[string]string{"password": "wrong"}, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == "dk_session" {
			t.Errorf("dk_session cookie should not be set on failure, got value %q", c.Value)
		}
	}
}

func TestAuth_SessionList(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "secretpw"),
	})

	cookie := h.SessionLogin(t, "secretpw")

	rec := h.Do(h.CookieRequest(http.MethodGet, "/api/v1/auth/sessions", nil, cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Sessions         []map[string]any `json:"sessions"`
		CurrentSessionID string           `json:"current_session_id"`
	}
	DecodeJSON(t, rec, &body)

	if len(body.Sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(body.Sessions))
	}
	if body.CurrentSessionID == "" {
		t.Error("current_session_id is empty")
	}
	if got, _ := body.Sessions[0]["id"].(string); got != body.CurrentSessionID {
		t.Errorf("session id = %q, want current_session_id = %q", got, body.CurrentSessionID)
	}
}

func TestAuth_SessionRevoke(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "secretpw"),
	})

	cookie := h.SessionLogin(t, "secretpw")

	// List to capture the session ID.
	rec := h.Do(h.CookieRequest(http.MethodGet, "/api/v1/auth/sessions", nil, cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Sessions         []map[string]any `json:"sessions"`
		CurrentSessionID string           `json:"current_session_id"`
	}
	DecodeJSON(t, rec, &listed)
	if len(listed.Sessions) != 1 {
		t.Fatalf("pre-revoke len(sessions) = %d, want 1", len(listed.Sessions))
	}
	id := listed.CurrentSessionID

	// Revoke the session.
	rec = h.Do(h.CookieRequest(http.MethodDelete, "/api/v1/auth/sessions/"+id, nil, cookie))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// After revoke, the cookie can still be decrypted but its session ID is
	// gone from SQLite. Read() falls back to legacy decryption and yields a
	// session with empty Email/Scopes, so RequireScope("admin", ...) blocks
	// the call with 403.
	rec = h.Do(h.CookieRequest(http.MethodGet, "/api/v1/auth/sessions", nil, cookie))
	if rec.Code != http.StatusForbidden {
		t.Errorf("post-revoke list: status = %d, want %d; body = %s",
			rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestAuth_PasswordChange(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "oldpw1234"),
	})

	// Change the password using the bootstrap admin bearer token.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auth/password", map[string]string{
		"current_password": "oldpw1234",
		"new_password":     "newpw1234",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("change: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var changed map[string]bool
	DecodeJSON(t, rec, &changed)
	if !changed["ok"] {
		t.Errorf("ok = %v, want true", changed["ok"])
	}

	// Old password no longer accepted.
	rec = h.Do(h.CookieRequest(http.MethodPost, "/auth/login",
		map[string]string{"password": "oldpw1234"}, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("old-pw login: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// New password accepted.
	rec = h.Do(h.CookieRequest(http.MethodPost, "/auth/login",
		map[string]string{"password": "newpw1234"}, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("new-pw login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
