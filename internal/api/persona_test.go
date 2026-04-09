package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
)

// testDepsWithPersona returns deps where the default engine has a loaded persona.
func testDepsWithPersona(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()

	// Write a minimal SOUL.md so persona loads successfully.
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are a test agent."), 0644); err != nil {
		t.Fatal(err)
	}
	// Write optional USER.md and MEMORY.md.
	if err := os.WriteFile(filepath.Join(dir, "USER.md"), []byte("Test user profile."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("Test memory."), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := persona.Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	logger := testLogger()
	mem, _ := agent.NewInMemoryStore()
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)

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

	e := agent.NewEngine("default", router, mem, nil, perms, p, "", []skill.Skill{}, nil, approvalMgr, logger)

	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": e},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil, logger,
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

func TestGetPersona_Success(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/default/persona/soul", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"section":"soul"`) {
		t.Errorf("response missing section field: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "You are a test agent.") {
		t.Errorf("response missing soul content: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"agent_mutable":true`) {
		t.Errorf("response missing agent_mutable:true for soul: %s", rec.Body.String())
	}
}

func TestGetPersona_AgentNotFound(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/no-such-agent/persona/soul", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetPersona_InvalidSection(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/default/persona/evil%2F..%2Fetc%2Fpasswd", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestGetPersona_NoPersona(t *testing.T) {
	deps := testDeps() // default engine has nil persona
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/default/persona/soul", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	// PersonaSection returns ok=false when persona is nil → 400.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdatePersona_Success(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"content":"Updated memory content."}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/default/persona/memory", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("update: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"updated"`) {
		t.Errorf("response missing status:updated: %s", rec.Body.String())
	}

	// Verify the content was persisted by reading back via GET.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/default/persona/memory", nil)
	req2.Header.Set("Authorization", "Bearer dk-test-key")
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("GET after update: status = %d, want %d", rec2.Code, http.StatusOK)
	}
	if !strings.Contains(rec2.Body.String(), "Updated memory content.") {
		t.Errorf("GET after update: content not updated: %s", rec2.Body.String())
	}
}

func TestUpdatePersona_InvalidSection(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"content":"test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/default/persona/invalid", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdatePersona_AgentNotFound(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"content":"test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/no-such-agent/persona/memory", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestUpdatePersona_InvalidJSON(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/default/persona/memory", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Errorf("expected invalid JSON error: %s", rec.Body.String())
	}
}

func TestUpdatePersona_NoPersona(t *testing.T) {
	deps := testDeps() // default engine has nil persona
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"content":"test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/default/persona/memory", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	// PersonaSection returns ok=false when persona is nil → 400.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdatePersona_RequiresWriteScope(t *testing.T) {
	readOnlyKey := config.APIKeyConfig{
		Name:   "read-only",
		Key:    "dk-readonly",
		Scopes: []string{"agents:read"},
	}
	deps := testDepsWithPersona(t)
	srv := New(testConfig(readOnlyKey), deps, testLogger())

	body := `{"content":"test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/default/persona/memory", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-readonly")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing agents:write scope, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestPersonaEndpoints_RequiresScope(t *testing.T) {
	readOnlyKey := config.APIKeyConfig{
		Name:   "no-agents-scope",
		Key:    "dk-limited",
		Scopes: []string{"chat"},
	}
	deps := testDepsWithPersona(t)
	srv := New(testConfig(readOnlyKey), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/default/persona/soul", nil)
	req.Header.Set("Authorization", "Bearer dk-limited")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing agents:read scope, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
