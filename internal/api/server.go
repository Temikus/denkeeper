package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/browser"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/scope"
	"github.com/Temikus/denkeeper/internal/tool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/crypto/bcrypt"
)

var (
	sseMeter       = otel.Meter("denkeeper.sse")
	sseActiveGauge metric.Int64UpDownCounter
)

func init() {
	sseActiveGauge, _ = sseMeter.Int64UpDownCounter("denkeeper.sse.active_streams",
		metric.WithDescription("Number of active SSE streaming connections"))
}

// Deps holds the application dependencies the API server needs to serve data.
type Deps struct {
	Dispatcher        *agent.Dispatcher
	Scheduler         *scheduler.Scheduler
	CostTracker       *llm.CostTracker
	Memory            agent.MemoryStore
	Config            *config.Config
	Approvals         *approval.Manager                                                // nil = approval endpoints return 503
	LifecycleMgr      *tool.LifecycleManager                                           // nil = tool CRUD endpoints return 503
	BrowserProfiles   *browser.ProfileService                                          // nil = browser endpoints return 503
	WebHandler        http.Handler                                                     // nil = no web dashboard served
	MetricsHandler    http.Handler                                                     // nil = no /metrics endpoint
	KeyStore          *KeyStore                                                        // nil = API key CRUD endpoints return 503
	KVStore           kv.Store                                                         // nil = KV endpoints return 503
	ConfigPath        string                                                           // TOML config path for schedule persistence
	Sessions          *SessionManager                                                  // nil = no session-based auth
	OIDCProvider      *OIDCProvider                                                    // nil = no OIDC endpoints
	PasswordHash      string                                                           // bcrypt hash for password login
	SetupPIN          string                                                           // one-time PIN for account setup (empty = disabled)
	ModelLister       func(ctx context.Context) []string                               // returns available LLM models; nil = endpoint returns 503
	ModelDetailLister func(ctx context.Context, providerFilter string) []llm.ModelInfo // returns enriched model metadata; nil = endpoint returns 503
	AuditStore        audit.Store                                                      // nil = audit endpoints return 503
	OAuthDeps         *OAuthDeps                                                       // nil = OAuth tool endpoints return 503
	ReloadFunc        func() error                                                     // nil = reload endpoint returns 503
	RestartFunc       func() error                                                     // nil = restart endpoint returns 503
	Version           string                                                           // build version (e.g. "1.2.3" or "dev")
	Commit            string                                                           // git commit hash
	BuildDate         string                                                           // build timestamp
}

// Server is the external REST API server.
type Server struct {
	httpServer *http.Server
	cfg        config.APIConfig
	deps       Deps
	logger     *slog.Logger

	// limiters tracks per-key rate limiter state.
	limiters   map[string]*rateLimiter
	limitersMu sync.Mutex

	// setupMu serialises the check+create in handleSetupInit to prevent a
	// TOCTOU race where two concurrent requests both see setup_required=true
	// and both succeed in creating a key.
	setupMu sync.Mutex

	// Auth: session-based login (password + OIDC).
	sessions     *SessionManager
	passwordHash string
	oidcProvider *OIDCProvider
	loginLimiter *loginRateLimiter

	// setupPIN is a one-time PIN for account creation, cleared after use.
	setupPIN string

	// wsHub manages active WebSocket connections. Nil when WebSocket is disabled.
	wsHub *WSHub

	// bcryptCost controls the bcrypt cost factor for password hashing.
	// Defaults to 13; tests override to bcrypt.MinCost for speed.
	bcryptCost int
}

// New creates a new API server. The server is not started until Run is called.
func New(cfg config.APIConfig, deps Deps, logger *slog.Logger) *Server {
	s := &Server{
		cfg:          cfg,
		deps:         deps,
		logger:       logger,
		limiters:     make(map[string]*rateLimiter),
		sessions:     deps.Sessions,
		passwordHash: deps.PasswordHash,
		oidcProvider: deps.OIDCProvider,
		loginLimiter: newLoginRateLimiter(cfg.GetLoginRateLimit(), cfg.GetLoginRateWindow()),
		setupPIN:     deps.SetupPIN,
		bcryptCost:   13,
	}

	mux := http.NewServeMux()

	// Health endpoint — no auth required.
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// Prometheus metrics endpoint — no auth required.
	if deps.MetricsHandler != nil {
		mux.Handle("GET /metrics", deps.MetricsHandler)
	}

	// Setup endpoints — no auth required; only functional when no keys/password exist.
	mux.HandleFunc("GET /api/v1/setup", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", s.handleSetupInit)
	mux.HandleFunc("POST /api/v1/setup/account", s.handleSetupAccount)

	// Chat endpoint.
	mux.HandleFunc("POST /api/v1/chat", s.RequireScope("chat", s.handleChat))

	// WebSocket streaming endpoint — auth handled inside the handler because
	// the upgrade must happen before RequireScope writes an HTTP response.
	if cfg.IsWebSocketEnabled() {
		s.wsHub = NewWSHub(cfg.WebSocketMaxConnections, cfg.WebSocketReplayTTL(), logger)
		mux.HandleFunc("GET /api/v1/ws", s.handleWebSocket)
		logger.Debug("ws: endpoint registered at /api/v1/ws")
	} else {
		logger.Warn("ws: endpoint disabled by config")
	}

	// Data endpoints — require auth with appropriate scopes.
	mux.HandleFunc("GET /api/v1/agents", s.RequireScope("admin", s.handleAgents))
	mux.HandleFunc("GET /api/v1/agents/{name}", s.RequireScope("admin", s.handleAgent))
	mux.HandleFunc("PATCH /api/v1/agents/{name}", s.RequireScope("agents:write", s.handleAgentConfigUpdate))
	mux.HandleFunc("GET /api/v1/agents/{name}/persona/{section}", s.RequireScope("agents:read", s.handleGetPersona))
	mux.HandleFunc("PUT /api/v1/agents/{name}/persona/{section}", s.RequireScope("agents:write", s.handleUpdatePersona))
	mux.HandleFunc("GET /api/v1/costs", s.RequireScope("costs:read", s.handleCosts))
	mux.HandleFunc("GET /api/v1/models", s.RequireScope("agents:read", s.handleModels))
	mux.HandleFunc("GET /api/v1/models/details", s.RequireScope("agents:read", s.handleModelDetails))
	mux.HandleFunc("GET /api/v1/skills", s.RequireScope("skills:read", s.handleSkills))
	mux.HandleFunc("GET /api/v1/skills/{agent}", s.RequireScope("skills:read", s.handleSkillsByAgent))
	mux.HandleFunc("GET /api/v1/skills/{agent}/{name}", s.RequireScope("skills:read", s.handleGetSkill))
	mux.HandleFunc("POST /api/v1/skills/{agent}", s.RequireScope("skills:write", s.handleCreateSkill))
	mux.HandleFunc("PUT /api/v1/skills/{agent}/{name}", s.RequireScope("skills:write", s.handleUpdateSkill))
	mux.HandleFunc("DELETE /api/v1/skills/{agent}/{name}", s.RequireScope("skills:write", s.handleDeleteSkill))
	mux.HandleFunc("GET /api/v1/schedules", s.RequireScope("schedules:read", s.handleSchedules))
	mux.HandleFunc("POST /api/v1/schedules", s.RequireScope("schedules:write", s.handleCreateSchedule))
	mux.HandleFunc("PATCH /api/v1/schedules/{name}", s.RequireScope("schedules:write", s.handleUpdateSchedule))
	mux.HandleFunc("DELETE /api/v1/schedules/{name}", s.RequireScope("schedules:write", s.handleDeleteSchedule))
	mux.HandleFunc("GET /api/v1/sessions", s.RequireScope("sessions:read", s.handleSessions))
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", s.RequireScope("sessions:read", s.handleSessionMessages))
	mux.HandleFunc("GET /api/v1/sessions/{id}/stats", s.RequireScope("sessions:read", s.handleSessionStats))
	mux.HandleFunc("GET /api/v1/sessions/{id}/tool-calls", s.RequireScope("sessions:read", s.handleSessionToolCalls))
	mux.HandleFunc("GET /api/v1/sessions/{id}/skills", s.RequireScope("sessions:read", s.handleSessionSkills))
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.RequireScope("sessions:read", s.handleDeleteSession))
	mux.HandleFunc("GET /api/v1/telemetry/summary", s.RequireScope("costs:read", s.handleTelemetrySummary))

	// Approval endpoints.
	mux.HandleFunc("GET /api/v1/approvals", s.RequireScope("approvals:read", s.handleListApprovals))
	mux.HandleFunc("GET /api/v1/approvals/{id}", s.RequireScope("approvals:read", s.handleGetApproval))
	mux.HandleFunc("POST /api/v1/approvals/{id}/approve", s.RequireScope("approvals:write", s.handleResolveApproval(true)))
	mux.HandleFunc("POST /api/v1/approvals/{id}/deny", s.RequireScope("approvals:write", s.handleResolveApproval(false)))

	// Auto-approve rule endpoints.
	mux.HandleFunc("GET /api/v1/auto-approve", s.RequireScope("approvals:read", s.handleListAutoApprove))
	mux.HandleFunc("POST /api/v1/auto-approve", s.RequireScope("approvals:write", s.handleCreateAutoApprove))
	mux.HandleFunc("DELETE /api/v1/auto-approve/{id}", s.RequireScope("approvals:write", s.handleDeleteAutoApprove))

	// Audit log endpoints.
	mux.HandleFunc("GET /api/v1/audit", s.RequireScope("audit:read", s.handleListAudit))
	mux.HandleFunc("GET /api/v1/audit/stats", s.RequireScope("audit:read", s.handleAuditStats))

	// Tool & plugin management endpoints.
	mux.HandleFunc("GET /api/v1/tools", s.RequireScope("tools:read", s.handleListTools))
	mux.HandleFunc("GET /api/v1/tools/{name}", s.RequireScope("tools:read", s.handleGetTool))
	mux.HandleFunc("POST /api/v1/tools", s.RequireScope("tools:write", s.handleAddTool))
	mux.HandleFunc("PUT /api/v1/tools/{name}", s.RequireScope("tools:write", s.handleUpdateTool))
	mux.HandleFunc("DELETE /api/v1/tools/{name}", s.RequireScope("tools:write", s.handleRemoveTool))
	mux.HandleFunc("GET /api/v1/tools/{name}/defs", s.RequireScope("tools:read", s.handleToolDefs))
	mux.HandleFunc("GET /api/v1/tools/{name}/health", s.RequireScope("tools:read", s.handleToolHealth))
	mux.HandleFunc("POST /api/v1/tools/{name}/restart", s.RequireScope("tools:write", s.handleRestartTool))

	// OAuth tool endpoints.
	mux.HandleFunc("GET /api/v1/tools/oauth/callback", s.handleOAuthCallback) // no auth (browser redirect)
	mux.HandleFunc("GET /api/v1/tools/oauth/pending", s.RequireScope("tools:read", s.handleListPendingOAuth))
	mux.HandleFunc("GET /api/v1/tools/{name}/oauth", s.RequireScope("tools:read", s.handleToolOAuthStatus))
	mux.HandleFunc("POST /api/v1/tools/{name}/oauth/connect", s.RequireScope("tools:write", s.handleToolOAuthConnect))
	mux.HandleFunc("DELETE /api/v1/tools/{name}/oauth/token", s.RequireScope("tools:write", s.handleToolOAuthRevoke))
	mux.HandleFunc("GET /api/v1/plugins", s.RequireScope("tools:read", s.handleListPlugins))
	mux.HandleFunc("GET /api/v1/plugins/{name}", s.RequireScope("tools:read", s.handleGetPlugin))
	mux.HandleFunc("POST /api/v1/plugins", s.RequireScope("tools:write", s.handleAddPlugin))
	mux.HandleFunc("DELETE /api/v1/plugins/{name}", s.RequireScope("tools:write", s.handleRemovePlugin))

	// Browser profile and session endpoints.
	mux.HandleFunc("GET /api/v1/browser/profiles", s.RequireScope("browser:read", s.handleListBrowserProfiles))
	mux.HandleFunc("GET /api/v1/browser/profiles/{name}", s.RequireScope("browser:read", s.handleGetBrowserProfile))
	mux.HandleFunc("DELETE /api/v1/browser/profiles/{name}", s.RequireScope("browser:write", s.handleDeleteBrowserProfile))
	mux.HandleFunc("GET /api/v1/browser/sessions", s.RequireScope("browser:read", s.handleListBrowserSessions))
	mux.HandleFunc("GET /api/v1/browser/config", s.RequireScope("browser:read", s.handleBrowserConfig))

	// KV store endpoints.
	mux.HandleFunc("GET /api/v1/kv/{agent}", s.RequireScope("kv:read", s.handleListKV))
	mux.HandleFunc("GET /api/v1/kv/{agent}/{key...}", s.RequireScope("kv:read", s.handleGetKV))
	mux.HandleFunc("PUT /api/v1/kv/{agent}/{key...}", s.RequireScope("kv:write", s.handleSetKV))
	mux.HandleFunc("DELETE /api/v1/kv/{agent}/{key...}", s.RequireScope("kv:write", s.handleDeleteKV))

	// LLM provider config endpoints (require admin scope).
	mux.HandleFunc("GET /api/v1/llm/providers", s.RequireScope("admin", s.handleGetLLMProviders))
	mux.HandleFunc("PATCH /api/v1/llm/providers/{name}", s.RequireScope("admin", s.handlePatchLLMProvider))
	mux.HandleFunc("PATCH /api/v1/llm/config", s.RequireScope("admin", s.handlePatchLLMConfig))

	// Server config endpoints (require admin scope).
	mux.HandleFunc("GET /api/v1/server/config", s.RequireScope("admin", s.handleGetServerConfig))
	mux.HandleFunc("PATCH /api/v1/server/config", s.RequireScope("admin", s.handlePatchServerConfig))
	mux.HandleFunc("POST /api/v1/server/reload", s.RequireScope("admin", s.handleReloadConfig))
	mux.HandleFunc("POST /api/v1/server/restart", s.RequireScope("admin", s.handleRestartProcess))

	// API key management endpoints (require admin scope).
	mux.HandleFunc("GET /api/v1/keys", s.RequireScope("admin", s.handleListKeys))
	mux.HandleFunc("POST /api/v1/keys", s.RequireScope("admin", s.handleCreateKey))
	mux.HandleFunc("DELETE /api/v1/keys/{id}", s.RequireScope("admin", s.handleRevokeKey))
	mux.HandleFunc("DELETE /api/v1/keys/{id}/permanent", s.RequireScope("admin", s.handleDeleteKey))
	mux.HandleFunc("POST /api/v1/keys/{id}/rotate", s.RequireScope("admin", s.handleRotateKey))

	// Auth endpoints — no auth required (login/logout/session check).
	mux.HandleFunc("GET /auth/config", s.handleAuthConfig)
	mux.HandleFunc("POST /auth/login", s.handlePasswordLogin)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /auth/session", s.handleSessionCheck)

	// Session management — requires admin scope.
	mux.HandleFunc("GET /api/v1/auth/sessions", s.RequireScope("admin", s.handleListSessions))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{id}", s.RequireScope("admin", s.handleRevokeSession))
	mux.HandleFunc("DELETE /api/v1/auth/sessions", s.RequireScope("admin", s.handleRevokeAllSessions))
	mux.HandleFunc("GET /api/v1/auth/status", s.RequireScope("admin", s.handleAuthStatus))
	mux.HandleFunc("POST /api/v1/auth/password", s.RequireScope("admin", s.handlePasswordChange))
	mux.HandleFunc("GET /api/v1/auth/oidc/test", s.RequireScope("admin", s.handleOIDCTest))
	mux.HandleFunc("POST /api/v1/auth/preferences", s.RequireScope("admin", s.handleAuthPreferences))
	mux.HandleFunc("GET /api/v1/onboarding", s.RequireScope("admin", s.handleOnboarding))
	mux.HandleFunc("POST /api/v1/onboarding/dismiss", s.RequireScope("admin", s.handleOnboardingDismiss))
	if s.oidcProvider != nil {
		mux.HandleFunc("GET /auth/oidc/login", s.oidcProvider.HandleLogin)
		mux.HandleFunc("GET /auth/callback", s.oidcProvider.HandleCallback)
	}

	// Web dashboard — catch-all for non-API paths (more-specific /api/v1/ routes always win).
	if deps.WebHandler != nil {
		mux.Handle("/", deps.WebHandler)
	}

	var handler http.Handler = mux
	handler = s.middlewareLogging(handler)
	handler = s.middlewareSecurityHeaders(handler)
	handler = s.middlewareCORS(handler)
	handler = s.middlewareRecover(handler)
	handler = otelhttp.NewHandler(handler, "denkeeper.http")

	s.httpServer = &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

// HTTPHandler returns the server's HTTP handler for use in tests.
func (s *Server) HTTPHandler() http.Handler {
	return s.httpServer.Handler
}

// Run starts the server and blocks until ctx is cancelled. It performs a
// graceful shutdown with a 5-second deadline.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", s.cfg.Listen, err)
	}

	// Start periodic replay-buffer cleanup (stops when ctx is cancelled).
	if s.wsHub != nil {
		s.wsHub.StartCleanup(ctx)
	}

	// Start periodic session record cleanup (stops when ctx is cancelled).
	if s.sessions != nil && s.sessions.Store != nil {
		s.sessions.Store.StartCleanup(ctx, 6*time.Hour, s.logger)
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("api server starting", "addr", s.cfg.Listen, "tls", s.cfg.TLS)
		if s.cfg.TLS {
			errCh <- s.httpServer.ServeTLS(ln, s.cfg.CertFile, s.cfg.KeyFile)
		} else {
			errCh <- s.httpServer.Serve(ln)
		}
	}()

	select {
	case <-ctx.Done():
		// Close all WebSocket connections before HTTP shutdown.
		if s.wsHub != nil {
			s.wsHub.Shutdown()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("api server shutdown error", "error", err)
			return fmt.Errorf("api: shutdown: %w", err)
		}
		s.logger.Info("api server stopped")
		return nil
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("api: serve: %w", err)
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"ws_enabled": s.wsHub != nil,
	})
}

// handleAgents lists all registered agents with metadata.
func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request) {
	type agentInfo struct {
		Name           string   `json:"name"`
		DisplayName    string   `json:"display_name"`
		PermissionTier string   `json:"permission_tier"`
		Provider       string   `json:"provider"`
		Model          string   `json:"model"`
		SkillCount     int      `json:"skill_count"`
		HasTools       bool     `json:"has_tools"`
		Adapters       []string `json:"adapters,omitempty"`
	}

	names := s.deps.Dispatcher.Agents()
	agents := make([]agentInfo, 0, len(names))
	// Look up configured adapter bindings for each agent.
	bindingMap := make(map[string][]string)
	for _, ac := range s.deps.Config.Agents {
		bindingMap[ac.Name] = ac.Adapters
	}
	for _, name := range names {
		e := s.deps.Dispatcher.Agent(name)
		if e == nil {
			continue
		}
		agents = append(agents, agentInfo{
			Name:           e.Name(),
			DisplayName:    e.DisplayName(),
			PermissionTier: e.PermissionTier(),
			Provider:       e.ProviderName(),
			Model:          e.ModelName(),
			SkillCount:     len(e.Skills()),
			HasTools:       e.HasTools(),
			Adapters:       bindingMap[name],
		})
	}
	writeJSON(w, http.StatusOK, agents)
}

// handleAgent returns details for a single agent.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	e := s.deps.Dispatcher.Agent(name)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	type skillInfo struct {
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Version     string   `json:"version,omitempty"`
		Triggers    []string `json:"triggers,omitempty"`
	}

	skills := e.Skills()
	skillList := make([]skillInfo, len(skills))
	for i, sk := range skills {
		skillList[i] = skillInfo{
			Name:        sk.Name,
			Description: sk.Description,
			Version:     sk.Version,
			Triggers:    sk.Triggers,
		}
	}

	var adapters []string
	var fallbacks []config.FallbackConfig
	for _, ac := range s.deps.Config.Agents {
		if ac.Name == name {
			adapters = ac.Adapters
			fallbacks = ac.Fallbacks
			break
		}
	}
	if fallbacks == nil {
		fallbacks = []config.FallbackConfig{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":             e.Name(),
		"display_name":     e.DisplayName(),
		"permission_tier":  e.PermissionTier(),
		"provider":         e.ProviderName(),
		"model":            e.ModelName(),
		"has_tools":        e.HasTools(),
		"adapters":         adapters,
		"skills":           skillList,
		"tool_names":       e.ToolNames(),
		"persona_dir":      e.PersonaDir(),
		"persona_sections": e.PersonaSections(),
		"fallbacks":        fallbacks,
	})
}

// handleCosts returns cost tracking data with per-agent breakdown.
func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	sessions := s.deps.CostTracker.AllSessionStats()
	agentFilter := r.URL.Query().Get("agent")

	// Filter by agent if requested.
	filtered := make(map[string]llm.SessionStats, len(sessions))
	for id, stats := range sessions {
		if agentFilter == "" || agentFromSession(id) == agentFilter {
			filtered[id] = stats
		}
	}

	byAgent := s.deps.CostTracker.AgentCosts()
	if agentFilter != "" {
		var one []llm.AgentStats
		for _, a := range byAgent {
			if a.Agent == agentFilter {
				one = append(one, a)
				break
			}
		}
		byAgent = one
	}

	// Build pricing config summary.
	pricingConfig := map[string]any{
		"fallback_rate_per_1k_tokens": 0.0,
		"custom_model_count":          0,
	}
	if s.deps.Config != nil {
		pricingConfig["fallback_rate_per_1k_tokens"] = s.deps.Config.Costs.DefaultRatePerKTokens
		pricingConfig["custom_model_count"] = len(s.deps.Config.Costs.ModelPrices)
	}

	limits := s.deps.CostTracker.DefaultLimits()
	writeJSON(w, http.StatusOK, map[string]any{
		"global_cost":     s.deps.CostTracker.GlobalCost(),
		"max_per_session": s.deps.CostTracker.MaxBudgetPerSession(), // deprecated, kept for compat
		"cost_limits": map[string]float64{
			"soft": limits.Soft,
			"hard": limits.Hard,
		},
		"session_count":  len(filtered),
		"session_costs":  s.deps.CostTracker.AllSessionCosts(),
		"session_stats":  filtered,
		"by_agent":       byAgent,
		"pricing_config": pricingConfig,
	})
}

// handleModels returns available LLM models from all configured providers.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if s.deps.ModelLister == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model listing not available"})
		return
	}
	models := s.deps.ModelLister(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// handleModelDetails returns enriched LLM model metadata from all configured providers.
// Accepts optional ?provider= query parameter to fetch only that provider's models.
func (s *Server) handleModelDetails(w http.ResponseWriter, r *http.Request) {
	if s.deps.ModelDetailLister == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model details not available"})
		return
	}
	providerFilter := r.URL.Query().Get("provider")
	models := s.deps.ModelDetailLister(r.Context(), providerFilter)
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// agentFromSession extracts the agent name from a session ID ("agent:adapter:ext").
func agentFromSession(id string) string {
	for i, c := range id {
		if c == ':' {
			return id[:i]
		}
	}
	return id
}

// handleSkills lists all skills across all agents (deduplicated by name).
func (s *Server) handleSkills(w http.ResponseWriter, _ *http.Request) {
	type skillInfo struct {
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Version     string   `json:"version,omitempty"`
		Triggers    []string `json:"triggers,omitempty"`
		Agent       string   `json:"agent"`
	}

	all := make([]skillInfo, 0)
	seen := make(map[string]bool)
	for _, name := range s.deps.Dispatcher.Agents() {
		e := s.deps.Dispatcher.Agent(name)
		if e == nil {
			continue
		}
		for _, sk := range e.Skills() {
			key := name + ":" + sk.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, skillInfo{
				Name:        sk.Name,
				Description: sk.Description,
				Version:     sk.Version,
				Triggers:    sk.Triggers,
				Agent:       name,
			})
		}
	}
	writeJSON(w, http.StatusOK, all)
}

// handleSkillsByAgent lists skills for a specific agent.
func (s *Server) handleSkillsByAgent(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agent")
	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	type skillInfo struct {
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Version     string   `json:"version,omitempty"`
		Triggers    []string `json:"triggers,omitempty"`
	}

	skills := e.Skills()
	out := make([]skillInfo, len(skills))
	for i, sk := range skills {
		out[i] = skillInfo{
			Name:        sk.Name,
			Description: sk.Description,
			Version:     sk.Version,
			Triggers:    sk.Triggers,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSchedules lists all registered schedule entries.
func (s *Server) handleSchedules(w http.ResponseWriter, _ *http.Request) {
	entries := s.deps.Scheduler.Entries()

	type scheduleInfo struct {
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Expression  string   `json:"expression"`
		Skill       string   `json:"skill,omitempty"`
		Agent       string   `json:"agent,omitempty"`
		SessionTier string   `json:"session_tier,omitempty"`
		SessionMode string   `json:"session_mode,omitempty"`
		Channel     string   `json:"channel,omitempty"`
		Tags        []string `json:"tags,omitempty"`
		Enabled     bool     `json:"enabled"`
		LastRun     string   `json:"last_run,omitempty"`
		NextRun     string   `json:"next_run,omitempty"`
	}

	out := make([]scheduleInfo, len(entries))
	for i, e := range entries {
		info := scheduleInfo{
			Name:        e.Name,
			Type:        string(e.Type),
			Expression:  e.Expr,
			Skill:       e.Skill,
			Agent:       e.Agent,
			SessionTier: e.SessionTier,
			SessionMode: e.SessionMode,
			Channel:     e.Channel,
			Tags:        e.Tags,
			Enabled:     e.Enabled,
		}
		if !e.LastRun.IsZero() {
			info.LastRun = e.LastRun.Format(time.RFC3339)
		}
		if !e.NextRun.IsZero() {
			info.NextRun = e.NextRun.Format(time.RFC3339)
		}
		out[i] = info
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSessions lists all conversations from the memory store.
// When backed by a TelemetryStore, includes telemetry stats.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if store, ok := s.deps.Memory.(agent.TelemetryStore); ok {
		convos, err := store.ListConversationsWithStats(r.Context())
		if err != nil {
			s.logger.Error("listing conversations with stats", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if convos == nil {
			convos = []agent.ConversationInfoWithStats{}
		}
		writeJSON(w, http.StatusOK, convos)
		return
	}
	convos, err := s.deps.Memory.ListConversations(r.Context())
	if err != nil {
		s.logger.Error("listing conversations", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if convos == nil {
		convos = []agent.ConversationInfo{}
	}
	writeJSON(w, http.StatusOK, convos)
}

// chatRequest is the JSON body for POST /api/v1/chat.
type chatRequest struct {
	Agent     string `json:"agent"`      // optional; defaults to "default"
	SessionID string `json:"session_id"` // optional; generated if blank
	Message   string `json:"message"`    // required
	UserID    string `json:"user_id"`    // optional
	UserName  string `json:"user_name"`  // optional
}

// chatResponse is the JSON body returned by POST /api/v1/chat (non-streaming).
type chatResponse struct {
	SessionID string `json:"session_id"`
	Response  string `json:"response"`
}

// handleChat handles POST /api/v1/chat. It accepts a JSON body describing the
// message and returns the agent's response. When the request includes
// Accept: text/event-stream, the response is streamed as Server-Sent Events.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	if len(req.Message) > maxChatMessageLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("message exceeds maximum length of %d bytes", maxChatMessageLen),
		})
		return
	}

	agentName := req.Agent
	if agentName == "" {
		agentName = "default"
	}
	eng := s.deps.Dispatcher.Agent(agentName)
	if eng == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = generateID()
	}

	msg := adapter.IncomingMessage{
		Adapter:        "api",
		ExternalID:     sessionID,
		UserID:         req.UserID,
		UserName:       req.UserName,
		Text:           req.Message,
		Timestamp:      time.Now(),
		ConversationID: sessionID,
	}

	if r.Header.Get("Accept") == "text/event-stream" {
		s.handleChatSSE(w, r, eng, msg, sessionID)
		return
	}

	responseText, err := eng.Chat(r.Context(), msg)
	if err != nil {
		s.logger.Error("chat error", "error", err, "agent", agentName, "session", sessionID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to process message"})
		return
	}

	writeJSON(w, http.StatusOK, chatResponse{
		SessionID: sessionID,
		Response:  responseText,
	})
}

// handleChatSSE streams the response as Server-Sent Events.
func (s *Server) handleChatSSE(w http.ResponseWriter, r *http.Request, eng *agent.Engine, msg adapter.IncomingMessage, sessionID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	sseActiveGauge.Add(r.Context(), 1)
	defer sseActiveGauge.Add(r.Context(), -1)

	stream := NewSSEStreamSession(w)
	s.runChatStream(r.Context(), stream, eng, msg, sessionID)
}

// handleDeleteSession handles DELETE /api/v1/sessions/{id}.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.Memory.DeleteConversation(r.Context(), id); err != nil {
		s.logger.Error("deleting conversation", "error", err, "session", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionMessages returns messages for a specific conversation.
func (s *Server) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	messages, err := s.deps.Memory.GetMessages(r.Context(), id, 200)
	if err != nil {
		s.logger.Error("getting messages", "error", err, "session", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	type messageInfo struct {
		Role             string    `json:"role"`
		Content          string    `json:"content"`
		TokensUsed       int       `json:"tokens_used,omitempty"`
		Cost             float64   `json:"cost,omitempty"`
		Model            string    `json:"model,omitempty"`
		Provider         string    `json:"provider,omitempty"`
		TokensPrompt     int       `json:"tokens_prompt,omitempty"`
		TokensCompletion int       `json:"tokens_completion,omitempty"`
		TokensCached     int       `json:"tokens_cached,omitempty"`
		CreatedAt        time.Time `json:"created_at"`
	}

	out := make([]messageInfo, len(messages))
	for i, m := range messages {
		out[i] = messageInfo{
			Role:             m.Role,
			Content:          m.Content,
			TokensUsed:       m.TokensUsed,
			Cost:             m.Cost,
			Model:            m.Model,
			Provider:         m.Provider,
			TokensPrompt:     m.TokensPrompt,
			TokensCompletion: m.TokensCompletion,
			TokensCached:     m.TokensCached,
			CreatedAt:        m.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSessionStats returns aggregated telemetry for a conversation.
func (s *Server) handleSessionStats(w http.ResponseWriter, r *http.Request) {
	store, ok := s.deps.Memory.(agent.TelemetryStore)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "telemetry not available"})
		return
	}
	id := r.PathValue("id")
	stats, err := store.GetConversationStats(r.Context(), id)
	if err != nil {
		s.logger.Error("getting conversation stats", "error", err, "session", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if stats == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no stats for session"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleSessionToolCalls returns tool call records for a conversation.
func (s *Server) handleSessionToolCalls(w http.ResponseWriter, r *http.Request) {
	store, ok := s.deps.Memory.(agent.TelemetryStore)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "telemetry not available"})
		return
	}
	id := r.PathValue("id")
	records, err := store.GetToolCalls(r.Context(), id)
	if err != nil {
		s.logger.Error("getting tool calls", "error", err, "session", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if records == nil {
		records = []agent.ToolCallRecord{}
	}
	writeJSON(w, http.StatusOK, records)
}

// handleSessionSkills returns skill usage records for a conversation.
func (s *Server) handleSessionSkills(w http.ResponseWriter, r *http.Request) {
	store, ok := s.deps.Memory.(agent.TelemetryStore)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "telemetry not available"})
		return
	}
	id := r.PathValue("id")
	records, err := store.GetSkillUsages(r.Context(), id)
	if err != nil {
		s.logger.Error("getting skill usages", "error", err, "session", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if records == nil {
		records = []agent.SkillUsageRecord{}
	}
	writeJSON(w, http.StatusOK, records)
}

// handleTelemetrySummary returns aggregated telemetry for A/B comparison.
func (s *Server) handleTelemetrySummary(w http.ResponseWriter, r *http.Request) {
	store, ok := s.deps.Memory.(agent.TelemetryStore)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "telemetry not available"})
		return
	}

	var since, until *time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = &t
		}
	}
	if v := r.URL.Query().Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = &t
		}
	}

	summary, err := store.GetTelemetrySummary(r.Context(), since, until)
	if err != nil {
		s.logger.Error("getting telemetry summary", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// ---------------------------------------------------------------------------
// Approval handlers
// ---------------------------------------------------------------------------

// approvalNotConfigured writes a 503 when the approval manager is not set.
func (s *Server) approvalNotConfigured(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "approvals not configured"})
}

// handleListApprovals handles GET /api/v1/approvals?status=<status>.
func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	if s.deps.Approvals == nil {
		s.approvalNotConfigured(w)
		return
	}
	status := approval.Status(r.URL.Query().Get("status"))
	if !approval.ValidStatus(status) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid status: must be one of pending, approved, denied, expired (or empty for all)",
		})
		return
	}
	reqs, err := s.deps.Approvals.List(r.Context(), status)
	if err != nil {
		s.logger.Error("listing approvals", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if reqs == nil {
		reqs = []approval.Request{}
	}
	writeJSON(w, http.StatusOK, reqs)
}

// handleGetApproval handles GET /api/v1/approvals/{id}.
func (s *Server) handleGetApproval(w http.ResponseWriter, r *http.Request) {
	if s.deps.Approvals == nil {
		s.approvalNotConfigured(w)
		return
	}
	id := r.PathValue("id")
	req, err := s.deps.Approvals.Get(r.Context(), id)
	if err != nil {
		if err == approval.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		s.logger.Error("getting approval", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// handleResolveApproval returns a handler for POST /api/v1/approvals/{id}/approve|deny.
func (s *Server) handleResolveApproval(approved bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Approvals == nil {
			s.approvalNotConfigured(w)
			return
		}
		id := r.PathValue("id")
		resolved, err := s.deps.Approvals.Resolve(r.Context(), id, approved, "api")
		if err != nil {
			switch err {
			case approval.ErrNotFound:
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			case approval.ErrAlreadyResolved:
				writeJSON(w, http.StatusConflict, map[string]string{"error": "already resolved"})
			default:
				s.logger.Error("resolving approval", "id", id, "approved", approved, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
			return
		}

		// If approved with auto_approve param, create an auto-approve rule.
		if approved {
			if autoScope := r.URL.Query().Get("auto_approve"); autoScope != "" {
				toolName := approval.ExtractToolName(resolved.Summary)
				if toolName != "" && resolved.Kind == approval.ActionKindToolCall {
					switch autoScope {
					case "session":
						s.deps.Approvals.AddSessionRule(r.Context(), resolved.AgentName, toolName, resolved.ConversationID, "api")
					case "permanent":
						if _, aaErr := s.deps.Approvals.AddPermanentRule(r.Context(), resolved.AgentName, toolName, "api"); aaErr != nil {
							s.logger.Error("creating auto-approve rule via approval", "error", aaErr)
						}
					}
				}
			}
		}

		// Notify the originating adapter channel of the resolution.
		action := "Denied"
		if approved {
			action = "Approved"
		}
		notifyMsg := fmt.Sprintf("%s via API: %s", action, resolved.Summary)
		if err := s.deps.Dispatcher.SendVia(r.Context(), resolved.AdapterName, adapter.OutgoingMessage{
			ExternalID: resolved.ExternalID,
			Text:       notifyMsg,
		}); err != nil {
			// Non-fatal: the action was already applied; just log.
			s.logger.Warn("failed to send approval notification", "id", id, "error", err)
		}

		writeJSON(w, http.StatusOK, resolved)
	}
}

// handleListAutoApprove handles GET /api/v1/auto-approve?agent={name}.
func (s *Server) handleListAutoApprove(w http.ResponseWriter, r *http.Request) {
	if s.deps.Approvals == nil {
		s.approvalNotConfigured(w)
		return
	}
	agentName := r.URL.Query().Get("agent")
	rules, err := s.deps.Approvals.ListAutoApproveRules(r.Context(), agentName)
	if err != nil {
		s.logger.Error("listing auto-approve rules", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

// handleCreateAutoApprove handles POST /api/v1/auto-approve.
func (s *Server) handleCreateAutoApprove(w http.ResponseWriter, r *http.Request) {
	if s.deps.Approvals == nil {
		s.approvalNotConfigured(w)
		return
	}
	var body struct {
		Agent          string `json:"agent"`
		Tool           string `json:"tool"`
		Scope          string `json:"scope"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.Agent == "" || body.Tool == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent and tool are required"})
		return
	}

	switch body.Scope {
	case "session":
		if body.ConversationID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id required for session scope"})
			return
		}
		s.deps.Approvals.AddSessionRule(r.Context(), body.Agent, body.Tool, body.ConversationID, "api")
		writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "scope": "session"})
	case "permanent", "":
		rule, err := s.deps.Approvals.AddPermanentRule(r.Context(), body.Agent, body.Tool, "api")
		if err != nil {
			s.logger.Error("creating auto-approve rule", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusCreated, rule)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope must be 'session' or 'permanent'"})
	}
}

// handleDeleteAutoApprove handles DELETE /api/v1/auto-approve/{id}.
func (s *Server) handleDeleteAutoApprove(w http.ResponseWriter, r *http.Request) {
	if s.deps.Approvals == nil {
		s.approvalNotConfigured(w)
		return
	}
	id := r.PathValue("id")
	if err := s.deps.Approvals.RemoveAutoApproveRule(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		s.logger.Error("deleting auto-approve rule", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Auth middleware
// ---------------------------------------------------------------------------

// contextKey is an unexported type used for context value keys.
type contextKey string

const keyNameKey contextKey = "api_key_name"

// RequireScope returns middleware that checks for a valid API key with the
// required scope. Use this to wrap individual route handlers.
func (s *Server) RequireScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keyName, ok := s.authenticate(r.Context(), r, scope)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		// Rate limiting (per key).
		if s.cfg.RateLimit > 0 {
			if !s.allowRequest(keyName) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
				return
			}
		}

		ctx := context.WithValue(r.Context(), keyNameKey, keyName)
		next(w, r.WithContext(ctx))
	}
}

// authenticate checks the Authorization header for a valid API key with the
// given scope. SQLite-managed keys are checked first, then TOML keys.
// Returns the key name and true if valid.
func (s *Server) authenticate(ctx context.Context, r *http.Request, scope string) (string, bool) {
	// 1. Try Bearer token authentication first.
	if name, ok := s.authenticateBearer(ctx, r, scope); ok {
		return name, true
	}

	// 2. Fall back to session cookie authentication.
	if s.sessions != nil {
		if sess, err := s.sessions.Read(r); err == nil {
			for _, sc := range sess.Scopes {
				if sc == scope {
					return sess.Email, true
				}
			}
			return "", false // session valid but scope missing
		}
	}

	return "", false
}

func (s *Server) authenticateBearer(ctx context.Context, r *http.Request, scope string) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}

	token := strings.TrimPrefix(header, "Bearer ")
	if token == header {
		return "", false // no "Bearer " prefix
	}

	// Check SQLite-managed keys first (allows runtime key management without restart).
	if s.deps.KeyStore != nil {
		sk, _ := s.deps.KeyStore.FindActiveByHash(ctx, hashToken(token))
		if sk != nil {
			var scopes []string
			_ = json.Unmarshal([]byte(sk.ScopesJSON), &scopes)
			for _, sc := range scopes {
				if sc == scope {
					go s.deps.KeyStore.TouchLastUsed(context.WithoutCancel(ctx), sk.ID)
					return sk.Name, true
				}
			}
			return "", false // key valid but scope missing
		}
	}

	// Fall back to TOML-configured keys (backward-compatible).
	for _, k := range s.cfg.Keys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(k.Key)) == 1 {
			for _, s := range k.Scopes {
				if s == scope {
					return k.Name, true
				}
			}
			return "", false // key valid but scope missing
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Tool & plugin handlers
// ---------------------------------------------------------------------------

// lifecycleRequired writes 503 when the lifecycle manager is not configured.
func (s *Server) lifecycleRequired(w http.ResponseWriter) bool {
	if s.deps.LifecycleMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool management not configured"})
		return false
	}
	return true
}

func (s *Server) handleListTools(w http.ResponseWriter, _ *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	tools := s.deps.LifecycleMgr.ListTools()
	if tools == nil {
		tools = []tool.ServerStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

func (s *Server) handleGetTool(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	info, ok := s.deps.LifecycleMgr.ToolManager().ServerInfo(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tool not found"})
		return
	}

	// Include config fields for edit pre-population.
	resp := map[string]any{
		"name":          info.Name,
		"command":       info.Command,
		"args_count":    info.ArgsCount,
		"tool_names":    info.ToolNames,
		"status":        info.Status,
		"transport":     info.Transport,
		"url":           info.URL,
		"restart_count": info.RestartCount,
		"last_error":    info.LastError,
		"uptime_secs":   info.UptimeSecs,
	}
	if info.AuthType != "" {
		resp["auth_type"] = info.AuthType
	}
	if info.OAuthStatus != nil {
		resp["oauth_status"] = info.OAuthStatus
	}
	if cfg, ok := s.deps.LifecycleMgr.ToolManager().ServerToolConfig(name); ok {
		resp["args"] = cfg.Args
		resp["env"] = cfg.Env
		resp["headers"] = cfg.Headers
		resp["request_timeout_secs"] = cfg.RequestTimeoutSecs
		resp["sse_keep_alive_secs"] = cfg.SSEKeepAliveSecs
		if cfg.Auth != "" {
			resp["auth"] = cfg.Auth
		}
		if cfg.ClientID != "" {
			resp["client_id"] = cfg.ClientID
		}
		if len(cfg.Scopes) > 0 {
			resp["scopes"] = cfg.Scopes
		}
		if cfg.AllowLoopback {
			resp["allow_loopback"] = true
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleToolDefs(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	defs, ok := s.deps.LifecycleMgr.ToolManager().ServerToolDefs(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tool server not found"})
		return
	}

	type toolDefResp struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	}
	result := make([]toolDefResp, 0, len(defs))
	for _, td := range defs {
		result = append(result, toolDefResp{
			Name:        td.Function.Name,
			Description: td.Function.Description,
			Parameters:  td.Function.Parameters,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": result})
}

func (s *Server) handleAddTool(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	var body struct {
		Name               string            `json:"name"`
		Command            string            `json:"command"`
		Args               []string          `json:"args"`
		Env                map[string]string `json:"env"`
		Transport          string            `json:"transport"`
		URL                string            `json:"url"`
		Headers            map[string]string `json:"headers"`
		RequestTimeoutSecs int               `json:"request_timeout_secs"`
		SSEKeepAliveSecs   int               `json:"sse_keep_alive_secs"`
		Auth               string            `json:"auth"`
		ClientID           string            `json:"client_id"`
		ClientSecret       string            `json:"client_secret"`
		Scopes             []string          `json:"scopes"`
		AllowLoopback      bool              `json:"allow_loopback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}

	cfg := config.ToolConfig{
		Command:            body.Command,
		Args:               body.Args,
		Env:                body.Env,
		Transport:          body.Transport,
		URL:                body.URL,
		Headers:            body.Headers,
		RequestTimeoutSecs: body.RequestTimeoutSecs,
		SSEKeepAliveSecs:   body.SSEKeepAliveSecs,
		Auth:               body.Auth,
		ClientID:           body.ClientID,
		ClientSecret:       body.ClientSecret,
		Scopes:             body.Scopes,
		AllowLoopback:      body.AllowLoopback,
	}

	if err := s.deps.LifecycleMgr.AddTool(r.Context(), body.Name, cfg); err != nil {
		s.logger.Error("adding tool", "name", body.Name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	info, _ := s.deps.LifecycleMgr.ToolManager().ServerInfo(body.Name)
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleUpdateTool(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	var body struct {
		Command            string            `json:"command"`
		Args               []string          `json:"args"`
		Env                map[string]string `json:"env"`
		Transport          string            `json:"transport"`
		URL                string            `json:"url"`
		Headers            map[string]string `json:"headers"`
		RequestTimeoutSecs int               `json:"request_timeout_secs"`
		SSEKeepAliveSecs   int               `json:"sse_keep_alive_secs"`
		Auth               string            `json:"auth"`
		ClientID           string            `json:"client_id"`
		ClientSecret       string            `json:"client_secret"`
		Scopes             []string          `json:"scopes"`
		AllowLoopback      bool              `json:"allow_loopback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	cfg := config.ToolConfig{
		Command:            body.Command,
		Args:               body.Args,
		Env:                body.Env,
		Transport:          body.Transport,
		URL:                body.URL,
		Headers:            body.Headers,
		RequestTimeoutSecs: body.RequestTimeoutSecs,
		SSEKeepAliveSecs:   body.SSEKeepAliveSecs,
		Auth:               body.Auth,
		ClientID:           body.ClientID,
		ClientSecret:       body.ClientSecret,
		Scopes:             body.Scopes,
		AllowLoopback:      body.AllowLoopback,
	}

	if err := s.deps.LifecycleMgr.UpdateTool(r.Context(), name, cfg); err != nil {
		s.logger.Error("updating tool", "name", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	info, _ := s.deps.LifecycleMgr.ToolManager().ServerInfo(name)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleRemoveTool(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	if err := s.deps.LifecycleMgr.RemoveTool(r.Context(), name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleToolHealth(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	info, ok := s.deps.LifecycleMgr.ToolManager().ServerInfo(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tool not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":          info.Name,
		"status":        info.Status,
		"transport":     info.Transport,
		"restart_count": info.RestartCount,
		"last_error":    info.LastError,
		"uptime_secs":   info.UptimeSecs,
	})
}

func (s *Server) handleRestartTool(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	if err := s.deps.LifecycleMgr.RestartTool(r.Context(), name); err != nil {
		s.logger.Error("restarting tool", "name", name, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	info, _ := s.deps.LifecycleMgr.ToolManager().ServerInfo(name)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleListPlugins(w http.ResponseWriter, _ *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	plugins := s.deps.LifecycleMgr.ListPlugins()
	if plugins == nil {
		plugins = []tool.PluginStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": plugins})
}

func (s *Server) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	plugins := s.deps.LifecycleMgr.ListPlugins()
	for _, p := range plugins {
		if p.Name == name {
			writeJSON(w, http.StatusOK, p)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "plugin not found"})
}

func (s *Server) handleAddPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	var body struct {
		Name         string            `json:"name"`
		Type         string            `json:"type"`
		Command      string            `json:"command"`
		Image        string            `json:"image"`
		Args         []string          `json:"args"`
		Env          map[string]string `json:"env"`
		Capabilities []string          `json:"capabilities"`
		MemoryLimit  string            `json:"memory_limit"`
		CPULimit     string            `json:"cpu_limit"`
		Network      string            `json:"network"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if body.Type != "subprocess" && body.Type != "docker" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type must be \"subprocess\" or \"docker\""})
		return
	}

	cfg := config.PluginConfig{
		Type:         body.Type,
		Command:      body.Command,
		Image:        body.Image,
		Args:         body.Args,
		Env:          body.Env,
		Capabilities: body.Capabilities,
		MemoryLimit:  body.MemoryLimit,
		CPULimit:     body.CPULimit,
		Network:      body.Network,
	}

	if err := s.deps.LifecycleMgr.AddPlugin(r.Context(), body.Name, cfg); err != nil {
		s.logger.Error("adding plugin", "name", body.Name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Return the newly created plugin status.
	plugins := s.deps.LifecycleMgr.ListPlugins()
	for _, p := range plugins {
		if p.Name == body.Name {
			writeJSON(w, http.StatusCreated, p)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name, "status": "connected"})
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	name := r.PathValue("name")
	if err := s.deps.LifecycleMgr.RemovePlugin(r.Context(), name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Key input validation
// ---------------------------------------------------------------------------

// ValidScopes is the set of scope values accepted by the key management system.
// It delegates to the canonical list in the scope package.
var ValidScopes = scope.Valid

const maxKeyNameLen = 255
const maxChatMessageLen = 32 * 1024 // 32 KB — matches WS frame size order of magnitude

// ValidateKeyInput checks that name is within the length limit and every scope
// is in the ValidScopes allowlist. Returns a user-facing error on failure.
func ValidateKeyInput(name string, scopes []string) error {
	if len(name) > maxKeyNameLen {
		return fmt.Errorf("name exceeds maximum length of %d characters", maxKeyNameLen)
	}
	for _, s := range scopes {
		if _, ok := ValidScopes[s]; !ok {
			return fmt.Errorf("unknown scope %q", s)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Browser profile & session handlers
// ---------------------------------------------------------------------------

// browserRequired writes 503 when the BrowserProfiles service is not configured.
func (s *Server) browserRequired(w http.ResponseWriter) bool {
	if s.deps.BrowserProfiles == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "browser automation not configured"})
		return false
	}
	return true
}

func (s *Server) handleListBrowserProfiles(w http.ResponseWriter, r *http.Request) {
	if !s.browserRequired(w) {
		return
	}
	profiles, err := s.deps.BrowserProfiles.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) handleGetBrowserProfile(w http.ResponseWriter, r *http.Request) {
	if !s.browserRequired(w) {
		return
	}
	name := r.PathValue("name")
	info, err := s.deps.BrowserProfiles.Info(r.Context(), name)
	if err != nil {
		if err == browser.ErrProfileNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "profile not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDeleteBrowserProfile(w http.ResponseWriter, r *http.Request) {
	if !s.browserRequired(w) {
		return
	}
	name := r.PathValue("name")
	if err := s.deps.BrowserProfiles.Delete(r.Context(), name); err != nil {
		if err == browser.ErrProfileNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "profile not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type browserSessionInfo struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	ToolCount int    `json:"tool_count"`
}

func (s *Server) handleListBrowserSessions(w http.ResponseWriter, r *http.Request) {
	if !s.browserRequired(w) {
		return
	}
	var sessions []browserSessionInfo
	if s.deps.LifecycleMgr != nil {
		info, ok := s.deps.LifecycleMgr.ToolManager().ServerInfo("browser")
		if ok {
			sessions = append(sessions, browserSessionInfo{
				Name:      "browser",
				Status:    info.Status,
				ToolCount: len(info.ToolNames),
			})
		}
	}
	if sessions == nil {
		sessions = []browserSessionInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) handleBrowserConfig(w http.ResponseWriter, _ *http.Request) {
	if !s.browserRequired(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Config.Browser)
}

// keyStoreRequired writes 503 when the KeyStore is not configured.
func (s *Server) keyStoreRequired(w http.ResponseWriter) bool {
	if s.deps.KeyStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "key management not configured"})
		return false
	}
	return true
}

// handleListKeys handles GET /api/v1/keys.
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	if !s.keyStoreRequired(w) {
		return
	}
	recs, err := s.deps.KeyStore.List(r.Context())
	if err != nil {
		s.logger.Error("listing api keys", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if recs == nil {
		recs = []APIKeyRecord{}
	}
	writeJSON(w, http.StatusOK, recs)
}

// handleCreateKey handles POST /api/v1/keys.
func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	if !s.keyStoreRequired(w) {
		return
	}
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if len(body.Scopes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one scope is required"})
		return
	}
	if err := ValidateKeyInput(body.Name, body.Scopes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rec, plaintext, err := s.deps.KeyStore.Create(r.Context(), body.Name, body.Scopes)
	if err != nil {
		s.logger.Error("creating api key", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         rec.ID,
		"name":       rec.Name,
		"key":        plaintext, // shown once
		"scopes":     rec.Scopes,
		"created_at": rec.CreatedAt,
	})
}

// handleRevokeKey handles DELETE /api/v1/keys/{id}.
func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if !s.keyStoreRequired(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.deps.KeyStore.Revoke(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteKey handles DELETE /api/v1/keys/{id}/permanent.
func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	if !s.keyStoreRequired(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.deps.KeyStore.Delete(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRotateKey handles POST /api/v1/keys/{id}/rotate.
func (s *Server) handleRotateKey(w http.ResponseWriter, r *http.Request) {
	if !s.keyStoreRequired(w) {
		return
	}
	id := r.PathValue("id")
	rec, plaintext, err := s.deps.KeyStore.Rotate(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         rec.ID,
		"name":       rec.Name,
		"key":        plaintext, // shown once
		"scopes":     rec.Scopes,
		"created_at": rec.CreatedAt,
	})
}

// ---------------------------------------------------------------------------
// Setup (first-run bootstrap)
// ---------------------------------------------------------------------------

// setupRequired returns true when there are no active keys anywhere and no
// password auth configured. TOML keys always satisfy the check so that users
// who prefer static config never see the setup prompt. Password auth also
// satisfies setup (the user already has a way to log in).
func (s *Server) setupRequired(ctx context.Context) (bool, error) {
	if s.passwordHash != "" {
		return false, nil
	}
	if len(s.cfg.Keys) > 0 {
		return false, nil
	}
	has, err := s.deps.KeyStore.HasActiveKey(ctx)
	if err != nil {
		return false, err
	}
	return !has, nil
}

// handleSetupStatus handles GET /api/v1/setup.
// Returns {"setup_required": true} when no active API keys or password exist.
// Also reports whether the PIN-protected account setup flow is available.
// No authentication required.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if !s.keyStoreRequired(w) {
		return
	}
	required, err := s.setupRequired(r.Context())
	if err != nil {
		s.logger.Error("checking setup status", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_required":          required,
		"account_setup_available": s.setupPIN != "",
	})
}

// handleSetupInit handles POST /api/v1/setup.
// Creates the first API key. Returns 409 Conflict once any active key exists.
// No authentication required — the endpoint locks itself after first use.
func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	if !s.keyStoreRequired(w) {
		return
	}

	// Parse and validate the body before acquiring the mutex so we hold the
	// lock only for the atomic check+create.
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		body.Name = "admin"
	}
	if len(body.Scopes) == 0 {
		body.Scopes = []string{"admin"}
	}
	if err := ValidateKeyInput(body.Name, body.Scopes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Serialise the check+create to prevent a TOCTOU race where two concurrent
	// requests both observe setup_required=true and both proceed to create a key.
	s.setupMu.Lock()
	defer s.setupMu.Unlock()

	required, err := s.setupRequired(r.Context())
	if err != nil {
		s.logger.Error("checking setup status", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if !required {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "setup already complete — manage keys on the API Keys page"})
		return
	}

	rec, plaintext, err := s.deps.KeyStore.Create(r.Context(), body.Name, body.Scopes)
	if err != nil {
		s.logger.Error("creating setup key", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	s.logger.Info("first-run setup complete", "key_name", rec.Name, "remote_addr", r.RemoteAddr)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         rec.ID,
		"name":       rec.Name,
		"key":        plaintext, // shown once
		"scopes":     rec.Scopes,
		"created_at": rec.CreatedAt,
	})
}

// handleSetupAccount handles POST /api/v1/setup/account.
// Creates an admin account (password login) verified by a one-time setup PIN.
// The PIN is displayed in server logs at startup. No authentication required —
// the endpoint self-disables after successful use.
func (s *Server) handleSetupAccount(w http.ResponseWriter, r *http.Request) {
	// Rate-limit PIN attempts (reuse login rate limiter).
	ip := clientIP(r)
	if !s.loginLimiter.allow(ip) {
		retryAfter := s.loginLimiter.retryAfter(ip)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		s.logger.Warn("setup account rate limited", "ip", ip)
		http.Error(w, `{"error":"too many attempts"}`, http.StatusTooManyRequests)
		return
	}

	var body struct {
		PIN      string `json:"pin"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.PIN) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pin is required"})
		return
	}
	if len(body.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	// Serialise PIN check + account creation to prevent TOCTOU races.
	s.setupMu.Lock()
	defer s.setupMu.Unlock()

	if s.setupPIN == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "account setup is no longer available"})
		return
	}

	// Constant-time comparison to prevent timing attacks on the PIN.
	if subtle.ConstantTimeCompare([]byte(s.setupPIN), []byte(strings.TrimSpace(body.PIN))) != 1 {
		s.logger.Warn("setup account: invalid PIN", "ip", ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid PIN"})
		return
	}

	// Hash password with bcrypt (consistent with `denkeeper passwd`).
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), s.bcryptCost)
	if err != nil {
		s.logger.Error("hashing password", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// Generate a cryptographic session secret (32 bytes, hex-encoded).
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		s.logger.Error("generating session secret", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	sessionSecret := hex.EncodeToString(secretBytes)

	// Persist to TOML config for restart survival.
	if err := tool.SetAuthConfig(s.deps.ConfigPath, string(hash), sessionSecret); err != nil {
		s.logger.Error("persisting auth config", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save configuration"})
		return
	}

	// Hot-reload: create SessionManager and update server state in-place.
	sm, err := NewSessionManager(sessionSecret, 24*time.Hour, s.cfg.TLS)
	if err != nil {
		s.logger.Error("creating session manager", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	s.sessions = sm
	s.passwordHash = string(hash)

	// Clear the PIN — single use.
	s.setupPIN = ""

	// Create session cookie to log the user in immediately.
	sess := Session{
		Email:  "admin",
		Scopes: adminScopes(),
	}
	if err := sm.Create(w, sess); err != nil {
		s.logger.Error("creating session after account setup", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	s.logger.Info("account setup complete", "remote_addr", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "email": "admin"}) //nolint:errcheck
}

// ---------------------------------------------------------------------------
// Rate limiter (token bucket, per-key)
// ---------------------------------------------------------------------------

type rateLimiter struct {
	mu       sync.Mutex
	tokens   float64
	maxRate  float64
	lastTime time.Time
}

func (s *Server) allowRequest(keyName string) bool {
	s.limitersMu.Lock()
	rl, ok := s.limiters[keyName]
	if !ok {
		rl = &rateLimiter{
			tokens:   s.cfg.RateLimit,
			maxRate:  s.cfg.RateLimit,
			lastTime: time.Now(),
		}
		s.limiters[keyName] = rl
	}
	s.limitersMu.Unlock()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastTime).Seconds()
	rl.tokens += elapsed * rl.maxRate
	if rl.tokens > rl.maxRate {
		rl.tokens = rl.maxRate
	}
	rl.lastTime = now

	if rl.tokens < 1 {
		return false
	}
	rl.tokens--
	return true
}

// ---------------------------------------------------------------------------
// WSHub returns the WebSocket hub, or nil if WebSocket is disabled.
func (s *Server) WSHub() *WSHub { return s.wsHub }

// Middleware
// ---------------------------------------------------------------------------

func (s *Server) middlewareLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		level := slog.LevelInfo
		if r.URL.Path == "/api/v1/health" {
			level = slog.LevelDebug
		}
		s.logger.Log(r.Context(), level, "api request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

func (s *Server) middlewareCORS(next http.Handler) http.Handler {
	if len(s.cfg.CORSOrigins) == 0 {
		return next
	}
	allowed := make(map[string]bool, len(s.cfg.CORSOrigins))
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) middlewareSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// Set Cache-Control on API routes only (not static assets served by the web handler).
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) middlewareRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("api panic recovered", "panic", rec, "path", r.URL.Path)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker so WebSocket upgrades work through the
// logging middleware.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support Hijack")
}

// Flush implements http.Flusher so SSE streaming works through the logging middleware.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// generateID returns a cryptographically random 16-character hex string suitable
// for use as a session identifier.
func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
