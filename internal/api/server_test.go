package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/browser"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
)

// mockProvider implements llm.Provider for testing.
type mockProvider struct {
	response *llm.ChatResponse
	err      error
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) ChatCompletion(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.response, m.err
}
func (m *mockProvider) HealthCheck(_ context.Context) error { return nil }

// sequentialProvider returns responses in order, one per call.
type sequentialProvider struct {
	responses []*llm.ChatResponse
	callIndex int
}

func (s *sequentialProvider) Name() string { return "mock" }
func (s *sequentialProvider) ChatCompletion(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if s.callIndex >= len(s.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", s.callIndex)
	}
	resp := s.responses[s.callIndex]
	s.callIndex++
	return resp, nil
}
func (s *sequentialProvider) HealthCheck(_ context.Context) error { return nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func boolPtr(b bool) *bool { return &b }

func testConfig(keys ...config.APIKeyConfig) config.APIConfig {
	return config.APIConfig{
		Enabled: boolPtr(true),
		Listen:  ":0",
		Keys:    keys,
	}
}

// testDeps builds a minimal Deps with real components suitable for testing.
func testDeps() Deps {
	logger := testLogger()
	mem, _ := agent.NewInMemoryStore()
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)

	// Build a minimal "default" engine with a mock LLM provider.
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

	e := agent.NewEngine("default", router, mem, nil, perms, nil, "test", []skill.Skill{
		{Name: "greet", Description: "Greeting skill", Version: "1.0", Triggers: []string{"command:hello"}},
		{Name: "help", Description: "Help system"},
	}, nil, approvalMgr, logger)

	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": e},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil,
		logger,
	)

	sched := scheduler.New(logger, nil)

	return Deps{
		Dispatcher:  dispatcher,
		Scheduler:   sched,
		CostTracker: costTracker,
		Memory:      mem,
		Approvals:   approvalMgr,
		Config: &config.Config{
			Agents: []config.AgentInstanceConfig{
				{Name: "default", Adapters: []string{"telegram"}},
			},
		},
	}
}

// authedRequest creates a request with a valid Bearer token.
func authedRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	return req
}

// allScopesKey returns an API key with all scopes for testing.
func allScopesKey() config.APIKeyConfig {
	return config.APIKeyConfig{
		Name: "test",
		Key:  "dk-test-key",
		Scopes: []string{
			"health", "admin", "chat",
			"sessions:read", "sessions:write", "costs:read",
			"agents:read", "agents:write",
			"skills:read", "skills:write",
			"schedules:read", "schedules:write",
			"approvals:read", "approvals:write",
			"tools:read", "tools:write",
			"browser:read", "browser:write",
			"kv:read", "kv:write",
			"channels:read", "channels:write", "audit:read",
		},
	}
}

// ---------------------------------------------------------------------------
// Health endpoint
// ---------------------------------------------------------------------------

func TestHealth_ReturnsOK(t *testing.T) {
	srv := New(testConfig(), testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if body == "" || body == "{}" {
		t.Error("expected non-empty JSON response")
	}
}

// logRecord captures slog output for assertions.
type logRecord struct {
	Level   slog.Level
	Message string
}

// capturingHandler is a slog.Handler that stores records in a slice.
type capturingHandler struct {
	records *[]logRecord
}

func (h *capturingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, logRecord{Level: r.Level, Message: r.Message})
	return nil
}
func (h *capturingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(_ string) slog.Handler      { return h }

func TestHealth_LoggedAtDebugLevel(t *testing.T) {
	var records []logRecord
	logger := slog.New(&capturingHandler{records: &records})
	srv := New(testConfig(), testDeps(), logger)

	// Reset records so we only capture logs from the HTTP handler, not from New().
	records = records[:0]

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := srv.middlewareLogging(mux)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(records))
	}
	if records[0].Level != slog.LevelDebug {
		t.Errorf("health log level = %v, want %v", records[0].Level, slog.LevelDebug)
	}
}

func TestNonHealthEndpoint_LoggedAtInfoLevel(t *testing.T) {
	var records []logRecord
	logger := slog.New(&capturingHandler{records: &records})
	srv := New(testConfig(), testDeps(), logger)

	// Reset records so we only capture logs from the HTTP handler, not from New().
	records = records[:0]

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agents", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := srv.middlewareLogging(mux)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil))

	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(records))
	}
	if records[0].Level != slog.LevelInfo {
		t.Errorf("non-health log level = %v, want %v", records[0].Level, slog.LevelInfo)
	}
}

// ---------------------------------------------------------------------------
// Auth middleware
// ---------------------------------------------------------------------------

func TestRequireScope_NoAuthHeader(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testDeps(), testLogger())

	handler := srv.RequireScope("health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_InvalidKey(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testDeps(), testLogger())

	handler := srv.RequireScope("health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-wrong-key")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	// X-Auth-Failure tells the web client that the credential itself is bad
	// (vs. a 401 from inside a handler), so it can clear the token and
	// bounce to login. Missing the header would defeat the bug fix.
	if got := rec.Header().Get("X-Auth-Failure"); got != "credential-invalid" {
		t.Errorf("X-Auth-Failure = %q, want %q", got, "credential-invalid")
	}
}

func TestRequireScope_ValidKeyWrongScope_NoAuthFailureHeader(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testDeps(), testLogger())

	handler := srv.RequireScope("chat", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-secret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	// 403 (insufficient scope) must NOT carry X-Auth-Failure — the
	// credential is fine, just lacks permission.
	if got := rec.Header().Get("X-Auth-Failure"); got != "" {
		t.Errorf("X-Auth-Failure on 403 = %q, want empty", got)
	}
}

func TestRequireScope_ValidKeyWrongScope(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testDeps(), testLogger())

	handler := srv.RequireScope("chat", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-secret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequireScope_ValidKeyValidScope(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health", "chat"},
	})
	srv := New(cfg, testDeps(), testLogger())

	handler := srv.RequireScope("chat", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-secret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequireScope_ContextContainsKeyName(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "my-key", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testDeps(), testLogger())

	var gotName string
	handler := srv.RequireScope("health", func(w http.ResponseWriter, r *http.Request) {
		gotName, _ = r.Context().Value(keyNameKey).(string)
		writeJSON(w, http.StatusOK, nil)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-secret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if gotName != "my-key" {
		t.Errorf("context key name = %q, want my-key", gotName)
	}
}

// ---------------------------------------------------------------------------
// CORS
// ---------------------------------------------------------------------------

func TestCORS_OriginAllowed(t *testing.T) {
	cfg := testConfig()
	cfg.CORSOrigins = []string{"https://dashboard.example.com"}
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.example.com" {
		t.Errorf("CORS origin = %q, want https://dashboard.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_OriginNotAllowed(t *testing.T) {
	cfg := testConfig()
	cfg.CORSOrigins = []string{"https://dashboard.example.com"}
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS should not set header for disallowed origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_Preflight(t *testing.T) {
	cfg := testConfig()
	cfg.CORSOrigins = []string{"https://dashboard.example.com"}
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestCORS_NoneConfigured(t *testing.T) {
	srv := New(testConfig(), testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS header should not be set when no origins configured")
	}
}

// ---------------------------------------------------------------------------
// Rate limiting
// ---------------------------------------------------------------------------

func TestRateLimit_Enforced(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "limited", Key: "dk-limited", Scopes: []string{"health"},
	})
	cfg.RateLimit = 1.0 // 1 request per second
	srv := New(cfg, testDeps(), testLogger())

	handler := srv.RequireScope("health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	// First request should succeed (bucket starts full).
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.Header.Set("Authorization", "Bearer dk-limited")
	rec1 := httptest.NewRecorder()
	handler(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	// Second request immediately should be rate limited.
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Bearer dk-limited")
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want %d", rec2.Code, http.StatusTooManyRequests)
	}
}

// ---------------------------------------------------------------------------
// Panic recovery
// ---------------------------------------------------------------------------

func TestRecover_PanicHandled(t *testing.T) {
	cfg := testConfig()
	srv := New(cfg, testDeps(), testLogger())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})

	handler := srv.middlewareRecover(srv.middlewareLogging(mux))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ---------------------------------------------------------------------------
// Agents endpoint
// ---------------------------------------------------------------------------

func TestAgents_ListsAgents(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var agents []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents count = %d, want 1", len(agents))
	}
	if agents[0]["name"] != "default" {
		t.Errorf("name = %v, want default", agents[0]["name"])
	}
	if agents[0]["provider"] != "mock" {
		t.Errorf("provider = %v, want mock", agents[0]["provider"])
	}
	if agents[0]["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", agents[0]["model"])
	}
	if agents[0]["permission_tier"] != "supervised" {
		t.Errorf("tier = %v, want supervised", agents[0]["permission_tier"])
	}
	skillCount, _ := agents[0]["skill_count"].(float64)
	if int(skillCount) != 2 {
		t.Errorf("skill_count = %v, want 2", agents[0]["skill_count"])
	}
}

func TestAgents_RequiresAuth(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	// No auth header.
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAgent_SingleAgent(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/default"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var detail map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail["provider"] != "mock" {
		t.Errorf("provider = %v, want mock", detail["provider"])
	}
	if detail["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", detail["model"])
	}
	skills, ok := detail["skills"].([]any)
	if !ok {
		t.Fatal("skills field missing or not array")
	}
	if len(skills) != 2 {
		t.Errorf("skills count = %d, want 2", len(skills))
	}
}

func TestAgent_ContextFields_NoPersonaNoTools(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/default"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var detail map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// persona_dir should be absent or empty string for an engine with no persona.
	if v, ok := detail["persona_dir"]; ok && v != "" {
		t.Errorf("persona_dir = %v, want empty or absent", v)
	}

	// tool_names should be absent or nil for an engine with no tools.
	if v, ok := detail["tool_names"]; ok && v != nil {
		t.Errorf("tool_names = %v, want nil or absent", v)
	}
}

func TestAgent_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ---------------------------------------------------------------------------
// Costs endpoint
// ---------------------------------------------------------------------------

func TestCosts_ReturnsData(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()

	// Persist costs via the telemetry store so they survive restarts.
	store, _ := deps.Memory.(agent.TelemetryStore) //nolint:errcheck
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "default", "s1")
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "default", "s2")
	_ = store.UpdateConversationStats(ctx, "default:tg:s1", "default", agent.StoredMessage{
		Cost: 0.05, Model: "test-model", Provider: "mock",
		TokensPrompt: 50, TokensCompletion: 25,
	}, 0, 0)
	_ = store.UpdateConversationStats(ctx, "default:tg:s2", "default", agent.StoredMessage{
		Cost: 0.10, Model: "test-model", Provider: "mock",
		TokensPrompt: 100, TokensCompletion: 50,
	}, 0, 0)

	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/costs"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var costs map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&costs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	globalCost, _ := costs["global_cost"].(float64)
	if globalCost < 0.14 {
		t.Errorf("global_cost = %v, want >= 0.15", costs["global_cost"])
	}
	sessionCount, _ := costs["session_count"].(float64)
	if int(sessionCount) != 2 {
		t.Errorf("session_count = %v, want 2", costs["session_count"])
	}
	maxPerSession, _ := costs["max_per_session"].(float64)
	if maxPerSession != 1.0 {
		t.Errorf("max_per_session = %v, want 1.0", costs["max_per_session"])
	}

	// Verify per-agent breakdown is populated.
	byAgent, ok := costs["by_agent"].([]any)
	if !ok || len(byAgent) == 0 {
		t.Fatal("by_agent should contain at least one agent entry")
	}
	agentEntry, _ := byAgent[0].(map[string]any)
	if agentEntry["agent"] != "default" {
		t.Errorf("by_agent[0].agent = %v, want default", agentEntry["agent"])
	}
	agentCost, _ := agentEntry["cost"].(float64)
	if agentCost < 0.14 {
		t.Errorf("by_agent[0].cost = %v, want >= 0.15", agentCost)
	}
}

// ---------------------------------------------------------------------------
// Skills endpoint
// ---------------------------------------------------------------------------

func TestSkills_ListsAll(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/skills"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var skills []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&skills); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("skills count = %d, want 2", len(skills))
	}
	// All skills should be tagged with their agent.
	for _, sk := range skills {
		if sk["agent"] != "default" {
			t.Errorf("skill agent = %v, want default", sk["agent"])
		}
	}
}

func TestSkillsByAgent_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/skills/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ---------------------------------------------------------------------------
// Schedules endpoint
// ---------------------------------------------------------------------------

func TestSchedules_ListsEntries(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	// Register a schedule.
	_ = deps.Scheduler.Register(scheduler.Config{
		Name:     "test-job",
		Type:     "agent",
		Schedule: "@daily",
		Skill:    "greet",
		Enabled:  true,
	}, func(_ scheduler.Entry) {})

	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/schedules"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var schedules []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&schedules); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("schedules count = %d, want 1", len(schedules))
	}
	if schedules[0]["name"] != "test-job" {
		t.Errorf("name = %v, want test-job", schedules[0]["name"])
	}
	if schedules[0]["expression"] != "@daily" {
		t.Errorf("expression = %v, want @daily", schedules[0]["expression"])
	}
}

// ---------------------------------------------------------------------------
// Sessions endpoint
// ---------------------------------------------------------------------------

func TestSessions_ListsConversations(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	// Create a conversation with a message.
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "telegram", "12345")
	_, _ = deps.Memory.AddMessage(ctx, "telegram:12345", agent.StoredMessage{
		Role: "user", Content: "hello",
	})

	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Sessions []map[string]any `json:"sessions"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(result.Sessions))
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1", result.Total)
	}
	msgCount, _ := result.Sessions[0]["message_count"].(float64)
	if int(msgCount) != 1 {
		t.Errorf("message_count = %v, want 1", result.Sessions[0]["message_count"])
	}
}

func TestSessionMessages_ReturnsMessages(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "telegram", "12345")
	_, _ = deps.Memory.AddMessage(ctx, "telegram:12345", agent.StoredMessage{
		Role: "user", Content: "hello",
	})
	_, _ = deps.Memory.AddMessage(ctx, "telegram:12345", agent.StoredMessage{
		Role: "assistant", Content: "hi there", TokensUsed: 10,
	})

	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions/telegram:12345/messages"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var messages []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&messages); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages count = %d, want 2", len(messages))
	}
	if messages[0]["role"] != "user" || messages[0]["content"] != "hello" {
		t.Errorf("messages[0] = %v, unexpected", messages[0])
	}
	tokensUsed, _ := messages[1]["tokens_used"].(float64)
	if messages[1]["role"] != "assistant" || int(tokensUsed) != 10 {
		t.Errorf("messages[1] = %v, unexpected", messages[1])
	}
}

func TestSessionMessages_EmptyForUnknown(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions/nonexistent/messages"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var messages []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&messages); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("messages count = %d, want 0", len(messages))
	}
}

// ---------------------------------------------------------------------------
// Chat endpoint
// ---------------------------------------------------------------------------

// makeChatReq builds an authenticated POST /api/v1/chat request with JSON body.
func makeChatReq(body map[string]any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestChat_RequiresAuth(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	b, _ := json.Marshal(map[string]string{"message": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestChat_EmptyMessage(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, makeChatReq(map[string]any{"message": ""}))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestChat_UnknownAgent(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, makeChatReq(map[string]any{
		"message": "hello",
		"agent":   "nonexistent",
	}))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestChat_JSONResponse(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, makeChatReq(map[string]any{"message": "hello"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["response"] != "Hello from mock!" {
		t.Errorf("response = %v, want 'Hello from mock!'", resp["response"])
	}
	if resp["session_id"] == "" {
		t.Error("session_id should be generated and returned")
	}
}

func TestChat_JSONResponse_ExplicitSessionID(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, makeChatReq(map[string]any{
		"message":    "hello",
		"session_id": "my-session-123",
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["session_id"] != "my-session-123" {
		t.Errorf("session_id = %v, want my-session-123", resp["session_id"])
	}
}

func TestChat_SSEResponse(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	b, _ := json.Marshal(map[string]string{"message": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Parse SSE events.
	var events []map[string]any
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("parse SSE event: %v", err)
		}
		events = append(events, ev)
	}

	// Events: thinking, usage, content, done
	if len(events) != 4 {
		t.Fatalf("events count = %d, want 4; events: %v", len(events), events)
	}
	if events[0]["type"] != "thinking" {
		t.Errorf("events[0] = %v, want thinking", events[0])
	}
	if events[1]["type"] != "usage" {
		t.Errorf("events[1] = %v, want usage", events[1])
	}
	if events[2]["type"] != "content" || events[2]["text"] != "Hello from mock!" {
		t.Errorf("events[2] = %v, want content/Hello from mock!", events[2])
	}
	if events[3]["type"] != "done" {
		t.Errorf("events[3] = %v, want done with session_id", events[3])
	}
}

func TestChat_HistoryPersisted(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	// First message.
	rec1 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec1, makeChatReq(map[string]any{
		"message":    "first message",
		"session_id": "persist-test",
	}))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first chat status = %d", rec1.Code)
	}

	// Second message in same session.
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, makeChatReq(map[string]any{
		"message":    "second message",
		"session_id": "persist-test",
	}))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second chat status = %d", rec2.Code)
	}

	// Verify 4 messages stored: 2 user + 2 assistant.
	ctx := context.Background()
	messages, err := deps.Memory.GetMessages(ctx, "persist-test", 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(messages) != 4 {
		t.Errorf("message count = %d, want 4", len(messages))
	}
}

// ---------------------------------------------------------------------------
// Delete session endpoint
// ---------------------------------------------------------------------------

func TestDeleteSession_DeletesConversation(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()

	// Create a conversation via the memory store.
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "telegram", "del-test")
	_, _ = deps.Memory.AddMessage(ctx, "telegram:del-test", agent.StoredMessage{Role: "user", Content: "hello"})

	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodDelete, "/api/v1/sessions/telegram:del-test")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	// Conversation should be gone — messages return empty.
	messages, err := deps.Memory.GetMessages(ctx, "telegram:del-test", 100)
	if err != nil {
		t.Fatalf("GetMessages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("messages count = %d after delete, want 0", len(messages))
	}
}

func TestDeleteSession_NonExistentIsNoOp(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	req := authedRequest(http.MethodDelete, "/api/v1/sessions/does-not-exist")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestDeleteSession_RequiresAuth(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/some-session", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ---------------------------------------------------------------------------
// Telemetry endpoints
// ---------------------------------------------------------------------------

func TestSessionStats_ReturnsStats(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "api", "s1")
	_, _ = deps.Memory.AddMessage(ctx, "api:s1", agent.StoredMessage{
		Role: "assistant", Content: "hi", Cost: 0.01,
		Model: "gpt-4", Provider: "openai",
		TokensPrompt: 100, TokensCompletion: 50,
	})

	store, _ := deps.Memory.(agent.TelemetryStore) //nolint:errcheck // test helper; always SQLiteMemoryStore
	_ = store.UpdateConversationStats(ctx, "api:s1", "api", agent.StoredMessage{
		Cost: 0.01, Model: "gpt-4", Provider: "openai",
		TokensPrompt: 100, TokensCompletion: 50,
	}, 1, 0)

	srv := New(cfg, deps, testLogger())
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions/api:s1/stats"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var stats agent.ConversationStatsRow
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.LastModel != "gpt-4" {
		t.Errorf("last_model = %q, want gpt-4", stats.LastModel)
	}
	if stats.TotalCost != 0.01 {
		t.Errorf("total_cost = %f, want 0.01", stats.TotalCost)
	}
}

func TestSessionStats_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions/nonexistent/stats"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSessionToolCalls_ReturnsRecords(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "api", "t1")
	msgID, _ := deps.Memory.AddMessage(ctx, "api:t1", agent.StoredMessage{Role: "assistant", Content: "used tools"})

	store, _ := deps.Memory.(agent.TelemetryStore) //nolint:errcheck // test helper; always SQLiteMemoryStore
	_ = store.AddToolCalls(ctx, "api:t1", msgID, []agent.ToolCallRecord{
		{ToolName: "web_search", ServerName: "web", Round: 1, DurationMs: 100, Success: true},
	})

	srv := New(cfg, deps, testLogger())
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions/api:t1/tool-calls"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var records []agent.ToolCallRecord
	if err := json.NewDecoder(rec.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 1 || records[0].ToolName != "web_search" {
		t.Errorf("tool calls: %+v", records)
	}
}

func TestSessionToolCalls_EmptyForUnknown(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions/nonexistent/tool-calls"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var records []agent.ToolCallRecord
	_ = json.NewDecoder(rec.Body).Decode(&records)
	if len(records) != 0 {
		t.Errorf("expected empty, got %d records", len(records))
	}
}

func TestSessionSkills_ReturnsRecords(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "api", "sk1")
	msgID, _ := deps.Memory.AddMessage(ctx, "api:sk1", agent.StoredMessage{Role: "user", Content: "hello"})

	store, _ := deps.Memory.(agent.TelemetryStore) //nolint:errcheck // test helper; always SQLiteMemoryStore
	_ = store.AddSkillUsages(ctx, "api:sk1", msgID, []agent.SkillUsageRecord{
		{SkillName: "greeting", MatchType: "always"},
	})

	srv := New(cfg, deps, testLogger())
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions/api:sk1/skills"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var records []agent.SkillUsageRecord
	if err := json.NewDecoder(rec.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 1 || records[0].SkillName != "greeting" {
		t.Errorf("skill usages: %+v", records)
	}
}

func TestTelemetrySummary_ReturnsAggregation(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "api", "sum1")
	msgID, _ := deps.Memory.AddMessage(ctx, "api:sum1", agent.StoredMessage{
		Role: "assistant", Content: "hi", Model: "gpt-4", Provider: "openai",
		Cost: 0.01, TokensPrompt: 100, TokensCompletion: 50,
	})

	store, _ := deps.Memory.(agent.TelemetryStore) //nolint:errcheck // test helper; always SQLiteMemoryStore
	_ = store.AddToolCalls(ctx, "api:sum1", msgID, []agent.ToolCallRecord{
		{ToolName: "search", ServerName: "web", DurationMs: 100, Success: true},
	})

	srv := New(cfg, deps, testLogger())
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/telemetry/summary"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var summary agent.TelemetrySummary
	if err := json.NewDecoder(rec.Body).Decode(&summary); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(summary.ByModel) != 1 || summary.ByModel[0].Model != "gpt-4" {
		t.Errorf("by_model: %+v", summary.ByModel)
	}
	if len(summary.ByTool) != 1 || summary.ByTool[0].ToolName != "search" {
		t.Errorf("by_tool: %+v", summary.ByTool)
	}
}

func TestTelemetrySummary_WithTimeFilter(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "api", "tf1")
	_, _ = deps.Memory.AddMessage(ctx, "api:tf1", agent.StoredMessage{
		Role: "assistant", Content: "hi", Model: "gpt-4", Provider: "openai",
		Cost: 0.01, TokensPrompt: 100, TokensCompletion: 50,
	})

	srv := New(cfg, deps, testLogger())

	// Query far in the future — should return nothing.
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/telemetry/summary?since=2099-01-01T00:00:00Z"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var summary agent.TelemetrySummary
	_ = json.NewDecoder(rec.Body).Decode(&summary)
	if len(summary.ByModel) != 0 {
		t.Errorf("expected empty with future filter, got %d models", len(summary.ByModel))
	}
}

// ---------------------------------------------------------------------------
// Approvals endpoints
// ---------------------------------------------------------------------------

// submitTestApproval creates a pending approval via the manager and returns its ID.
func submitTestApproval(t *testing.T, mgr *approval.Manager, summary string) string {
	t.Helper()
	req, err := mgr.Submit(
		context.Background(),
		"default",
		approval.ActionKindUserUpdate,
		summary,
		"payload content",
		"chat-123",
		"api",
		"session-abc",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return req.ID
}

func TestHandleListApprovals_Empty(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/approvals"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var list []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("approvals count = %d, want 0", len(list))
	}
}

func TestHandleListApprovals_FilterByStatus(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	submitTestApproval(t, deps.Approvals, "pending approval")
	srv := New(cfg, deps, testLogger())

	// Filter by pending — should return 1.
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/approvals?status=pending"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var list []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("approvals count = %d, want 1", len(list))
	}
	if list[0]["status"] != "pending" {
		t.Errorf("status = %v, want pending", list[0]["status"])
	}

	// Filter by approved — should return 0.
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, authedRequest(http.MethodGet, "/api/v1/approvals?status=approved"))
	var list2 []map[string]any
	if err := json.NewDecoder(rec2.Body).Decode(&list2); err != nil {
		t.Fatalf("decode approved: %v", err)
	}
	if len(list2) != 0 {
		t.Errorf("approved count = %d, want 0", len(list2))
	}
}

func TestHandleGetApproval_Found(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	id := submitTestApproval(t, deps.Approvals, "get me")
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/approvals/"+id))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["id"] != id {
		t.Errorf("id = %v, want %s", resp["id"], id)
	}
	if resp["summary"] != "get me" {
		t.Errorf("summary = %v, want 'get me'", resp["summary"])
	}
}

func TestHandleGetApproval_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/approvals/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleApproveApproval_Success(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	actionCalled := false
	req, err := deps.Approvals.Submit(
		context.Background(),
		"default", approval.ActionKindUserUpdate,
		"approve me", "payload",
		"chat-123", "api", "session-1",
		func(_ context.Context, _ string) error {
			actionCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/approve"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !actionCalled {
		t.Error("expected action closure to be called on approval")
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "approved" {
		t.Errorf("status = %v, want approved", resp["status"])
	}
}

func TestHandleDenyApproval_Success(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	actionCalled := false
	req, err := deps.Approvals.Submit(
		context.Background(),
		"default", approval.ActionKindUserUpdate,
		"deny me", "payload",
		"chat-123", "api", "session-1",
		func(_ context.Context, _ string) error {
			actionCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/deny"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if actionCalled {
		t.Error("action closure should NOT be called on denial")
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "denied" {
		t.Errorf("status = %v, want denied", resp["status"])
	}
}

func TestHandleApproveApproval_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/approvals/doesnotexist/approve"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleApproveApproval_AlreadyResolved(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	id := submitTestApproval(t, deps.Approvals, "already resolved")
	// Resolve once.
	if _, err := deps.Approvals.Resolve(context.Background(), id, true, "api"); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	srv := New(cfg, deps, testLogger())

	// Second resolve should return 409.
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/approvals/"+id+"/approve"))

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestHandleApprovals_NilManager_Returns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Approvals = nil // simulate unconfigured approvals
	srv := New(cfg, deps, testLogger())

	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/approvals"},
		{http.MethodGet, "/api/v1/approvals/someid"},
		{http.MethodPost, "/api/v1/approvals/someid/approve"},
		{http.MethodPost, "/api/v1/approvals/someid/deny"},
	}

	for _, p := range paths {
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, authedRequest(p.method, p.path))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want %d", p.method, p.path, rec.Code, http.StatusServiceUnavailable)
		}
	}
}

func TestHandleApprovals_RequiresScope(t *testing.T) {
	// Key with only approvals:read — write endpoints should be 403.
	readOnlyKey := config.APIKeyConfig{
		Name: "readonly", Key: "dk-readonly", Scopes: []string{"approvals:read"},
	}
	cfg := testConfig(readOnlyKey)
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/someid/approve", nil)
	req.Header.Set("Authorization", "Bearer dk-readonly")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// ---------------------------------------------------------------------------
// Auto-approve rule endpoints
// ---------------------------------------------------------------------------

func TestHandleListAutoApprove_Empty(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/auto-approve"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var list []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d items", len(list))
	}
}

func TestHandleCreateAutoApprove_Permanent(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	body := `{"agent":"default","tool":"web_search","scope":"permanent"}`
	req := authedRequest(http.MethodPost, "/api/v1/auto-approve")
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var rule map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule["tool_name"] != "web_search" {
		t.Errorf("tool_name = %v, want web_search", rule["tool_name"])
	}
	if rule["scope"] != "permanent" {
		t.Errorf("scope = %v, want permanent", rule["scope"])
	}
}

func TestHandleCreateAutoApprove_MissingFields(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	body := `{"agent":"","tool":""}`
	req := authedRequest(http.MethodPost, "/api/v1/auto-approve")
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateAutoApprove_InvalidScope(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	body := `{"agent":"default","tool":"web_search","scope":"invalid"}`
	req := authedRequest(http.MethodPost, "/api/v1/auto-approve")
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleDeleteAutoApprove_Success(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	// Create a rule first via the manager.
	rule, err := deps.Approvals.AddPermanentRule(context.Background(), "default", "web_search", "test")
	if err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/auto-approve/"+rule.ID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleDeleteAutoApprove_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/auto-approve/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleListAutoApprove_FilterByAgent(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	if _, err := deps.Approvals.AddPermanentRule(context.Background(), "agent1", "tool_a", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Approvals.AddPermanentRule(context.Background(), "agent2", "tool_b", "test"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/auto-approve?agent=agent1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var list []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 rule for agent1, got %d", len(list))
	}
}

func TestHandleApproveApproval_WithAutoApprove_CreatesRule(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()

	// Submit a tool-call approval.
	noOp := func(_ context.Context, _ string) error { return nil }
	req, err := deps.Approvals.Submit(
		context.Background(),
		"default",
		approval.ActionKindToolCall,
		`Execute tool "web_search" with args: {}`,
		`{}`,
		"chat-123", "api", "conv-abc", noOp,
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, deps, testLogger())

	// Approve with auto_approve=permanent param.
	httpReq := authedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/approve?auto_approve=permanent")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Permanent auto-approve rule should now exist.
	ok, scope := deps.Approvals.ShouldAutoApprove(context.Background(), "default", "web_search", "any-conv")
	if !ok {
		t.Error("expected permanent auto-approve rule to be created")
	}
	if scope != approval.ScopePermanent {
		t.Errorf("scope = %v, want permanent", scope)
	}
}

func TestHandleAutoApprove_NilManager_Returns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Approvals = nil
	srv := New(cfg, deps, testLogger())

	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/auto-approve"},
		{http.MethodPost, "/api/v1/auto-approve"},
		{http.MethodDelete, "/api/v1/auto-approve/someid"},
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		req := authedRequest(p.method, p.path)
		if p.method == http.MethodPost {
			req.Body = io.NopCloser(strings.NewReader(`{"agent":"a","tool":"b"}`))
		}
		srv.httpServer.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want %d", p.method, p.path, rec.Code, http.StatusServiceUnavailable)
		}
	}
}

// ---------------------------------------------------------------------------
// Nil-array regression tests (JSON must be [] not null when empty)
// ---------------------------------------------------------------------------

func TestSessions_EmptyListReturnsArray(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger()) // no sessions created

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := strings.TrimSpace(rec.Body.String())
	var result struct {
		Sessions []map[string]any `json:"sessions"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Sessions == nil {
		t.Error("sessions decoded to nil slice, want non-nil empty slice")
	}
	if result.Total != 0 {
		t.Errorf("total = %d, want 0", result.Total)
	}
}

func TestSkills_NoSkillsReturnsArray(t *testing.T) {
	// Build deps with an agent that has no skills.
	logger := testLogger()
	mem, _ := agent.NewInMemoryStore()
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	perms, _ := security.NewPermissionEngine("supervised")
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{Content: "ok", Model: "test-model", FinishReason: "stop"},
	})
	approvalStore, _ := approval.NewInMemoryStore()
	approvalMgr := approval.NewManager(approvalStore, logger)
	e := agent.NewEngine("default", router, mem, nil, perms, nil, "test",
		[]skill.Skill{}, // no skills
		nil, approvalMgr, logger)
	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": e},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil, logger,
	)
	deps := Deps{
		Dispatcher:  dispatcher,
		Scheduler:   scheduler.New(logger, nil),
		CostTracker: costTracker,
		Memory:      mem,
		Approvals:   approvalMgr,
		Config: &config.Config{
			Agents: []config.AgentInstanceConfig{{Name: "default", Adapters: []string{"telegram"}}},
		},
	}

	cfg := testConfig(allScopesKey())
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/skills"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body == "null" {
		t.Error("skills response is JSON null; want [] for empty list (Svelte #each crashes on null)")
	}
	var list []map[string]any
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list == nil {
		t.Error("skills decoded to nil slice, want non-nil empty slice")
	}
}

func TestSchedules_EmptyReturnsArray(t *testing.T) {
	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger()) // no schedules registered

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/schedules"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body == "null" {
		t.Error("schedules response is JSON null; want [] for empty list (Svelte #each crashes on null)")
	}
	var list []map[string]any
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list == nil {
		t.Error("schedules decoded to nil slice, want non-nil empty slice")
	}
}

// ---------------------------------------------------------------------------
// WebHandler routing
// ---------------------------------------------------------------------------

func TestWebHandler_ServedForNonAPIPath(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.WebHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>dashboard</html>"))
	})
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "dashboard") {
		t.Error("expected web handler body in response")
	}
}

func TestWebHandler_APIRoutesNotIntercepted(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.WebHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// If this is reached for /api/v1/health the test should fail.
		http.Error(w, "web handler intercepted API route", http.StatusTeapot)
	})
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rec.Code == http.StatusTeapot {
		t.Fatal("web handler intercepted /api/v1/health; API routes must take priority")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("health status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// ---------------------------------------------------------------------------
// Tool & plugin endpoints
// ---------------------------------------------------------------------------

func testLifecycleMgr(t *testing.T) *tool.LifecycleManager {
	t.Helper()
	dir := t.TempDir()
	cfgPath := dir + "/denkeeper.toml"
	_ = os.WriteFile(cfgPath, []byte("[telegram]\ntoken = \"test\"\n"), 0644)
	mgr := tool.NewManager(testLogger())
	return tool.NewLifecycleManager(mgr, cfgPath, 50, testLogger())
}

func TestListTools_NilLifecycleMgr_Returns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = nil
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/tools"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestListTools_Empty(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/tools"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	tools, ok := body["tools"].([]any)
	if !ok {
		t.Fatal("expected tools array in response")
	}
	if len(tools) != 0 {
		t.Errorf("expected empty tools array, got %d", len(tools))
	}
}

func TestListPlugins_Empty(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/plugins"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	plugins, ok := body["plugins"].([]any)
	if !ok {
		t.Fatal("expected plugins array in response")
	}
	if len(plugins) != 0 {
		t.Errorf("expected empty plugins array, got %d", len(plugins))
	}
}

func TestAddTool_MissingName(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	body := `{"command": "/usr/bin/test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAddTool_MissingCommand(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	body := `{"name": "my-tool"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAddPlugin_InvalidType(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	body := `{"name": "bad-plugin", "type": "invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRemoveTool_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/tools/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetTool_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/tools/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestToolHealth_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/tools/nonexistent/health"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestToolHealth_NilLifecycleMgr_Returns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = nil
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/tools/test/health"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRestartTool_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/tools/nonexistent/restart"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRestartTool_NilLifecycleMgr_Returns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = nil
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/tools/test/restart"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGetPlugin_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/plugins/nonexistent"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestToolsEndpoints_RequireScope(t *testing.T) {
	// Key with no tool scopes should be rejected.
	noToolsKey := config.APIKeyConfig{
		Name:   "no-tools",
		Key:    "dk-test-key",
		Scopes: []string{"health", "chat"},
	}
	cfg := testConfig(noToolsKey)
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	// GET /api/v1/tools requires tools:read
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/tools"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET /tools without scope: status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	// POST /api/v1/tools requires tools:write
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools", strings.NewReader(`{"name":"x","command":"y"}`))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /tools without scope: status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRemovePlugin_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/plugins/nonexistent"))

	// RemovePlugin is idempotent — removing a non-existent plugin succeeds silently.
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUpdateTool_NotFound(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	body := `{"command": "/usr/bin/new-cmd"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tools/nonexistent", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateTool_MissingCommand(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(cfg, deps, testLogger())

	body := `{"transport": "stdio"}` // no command
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tools/nonexistent", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateTool_NilLifecycleMgr_Returns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.LifecycleMgr = nil
	srv := New(cfg, deps, testLogger())

	body := `{"command": "/usr/bin/test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tools/any", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// ---------------------------------------------------------------------------
// Browser profile & session endpoints
// ---------------------------------------------------------------------------

func TestBrowserProfiles_List_NoBrowser(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	// BrowserProfiles is nil by default — should return 503.
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/browser/profiles"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestBrowserProfiles_List(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/agent-a", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/agent-a/data", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.BrowserProfiles = browser.NewProfileService(dir, testLogger())
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/browser/profiles"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Profiles []struct {
			Agent string `json:"agent"`
		} `json:"profiles"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Profiles) != 1 || resp.Profiles[0].Agent != "agent-a" {
		t.Errorf("unexpected profiles: %+v", resp.Profiles)
	}
}

func TestBrowserProfiles_Get_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.BrowserProfiles = browser.NewProfileService(dir, testLogger())
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/browser/profiles/ghost"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestBrowserProfiles_Delete(t *testing.T) {
	dir := t.TempDir()
	agentDir := dir + "/deleteme"
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentDir+"/data", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.BrowserProfiles = browser.NewProfileService(dir, testLogger())
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/browser/profiles/deleteme"))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if _, err := os.Stat(agentDir); !os.IsNotExist(err) {
		t.Error("expected directory to be removed")
	}
}

func TestBrowserSessions_NoBrowser(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/browser/sessions"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestBrowserConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.BrowserProfiles = browser.NewProfileService(dir, testLogger())
	deps.Config.Browser = config.BrowserConfig{
		Enabled:     true,
		Image:       "ghcr.io/temikus/denkeeper-browser:latest",
		MemoryLimit: "512m",
		CPULimit:    "1",
		ProfileDir:  "data/browser-profiles",
		SessionTTL:  "10m",
		MaxPages:    5,
	}
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/browser/config"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp config.BrowserConfig
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Image != "ghcr.io/temikus/denkeeper-browser:latest" {
		t.Errorf("unexpected image: %s", resp.Image)
	}
	if resp.MaxPages != 5 {
		t.Errorf("unexpected max_pages: %d", resp.MaxPages)
	}
}

func TestBrowserProfiles_Get(t *testing.T) {
	dir := t.TempDir()
	agentDir := dir + "/myagent"
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentDir+"/state.json", make([]byte, 256), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.BrowserProfiles = browser.NewProfileService(dir, testLogger())
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/browser/profiles/myagent"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var info struct {
		Agent     string `json:"agent"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Agent != "myagent" {
		t.Errorf("agent = %q, want %q", info.Agent, "myagent")
	}
	if info.SizeBytes < 256 {
		t.Errorf("size_bytes = %d, want >= 256", info.SizeBytes)
	}
}

func TestBrowserProfiles_Delete_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.BrowserProfiles = browser.NewProfileService(dir, testLogger())
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/browser/profiles/ghost"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestBrowserConfig_NoBrowser(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/browser/config"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestChat_SSEToolEvents(t *testing.T) {
	logger := testLogger()
	mem, _ := agent.NewInMemoryStore()
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)

	// Use autonomous tier so tool calls execute without approval blocking.
	perms, _ := security.NewPermissionEngine("autonomous")
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "get_weather", Arguments: `{}`}},
				},
				TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
				FinishReason: "tool_calls",
			},
			{
				Content:      "It's sunny!",
				TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
				Model:        "test-model",
				FinishReason: "stop",
			},
		},
	})

	approvalStore, _ := approval.NewInMemoryStore()
	approvalMgr := approval.NewManager(approvalStore, logger)
	toolMgr := tool.NewManager(logger)

	e := agent.NewEngine("default", router, mem, nil, perms, nil, "test", []skill.Skill{}, toolMgr, approvalMgr, logger)

	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": e},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil, logger,
	)

	sched := scheduler.New(logger, nil)
	cfg := testConfig(allScopesKey())
	deps := Deps{
		Dispatcher:  dispatcher,
		Scheduler:   sched,
		CostTracker: costTracker,
		Memory:      mem,
		Approvals:   approvalMgr,
		Config: &config.Config{
			Agents: []config.AgentInstanceConfig{
				{Name: "default", Adapters: []string{"telegram"}},
			},
		},
	}
	srv := New(cfg, deps, logger)

	b, _ := json.Marshal(map[string]string{"message": "weather?"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Parse SSE events.
	var events []map[string]any
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("parse SSE event: %v", err)
		}
		events = append(events, ev)
	}

	// Expect: thinking, tool_start, tool_end, thinking, usage, content, done = 7 events.
	if len(events) != 7 {
		t.Fatalf("events count = %d, want 7; events: %+v", len(events), events)
	}
	if events[0]["type"] != "thinking" {
		t.Errorf("events[0] = %v, want thinking", events[0])
	}
	if events[1]["type"] != "tool_start" || events[1]["tool"] != "get_weather" {
		t.Errorf("events[1] = %v, want tool_start/get_weather", events[1])
	}
	if events[2]["type"] != "tool_end" || events[2]["tool"] != "get_weather" {
		t.Errorf("events[2] = %v, want tool_end/get_weather", events[2])
	}
	if events[3]["type"] != "thinking" {
		t.Errorf("events[3] = %v, want thinking", events[3])
	}
	if events[4]["type"] != "usage" {
		t.Errorf("events[4] = %v, want usage", events[4])
	}
	if events[5]["type"] != "content" || events[5]["text"] != "It's sunny!" {
		t.Errorf("events[5] = %v, want content/It's sunny!", events[5])
	}
	if events[6]["type"] != "done" {
		t.Errorf("events[6] = %v, want done", events[6])
	}
}

func TestHandleModels_ReturnsList(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.ModelLister = func(_ context.Context) []string {
		return []string{"anthropic/claude-opus-4", "openai/gpt-4o"}
	}
	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/models")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	models, ok := resp["models"].([]any)
	if !ok {
		t.Fatalf("models field missing or wrong type: %v", resp)
	}
	if len(models) != 2 {
		t.Errorf("len(models) = %d, want 2", len(models))
	}
}

func TestHandleModels_NilListerReturns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	// ModelLister is nil (not configured)
	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/models")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestHandleModels_RequiresAuth(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.ModelLister = func(_ context.Context) []string { return nil }
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleModelDetails_ReturnsList(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	inp := 3.0
	out := 15.0
	deps.ModelDetailLister = func(_ context.Context, _ string) []llm.ModelInfo {
		return []llm.ModelInfo{
			{ID: "anthropic/claude-opus-4", Name: "Claude Opus 4", Provider: "openrouter", InputPerMTok: &inp, OutputPerMTok: &out, SupportsTools: true},
		}
	}
	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/models/details")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	models, ok := resp["models"].([]any)
	if !ok {
		t.Fatalf("models field missing or wrong type: %v", resp)
	}
	if len(models) != 1 {
		t.Errorf("len(models) = %d, want 1", len(models))
	}
}

func TestHandleModelDetails_NilListerReturns503(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	// ModelDetailLister is nil (not configured)
	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/models/details")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestHandleModelDetails_RequiresAuth(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.ModelDetailLister = func(_ context.Context, _ string) []llm.ModelInfo { return nil }
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/details", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
