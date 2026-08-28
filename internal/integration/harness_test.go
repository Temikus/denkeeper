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
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
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

	// delay pauses every completion, so a test can catch a run mid-flight.
	// Honours the context, which is what makes cancellation observable.
	delay time.Duration
}

// SetDelay makes every subsequent completion take d, so a background run stays
// in flight long enough for a test to act on it.
func (m *mockProvider) SetDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = d
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) ChatCompletion(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	idx := m.calls
	m.calls++
	errs, responses, delay := m.errors, m.responses, m.delay
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Return errors if configured.
	if idx < len(errs) && errs[idx] != nil {
		return nil, errs[idx]
	}

	// Return responses in order; last one repeats.
	if len(responses) == 0 {
		return &llm.ChatResponse{
			Content:      "default mock response",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		}, nil
	}
	if idx >= len(responses) {
		idx = len(responses) - 1
	}
	return responses[idx], nil
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
	Auditor     audit.Emitter
	auditorBE   *audit.BufferedEmitter // concrete type for Flush/Close
	Approvals   *approval.Manager
	CostTracker *llm.CostTracker
	EvalStore   *eval.Store         // nil unless WithEval is set
	EvalRunner  *eval.Runner        // nil unless WithEval is set
	Sessions    *api.SessionManager // always wired by the harness
	KeyStore    *api.KeyStore       // nil unless WithKeyStore is set
	APIKey      string
	configPath  string
	config      *config.Holder
}

// ConfigPath returns the TOML config path used by this harness (empty if none).
func (h *Harness) ConfigPath() string { return h.configPath }

// Config returns the in-memory Config snapshot used by this harness. Tests
// mutate it directly during setup, which is safe because nothing is serving
// yet and the harness never swaps the pointer.
func (h *Harness) Config() *config.Config { return h.config.Get() }

// ConfigHolder returns the holder the API server reads through, for tests that
// need to publish a new snapshot mid-flight (hot reload).
func (h *Harness) ConfigHolder() *config.Holder { return h.config }

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

	// WithConfigMCP, when true, creates and registers a Config MCP server
	// for the first agent with channel tools wired from the dispatcher.
	// Requires Channels to be set.
	WithConfigMCP bool

	// WithAgentFactory, when true, populates deps.AgentFactory so that
	// agent CRUD endpoints can build engines at runtime.
	WithAgentFactory bool

	// PasswordHash sets the bcrypt password hash for /auth/login. The harness
	// always wires a SessionManager so session-based auth and cookie-bearing
	// requests work without extra opt-in.
	PasswordHash string

	// WithKeyStore, when true, constructs an in-memory KeyStore so that
	// /api/v1/keys CRUD endpoints are available.
	WithKeyStore bool

	// ModelLister, when non-nil, populates deps.ModelLister so /api/v1/models
	// returns the values produced by this function instead of 503.
	ModelLister func(ctx context.Context) []string

	// ModelDetailLister, when non-nil, populates deps.ModelDetailLister so
	// /api/v1/models/details returns the values produced by this function.
	ModelDetailLister func(ctx context.Context, providerFilter string) []llm.ModelInfo

	// ReloadFunc, when non-nil, populates deps.ReloadFunc so that
	// POST /api/v1/server/reload invokes this callback instead of returning 503.
	ReloadFunc func() error

	// RestartFunc, when non-nil, populates deps.RestartFunc so that
	// POST /api/v1/server/restart invokes this callback instead of returning 503.
	RestartFunc func() error

	// Adapters registers adapters with the dispatcher. Tests that need to
	// observe (or assert the absence of) adapter Send calls should provide a
	// recording mock here.
	Adapters []adapter.Adapter

	// WithEval, when true, builds an in-memory eval store and a runner over
	// the harness dispatcher and wires the OnPanic chain the way main.go does,
	// so the eval endpoints are live and the panic switch reaches runs.
	WithEval bool

	// EvalConfig overrides the runner's [eval] settings. Zero values take the
	// same defaults config.Load would apply.
	EvalConfig eval.Config

	// AutoApproveTools seeds config-scoped ("config") auto-approve rules,
	// agent name → tool names, standing in for the [[agents]]
	// auto_approve_tools TOML field that cmd/denkeeper wires at startup.
	AutoApproveTools map[string][]string
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
		"sessions:read", "sessions:write", "costs:read",
		"agents:read", "agents:write",
		"skills:read", "skills:write",
		"schedules:read", "schedules:write",
		"approvals:read", "approvals:write",
		"tools:read", "tools:write",
		"browser:read", "browser:write",
		"kv:read", "kv:write",
		"audit:read",
		"channels:read", "channels:write",
		"eval:read", "eval:write",
	}
}

// buildEvalDeps mirrors cmd/denkeeper/main.go: an eval store on the same DB,
// a runner over the live dispatcher (with the typed-nil guard), and the
// OnPanic hook that reaches active runs — eval turns are never in the
// dispatcher's inFlight map, so without this the panic switch would miss them.
func buildEvalDeps(t *testing.T, opts *HarnessOpts, dispatcher *agent.Dispatcher, auditor audit.Emitter, logger *slog.Logger) (*eval.Store, *eval.Runner) {
	t.Helper()
	store, err := eval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating eval store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := opts.EvalConfig
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 2
	}
	if cfg.MaxCostPerRun <= 0 {
		cfg.MaxCostPerRun = 2.0
	}
	if cfg.DefaultK < 1 {
		cfg.DefaultK = 3
	}
	if cfg.CompletenessFloor <= 0 {
		cfg.CompletenessFloor = 0.8
	}

	runner := eval.NewRunner(store, func(name string) (eval.Engine, bool) {
		e := dispatcher.Agent(name)
		if e == nil {
			return nil, false
		}
		return e, true
	}, auditor, cfg, logger)
	t.Cleanup(runner.Shutdown)

	prevPanic := dispatcher.OnPanic
	dispatcher.OnPanic = func() {
		if prevPanic != nil {
			prevPanic()
		}
		runner.StopAll()
	}
	return store, runner
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
	if len(opts.AutoApproveTools) > 0 {
		approvalMgr.SetConfigRules(context.Background(), opts.AutoApproveTools)
	}

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
	t.Cleanup(func() { auditor.Close() })

	// Wire the auditor into the approval manager so resolutions land in the
	// audit log (the runtime wires this in cmd/denkeeper/main.go).
	approvalMgr.Auditor = auditor

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
					skilltest.NewVersioned("greet", "Greeting skill", "1.0", []string{"command:hello"}, ""),
					skilltest.New("help", "Help system", nil, ""),
				},
			},
		}
	}

	// Pre-create ToolManager when Config MCP is requested so engines get it.
	if opts.WithConfigMCP && opts.ToolManager == nil {
		opts.ToolManager = tool.NewManager(logger)
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
		e.SetAuditor(auditor)
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
	dispatcher := agent.NewDispatcher(engines, bindings, opts.Adapters, logger, dispatcherOpts...)

	// Wire Config MCP channel tools into the ToolManager after dispatcher creation.
	if opts.WithConfigMCP && len(opts.Channels) > 0 {
		cmcpDeps := configmcp.Deps{
			AgentName:      agents[0].Name,
			PermissionTier: func() string { return "autonomous" },
			GetChannels: func() map[string]*agent.Channel {
				return dispatcher.Channels()
			},
			SetActiveChannel:         dispatcher.SetActiveChannelByKey,
			ActiveChannelsForChannel: dispatcher.ActiveChannelsForChannel,
			Logger:                   logger,
		}
		cmcpSrv := configmcp.New(cmcpDeps)
		session, err := cmcpSrv.Connect(context.Background())
		if err != nil {
			t.Fatalf("config MCP connect: %v", err)
		}
		t.Cleanup(func() { _ = session.Close() })
		if err := opts.ToolManager.RegisterSession(context.Background(), "config-test", session); err != nil {
			t.Fatalf("registering config MCP: %v", err)
		}
	}

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

	// Session manager: always wired so cookie-based auth, /auth/login, and
	// /auth/sessions work without per-test opt-in. Uses an in-memory SQLite
	// store so sessions are server-tracked.
	hexKey := strings.Repeat("0", 64) // 32 bytes (AES-256), test-only fixed key.
	sessionMgr, err := api.NewSessionManager(hexKey, 24*time.Hour, false)
	if err != nil {
		t.Fatalf("creating session manager: %v", err)
	}
	sessionStore, err := api.NewInMemorySessionStore()
	if err != nil {
		t.Fatalf("creating session store: %v", err)
	}
	t.Cleanup(func() { _ = sessionStore.Close() })
	sessionMgr.Store = sessionStore

	// API key store: enabled by WithKeyStore.
	var keyStore *api.KeyStore
	if opts.WithKeyStore {
		ks, err := api.NewInMemoryKeyStore()
		if err != nil {
			t.Fatalf("creating key store: %v", err)
		}
		keyStore = ks
	}

	var evalStore *eval.Store
	var evalRunner *eval.Runner
	if opts.WithEval {
		evalStore, evalRunner = buildEvalDeps(t, opts, dispatcher, auditor, logger)
	}

	deps := api.Deps{
		Dispatcher:        dispatcher,
		Scheduler:         sched,
		CostTracker:       costTracker,
		Memory:            mem,
		Approvals:         approvalMgr,
		KVStore:           kvStore,
		AuditStore:        auditStore,
		Auditor:           auditor,
		ConfigPath:        opts.ConfigPath,
		LifecycleMgr:      lifecycleMgr,
		Sessions:          sessionMgr,
		KeyStore:          keyStore,
		PasswordHash:      opts.PasswordHash,
		ModelLister:       opts.ModelLister,
		ModelDetailLister: opts.ModelDetailLister,
		ReloadFunc:        opts.ReloadFunc,
		RestartFunc:       opts.RestartFunc,
		EvalStore:         evalStore,
		EvalRunner:        evalRunner,
		Config: config.NewHolder(&config.Config{
			Agents: agentConfigs,
			Eval: config.EvalConfig{
				Audit:             "full",
				MaxConcurrent:     2,
				MaxCostPerRun:     2.0,
				DefaultK:          3,
				CompletenessFloor: 0.8,
			},
		}),
	}
	if opts.WithEval {
		if c := opts.EvalConfig; c.CompletenessFloor > 0 {
			deps.Config.Get().Eval.CompletenessFloor = c.CompletenessFloor
		}
	}

	if opts.WithAgentFactory {
		deps.AgentFactory = func(ac config.AgentInstanceConfig) (*agent.Engine, []agent.Binding, error) {
			tier := ac.SessionTier
			if tier == "" {
				tier = "supervised"
			}
			model := ac.LLMModel
			if model == "" {
				model = "test-model"
			}
			perms, err := security.NewPermissionEngine(tier)
			if err != nil {
				return nil, nil, err
			}
			router := llm.NewRouter("mock", model, costTracker)
			router.RegisterProvider(mock)
			e := agent.NewEngine(
				ac.Name, router, mem, nil, perms, nil,
				"You are "+ac.Name+" test agent.",
				nil, opts.ToolManager, approvalMgr, logger,
			)
			var bindings []agent.Binding
			for _, adapter := range ac.Adapters {
				bindings = append(bindings, agent.Binding{Pattern: adapter, AgentName: ac.Name})
			}
			return e, bindings, nil
		}
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
		auditorBE:   auditor,
		Approvals:   approvalMgr,
		CostTracker: costTracker,
		EvalStore:   evalStore,
		EvalRunner:  evalRunner,
		Sessions:    sessionMgr,
		KeyStore:    keyStore,
		APIKey:      apiKey,
		configPath:  opts.ConfigPath,
		config:      deps.Config,
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

// FlushAudit synchronously drains all buffered audit events to the store.
// Call before querying AuditStore in tests. The emitter remains usable.
func (h *Harness) FlushAudit(t *testing.T) {
	t.Helper()
	h.auditorBE.Flush()
}

// bcryptHashFor returns a bcrypt.MinCost hash of password. Test-only.
func bcryptHashFor(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}
	return string(hash)
}

// SessionLogin performs POST /auth/login with the given password and returns
// the dk_session cookie set by the response. Fails the test if login does not
// return 200 or the cookie is missing.
func (h *Harness) SessionLogin(t *testing.T, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := h.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "dk_session" {
			return c
		}
	}
	t.Fatalf("login: dk_session cookie not set")
	return nil
}

// CookieRequest creates a request authenticated with a session cookie instead
// of a bearer token.
func (h *Harness) CookieRequest(method, path string, body any, cookie *http.Cookie) *http.Request {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// BearerRequest creates a request authenticated with the given API key string.
// Useful for testing freshly-created keys (use h.AuthedRequest for the harness's
// bootstrap key).
func (h *Harness) BearerRequest(method, path string, body any, key string) *http.Request {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func boolPtr(b bool) *bool { return &b }
