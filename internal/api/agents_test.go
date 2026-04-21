package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
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

func TestAgentConfigUpdate_LLMProvider(t *testing.T) {
	cfg := testConfig(allScopesKey())

	// Build deps with two registered providers.
	logger := testLogger()
	mem, _ := agent.NewInMemoryStore()
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	perms, _ := security.NewPermissionEngine("supervised")
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&namedMockProvider{
		name:     "mock",
		response: &llm.ChatResponse{Content: "mock reply", TokensUsed: llm.TokenUsage{Total: 1}},
	})
	router.RegisterProvider(&namedMockProvider{
		name:     "other",
		response: &llm.ChatResponse{Content: "other reply", TokensUsed: llm.TokenUsage{Total: 1}},
	})
	approvalStore, _ := approval.NewInMemoryStore()
	approvalMgr := approval.NewManager(approvalStore, logger)
	e := agent.NewEngine("default", router, mem, nil, perms, nil, "test", []skill.Skill{}, nil, approvalMgr, logger)
	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": e},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil, logger,
	)
	deps := Deps{
		Dispatcher:  dispatcher,
		CostTracker: costTracker,
		Memory:      mem,
		Config:      &config.Config{Agents: []config.AgentInstanceConfig{{Name: "default"}}},
	}

	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"llm_provider": "other"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if e.ProviderName() != "other" {
		t.Errorf("provider = %q, want other", e.ProviderName())
	}
}

func TestAgentConfigUpdate_LLMProvider_Unknown(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"llm_provider": "nonexistent"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
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

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
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

// namedMockProvider is a mock LLM provider with a configurable name.
type namedMockProvider struct {
	name     string
	response *llm.ChatResponse
}

func (m *namedMockProvider) Name() string { return m.name }
func (m *namedMockProvider) ChatCompletion(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.response, nil
}
func (m *namedMockProvider) HealthCheck(_ context.Context) error { return nil }

// testDepsWithAgent creates test deps with a named non-default agent alongside default.
func testDepsWithAgent(name string) Deps {
	logger := testLogger()
	mem, _ := agent.NewInMemoryStore()
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)

	mkEngine := func(n string) *agent.Engine {
		perms, _ := security.NewPermissionEngine("supervised")
		router := llm.NewRouter("mock", "test-model", costTracker)
		router.RegisterProvider(&mockProvider{
			response: &llm.ChatResponse{
				Content:      "Hello from mock!",
				TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
				Model:        "test-model",
				FinishReason: "stop",
			},
		})
		approvalStore, _ := approval.NewInMemoryStore()
		approvalMgr := approval.NewManager(approvalStore, logger)
		return agent.NewEngine(n, router, mem, nil, perms, nil, "test", []skill.Skill{}, nil, approvalMgr, logger)
	}

	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": mkEngine("default"), name: mkEngine(name)},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil,
		logger,
	)

	return Deps{
		Dispatcher:  dispatcher,
		Scheduler:   scheduler.New(logger, nil),
		CostTracker: costTracker,
		Memory:      mem,
		Config: &config.Config{
			Agents: []config.AgentInstanceConfig{
				{Name: "default", Adapters: []string{"telegram"}},
				{Name: name},
			},
		},
	}
}

func TestAgentConfigUpdate_Rename(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDepsWithAgent("alice")
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"name": "bob"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/alice", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Old name gone, new name exists.
	if deps.Dispatcher.Agent("alice") != nil {
		t.Error("agent 'alice' should no longer exist")
	}
	e := deps.Dispatcher.Agent("bob")
	if e == nil {
		t.Fatal("agent 'bob' should exist after rename")
	}
	if e.Name() != "bob" {
		t.Errorf("engine name = %q, want 'bob'", e.Name())
	}

	// In-memory config updated.
	found := false
	for _, ac := range deps.Config.Agents {
		if ac.Name == "bob" {
			found = true
		}
		if ac.Name == "alice" {
			t.Error("in-memory config still has 'alice'")
		}
	}
	if !found {
		t.Error("in-memory config missing 'bob'")
	}
}

func TestAgentConfigUpdate_RenameDefault(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	body, _ := json.Marshal(map[string]any{"name": "custom"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestAgentConfigUpdate_RenameDuplicate(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDepsWithAgent("alice")
	srv := New(cfg, deps, testLogger())

	// Try to rename alice to default (which already exists).
	body, _ := json.Marshal(map[string]any{"name": "default"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/alice", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestAgentConfigUpdate_RenameInvalidName(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDepsWithAgent("alice")
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"name": "INVALID NAME!"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/alice", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestAgentConfigUpdate_Fallbacks_Valid(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	rules := []map[string]any{
		{"trigger": "rate_limit", "action": "wait_and_retry", "max_retries": 3, "backoff": "exponential"},
		{"trigger": "error", "action": "switch_provider", "provider": "ollama", "model": "llama3"},
	}
	body, _ := json.Marshal(map[string]any{"fallbacks": rules})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify in-memory config updated.
	got := deps.Config.Agents[0].Fallbacks
	if len(got) != 2 {
		t.Fatalf("fallbacks len = %d, want 2", len(got))
	}
	if got[0].Trigger != "rate_limit" || got[0].Action != "wait_and_retry" || got[0].MaxRetries != 3 {
		t.Errorf("fallback[0] = %+v, unexpected", got[0])
	}
	if got[1].Trigger != "error" || got[1].Provider != "ollama" {
		t.Errorf("fallback[1] = %+v, unexpected", got[1])
	}
}

func TestAgentConfigUpdate_Fallbacks_InvalidTrigger(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rules := []map[string]any{
		{"trigger": "invalid", "action": "wait_and_retry", "max_retries": 1},
	}
	body, _ := json.Marshal(map[string]any{"fallbacks": rules})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAgentConfigUpdate_Fallbacks_MissingProvider(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rules := []map[string]any{
		{"trigger": "error", "action": "switch_provider"},
	}
	body, _ := json.Marshal(map[string]any{"fallbacks": rules})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAgentConfigUpdate_Fallbacks_EmptyClears(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.Agents[0].Fallbacks = []config.FallbackConfig{
		{Trigger: "error", Action: "wait_and_retry", MaxRetries: 1},
	}
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"fallbacks": []any{}})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if len(deps.Config.Agents[0].Fallbacks) != 0 {
		t.Errorf("fallbacks should be empty, got %d", len(deps.Config.Agents[0].Fallbacks))
	}
}

func TestAgentDetail_IncludesFallbacks(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.Agents[0].Fallbacks = []config.FallbackConfig{
		{Trigger: "rate_limit", Action: "wait_and_retry", MaxRetries: 3, Backoff: "exponential"},
	}
	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/agents/default")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fb, ok := resp["fallbacks"].([]any)
	if !ok {
		t.Fatalf("fallbacks field missing or wrong type: %v", resp["fallbacks"])
	}
	if len(fb) != 1 {
		t.Errorf("fallbacks len = %d, want 1", len(fb))
	}
}

func TestAgentDetail_FallbacksEmptyNotNull(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/agents/default")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	fb, ok := resp["fallbacks"].([]any)
	if !ok {
		t.Fatalf("fallbacks should be an array, got %T", resp["fallbacks"])
	}
	if len(fb) != 0 {
		t.Errorf("fallbacks should be empty, got %d", len(fb))
	}
}
