package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func TestAgentConfigUpdate_SessionTier(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"session_tier": "autonomous"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify tier changed on the engine.
	e := deps.Dispatcher.Agent("default")
	if e.PermissionTier() != "autonomous" {
		t.Errorf("permission tier = %q, want autonomous", e.PermissionTier())
	}
}

func TestAgentConfigUpdate_LLMModel(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"llm_model": "new-model-v2"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	e := deps.Dispatcher.Agent("default")
	if e.ModelName() != "new-model-v2" {
		t.Errorf("model = %q, want new-model-v2", e.ModelName())
	}
}

func TestAgentConfigUpdate_InvalidTier(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	body, _ := json.Marshal(map[string]any{"session_tier": "superuser"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAgentConfigUpdate_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	body, _ := json.Marshal(map[string]any{"session_tier": "autonomous"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/nonexistent", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAgentConfigUpdate_WrongScope(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name:   "readonly",
		Key:    "dk-test-key",
		Scopes: []string{"agents:read"},
	})
	srv := New(cfg, testDeps(), testLogger())

	body, _ := json.Marshal(map[string]any{"session_tier": "autonomous"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAgentConfigUpdate_BrowserURLAllowlist(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"browser_url_allowlist": []string{"example.com", "*.trusted.io"}})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify in-memory config updated.
	got := deps.Config.Agents[0].BrowserURLAllowlist
	if len(got) != 2 || got[0] != "example.com" || got[1] != "*.trusted.io" {
		t.Errorf("browser_url_allowlist = %v, want [example.com *.trusted.io]", got)
	}
}

func TestAgentConfigUpdate_Description(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"description": "Updated description"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify in-memory config updated.
	if deps.Config.Agents[0].Description != "Updated description" {
		t.Errorf("description = %q, want 'Updated description'", deps.Config.Agents[0].Description)
	}
}
