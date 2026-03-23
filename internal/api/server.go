package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// Deps holds the application dependencies the API server needs to serve data.
type Deps struct {
	Dispatcher  *agent.Dispatcher
	Scheduler   *scheduler.Scheduler
	CostTracker *llm.CostTracker
	Memory      agent.MemoryStore
	Config      *config.Config
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

	// Data endpoints — require auth with appropriate scopes.
	mux.HandleFunc("GET /api/v1/agents", s.RequireScope("admin", s.handleAgents))
	mux.HandleFunc("GET /api/v1/agents/{name}", s.RequireScope("admin", s.handleAgent))
	mux.HandleFunc("GET /api/v1/costs", s.RequireScope("costs:read", s.handleCosts))
	mux.HandleFunc("GET /api/v1/skills", s.RequireScope("skills:read", s.handleSkills))
	mux.HandleFunc("GET /api/v1/skills/{agent}", s.RequireScope("skills:read", s.handleSkillsByAgent))
	mux.HandleFunc("GET /api/v1/schedules", s.RequireScope("schedules:read", s.handleSchedules))
	mux.HandleFunc("GET /api/v1/sessions", s.RequireScope("sessions:read", s.handleSessions))
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", s.RequireScope("sessions:read", s.handleSessionMessages))

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
		"name":            e.Name(),
		"permission_tier": e.PermissionTier(),
		"model":           e.ModelName(),
		"has_tools":       e.HasTools(),
		"adapters":        adapters,
		"skills":          skillList,
	})
}

// handleCosts returns cost tracking data.
func (s *Server) handleCosts(w http.ResponseWriter, _ *http.Request) {
	sessions := s.deps.CostTracker.AllSessionCosts()
	writeJSON(w, http.StatusOK, map[string]any{
		"global_cost":        s.deps.CostTracker.GlobalCost(),
		"max_per_session":    s.deps.CostTracker.MaxBudgetPerSession(),
		"session_count":      len(sessions),
		"session_costs":      sessions,
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

	var all []skillInfo
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
	writeJSON(w, http.StatusOK, convos)
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
// Auth middleware
// ---------------------------------------------------------------------------

// contextKey is an unexported type used for context value keys.
type contextKey string

const keyNameKey contextKey = "api_key_name"

// RequireScope returns middleware that checks for a valid API key with the
// required scope. Use this to wrap individual route handlers.
func (s *Server) RequireScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keyName, ok := s.authenticate(r, scope)
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
// given scope. Returns the key name and true if valid.
func (s *Server) authenticate(r *http.Request, scope string) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}

	token := strings.TrimPrefix(header, "Bearer ")
	if token == header {
		return "", false // no "Bearer " prefix
	}

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
