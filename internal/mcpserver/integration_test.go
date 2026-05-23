//go:build integration

package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/mcpserver"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testDeps(t *testing.T) mcpserver.Deps {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	return mcpserver.Deps{
		Dispatcher: dispatcher,
		TOMLKeys: []config.APIKeyConfig{
			{Name: "test-key", Key: "dk-test-integration-key", Scopes: []string{"admin"}},
		},
		Logger: logger,
	}
}

func startMCPServer(t *testing.T, deps mcpserver.Deps) *httptest.Server {
	t.Helper()
	cfg := config.APIMCPServerConfig{
		Transport:      "streamable",
		SessionTimeout: "30m",
		ChatTimeout:    "2m",
	}
	srv := mcpserver.New(cfg, deps)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func connectClient(t *testing.T, ts *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL,
		HTTPClient: &http.Client{
			Transport: &bearerTransport{token: token, base: http.DefaultTransport},
		},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (bt *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+bt.token)
	return bt.base.RoundTrip(req)
}

func TestMCPServer_Auth_NoToken(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)

	transport := &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	_, err := client.Connect(context.Background(), transport, nil)
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
}

func TestMCPServer_Auth_InvalidToken(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)

	transport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL,
		HTTPClient: &http.Client{
			Transport: &bearerTransport{token: "dk-invalid-key", base: http.DefaultTransport},
		},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	_, err := client.Connect(context.Background(), transport, nil)
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
}

func TestMCPServer_Auth_ValidToken(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)

	session := connectClient(t, ts, "dk-test-integration-key")
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}
}

func TestMCPServer_ToolList(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	toolNames := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
	}

	expected := []string{"panic", "resume", "panic_status", "agent_list", "agent_info",
		"schedule_list", "schedule_create", "schedule_update", "schedule_delete"}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("expected tool %q in list", name)
		}
	}
}

func TestMCPServer_PanicStatus(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "panic_status",
	})
	if err != nil {
		t.Fatalf("call panic_status: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(text.Text), &status); err != nil {
		t.Fatalf("unmarshal panic_status: %v", err)
	}
	if status["panicked"] != false {
		t.Errorf("expected panicked=false, got %v", status["panicked"])
	}
}

func TestMCPServer_AgentList(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "agent_list",
	})
	if err != nil {
		t.Fatalf("call agent_list: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestMCPServer_Chat_PanicState(t *testing.T) {
	deps := testDeps(t)
	deps.Dispatcher.Panic()
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("call chat: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when system is panicked")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "panic") {
		t.Errorf("expected panic error message, got: %v", text)
	}
}

func TestMCPServer_Chat_AgentNotFound(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"message": "hello", "agent": "nonexistent-agent"},
	})
	if err != nil {
		t.Fatalf("call chat: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent agent")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "not found") {
		t.Errorf("expected 'not found' error message, got: %v", text)
	}
}

func TestMCPServer_Chat_ScopeRequired(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	deps := mcpserver.Deps{
		Dispatcher: dispatcher,
		TOMLKeys: []config.APIKeyConfig{
			{Name: "read-only", Key: "dk-readonly-key", Scopes: []string{"agents:read"}},
		},
		Logger: logger,
	}
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-readonly-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("call chat: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected scope error for chat without chat scope")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "scope") {
		t.Errorf("expected scope error message, got: %v", text)
	}
}

func TestMCPServer_ScopeEnforcement(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	deps := mcpserver.Deps{
		Dispatcher: dispatcher,
		TOMLKeys: []config.APIKeyConfig{
			{Name: "limited-key", Key: "dk-limited-key", Scopes: []string{"kv:read"}},
		},
		Logger: logger,
	}
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-limited-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "panic_status",
	})
	if err != nil {
		t.Fatalf("call panic_status: %v", err)
	}

	if !result.IsError {
		text, _ := result.Content[0].(*mcp.TextContent)
		t.Errorf("expected error result for missing scope, got text: %s", text.Text)
	}
}

func testDepsWithScheduler(t *testing.T) mcpserver.Deps {
	t.Helper()
	deps := testDeps(t)
	deps.Scheduler = scheduler.New(deps.Logger, time.UTC)
	return deps
}

func TestMCPServer_ScheduleList_Empty(t *testing.T) {
	deps := testDepsWithScheduler(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_list",
	})
	if err != nil {
		t.Fatalf("call schedule_list: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || text.Text != "[]" {
		t.Errorf("expected empty array, got: %v", text)
	}
}

func TestMCPServer_ScheduleCreate_NoScheduler(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_create",
		Arguments: map[string]any{
			"name":     "test-sched",
			"schedule": "0 9 * * *",
			"channel":  "telegram:12345",
		},
	})
	if err != nil {
		t.Fatalf("call schedule_create: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no scheduler")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "not available") {
		t.Errorf("expected 'not available', got: %v", text)
	}
}

func TestMCPServer_ScheduleCreate_MissingFields(t *testing.T) {
	deps := testDepsWithScheduler(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "schedule_create",
		Arguments: map[string]any{"name": "test-sched"},
	})
	if err != nil {
		t.Fatalf("call schedule_create: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected validation error for missing fields")
	}
}

func TestMCPServer_ScheduleCreate_InvalidCron(t *testing.T) {
	deps := testDepsWithScheduler(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_create",
		Arguments: map[string]any{
			"name":     "bad-cron",
			"schedule": "not valid",
			"channel":  "telegram:12345",
		},
	})
	if err != nil {
		t.Fatalf("call schedule_create: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid cron")
	}
}

func TestMCPServer_ScheduleCreate_NoAgent(t *testing.T) {
	deps := testDepsWithScheduler(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_create",
		Arguments: map[string]any{
			"name":     "test-sched",
			"schedule": "0 9 * * *",
			"channel":  "telegram:12345",
		},
	})
	if err != nil {
		t.Fatalf("call schedule_create: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no agent available")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "agent not found") {
		t.Errorf("expected 'agent not found', got: %v", text)
	}
}

func TestMCPServer_ScheduleUpdate_NotFound(t *testing.T) {
	deps := testDepsWithScheduler(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_update",
		Arguments: map[string]any{
			"name":    "nonexistent",
			"enabled": false,
		},
	})
	if err != nil {
		t.Fatalf("call schedule_update: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent schedule")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "not found") {
		t.Errorf("expected 'not found', got: %v", text)
	}
}

func TestMCPServer_ScheduleDelete_NotFound(t *testing.T) {
	deps := testDepsWithScheduler(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "schedule_delete",
		Arguments: map[string]any{"name": "nonexistent"},
	})
	if err != nil {
		t.Fatalf("call schedule_delete: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent schedule")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "not found") {
		t.Errorf("expected 'not found', got: %v", text)
	}
}

// stubProvider satisfies llm.Provider for tests that need an Engine but never
// call the LLM (e.g. schedule registration).
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }
func (stubProvider) ChatCompletion(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "stub"}, nil
}
func (stubProvider) HealthCheck(context.Context) error { return nil }

func testDepsWithAgent(t *testing.T) mcpserver.Deps {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mem, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}

	perms, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	router := llm.NewRouter("stub", "test-model", nil)
	router.RegisterProvider(stubProvider{})

	e := agent.NewEngine("test-agent", router, mem, nil, perms, nil,
		"You are a test agent.", nil, nil, nil, logger)

	engines := map[string]*agent.Engine{"test-agent": e}
	bindings := []agent.Binding{{Pattern: "telegram:*", AgentName: "test-agent"}}
	dispatcher := agent.NewDispatcher(engines, bindings, nil, logger)

	return mcpserver.Deps{
		Dispatcher: dispatcher,
		Scheduler:  scheduler.New(logger, time.UTC),
		TOMLKeys: []config.APIKeyConfig{
			{Name: "test-key", Key: "dk-test-integration-key", Scopes: []string{"admin"}},
		},
		Logger: logger,
	}
}

func TestMCPServer_ScheduleCreateListDeleteLifecycle(t *testing.T) {
	deps := testDepsWithAgent(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	// Create a schedule
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_create",
		Arguments: map[string]any{
			"name":     "daily-test",
			"schedule": "0 9 * * *",
			"channel":  "telegram:12345",
			"agent":    "test-agent",
			"skill":    "",
		},
	})
	if err != nil {
		t.Fatalf("call schedule_create: %v", err)
	}
	if result.IsError {
		text, _ := result.Content[0].(*mcp.TextContent)
		t.Fatalf("schedule_create failed: %s", text.Text)
	}

	// Verify it appears in schedule_list
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_list",
	})
	if err != nil {
		t.Fatalf("call schedule_list: %v", err)
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if !strings.Contains(text.Text, "daily-test") {
		t.Errorf("expected 'daily-test' in schedule list, got: %s", text.Text)
	}

	var schedules []map[string]any
	if err := json.Unmarshal([]byte(text.Text), &schedules); err != nil {
		t.Fatalf("unmarshal schedule list: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(schedules))
	}
	if schedules[0]["name"] != "daily-test" {
		t.Errorf("expected name=daily-test, got %v", schedules[0]["name"])
	}
	if schedules[0]["agent"] != "test-agent" {
		t.Errorf("expected agent=test-agent, got %v", schedules[0]["agent"])
	}

	// Delete the schedule
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "schedule_delete",
		Arguments: map[string]any{"name": "daily-test"},
	})
	if err != nil {
		t.Fatalf("call schedule_delete: %v", err)
	}
	if result.IsError {
		text, _ := result.Content[0].(*mcp.TextContent)
		t.Fatalf("schedule_delete failed: %s", text.Text)
	}

	// Verify it's gone
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_list",
	})
	if err != nil {
		t.Fatalf("call schedule_list after delete: %v", err)
	}
	text, _ = result.Content[0].(*mcp.TextContent)
	if text.Text != "[]" {
		t.Errorf("expected empty list after delete, got: %s", text.Text)
	}
}

func TestMCPServer_ScheduleUpdateLifecycle(t *testing.T) {
	deps := testDepsWithAgent(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	// Create a schedule
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_create",
		Arguments: map[string]any{
			"name":     "updatable",
			"schedule": "0 9 * * *",
			"channel":  "telegram:12345",
			"agent":    "test-agent",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if result.IsError {
		text, _ := result.Content[0].(*mcp.TextContent)
		t.Fatalf("create failed: %s", text.Text)
	}

	// Update the schedule expression
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_update",
		Arguments: map[string]any{
			"name":     "updatable",
			"schedule": "0 18 * * *",
			"enabled":  false,
		},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if result.IsError {
		text, _ := result.Content[0].(*mcp.TextContent)
		t.Fatalf("update failed: %s", text.Text)
	}

	// Verify the update via schedule_list
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_list",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	text, _ := result.Content[0].(*mcp.TextContent)
	var schedules []map[string]any
	if err := json.Unmarshal([]byte(text.Text), &schedules); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(schedules))
	}
	if schedules[0]["expression"] != "0 18 * * *" {
		t.Errorf("expected updated expression '0 18 * * *', got %v", schedules[0]["expression"])
	}
	if schedules[0]["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", schedules[0]["enabled"])
	}
}

func TestMCPServer_ScheduleCRUD_ScopeRequired(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	deps := mcpserver.Deps{
		Dispatcher: dispatcher,
		Scheduler:  scheduler.New(logger, time.UTC),
		TOMLKeys: []config.APIKeyConfig{
			{Name: "read-only", Key: "dk-schedread-key", Scopes: []string{"schedules:read"}},
		},
		Logger: logger,
	}
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-schedread-key")

	// schedule_list should work with schedules:read
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_list",
	})
	if err != nil {
		t.Fatalf("call schedule_list: %v", err)
	}
	if result.IsError {
		t.Error("schedule_list should succeed with schedules:read scope")
	}

	// schedule_create should fail without schedules:write
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "schedule_create",
		Arguments: map[string]any{
			"name":     "test",
			"schedule": "0 9 * * *",
			"channel":  "telegram:12345",
		},
	})
	if err != nil {
		t.Fatalf("call schedule_create: %v", err)
	}
	if !result.IsError {
		t.Error("schedule_create should fail without schedules:write scope")
	}

	// schedule_delete should fail without schedules:write
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "schedule_delete",
		Arguments: map[string]any{"name": "test"},
	})
	if err != nil {
		t.Fatalf("call schedule_delete: %v", err)
	}
	if !result.IsError {
		t.Error("schedule_delete should fail without schedules:write scope")
	}
}
