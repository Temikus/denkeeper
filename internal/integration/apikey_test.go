//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// createTestKey creates an API key via POST /api/v1/keys using the harness's
// bootstrap admin token and returns its (id, plaintext key).
func createTestKey(t *testing.T, h *Harness, name string, scopes []string) (string, string) {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/keys", map[string]any{
		"name":   name,
		"scopes": scopes,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create key %q: status = %d, body = %s", name, rec.Code, rec.Body.String())
	}
	var body struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	DecodeJSON(t, rec, &body)
	if body.ID == "" || body.Key == "" {
		t.Fatalf("create key %q: missing id or key in response: %+v", name, body)
	}
	return body.ID, body.Key
}

func TestAPIKey_CreateAndUse(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{WithKeyStore: true})

	_, key := createTestKey(t, h, "create-and-use", []string{"admin"})

	// The new key authenticates an admin-scoped request via the SQLite KeyStore.
	rec := h.Do(h.BearerRequest(http.MethodGet, "/api/v1/agents", nil, key))
	if rec.Code != http.StatusOK {
		t.Errorf("agents with new key: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKey_Rotate(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{WithKeyStore: true})

	id, oldKey := createTestKey(t, h, "rotate-me", []string{"admin"})

	// Rotate.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/keys/"+id+"/rotate", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rotated struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	DecodeJSON(t, rec, &rotated)
	if rotated.Key == "" || rotated.Key == oldKey {
		t.Fatalf("rotate: new key empty or unchanged (old=%q new=%q)", oldKey, rotated.Key)
	}

	// Old key no longer authenticates.
	rec = h.Do(h.BearerRequest(http.MethodGet, "/api/v1/agents", nil, oldKey))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("old key: status = %d, want %d; body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	// New key works.
	rec = h.Do(h.BearerRequest(http.MethodGet, "/api/v1/agents", nil, rotated.Key))
	if rec.Code != http.StatusOK {
		t.Errorf("new key: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKey_Revoke(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{WithKeyStore: true})

	id, key := createTestKey(t, h, "revoke-me", []string{"admin"})

	// Revoke.
	rec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/keys/"+id, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Revoked key is rejected.
	rec = h.Do(h.BearerRequest(http.MethodGet, "/api/v1/agents", nil, key))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked key: status = %d, want %d; body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}
