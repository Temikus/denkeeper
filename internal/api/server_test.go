package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(keys ...config.APIKeyConfig) config.APIConfig {
	return config.APIConfig{
		Enabled: true,
		Listen:  ":0",
		Keys:    keys,
	}
}

// testDeps builds a minimal Deps with real components suitable for testing.
func testDeps() Deps {
	logger := testLogger()
	mem, _ := agent.NewInMemoryStore()
	costTracker := llm.NewCostTracker(1.0)

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
	e := agent.NewEngine("default", router, mem, nil, perms, nil, "test", []skill.Skill{
		{Name: "greet", Description: "Greeting skill", Version: "1.0", Triggers: []string{"command:hello"}},
		{Name: "help", Description: "Help system"},
	}, nil, logger)

	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": e},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil,
		logger,
	)

	sched := scheduler.New(logger)

	return Deps{
		Dispatcher:  dispatcher,
		Scheduler:   sched,
		CostTracker: costTracker,
		Memory:      mem,
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
			"sessions:read", "costs:read",
			"skills:read", "skills:write",
			"schedules:read", "schedules:write",
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

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
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
	skills, ok := detail["skills"].([]any)
	if !ok {
		t.Fatal("skills field missing or not array")
	}
	if len(skills) != 2 {
		t.Errorf("skills count = %d, want 2", len(skills))
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
	// Record some costs.
	deps.CostTracker.Record("session-1", 0.05)
	deps.CostTracker.Record("session-2", 0.10)
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
	_ = deps.Memory.AddMessage(ctx, "telegram:12345", agent.StoredMessage{
		Role: "user", Content: "hello",
	})

	srv := New(cfg, deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sessions"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var sessions []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(sessions))
	}
	msgCount, _ := sessions[0]["message_count"].(float64)
	if int(msgCount) != 1 {
		t.Errorf("message_count = %v, want 1", sessions[0]["message_count"])
	}
}

func TestSessionMessages_ReturnsMessages(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	ctx := context.Background()
	_, _ = deps.Memory.GetOrCreateConversation(ctx, "telegram", "12345")
	_ = deps.Memory.AddMessage(ctx, "telegram:12345", agent.StoredMessage{
		Role: "user", Content: "hello",
	})
	_ = deps.Memory.AddMessage(ctx, "telegram:12345", agent.StoredMessage{
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
	var events []map[string]string
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("parse SSE event: %v", err)
		}
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("events count = %d, want 2", len(events))
	}
	if events[0]["type"] != "content" || events[0]["text"] != "Hello from mock!" {
		t.Errorf("events[0] = %v, want content/Hello from mock!", events[0])
	}
	if events[1]["type"] != "done" || events[1]["session_id"] == "" {
		t.Errorf("events[1] = %v, want done with session_id", events[1])
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
	_ = deps.Memory.AddMessage(ctx, "telegram:del-test", agent.StoredMessage{Role: "user", Content: "hello"})

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
