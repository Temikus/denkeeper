//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuth_NoToken_Returns401(t *testing.T) {
	h := NewHarness(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := h.Do(req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuth_InvalidToken_Returns401(t *testing.T) {
	h := NewHarness(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer dk-invalid-key")
	rec := h.Do(req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuth_ChatScopeCanChat(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"chat"},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "hello",
	}))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestAuth_ChatScopeCannotListAgents(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"chat"},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/agents", nil))

	// The API returns 401 for both missing auth and insufficient scope.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuth_ReadOnlyScopeCannotChat(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"sessions:read", "agents:read"},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "hello",
	}))

	// The API returns 401 for both missing auth and insufficient scope.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuth_ReadOnlyScopeCanListSessions(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"sessions:read"},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/sessions", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAuth_HealthRequiresNoAuth(t *testing.T) {
	h := NewHarness(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
