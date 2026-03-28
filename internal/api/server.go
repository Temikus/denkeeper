package api

import (
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
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/tool"
)

// Deps holds the application dependencies the API server needs to serve data.
type Deps struct {
	Dispatcher  *agent.Dispatcher
	Scheduler   *scheduler.Scheduler
	CostTracker *llm.CostTracker
	Memory      agent.MemoryStore
	Config       *config.Config
	Approvals    *approval.Manager        // nil = approval endpoints return 503
	LifecycleMgr *tool.LifecycleManager   // nil = tool CRUD endpoints return 503
	WebHandler   http.Handler             // nil = no web dashboard served
	KeyStore     *KeyStore                // nil = API key CRUD endpoints return 503
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
}

// New creates a new API server. The server is not started until Run is called.
func New(cfg config.APIConfig, deps Deps, logger *slog.Logger) *Server {
	s := &Server{
		cfg:      cfg,
		deps:     deps,
		logger:   logger,
		limiters: make(map[string]*rateLimiter),
	}

	mux := http.NewServeMux()

	// Health endpoint — no auth required.
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// Setup endpoints — no auth required; only functional when no keys exist.
	mux.HandleFunc("GET /api/v1/setup", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", s.handleSetupInit)

	// Chat endpoint.
	mux.HandleFunc("POST /api/v1/chat", s.RequireScope("chat", s.handleChat))

	// Data endpoints — require auth with appropriate scopes.
	mux.HandleFunc("GET /api/v1/agents", s.RequireScope("admin", s.handleAgents))
	mux.HandleFunc("GET /api/v1/agents/{name}", s.RequireScope("admin", s.handleAgent))
	mux.HandleFunc("GET /api/v1/costs", s.RequireScope("costs:read", s.handleCosts))
	mux.HandleFunc("GET /api/v1/skills", s.RequireScope("skills:read", s.handleSkills))
	mux.HandleFunc("GET /api/v1/skills/{agent}", s.RequireScope("skills:read", s.handleSkillsByAgent))
	mux.HandleFunc("GET /api/v1/schedules", s.RequireScope("schedules:read", s.handleSchedules))
	mux.HandleFunc("GET /api/v1/sessions", s.RequireScope("sessions:read", s.handleSessions))
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", s.RequireScope("sessions:read", s.handleSessionMessages))
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.RequireScope("sessions:read", s.handleDeleteSession))

	// Approval endpoints.
	mux.HandleFunc("GET /api/v1/approvals", s.RequireScope("approvals:read", s.handleListApprovals))
	mux.HandleFunc("GET /api/v1/approvals/{id}", s.RequireScope("approvals:read", s.handleGetApproval))
	mux.HandleFunc("POST /api/v1/approvals/{id}/approve", s.RequireScope("approvals:write", s.handleResolveApproval(true)))
	mux.HandleFunc("POST /api/v1/approvals/{id}/deny", s.RequireScope("approvals:write", s.handleResolveApproval(false)))

	// Tool & plugin management endpoints.
	mux.HandleFunc("GET /api/v1/tools", s.RequireScope("tools:read", s.handleListTools))
	mux.HandleFunc("GET /api/v1/tools/{name}", s.RequireScope("tools:read", s.handleGetTool))
	mux.HandleFunc("POST /api/v1/tools", s.RequireScope("tools:write", s.handleAddTool))
	mux.HandleFunc("DELETE /api/v1/tools/{name}", s.RequireScope("tools:write", s.handleRemoveTool))
	mux.HandleFunc("GET /api/v1/plugins", s.RequireScope("tools:read", s.handleListPlugins))
	mux.HandleFunc("GET /api/v1/plugins/{name}", s.RequireScope("tools:read", s.handleGetPlugin))
	mux.HandleFunc("POST /api/v1/plugins", s.RequireScope("tools:write", s.handleAddPlugin))
	mux.HandleFunc("DELETE /api/v1/plugins/{name}", s.RequireScope("tools:write", s.handleRemovePlugin))

	// API key management endpoints (require admin scope).
	mux.HandleFunc("GET /api/v1/keys", s.RequireScope("admin", s.handleListKeys))
	mux.HandleFunc("POST /api/v1/keys", s.RequireScope("admin", s.handleCreateKey))
	mux.HandleFunc("DELETE /api/v1/keys/{id}", s.RequireScope("admin", s.handleRevokeKey))
	mux.HandleFunc("DELETE /api/v1/keys/{id}/permanent", s.RequireScope("admin", s.handleDeleteKey))
	mux.HandleFunc("POST /api/v1/keys/{id}/rotate", s.RequireScope("admin", s.handleRotateKey))

	// Web dashboard — catch-all for non-API paths (more-specific /api/v1/ routes always win).
	if deps.WebHandler != nil {
		mux.Handle("/", deps.WebHandler)
	}

	var handler http.Handler = mux
	handler = s.middlewareLogging(handler)
	handler = s.middlewareCORS(handler)
	handler = s.middlewareRecover(handler)

	s.httpServer = &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

// Run starts the server and blocks until ctx is cancelled. It performs a
// graceful shutdown with a 5-second deadline.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", s.cfg.Listen, err)
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAgents lists all registered agents with metadata.
func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request) {
	type agentInfo struct {
		Name           string   `json:"name"`
		PermissionTier string   `json:"permission_tier"`
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
			PermissionTier: e.PermissionTier(),
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
	for _, ac := range s.deps.Config.Agents {
		if ac.Name == name {
			adapters = ac.Adapters
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":             e.Name(),
		"permission_tier":  e.PermissionTier(),
		"model":            e.ModelName(),
		"has_tools":        e.HasTools(),
		"adapters":         adapters,
		"skills":           skillList,
		"tool_names":       e.ToolNames(),
		"persona_dir":      e.PersonaDir(),
		"persona_sections": e.PersonaSections(),
	})
}

// handleCosts returns cost tracking data.
func (s *Server) handleCosts(w http.ResponseWriter, _ *http.Request) {
	sessions := s.deps.CostTracker.AllSessionCosts()
	writeJSON(w, http.StatusOK, map[string]any{
		"global_cost":     s.deps.CostTracker.GlobalCost(),
		"max_per_session": s.deps.CostTracker.MaxBudgetPerSession(),
		"session_count":   len(sessions),
		"session_costs":   sessions,
	})
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
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
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
// The engine pipeline is currently synchronous, so we send a single content
// event followed by a done event once the response is ready.
func (s *Server) handleChatSSE(w http.ResponseWriter, r *http.Request, eng *agent.Engine, msg adapter.IncomingMessage, sessionID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	writeEvent := func(data any) {
		b, _ := json.Marshal(data)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		flush()
	}

	responseText, err := eng.Chat(r.Context(), msg)
	if err != nil {
		s.logger.Error("chat SSE error", "error", err, "session", sessionID)
		writeEvent(map[string]string{"type": "error", "message": "failed to process message"})
		return
	}

	writeEvent(map[string]string{"type": "content", "text": responseText})
	writeEvent(map[string]string{"type": "done", "session_id": sessionID})
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
		Role       string    `json:"role"`
		Content    string    `json:"content"`
		TokensUsed int       `json:"tokens_used,omitempty"`
		CreatedAt  time.Time `json:"created_at"`
	}

	out := make([]messageInfo, len(messages))
	for i, m := range messages {
		out[i] = messageInfo{
			Role:       m.Role,
			Content:    m.Content,
			TokensUsed: m.TokensUsed,
			CreatedAt:  m.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, out)
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
					go s.deps.KeyStore.TouchLastUsed(context.Background(), sk.ID)
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
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleAddTool(w http.ResponseWriter, r *http.Request) {
	if !s.lifecycleRequired(w) {
		return
	}
	var body struct {
		Name    string            `json:"name"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if strings.TrimSpace(body.Command) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}

	cfg := config.ToolConfig{
		Command: body.Command,
		Args:    body.Args,
		Env:     body.Env,
	}

	if err := s.deps.LifecycleMgr.AddTool(r.Context(), body.Name, cfg); err != nil {
		s.logger.Error("adding tool", "name", body.Name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	info, _ := s.deps.LifecycleMgr.ToolManager().ServerInfo(body.Name)
	writeJSON(w, http.StatusCreated, info)
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
// Exported so the CLI can share the same allowlist.
var ValidScopes = map[string]struct{}{
	"admin":           {},
	"chat":            {},
	"sessions:read":   {},
	"costs:read":      {},
	"skills:read":     {},
	"schedules:read":  {},
	"approvals:read":  {},
	"approvals:write": {},
	"tools:read":      {},
	"tools:write":     {},
	"health":          {},
}

const maxKeyNameLen = 255

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

// setupRequired returns true when there are no active keys anywhere — neither
// in the SQLite store nor in the TOML config. TOML keys always satisfy the
// check so that users who prefer static config never see the setup prompt.
func (s *Server) setupRequired(ctx context.Context) (bool, error) {
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
// Returns {"setup_required": true} when no active API keys exist.
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
	writeJSON(w, http.StatusOK, map[string]bool{"setup_required": required})
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
// Middleware
// ---------------------------------------------------------------------------

func (s *Server) middlewareLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		s.logger.Info("api request",
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
