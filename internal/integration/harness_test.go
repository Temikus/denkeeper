//go:build integration

// Package integration provides end-to-end tests that boot a full API server
// in-process with mock LLM providers and exercise core flows.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
)

// ---------------------------------------------------------------------------
// Configurable mock LLM provider
// ---------------------------------------------------------------------------

// mockProvider implements llm.Provider with configurable response sequences.
type mockProvider struct {
	mu        sync.Mutex
	responses []*llm.ChatResponse
	errors    []error
	calls     int
	requests  []llm.ChatRequest
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	idx := m.calls
	m.calls++

	// Return errors if configured.
	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	// Return responses in order; last one repeats.
	if len(m.responses) == 0 {
		return &llm.ChatResponse{
			Content:      "default mock response",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		}, nil
	}
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	return m.responses[idx], nil
}

func (m *mockProvider) HealthCheck(_ context.Context) error { return nil }

func (m *mockProvider) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockProvider) LastRequest() llm.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return llm.ChatRequest{}
	}
	return m.requests[len(m.requests)-1]
}

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

// Harness boots a full API server with in-memory stores and mock LLM.
type Harness struct {
	Server      *api.Server
	Handler     http.Handler
	MockLLM     *mockProvider
	Memory      *agent.SQLiteMemoryStore
	Dispatcher  *agent.Dispatcher
	Scheduler   *scheduler.Scheduler
	KVStore     kv.Store
	AuditStore  audit.Store
	Auditor     *audit.BufferedEmitter
	Approvals   *approval.Manager
	CostTracker *llm.CostTracker
	APIKey      string
}

// HarnessOpts allows customizing the harness setup.
type HarnessOpts struct {
	// Agents defines the agent configs. If nil, a single "default" agent is created.
	Agents []agentSetup

	// Responses configures the mock LLM response sequence.
	Responses []*llm.ChatResponse

	// Scopes configures the API key scopes. If nil, all scopes are granted.
	Scopes []string

	// ConfigPath sets the TOML config path for handlers that persist to disk
	// (schedules, tools). If empty, those handlers may return 503.
	ConfigPath string

	// WithLifecycleMgr, when true, creates a tool.LifecycleManager with an
	// empty tool.Manager so that tool CRUD endpoints are available.
	// Requires ConfigPath to be set for persistence.
	WithLifecycleMgr bool

	// Channels configures channel-based routing. When set, the dispatcher
	// uses channel resolution instead of legacy agent bindings.
	Channels []*agent.Channel

	// ToolManager, when set, is passed to all engines so tools are available
	// during chat (required for tool-call loop integration tests).
	ToolManager *tool.Manager
}

type agentSetup struct {
	Name        string
	Tier        string
	Adapters    []string
	Skills      []skill.Skill
	LLMModel    string
	Description string
}

func defaultResponse() *llm.ChatResponse {
	return &llm.ChatResponse{
		Content:      "Hello from mock!",
		TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
		Model:        "test-model",
		FinishReason: "stop",
	}
}

func allScopes() []string {
	return []string{
		"health", "admin", "chat",
		"sessions:read", "costs:read",
		"agents:read", "agents:write",
		"skills:read", "skills:write",
		"schedules:read", "schedules:write",
		"approvals:read", "approvals:write",
		"tools:read", "tools:write",
		"browser:read", "browser:write",
		"kv:read", "kv:write",
		"audit:read",
		"channels:read", "channels:write",
	}
}

// NewHarness creates and returns a fully wired test harness.
func NewHarness(t *testing.T, opts *HarnessOpts) *Harness {
	t.Helper()

	if opts == nil {
		opts = &HarnessOpts{}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Stores.
	mem, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	approvalMgr := approval.NewManager(approvalStore, logger)

	kvStore, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating kv store: %v", err)
	}

	auditStore, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating audit store: %v", err)
	}
	t.Cleanup(func() { _ = auditStore.Close() })

	auditor := audit.NewBufferedEmitter(auditStore, 100, logger)
	auditor.Start(context.Background())

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)

	// Mock LLM.
	mock := &mockProvider{}
	if len(opts.Responses) > 0 {
		mock.responses = opts.Responses
	} else {
		mock.responses = []*llm.ChatResponse{defaultResponse()}
	}

	// Build agents.
	agents := opts.Agents
	if len(agents) == 0 {
		agents = []agentSetup{
			{
				Name:     "default",
				Tier:     "supervised",
				Adapters: []string{"telegram"},
				Skills: []skill.Skill{
					{Name: "greet", Description: "Greeting skill", Version: "1.0", Triggers: []string{"command:hello"}},
					{Name: "help", Description: "Help system"},
				},
			},
		}
	}

	engines := make(map[string]*agent.Engine, len(agents))
	var bindings []agent.Binding
	var agentConfigs []config.AgentInstanceConfig

	for _, a := range agents {
		tier := a.Tier
		if tier == "" {
			tier = "supervised"
		}
		model := a.LLMModel
		if model == "" {
			model = "test-model"
		}

		perms, err := security.NewPermissionEngine(tier)
		if err != nil {
			t.Fatalf("creating permissions for %s: %v", a.Name, err)
		}

		router := llm.NewRouter("mock", model, costTracker)
		router.RegisterProvider(mock)

		e := agent.NewEngine(
			a.Name, router, mem, nil, perms, nil,
			"You are "+a.Name+" test agent.",
			a.Skills, opts.ToolManager, approvalMgr, logger,
		)
		engines[a.Name] = e

		for _, adapter := range a.Adapters {
			bindings = append(bindings, agent.Binding{Pattern: adapter, AgentName: a.Name})
		}

		agentConfigs = append(agentConfigs, config.AgentInstanceConfig{
			Name:        a.Name,
			Description: a.Description,
			Adapters:    a.Adapters,
			LLMModel:    model,
			SessionTier: tier,
		})
	}

	var dispatcherOpts []agent.DispatcherOption
	if len(opts.Channels) > 0 {
		dispatcherOpts = append(dispatcherOpts, agent.WithChannels(opts.Channels, mem))
	}
	dispatcher := agent.NewDispatcher(engines, bindings, nil, logger, dispatcherOpts...)
	sched := scheduler.New(logger, nil)

	// API key.
	apiKey := "dk-integration-test-key"
	scopes := opts.Scopes
	if len(scopes) == 0 {
		scopes = allScopes()
	}

	cfg := config.APIConfig{
		Enabled: boolPtr(true),
		Listen:  ":0",
		Keys: []config.APIKeyConfig{
			{Name: "integration-test", Key: apiKey, Scopes: scopes},
		},
	}

	var lifecycleMgr *tool.LifecycleManager
	if opts.WithLifecycleMgr {
		toolMgr := tool.NewManager(logger)
		lifecycleMgr = tool.NewLifecycleManager(toolMgr, opts.ConfigPath, 0, logger)
	}

	deps := api.Deps{
		Dispatcher:   dispatcher,
		Scheduler:    sched,
		CostTracker:  costTracker,
		Memory:       mem,
		Approvals:    approvalMgr,
		KVStore:      kvStore,
		AuditStore:   auditStore,
		Auditor:      auditor,
		ConfigPath:   opts.ConfigPath,
		LifecycleMgr: lifecycleMgr,
		Config: &config.Config{
			Agents: agentConfigs,
		},
	}

	srv := api.New(cfg, deps, logger)

	return &Harness{
		Server:      srv,
		Handler:     srv.HTTPHandler(),
		MockLLM:     mock,
		Memory:      mem,
		Dispatcher:  dispatcher,
		Scheduler:   sched,
		KVStore:     kvStore,
		AuditStore:  auditStore,
		Auditor:     auditor,
		Approvals:   approvalMgr,
		CostTracker: costTracker,
		APIKey:      apiKey,
	}
}

// Do executes an HTTP request against the harness and returns the response recorder.
func (h *Harness) Do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.Handler.ServeHTTP(rec, req)
	return rec
}

// AuthedRequest creates a request with the harness API key.
func (h *Harness) AuthedRequest(method, path string, body any) *http.Request {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// DecodeJSON decodes the response body into the provided target.
func DecodeJSON(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(target); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
}

// FlushAudit closes and re-creates the auditor so all buffered events are
// flushed to the store. Call before querying AuditStore in tests.
func (h *Harness) FlushAudit(t *testing.T) {
	t.Helper()
	h.Auditor.Close()
	// Re-create so subsequent emissions don't panic on closed channel.
	h.Auditor = audit.NewBufferedEmitter(h.AuditStore, 100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.Auditor.Start(context.Background())
}

func boolPtr(b bool) *bool { return &b }
