package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

var (
	toolTracer   = otel.Tracer("denkeeper.tool")
	toolMeter    = otel.Meter("denkeeper.tool")
	toolDuration metric.Float64Histogram
)

// RejectionError indicates a tool ran successfully at the transport level but
// returned an application-level error result (IsError=true) — typically because
// the model passed invalid or unusable arguments. It is distinct from genuine
// execution/transport failures (server unreachable, crashed, timed out), which
// surface as plain errors. Callers can use errors.As to classify a tool-call
// telemetry outcome as "rejected" (healthy tool, bad args) vs "failed".
type RejectionError struct {
	Tool string // tool/function name that returned the error result
	Text string // the error text the tool returned
}

func (e *RejectionError) Error() string {
	return fmt.Sprintf("tool %q returned error: %s", e.Tool, e.Text)
}

func init() {
	toolDuration, _ = toolMeter.Float64Histogram("denkeeper.tool.duration",
		metric.WithDescription("Tool execution latency in seconds"),
		metric.WithUnit("s"))
}

// drainState is a serverConn's teardown lifecycle. A conn starts live, moves to
// draining for the duration of teardown phase 2 (waiting out in-flight calls),
// and ends closed once its transport is shut. It never moves backwards — a torn
// down conn is replaced by a fresh one, never revived.
//
// See UnregisterServer for the two-phase teardown this state drives, and the
// finding it comes from.
type drainState uint8

const (
	drainStateLive     drainState = iota // accepting new tool calls
	drainStateDraining                   // teardown phase 2 in progress
	drainStateClosed                     // transport closed
)

// serverConn tracks a connected MCP server subprocess and its session.
type serverConn struct {
	name      string
	command   string            // binary path (empty for in-process or SSE sessions)
	args      []string          // command-line arguments
	transport string            // "stdio", "sse", or "" (in-process)
	url       string            // remote server URL (SSE only)
	cfg       config.ToolConfig // stored for restart
	session   *mcp.ClientSession

	// Teardown state, guarded by drainMu rather than Manager.mu: Execute must
	// admit a call (and bump the counter) while holding only m.mu.RLock, and
	// teardown phase 2 must wait for the counter to reach zero while holding no
	// manager lock at all. Lock order is always m.mu → drainMu, never reverse.
	drainMu   sync.Mutex
	drain     drainState
	inFlight  int           // tool calls currently executing against this conn
	drainDone chan struct{} // non-nil while draining with calls outstanding; closed at zero

	// Health monitoring state.
	connectedAt   time.Time // when the server was last successfully connected
	restartCount  int       // consecutive restart attempts
	probeFailures int       // consecutive health-probe failures (reset on healthy probe)
	oauthWarned   bool      // true once a pending_auth OAuth warning has been emitted (reset when a token/session appears)
	lastError     string    // most recent failure message
	disabled      bool      // true when restarts exhausted
	connecting    bool      // true while background init retries are in progress
	userDisabled  bool      // true when explicitly disabled by user
	configError   string    // non-empty when auto-disabled due to config validation error

	// tools holds this server's discovered tool definitions under their
	// server-local names, in discovery order. It is the source of truth the
	// manager's advertised index is rebuilt from (see rebuildToolIndex), so a
	// server that is unregistered must have it cleared.
	tools []llm.ToolDef

	// Tool filtering — tools in disabledSet are excluded from the LLM payload.
	// Keyed by server-local tool name, never by the advertised (possibly
	// server-qualified) one.
	disabledSet map[string]bool

	// readOnlyHinted records tools whose MCP readOnlyHint annotation was true
	// at discovery time. Consulted by IsIdempotent only when the server's
	// config sets trust_annotations; repopulated on restart/re-register.
	readOnlyHinted map[string]bool

	// OAuth state (nil for non-OAuth tools).
	oauthHandler oauthHandler
}

// ErrServerDraining is returned when a tool call targets an MCP server that is
// being torn down (removed, disabled, restarted, or reconfigured). The refusal
// is immediate — the model sees it and adapts, exactly as it does for an
// unknown tool. Callers classify with errors.Is, mirroring ErrToolNotFound.
var ErrServerDraining = errors.New("server is shutting down")

// enterCall admits a tool call and records it as in flight. It returns false if
// the server is no longer live, in which case the caller must refuse the call
// with ErrServerDraining and must NOT call leaveCall.
func (sc *serverConn) enterCall() bool {
	sc.drainMu.Lock()
	defer sc.drainMu.Unlock()
	if sc.drain != drainStateLive {
		return false
	}
	sc.inFlight++
	return true
}

// leaveCall retires an admitted tool call, releasing a waiting drain once the
// last one finishes.
func (sc *serverConn) leaveCall() {
	sc.drainMu.Lock()
	defer sc.drainMu.Unlock()
	sc.inFlight--
	if sc.inFlight == 0 && sc.drainDone != nil {
		close(sc.drainDone)
		sc.drainDone = nil
	}
}

// beginDrain is teardown phase 1: it stops the conn admitting new calls and
// returns a channel closed once the in-flight count reaches zero, along with
// the count at this instant. The channel is already closed when nothing is in
// flight, so the common case costs no waiting. Idempotent — a conn that is
// already draining or closed keeps its state.
func (sc *serverConn) beginDrain() (<-chan struct{}, int) {
	sc.drainMu.Lock()
	defer sc.drainMu.Unlock()
	if sc.drain == drainStateLive {
		sc.drain = drainStateDraining
	}
	if sc.inFlight == 0 {
		done := make(chan struct{})
		close(done)
		return done, 0
	}
	if sc.drainDone == nil {
		sc.drainDone = make(chan struct{})
	}
	return sc.drainDone, sc.inFlight
}

// finishDrain marks the conn closed, ending the "draining" status window.
func (sc *serverConn) finishDrain() {
	sc.drainMu.Lock()
	defer sc.drainMu.Unlock()
	sc.drain = drainStateClosed
}

// drainStatus reports the conn's teardown state.
func (sc *serverConn) drainStatus() drainState {
	sc.drainMu.Lock()
	defer sc.drainMu.Unlock()
	return sc.drain
}

// inFlightCount reports how many tool calls are currently executing.
func (sc *serverConn) inFlightCount() int {
	sc.drainMu.Lock()
	defer sc.drainMu.Unlock()
	return sc.inFlight
}

// oauthHandler is an internal interface satisfied by oauth.Handler.
// It abstracts the concrete type so the manager can reference it without
// importing the build-tag-gated oauth package.
type oauthHandler interface {
	ToolName() string
	HasToken() bool
	ClearToken() error
	Close()
	InitiateOAuth(ctx context.Context, serverURL string) error
}

// OAuthSupport holds OAuth infrastructure injected into the Manager.
type OAuthSupport struct {
	// HandlerFactory creates an oauth.Handler for a tool. This is set from
	// a build-tag-gated init in manager_oauth.go.
	HandlerFactory OAuthHandlerFactory
	CallbackURL    string

	// TokenStore deletes persisted tokens directly, independent of any live
	// handler. Satisfied by *oauth.TokenStore. Needed because tokens are
	// keyed by tool name alone: a tool that is disabled, config-errored, or
	// pending at removal time has no oauthHandler, yet may still own a row.
	TokenStore TokenDeleter
}

// TokenDeleter removes a persisted OAuth token by tool name.
type TokenDeleter interface {
	Delete(toolName string) error
}

// OAuthHandlerFactory creates an OAuthHandler and its corresponding
// auth.OAuthHandler for use with StreamableClientTransport.
// The second return value is the transport-compatible handler.
type OAuthHandlerFactory func(name string, cfg config.ToolConfig, httpClient *http.Client) (oauthHandler, any, error)

// ServerStatus exposes metadata about a registered MCP server.
type ServerStatus struct {
	Name           string           `json:"name"`
	Command        string           `json:"command,omitempty"`
	Args           []string         `json:"-"`          // excluded from JSON (may contain secrets)
	ArgsCount      int              `json:"args_count"` // safe count for display
	ToolNames      []string         `json:"tool_names"`
	Status         string           `json:"status"` // "connected", "connecting", "draining", "error", "disabled", "config_error", "pending_auth"
	Transport      string           `json:"transport,omitempty"`
	URL            string           `json:"url,omitempty"` // redacted
	RestartCount   int              `json:"restart_count,omitempty"`
	LastError      string           `json:"last_error,omitempty"`
	UptimeSecs     float64          `json:"uptime_secs,omitempty"`
	AuthType       string           `json:"auth_type,omitempty"` // "oauth" or ""
	OAuthStatus    *OAuthStatusInfo `json:"oauth_status,omitempty"`
	DisabledTools  []string         `json:"disabled_tools,omitempty"`
	EnabledCount   int              `json:"enabled_count"`
	TotalToolCount int              `json:"total_tool_count"`
	Enabled        bool             `json:"enabled"`
	ConfigError    string           `json:"config_error,omitempty"`
}

// OAuthStatusInfo is a non-sensitive view of OAuth state for API responses.
type OAuthStatusInfo struct {
	HasToken    bool `json:"has_token"`
	NeedsReauth bool `json:"needs_reauth"`
}

// Manager manages MCP tool server connections and tool execution.
//
// toolMap, owners, localOf and toolDefs are one derived projection of the
// per-server tool lists, rebuilt as a whole by rebuildToolIndex on every
// registration change. Two servers may advertise the same tool name; when they
// do, neither is reachable under the bare name and both are advertised as
// "<server>__<tool>" instead (see toolindex.go).
type Manager struct {
	mu             sync.RWMutex
	parent         *Manager                 // optional parent for delegated lookups (set by AdoptFrom)
	servers        map[string]*serverConn   // keyed by config name (e.g. "web-search")
	toolMap        map[string]*serverConn   // keyed by *advertised* tool name → owning server
	owners         map[string][]*serverConn // keyed by bare tool name → every server advertising it, registration order
	localOf        map[string]string        // advertised name → server-local name (qualified entries only)
	discoveryOrder []string                 // server names in discovery order; fixes the order of toolDefs
	toolDefs       []llm.ToolDef            // cached OpenAI-format tool definitions, advertised names
	disabledCount  int                      // total disabled tools across all servers; 0 = fast-path in ToolDefs
	mcpCfg         config.MCPConfig         // global MCP settings
	logger         *slog.Logger
	oauth          *OAuthSupport // nil if OAuth not configured
	Auditor        audit.Emitter // nil = no audit events
}

// SetOAuthSupport injects OAuth infrastructure into the Manager.
func (m *Manager) SetOAuthSupport(o *OAuthSupport) {
	m.oauth = o
}

// NewManager creates a manager with no servers registered.
func NewManager(logger *slog.Logger, mcpCfg ...config.MCPConfig) *Manager {
	m := &Manager{
		servers: make(map[string]*serverConn),
		toolMap: make(map[string]*serverConn),
		owners:  make(map[string][]*serverConn),
		localOf: make(map[string]string),
		logger:  logger,
	}
	if len(mcpCfg) > 0 {
		m.mcpCfg = mcpCfg[0]
	}
	return m
}

// RegisterPending creates a placeholder entry for a remote MCP server that is
// not yet connected. This makes the tool visible in the UI with "connecting"
// status while background retries are in progress. The placeholder is replaced
// by RegisterServer once the connection succeeds.
func (m *Manager) RegisterPending(name string, cfg config.ToolConfig, lastErr string) {
	transport := cfg.Transport
	if transport == "" {
		transport = "sse"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[name] = &serverConn{
		name:       name,
		command:    cfg.Command,
		args:       cfg.Args,
		transport:  transport,
		url:        cfg.URL,
		cfg:        cfg,
		lastError:  lastErr,
		connecting: true,
		// session is nil — health checker skips this entry,
		// background retries handle reconnection instead.
	}
}

// MarkDisabled transitions a pending/connecting server to disabled state.
// Called when background init retries are exhausted.
func (m *Manager) MarkDisabled(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sc, ok := m.servers[name]; ok {
		sc.connecting = false
		sc.disabled = true
	}
}

// RegisterDisabled creates a placeholder entry for a tool that should not
// spawn a process. The tool appears in listings with its disabled reason.
// isConfigError distinguishes config validation failures from user-initiated disabling.
func (m *Manager) RegisterDisabled(name string, cfg config.ToolConfig, reason string, isConfigError bool) {
	transport := cfg.Transport
	if transport == "" {
		transport = "stdio"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sc := &serverConn{
		name:      name,
		command:   cfg.Command,
		args:      cfg.Args,
		transport: transport,
		url:       cfg.URL,
		cfg:       cfg,
	}
	if isConfigError {
		sc.configError = reason
	} else {
		sc.userDisabled = true
	}
	m.servers[name] = sc
}

// RegisterServer connects to an MCP server (stdio subprocess or remote SSE)
// based on the transport field in cfg, and discovers its available tools.
func (m *Manager) RegisterServer(ctx context.Context, name string, cfg config.ToolConfig) error {
	transport := cfg.Transport
	if transport == "" {
		transport = "stdio"
	}

	ctx, span := toolTracer.Start(ctx, "tool.connect", trace.WithAttributes(
		attribute.String("tool.server", name),
		attribute.String("tool.transport.requested", transport),
	))
	defer span.End()

	var err error
	switch transport {
	case "stdio":
		err = m.registerStdio(ctx, name, cfg)
	case "sse", "sse-legacy":
		err = m.registerSSE(ctx, name, cfg)
	default:
		err = fmt.Errorf("unsupported transport %q for MCP server %q", transport, name)
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	// Record the negotiated transport after successful connection.
	m.mu.RLock()
	if sc, ok := m.servers[name]; ok {
		span.SetAttributes(attribute.String("tool.transport.negotiated", sc.transport))
	}
	m.mu.RUnlock()
	span.SetStatus(codes.Ok, "")
	return nil
}

// registerStdio spawns an MCP server subprocess and connects over stdio.
func (m *Manager) registerStdio(ctx context.Context, name string, cfg config.ToolConfig) error {
	cmd := exec.Command(cfg.Command, cfg.Args...) // #nosec G204 -- MCP tool servers are spawned from config-declared commands
	// Scope the subprocess environment to a non-secret allowlist plus any
	// explicit passthrough vars, rather than inheriting the full secret-bearing
	// parent environment. See buildStdioEnv (internal/tool/env.go).
	passthrough := append(append([]string(nil), m.mcpCfg.EnvPassthrough...), cfg.EnvPassthrough...)
	cmd.Env = buildStdioEnv(cfg.Env, passthrough, m.logger)

	// Capture stderr so we can surface diagnostic output when the server
	// fails to start (e.g. missing deps, bad config, crash on init).
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	cmdTransport := &mcp.CommandTransport{Command: cmd}
	session, err := client.Connect(ctx, cmdTransport, nil)
	if err != nil {
		stderrOutput := strings.TrimSpace(stderrBuf.String())
		if stderrOutput != "" {
			// Truncate very long stderr to keep error messages readable.
			const maxStderr = 1024
			if len(stderrOutput) > maxStderr {
				stderrOutput = stderrOutput[:maxStderr] + "... (truncated)"
			}
			m.logger.Error("MCP server stderr", "server", name, "stderr", stderrOutput)
			return fmt.Errorf("connecting to MCP server %q: %w\nserver stderr:\n%s", name, err, stderrOutput)
		}
		return fmt.Errorf("connecting to MCP server %q: %w", name, err)
	}

	sc := &serverConn{
		name:        name,
		command:     cfg.Command,
		args:        cfg.Args,
		transport:   "stdio",
		cfg:         cfg,
		session:     session,
		connectedAt: time.Now(),
		disabledSet: buildDisabledSet(cfg.DisabledTools),
	}

	m.mu.Lock()
	m.disabledCount += len(sc.disabledSet)
	m.servers[name] = sc
	m.mu.Unlock()

	return m.discoverTools(ctx, sc)
}

// registerSSE connects to a remote MCP server over Streamable HTTP or legacy SSE.
// It first attempts the Streamable HTTP transport (2025-03-26 spec), then falls
// back to the legacy SSE transport (2024-11-05 spec) if the server doesn't
// support the newer protocol. OAuth tools always use Streamable HTTP (no fallback).
func (m *Manager) registerSSE(ctx context.Context, name string, cfg config.ToolConfig) error {
	// Resolve ${VAR} placeholders in URL and header values (with denylist).
	resolvedURL, err := resolveEnvPlaceholders(cfg.URL, cfg.Env)
	if err != nil {
		return fmt.Errorf("resolving URL for MCP server %q: %w", name, err)
	}

	// SSRF validation.
	if err := validateToolURL(resolvedURL, m.mcpCfg.URLAllowlist, cfg.AllowLoopback); err != nil {
		return fmt.Errorf("MCP server %q URL rejected: %w", name, err)
	}

	// Resolve and validate headers.
	resolvedHeaders := make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		resolved, err := resolveEnvPlaceholders(v, cfg.Env)
		if err != nil {
			return fmt.Errorf("resolving header %q for MCP server %q: %w", k, name, err)
		}
		resolvedHeaders[k] = resolved
	}
	if err := validateHeaders(resolvedHeaders); err != nil {
		return fmt.Errorf("MCP server %q header rejected: %w", name, err)
	}

	// Build HTTP client with SSRF-safe redirect checking and header injection.
	// SSRFSafeTransport validates resolved IPs at TCP connect time to prevent
	// DNS-rebinding attacks; redirectCheckingRoundTripper provides fast-path
	// string-based URL validation for each redirect hop.
	keepAlive := time.Duration(m.mcpCfg.SSEKeepAliveSecs) * time.Second
	if cfg.SSEKeepAliveSecs > 0 {
		keepAlive = time.Duration(cfg.SSEKeepAliveSecs) * time.Second
	}
	requestTimeout := time.Duration(m.mcpCfg.RequestTimeoutSecs) * time.Second
	if cfg.RequestTimeoutSecs > 0 {
		requestTimeout = time.Duration(cfg.RequestTimeoutSecs) * time.Second
	}
	baseRT := http.RoundTripper(SSRFSafeTransport(cfg.AllowLoopback, keepAlive, requestTimeout))
	rt := http.RoundTripper(&redirectCheckingRoundTripper{
		base:          baseRT,
		allowlist:     m.mcpCfg.URLAllowlist,
		allowLoopback: cfg.AllowLoopback,
	})
	if len(resolvedHeaders) > 0 {
		rt = &headerRoundTripper{base: rt, headers: resolvedHeaders}
	}

	// Do NOT set http.Client.Timeout — it covers the entire HTTP request
	// lifecycle including streaming SSE/Streamable HTTP responses, killing
	// long-lived connections after the timeout fires. Per-request timeouts
	// are applied via context deadlines on individual MCP calls instead
	// (see probeServer, Execute, and discoverTools).
	httpClient := &http.Client{
		Transport: rt,
	}

	streamableTransport := &mcp.StreamableClientTransport{
		Endpoint:   resolvedURL,
		HTTPClient: httpClient,
	}

	// Wire OAuth handler. On first registration of an OAuth tool without a
	// cached token, short-circuit to pending_auth state so the API call
	// returns immediately. On re-registration (connect flow), proceed to
	// Connect() which triggers the actual authorization flow.
	m.mu.RLock()
	_, isReregistration := m.servers[name]
	m.mu.RUnlock()

	m.logger.Debug("registerSSE: setting up OAuth",
		slog.String("tool", name),
		slog.String("auth", cfg.Auth),
		slog.Bool("is_reregistration", isReregistration))

	oh, done, err := m.setupOAuth(name, cfg, httpClient, streamableTransport, resolvedURL)
	if err != nil {
		return err
	}
	if done && !isReregistration {
		return nil
	}

	m.logger.Debug("registerSSE: proceeding to Connect",
		slog.String("tool", name),
		slog.Bool("setup_done", done),
		slog.Bool("is_reregistration", isReregistration),
		slog.Bool("has_oauth_handler", oh != nil))

	// Try Streamable HTTP first, fall back to legacy SSE for non-OAuth tools.
	session, transport, err := m.connectSSE(ctx, name, cfg, httpClient, streamableTransport, resolvedURL)
	if err != nil {
		return err
	}

	sc := &serverConn{
		name:         name,
		transport:    transport,
		url:          resolvedURL,
		cfg:          cfg,
		session:      session,
		connectedAt:  time.Now(),
		disabledSet:  buildDisabledSet(cfg.DisabledTools),
		oauthHandler: oh,
	}

	m.mu.Lock()
	// Retire the old session if we're overwriting (e.g. OAuth reconnect).
	var old *serverConn
	var oldDone <-chan struct{}
	var oldInFlight int
	if prev, exists := m.servers[name]; exists {
		m.disabledCount -= len(prev.disabledSet)
		old = prev
		oldDone, oldInFlight = old.beginDrain()
	}
	m.disabledCount += len(sc.disabledSet)
	m.servers[name] = sc
	m.mu.Unlock()

	// Drain and close the replaced session off the lock, and off this
	// goroutine: a fresh session for this name is already live, so nothing
	// here waits on the old transport and blocking discovery behind an old
	// call's drain window would be a pointless stall.
	if old != nil {
		go m.drainAndClose(old, oldDone, oldInFlight, m.drainTimeout())
	}

	return m.discoverTools(ctx, sc)
}

// connectSSE attempts to connect using Streamable HTTP, falling back to legacy
// SSE if the server doesn't support the newer protocol. OAuth tools skip the
// fallback since they require the Streamable HTTP transport for auth handling.
// Returns the session, the transport name used ("sse" or "sse-legacy"), and error.
func (m *Manager) connectSSE(
	ctx context.Context,
	name string,
	cfg config.ToolConfig,
	httpClient *http.Client,
	streamableTransport *mcp.StreamableClientTransport,
	resolvedURL string,
) (*mcp.ClientSession, string, error) {
	// If a previous connection already negotiated legacy SSE, skip the
	// Streamable HTTP attempt to avoid a pointless 405 round-trip on restart.
	//
	// Note: we pass the parent ctx (not a short-lived timeout context) to
	// Connect because MCP sessions — especially legacy SSE — use the context
	// for the lifetime of the persistent stream. The TCP dial timeout (30s)
	// in SSRFSafeTransport handles connection-establishment timeouts.
	var streamableErr error
	if cfg.Transport != "sse-legacy" {
		client := mcp.NewClient(&mcp.Implementation{
			Name:    "denkeeper",
			Version: "v1.0.0",
		}, nil)

		var session *mcp.ClientSession
		session, streamableErr = client.Connect(ctx, streamableTransport, nil)
		if streamableErr == nil {
			m.logger.Info("connected to remote MCP server via Streamable HTTP",
				slog.String("tool", name))
			return session, "sse", nil
		}

		// OAuth tools require Streamable HTTP for the auth handler — no fallback.
		if cfg.Auth == "oauth" {
			return nil, "", fmt.Errorf("connecting to remote MCP server %q at %s: %w", name, redactURL(resolvedURL), streamableErr)
		}

		m.logger.Info("streamable HTTP failed, falling back to legacy SSE",
			slog.String("tool", name),
			slog.String("error", streamableErr.Error()))
	}

	legacyTransport := &mcp.SSEClientTransport{
		Endpoint:   resolvedURL,
		HTTPClient: httpClient,
	}

	// Need a fresh client for the new transport.
	legacyClient := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	session, err := legacyClient.Connect(ctx, legacyTransport, nil)
	if err != nil {
		if streamableErr != nil {
			return nil, "", fmt.Errorf("connecting to remote MCP server %q at %s (tried Streamable HTTP and legacy SSE): streamable: %v; legacy SSE: %w",
				name, redactURL(resolvedURL), streamableErr, err)
		}
		return nil, "", fmt.Errorf("connecting to remote MCP server %q at %s via legacy SSE: %w",
			name, redactURL(resolvedURL), err)
	}

	m.logger.Info("connected to remote MCP server via legacy SSE",
		slog.String("tool", name))
	return session, "sse-legacy", nil
}

// setupOAuth wires the OAuth handler for an SSE tool. If the tool needs OAuth
// but has no cached token, it registers a pending-auth server and returns
// done=true so the caller can short-circuit without blocking on Connect().
func (m *Manager) setupOAuth(name string, cfg config.ToolConfig, httpClient *http.Client, transport *mcp.StreamableClientTransport, resolvedURL string) (oauthHandler, bool, error) {
	if cfg.Auth != "oauth" {
		return nil, false, nil
	}
	if m.oauth == nil || m.oauth.HandlerFactory == nil {
		return nil, false, fmt.Errorf("tool %q requires auth = \"oauth\" but OAuth support is not configured (missing session_secret?)", name)
	}

	handler, transportHandler, err := m.oauth.HandlerFactory(name, cfg, httpClient)
	if err != nil {
		return nil, false, fmt.Errorf("creating OAuth handler for %q: %w", name, err)
	}
	setTransportOAuthHandler(transport, transportHandler)
	m.logger.Info("oauth: handler created for remote MCP server",
		slog.String("tool", name),
		slog.Bool("has_cached_token", handler.HasToken()))

	// Without a cached token, register in "pending auth" state.
	// The user completes OAuth via the dashboard's "Connect" button.
	if !handler.HasToken() {
		sc := &serverConn{
			name:         name,
			transport:    "sse",
			url:          resolvedURL,
			cfg:          cfg,
			disabledSet:  buildDisabledSet(cfg.DisabledTools),
			oauthHandler: handler,
		}
		m.mu.Lock()
		m.disabledCount += len(sc.disabledSet)
		m.servers[name] = sc
		m.mu.Unlock()
		m.logger.Info("oauth: tool registered pending authorization",
			slog.String("tool", name))
		return handler, true, nil
	}

	return handler, false, nil
}

// buildDisabledSet converts a slice of tool names into a lookup map.
func buildDisabledSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}

// discoverTools calls ListTools on the server's session, stores the result on
// the connection, and rebuilds the manager's advertised tool index. Called by
// both RegisterServer and RegisterSession.
//
// The server's own list replaces any previous one rather than accumulating, so
// re-discovery (restart, OAuth reconnect) cannot leave stale definitions
// behind.
func (m *Manager) discoverTools(ctx context.Context, sc *serverConn) error {
	timeout := time.Duration(m.mcpCfg.RequestTimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	discoverCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := sc.session.ListTools(discoverCtx, nil)
	if err != nil {
		_ = sc.session.Close()
		return fmt.Errorf("listing tools from MCP server %q: %w", sc.name, err)
	}

	defs := make([]llm.ToolDef, 0, len(result.Tools))
	var hinted map[string]bool
	for _, tool := range result.Tools {
		// Convert InputSchema (*jsonschema.Schema) to map[string]any for OpenAI format.
		params, err := schemaToMap(tool.InputSchema)
		if err != nil {
			m.logger.Warn("skipping tool with unparseable schema",
				"server", sc.name, "tool", tool.Name, "error", err)
			continue
		}

		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			if hinted == nil {
				hinted = make(map[string]bool)
			}
			hinted[tool.Name] = true
		}

		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  params,
			},
		})
		m.logger.Debug("discovered tool", "server", sc.name, "tool", tool.Name)
	}

	m.mu.Lock()
	sc.readOnlyHinted = hinted
	sc.tools = defs
	m.noteDiscoveryOrder(sc.name)
	collisions, cleared := m.rebuildToolIndex()
	m.mu.Unlock()

	// Report outside the lock — the auditor may block.
	m.reportCollisions(ctx, sc.name, collisions)
	m.reportClearedCollisions(cleared)

	return nil
}

// RegisterSession registers an already-connected MCP client session without
// spawning a subprocess. Use this for in-process servers (e.g. configmcp).
func (m *Manager) RegisterSession(ctx context.Context, name string, session *mcp.ClientSession) error {
	sc := &serverConn{name: name, session: session}

	m.mu.Lock()
	m.servers[name] = sc
	m.mu.Unlock()

	return m.discoverTools(ctx, sc)
}

// AdoptFrom stores a reference to source as a parent manager. The child
// manager delegates tool lookups to the parent, so tools added to the parent
// at runtime (e.g. via the REST API) are immediately visible to all agents.
// Both managers share the same underlying *mcp.ClientSession pointers,
// which is safe for concurrent use.
func (m *Manager) AdoptFrom(source *Manager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parent = source
}

// ToolDefs returns OpenAI-format tool definitions for all registered tools,
// including those from the parent manager (if any). Disabled tools are
// excluded from the result.
func (m *Manager) ToolDefs() []llm.ToolDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

	local := m.enabledToolDefs()

	if m.parent == nil {
		return local
	}
	parentDefs := m.parent.ToolDefs()
	if len(local) == 0 {
		return parentDefs
	}
	merged := make([]llm.ToolDef, 0, len(parentDefs)+len(local))
	merged = append(merged, parentDefs...)
	merged = append(merged, local...)
	return merged
}

// enabledToolDefs returns toolDefs with disabled tools filtered out.
// The disabled check resolves through resolveTool so it consults the owning
// server's set under the server-local name — a tool disabled on one server is
// never re-advertised because another server happens to share the name.
// Caller must hold m.mu.
func (m *Manager) enabledToolDefs() []llm.ToolDef {
	if m.disabledCount == 0 {
		return m.toolDefs
	}

	filtered := make([]llm.ToolDef, 0, len(m.toolDefs))
	for _, td := range m.toolDefs {
		if sc, local, err := m.resolveTool(td.Function.Name); err == nil && sc.disabledSet[local] {
			continue
		}
		filtered = append(filtered, td)
	}
	return filtered
}

// recomputeDisabledCount re-derives disabledCount from all servers.
// Caller must hold m.mu (write lock).
func (m *Manager) recomputeDisabledCount() {
	var count int
	for _, sc := range m.servers {
		count += len(sc.disabledSet)
	}
	if count != m.disabledCount {
		m.logger.Warn("disabledCount drift detected and corrected",
			slog.Int("had", m.disabledCount),
			slog.Int("recomputed", count),
		)
	}
	m.disabledCount = count
}

// ToolNames returns the names of all registered MCP tools,
// including those from the parent manager (if any).
func (m *Manager) ToolNames() []string {
	defs := m.ToolDefs()
	names := make([]string, len(defs))
	for i, td := range defs {
		names[i] = td.Function.Name
	}
	return names
}

// ServerToolDefs returns tool definitions for a specific server, under the
// names they are advertised by (server-qualified when another server shares
// the tool name). Returns false if the server is not registered.
func (m *Manager) ServerToolDefs(serverName string) ([]llm.ToolDef, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sc, ok := m.servers[serverName]
	if !ok {
		if m.parent != nil {
			return m.parent.ServerToolDefs(serverName)
		}
		return nil, false
	}

	var defs []llm.ToolDef
	for _, td := range m.toolDefs {
		if owner, exists := m.toolMap[td.Function.Name]; exists && owner == sc {
			defs = append(defs, td)
		}
	}
	return defs, true
}

// ToolServer returns the MCP server name that hosts the given tool.
// Returns an empty string if the tool is not found, or if a bare name is
// ambiguous — there is no single hosting server to name.
func (m *Manager) ToolServer(toolName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sc, _, err := m.resolveTool(toolName)
	if err != nil {
		return ""
	}
	return sc.name
}

// builtinIdempotentTools names in-process read-only tools whose results are
// safe to memoize within one turn. Keyed by MCP tool name; consulted only for
// session-registered (in-process) servers.
var builtinIdempotentTools = map[string]bool{
	"kv_get":     true,
	"kv_list":    true,
	"web_fetch":  true,
	"web_search": true,
}

// IsIdempotent reports whether toolName's results may be memoized within a
// single turn. In-process tools use the built-in allowlist; external MCP
// tools default to false unless their [tools.*] config opts in via
// idempotent / idempotent_tools, or via trust_annotations for tools the
// server marked readOnlyHint at discovery.
func (m *Manager) IsIdempotent(toolName string) bool {
	m.mu.RLock()
	sc, local, err := m.resolveTool(toolName)
	parent := m.parent
	// Everything the decision needs is read under the lock: discoverTools and
	// SetDisabledTools mutate these fields under m.mu during restart,
	// re-register and config edits.
	var inProcess, optedIn, trusted, hinted bool
	if err == nil {
		// In-process sessions (RegisterSession) have no transport and no
		// command; external servers always set one of them.
		inProcess = sc.transport == "" && sc.command == ""
		optedIn = sc.cfg.IsIdempotentTool(local)
		trusted = sc.cfg.TrustAnnotations
		hinted = sc.readOnlyHinted[local]
	}
	m.mu.RUnlock()

	if err != nil {
		// An ambiguous name is not memoizable — and must not be answered by
		// the parent either, since the collision is here.
		if parent != nil && errors.Is(err, ErrToolNotFound) {
			return parent.IsIdempotent(toolName)
		}
		return false
	}
	if inProcess {
		return builtinIdempotentTools[local]
	}
	if optedIn {
		return true
	}
	return trusted && hinted
}

// ToolDescription returns the MCP description for the named tool, or ""
// if the tool is not found or has no description.
//
// Lookup is by *advertised* name, which is the surface the model calls: a bare
// name two servers claim has no definition of its own, so it returns "" rather
// than describing one of the two candidates.
func (m *Manager) ToolDescription(toolName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, td := range m.toolDefs {
		if td.Function.Name == toolName {
			return td.Function.Description
		}
	}
	if m.parent != nil {
		return m.parent.ToolDescription(toolName)
	}
	return ""
}

// Execute runs a single tool call and returns the text result.
// If the tool is not found locally, it delegates to the parent manager.
//
// The name the model called (bare when unique, "<server>__<tool>" when two
// servers share it) is resolved to its owning server; the server-local name is
// what goes on the wire. A bare name that two servers claim is never guessed
// at — it returns ErrAmbiguousTool naming the alternatives.
func (m *Manager) Execute(ctx context.Context, call llm.ToolCall) (string, error) {
	// Admission to the in-flight set is taken under the same read lock that
	// resolves the server and snapshots its session: teardown phase 1 flips the
	// conn to draining under the write lock, so an admission decided after the
	// unlock could let a call slip past a teardown that has already begun.
	m.mu.RLock()
	sc, local, resolveErr := m.resolveTool(call.Function.Name)
	parent := m.parent
	var (
		serverName string
		session    *mcp.ClientSession
		disabled   bool
		admitted   bool
	)
	if resolveErr == nil {
		serverName, session, disabled = sc.name, sc.session, sc.disabledSet[local]
		admitted = sc.enterCall()
	}
	m.mu.RUnlock()

	if resolveErr != nil {
		// Only a genuine miss delegates: an ambiguous name means the collision
		// is in this manager, and the parent cannot resolve it either.
		if errors.Is(resolveErr, ErrToolNotFound) {
			if parent != nil {
				return parent.Execute(ctx, call)
			}
			return "", fmt.Errorf("unknown tool %q", call.Function.Name)
		}
		return "", resolveErr
	}
	if !admitted {
		return "", fmt.Errorf("tool %q on MCP server %q: %w", call.Function.Name, serverName, ErrServerDraining)
	}
	defer sc.leaveCall()

	if disabled {
		return "", fmt.Errorf("tool %q is disabled", call.Function.Name)
	}

	ctx, span := toolTracer.Start(ctx, "tool.execute", trace.WithAttributes(
		attribute.String("tool.name", call.Function.Name),
		attribute.String("tool.server", serverName),
		attribute.Int("tool.args.size_bytes", len(call.Function.Arguments)),
	))
	start := time.Now()
	defer func() {
		elapsed := time.Since(start).Seconds()
		toolDuration.Record(ctx, elapsed,
			metric.WithAttributes(
				attribute.String("tool.name", call.Function.Name),
				attribute.String("tool.server", serverName),
			))
		span.End()
	}()

	if session == nil {
		err := fmt.Errorf("tool %q is not connected (OAuth authorization required)", call.Function.Name)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	var arguments map[string]any
	if call.Function.Arguments != "" {
		// Arg-parse failure is arguably a model-driven rejection, but we keep it
		// as a plain "failed" error for now to keep this change tightly scoped.
		if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
			err = fmt.Errorf("parsing tool arguments: %w", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return "", err
		}
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      local,
		Arguments: arguments,
	})
	if err != nil {
		err = fmt.Errorf("calling tool %q: %w", call.Function.Name, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	// Extract text from content blocks.
	var text string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if text != "" {
				text += "\n"
			}
			text += tc.Text
		}
	}

	span.SetAttributes(attribute.Int("tool.result.size_bytes", len(text)))

	if result.IsError {
		// App-level rejection: the tool is healthy but returned an error result
		// (typically bad/unusable args). Surface as a typed RejectionError so
		// telemetry can distinguish this from transport/exec failures above.
		err = &RejectionError{Tool: call.Function.Name, Text: text}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return text, err
	}

	span.SetStatus(codes.Ok, "")
	return text, nil
}

// CleanupOAuthToken removes the OAuth token for a tool, if any.
// Called during tool removal to avoid leaving orphaned tokens.
//
// A live OAuth connection clears through its handler, which also drops the
// in-memory token source. Handlers exist only on live connections, though —
// a tool that is disabled, config-errored, pending, or not registered at all
// may still own a persisted row keyed by its name, so those fall through to
// a direct store delete. Otherwise the row survives and a tool later created
// with the same name silently adopts the stale token.
func (m *Manager) CleanupOAuthToken(name string) {
	m.mu.RLock()
	sc, ok := m.servers[name]
	m.mu.RUnlock()

	if ok && sc.oauthHandler != nil {
		if err := sc.oauthHandler.ClearToken(); err != nil {
			m.logger.Warn("oauth: failed to clean up token on removal",
				slog.String("tool", name),
				slog.String("error", err.Error()))
		} else {
			return
		}
	}

	if m.oauth != nil && m.oauth.TokenStore != nil {
		if err := m.oauth.TokenStore.Delete(name); err != nil {
			m.logger.Warn("oauth: failed to delete stored token on removal",
				slog.String("tool", name),
				slog.String("error", err.Error()))
		}
	}
}

// GetOAuthHandler returns the OAuth handler for a tool, or nil.
func (m *Manager) GetOAuthHandler(name string) oauthHandler {
	m.mu.RLock()
	sc, ok := m.servers[name]
	m.mu.RUnlock()

	if ok {
		return sc.oauthHandler
	}
	return nil
}

// ServerResolvedURL returns the resolved (non-redacted) URL for a remote
// server. Returns empty string if the server is not found or has no URL.
func (m *Manager) ServerResolvedURL(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if sc, ok := m.servers[name]; ok {
		return sc.url
	}
	return ""
}

// defaultDrainTimeout bounds how long teardown waits for in-flight tool calls.
// Just above the engine's 30s per-tool-call timeout, so a call that is going to
// complete gets to.
const defaultDrainTimeout = 35 * time.Second

// shutdownDrainTimeout caps the per-server drain during process shutdown.
// Shutdown trades a possibly-truncated call for a prompt exit; the ordinary
// teardown ceiling would make a hung server hold the process open far too long.
const shutdownDrainTimeout = 5 * time.Second

// drainTimeout returns the configured [mcp] drain_timeout, falling back to
// defaultDrainTimeout for unset or unparseable values.
func (m *Manager) drainTimeout() time.Duration {
	if d, err := time.ParseDuration(m.mcpCfg.DrainTimeout); err == nil && d > 0 {
		return d
	}
	return defaultDrainTimeout
}

// UnregisterServer stops the MCP server for the given config name,
// removes its tools from the tool map, and closes the connection.
// Returns an error if the server is not registered.
//
// Teardown is two-phase. Phase 1 runs under the write lock and only unpublishes
// the server: its tools leave the advertised set and new calls are refused with
// ErrServerDraining. Phase 2 runs with no manager lock held — it waits out the
// calls already executing, then closes the transport. The wait matters because
// the MCP SDK's ClientSession.Close blocks until outgoing calls retire; holding
// m.mu across it would freeze every other agent's tool calls, the health
// checker, and the tools API for the whole drain.
//
// The two-phase shape follows the drain-before-withdraw teardown model in
// "A Programming Paradigm for Spatiotemporal Composability" (Shi, Zhang, Cui —
// https://github.com/cordiverse/paper/blob/main/paper.pdf): a component stops
// being offered before it is dismantled, and the effects already in flight are
// allowed to retire under a bounded window rather than being severed.
func (m *Manager) UnregisterServer(name string) error {
	// Phase 1: unpublish, fast, under the write lock.
	m.mu.Lock()
	sc, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("server %q: %w", name, ErrToolNotFound)
	}

	done, inFlight := sc.beginDrain()

	// Drop this server's contribution and rebuild the advertised projection
	// from what remains, rather than filtering definitions in place: under a
	// name collision, in-place filtering deleted the *owner's* entry when the
	// other collider was unregistered, and left the survivor's definition
	// stranded when the owner went first.
	//
	// Clearing sc.tools matters beyond this rebuild: handleServerFailure
	// re-inserts this same serverConn after unregistering it, and a stale list
	// would resurrect the dead server's tools on the next rebuild.
	sc.tools = nil
	delete(m.servers, name)
	m.discoveryOrder = slices.DeleteFunc(m.discoveryOrder, func(s string) bool { return s == name })
	_, cleared := m.rebuildToolIndex()
	m.reportClearedCollisions(cleared)
	m.recomputeDisabledCount()
	m.mu.Unlock()

	// Phase 2: drain, then close — no manager lock held.
	m.drainAndClose(sc, done, inFlight, m.drainTimeout())
	return nil
}

// drainAndClose is teardown phase 2. It waits for the conn's in-flight calls to
// retire, bounded by timeout, then closes the transport. Must be called with no
// manager lock held, after beginDrain has already unpublished the server.
//
// A ceiling that expires forces the close and emits a single forced_close audit
// event. Session cleanup is best-effort: the server is already out of the maps,
// so a close error has nowhere useful to go — log it and move on.
func (m *Manager) drainAndClose(sc *serverConn, done <-chan struct{}, inFlight int, timeout time.Duration) {
	if inFlight > 0 {
		m.logger.Info("draining MCP server before close",
			"server", sc.name, "in_flight", inFlight, "drain_timeout", timeout)
		select {
		case <-done:
		case <-time.After(timeout):
			m.forcedClose(sc, timeout)
		}
	}

	if sc.session != nil {
		if err := sc.session.Close(); err != nil {
			m.logger.Warn("error closing MCP session during unregister",
				"server", sc.name, "error", err)
		}
	}
	sc.finishDrain()
}

// forcedClose records a drain window that expired with calls still running.
func (m *Manager) forcedClose(sc *serverConn, timeout time.Duration) {
	remaining := sc.inFlightCount()
	m.logger.Warn("MCP drain window expired, forcing close",
		"server", sc.name, "in_flight", remaining, "drain_timeout", timeout)
	if m.Auditor == nil {
		return
	}
	m.Auditor.Emit(context.Background(), audit.Event{
		Category: audit.CategoryMCP,
		Action:   "forced_close",
		Summary:  fmt.Sprintf("MCP server %s closed with %d call(s) still in flight", sc.name, remaining),
		Detail: fmt.Sprintf(`{"server":"%s","in_flight":%d,"drain_timeout":"%s"}`,
			sc.name, remaining, timeout),
		Status: audit.StatusError,
		Source: "tool_manager",
	})
}

// RestartServer stops and re-registers an MCP server using its stored config.
// It resets the server's health state (disabled flag, error, restart count).
// If re-registration fails the server remains visible with status "error"
// so the user can retry or the health checker can pick it up.
func (m *Manager) RestartServer(ctx context.Context, name string) error {
	m.mu.RLock()
	sc, ok := m.servers[name]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("server %q: %w", name, ErrToolNotFound)
	}
	cfg := sc.cfg
	// Carry over the negotiated transport so restarts skip failed protocols
	// (e.g. don't retry Streamable HTTP for servers that only support legacy SSE).
	if sc.transport != "" && sc.transport != cfg.Transport {
		cfg.Transport = sc.transport
	}
	transport := sc.transport
	url := sc.url
	m.mu.RUnlock()

	if err := m.UnregisterServer(name); err != nil {
		return fmt.Errorf("stopping server %q: %w", name, err)
	}

	// Re-add a placeholder so registerSSE sees isReregistration=true (needed
	// for OAuth tools) and so the tool stays visible if RegisterServer fails.
	m.mu.Lock()
	placeholder := &serverConn{
		name:      name,
		transport: transport,
		url:       url,
		cfg:       cfg,
	}
	m.servers[name] = placeholder
	m.mu.Unlock()

	if err := m.RegisterServer(ctx, name, cfg); err != nil {
		// Registration failed — keep the placeholder with error status so the
		// tool remains visible in the UI and can be retried.
		m.mu.Lock()
		placeholder.lastError = err.Error()
		m.mu.Unlock()
		return fmt.Errorf("restarting server %q: %w", name, err)
	}

	m.mu.Lock()
	if newSc, ok := m.servers[name]; ok {
		newSc.connectedAt = time.Now()
	}
	m.mu.Unlock()

	m.logger.Info("MCP server manually restarted", "server", name)
	return nil
}

// ServerNames returns the names of all registered MCP servers,
// including those from the parent manager (if any).
func (m *Manager) ServerNames() []string {
	m.mu.RLock()
	parent := m.parent
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	m.mu.RUnlock()

	if parent != nil {
		seen := make(map[string]bool, len(names))
		for _, n := range names {
			seen[n] = true
		}
		for _, n := range parent.ServerNames() {
			if !seen[n] {
				names = append(names, n)
			}
		}
	}
	return names
}

// ServerInfo returns metadata about a registered server.
// The second return value is false if the server is not registered.
// Checks the parent manager if the server is not found locally.
func (m *Manager) ServerInfo(name string) (ServerStatus, bool) {
	// The read lock covers the whole build, not just the lookup. Every
	// serverConn field buildServerStatus reads — lastError, restartCount,
	// connectedAt, disabledSet — is written under m.mu's write lock by
	// handleServerFailure and SetDisabledTools, so building the status after
	// unlocking races them on the dashboard-polled GET /api/v1/tools path.
	// oauthHandler.HasToken is an in-memory check that never re-enters m.mu,
	// so it is safe to call from here.
	m.mu.RLock()
	sc, ok := m.servers[name]
	parent := m.parent
	if !ok {
		m.mu.RUnlock()
		if parent != nil {
			return parent.ServerInfo(name)
		}
		return ServerStatus{}, false
	}

	// Server-local tool names in discovery order: this is what the disable
	// controls and [tools.*] disabled_tools are keyed by, and it stays correct
	// when the advertised name is server-qualified by a collision.
	var toolNames []string
	if len(sc.tools) > 0 {
		toolNames = make([]string, 0, len(sc.tools))
		for _, td := range sc.tools {
			toolNames = append(toolNames, td.Function.Name)
		}
	}

	ss := buildServerStatus(sc, toolNames)

	if sc.cfg.Auth == "oauth" {
		ss.AuthType = "oauth"
		if sc.oauthHandler != nil {
			ss.OAuthStatus = &OAuthStatusInfo{
				HasToken:    sc.oauthHandler.HasToken(),
				NeedsReauth: !sc.oauthHandler.HasToken(),
			}
		} else {
			ss.OAuthStatus = &OAuthStatusInfo{NeedsReauth: true}
		}
	}
	m.mu.RUnlock()

	return ss, true
}

func serverConnStatus(sc *serverConn) string {
	switch {
	case sc.drainStatus() == drainStateDraining:
		// Teardown phase 2: unpublished, waiting out in-flight calls. Wins over
		// every other state because it describes what is happening right now —
		// a server torn down after a health failure carries lastError too.
		return "draining"
	case sc.userDisabled:
		return "disabled"
	case sc.configError != "":
		return "config_error"
	case sc.disabled:
		return "disabled"
	case sc.session == nil && sc.cfg.Auth == "oauth":
		return "pending_auth"
	case sc.connecting:
		return "connecting"
	case sc.lastError != "":
		return "error"
	default:
		return "connected"
	}
}

func buildServerStatus(sc *serverConn, toolNames []string) ServerStatus {
	var displayURL string
	if sc.url != "" {
		displayURL = redactURL(sc.url)
	}

	var uptimeSecs float64
	if !sc.connectedAt.IsZero() {
		uptimeSecs = time.Since(sc.connectedAt).Seconds()
	}

	totalCount := len(toolNames)
	disabledCount := 0
	for _, tn := range toolNames {
		if sc.disabledSet[tn] {
			disabledCount++
		}
	}

	var disabledTools []string
	if len(sc.cfg.DisabledTools) > 0 {
		disabledTools = sc.cfg.DisabledTools
	}

	return ServerStatus{
		Name:           sc.name,
		Command:        sc.command,
		Args:           sc.args,
		ArgsCount:      len(sc.args),
		ToolNames:      toolNames,
		Status:         serverConnStatus(sc),
		Transport:      sc.transport,
		URL:            displayURL,
		RestartCount:   sc.restartCount,
		LastError:      sc.lastError,
		UptimeSecs:     uptimeSecs,
		DisabledTools:  disabledTools,
		EnabledCount:   totalCount - disabledCount,
		TotalToolCount: totalCount,
		Enabled:        !sc.userDisabled && !sc.disabled && sc.configError == "",
		ConfigError:    sc.configError,
	}
}

// ServerToolConfig returns the stored config.ToolConfig for a registered server.
// This is used to pre-populate edit forms. Returns false if not found.
func (m *Manager) ServerToolConfig(name string) (config.ToolConfig, bool) {
	m.mu.RLock()
	sc, ok := m.servers[name]
	m.mu.RUnlock()

	if !ok {
		return config.ToolConfig{}, false
	}
	return sc.cfg, true
}

// SetDisabledTools updates the in-memory disabled tool set for a server.
// No MCP reconnect is performed — changes take effect on the next ToolDefs() call.
func (m *Manager) SetDisabledTools(serverName string, disabled []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sc, ok := m.servers[serverName]
	if !ok {
		return fmt.Errorf("tool server %q not found", serverName)
	}
	m.disabledCount += len(disabled) - len(sc.disabledSet)
	sc.disabledSet = buildDisabledSet(disabled)
	sc.cfg.DisabledTools = disabled
	return nil
}

// Close shuts down all MCP server connections and OAuth handlers.
//
// Shutdown follows the same two phases as UnregisterServer — every server is
// unpublished under one write lock, then drained and closed with no lock held.
// The drains run concurrently and under a short ceiling, so a process exit
// costs at most one drain window rather than one per server.
func (m *Manager) Close() error {
	type pending struct {
		sc       *serverConn
		done     <-chan struct{}
		inFlight int
	}

	// Phase 1: unpublish everything. Clear each server's tool list and rebuild
	// rather than hand-resetting the derived maps: toolMap/owners/localOf/
	// toolDefs are one projection of serverConn.tools, and resetting a subset
	// would leave owners and localOf pointing at torn-down conns. The rebuild's
	// collision reports are dropped on purpose — nothing is reclaiming a name
	// at shutdown. disabledCount is set directly for the same reason
	// recomputeDisabledCount is not called: an emptied registry is a known 0,
	// not drift worth warning about.
	m.mu.Lock()
	drains := make([]pending, 0, len(m.servers))
	for _, sc := range m.servers {
		done, inFlight := sc.beginDrain()
		sc.tools = nil
		drains = append(drains, pending{sc: sc, done: done, inFlight: inFlight})
	}
	m.servers = make(map[string]*serverConn)
	m.discoveryOrder = nil
	m.rebuildToolIndex()
	m.disabledCount = 0
	m.mu.Unlock()

	timeout := min(m.drainTimeout(), shutdownDrainTimeout)

	// Phase 2: drain and close, concurrently, no lock held.
	var wg sync.WaitGroup
	errs := make([]error, len(drains))
	for i, p := range drains {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.sc.oauthHandler != nil {
				p.sc.oauthHandler.Close()
			}
			if p.inFlight > 0 {
				select {
				case <-p.done:
				case <-time.After(timeout):
					m.forcedClose(p.sc, timeout)
				}
			}
			if p.sc.session != nil {
				if err := p.sc.session.Close(); err != nil {
					errs[i] = fmt.Errorf("closing MCP server %q: %w", p.sc.name, err)
				}
			}
			p.sc.finishDrain()
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// StartHealthChecker runs a background goroutine that periodically probes MCP
// servers and restarts crashed ones. It respects the [mcp] config settings:
// auto_restart, max_restart_attempts, and restart_cooldown.
func (m *Manager) StartHealthChecker(ctx context.Context, interval time.Duration) {
	if m.mcpCfg.AutoRestart != nil && !*m.mcpCfg.AutoRestart {
		m.logger.Info("MCP auto-restart disabled")
		return
	}

	cooldown := 5 * time.Minute
	if d, err := time.ParseDuration(m.mcpCfg.RestartCooldown); err == nil && d > 0 {
		cooldown = d
	}
	maxAttempts := m.mcpCfg.MaxRestartAttempts
	if maxAttempts == 0 {
		maxAttempts = 3
	}
	failThreshold := m.mcpCfg.HealthFailThreshold
	if failThreshold == 0 {
		failThreshold = 3
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.checkServers(ctx, maxAttempts, cooldown, failThreshold)
			}
		}
	}()
}

// checkServers probes each registered server and restarts any that are unresponsive.
// failThreshold is the number of consecutive probe failures before a health_fail
// audit event is emitted for remote servers (stdio servers emit immediately).
func (m *Manager) checkServers(ctx context.Context, maxAttempts int, cooldown time.Duration, failThreshold int) {
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		m.mu.RLock()
		sc, ok := m.servers[name]
		m.mu.RUnlock()
		if !ok || sc.disabled || sc.userDisabled || sc.configError != "" {
			// Skip disabled and config-error servers.
			continue
		}
		if sc.drainStatus() == drainStateDraining {
			// Teardown owns this conn right now. Probing it would fail and kick
			// off a redundant second teardown of the same server. A *closed*
			// conn is deliberately not skipped: handleServerFailure re-inserts
			// one when re-registration fails, relying on the next probe to fail
			// and retry.
			continue
		}
		// OAuth tools that never completed authorization sit with session==nil
		// and are invisible to the probe path below (we deliberately do not
		// ListTools-probe a tokenless server). Surface them in the audit log
		// once, debounced, so a dead OAuth tool doesn't fail silently for days.
		if sc.cfg.Auth == "oauth" {
			m.auditOAuthPending(ctx, sc, name)
		}
		if sc.transport == "" || sc.session == nil {
			// Skip in-process and not-yet-connected (incl. OAuth-pending) servers.
			continue
		}

		probeCtx, probeSpan := toolTracer.Start(ctx, "tool.health_check", trace.WithAttributes(
			attribute.String("tool.server", name),
			attribute.String("tool.transport", sc.transport),
		))
		if err := m.probeServer(probeCtx, sc); err != nil {
			probeSpan.RecordError(err)
			probeSpan.SetStatus(codes.Error, err.Error())
			m.auditProbeFailure(probeCtx, sc, name, failThreshold, err)
			m.handleServerFailure(probeCtx, sc, maxAttempts, cooldown, err.Error())
			probeSpan.End()
		} else {
			probeSpan.SetStatus(codes.Ok, "")
			probeSpan.End()
			// Reset the consecutive-failure counter after the server has been
			// connected longer than the cooldown. This must run on every
			// healthy probe (not just error→success transitions), otherwise
			// the counter drifts monotonically across intermittent failures
			// separated by long healthy periods.
			m.mu.Lock()
			if sc.restartCount > 0 && !sc.connectedAt.IsZero() && time.Since(sc.connectedAt) > cooldown {
				sc.restartCount = 0
			}
			sc.probeFailures = 0
			sc.lastError = ""
			m.mu.Unlock()
		}
	}
}

// auditProbeFailure bumps the consecutive-failure counter and emits a
// health_fail audit event. Emission is debounced for remote servers —
// transient network drops shouldn't pollute the error view until
// failThreshold consecutive failures — while a dead local stdio subprocess
// is meaningful immediately.
func (m *Manager) auditProbeFailure(ctx context.Context, sc *serverConn, name string, failThreshold int, err error) {
	m.logger.Warn("MCP server health check failed", "server", name, "error", err)
	m.mu.Lock()
	sc.probeFailures++
	probeFailures := sc.probeFailures
	m.mu.Unlock()
	if m.Auditor != nil && (sc.transport == "stdio" || probeFailures >= failThreshold) {
		m.Auditor.Emit(ctx, audit.Event{
			Category: audit.CategoryMCP,
			Action:   "health_fail",
			Summary:  fmt.Sprintf("MCP server %s health check failed", name),
			Detail:   fmt.Sprintf(`{"server":"%s","consecutive_failures":%d}`, name, probeFailures),
			Status:   audit.StatusError,
			Source:   "health_checker",
		})
	}
}

// auditOAuthPending emits a single audit warning when an OAuth tool is stuck in
// pending_auth (no session and no token). Emission is debounced via a
// per-serverConn oauthWarned flag so a tool that never authorizes doesn't warn
// on every 30s tick. The flag resets once a token/session appears so a tool
// that later breaks again will re-warn.
func (m *Manager) auditOAuthPending(ctx context.Context, sc *serverConn, name string) {
	hasToken := sc.oauthHandler != nil && sc.oauthHandler.HasToken()

	m.mu.Lock()
	if sc.session != nil || hasToken {
		// Authorization completed — clear the flag so a future breakage re-warns.
		sc.oauthWarned = false
		m.mu.Unlock()
		return
	}
	if sc.oauthWarned {
		m.mu.Unlock()
		return
	}
	sc.oauthWarned = true
	m.mu.Unlock()

	m.logger.Warn("OAuth MCP tool stuck in pending_auth", "server", name)
	if m.Auditor != nil {
		m.Auditor.Emit(ctx, audit.Event{
			Category: audit.CategoryMCP,
			Action:   "oauth_pending",
			Summary:  fmt.Sprintf("OAuth MCP tool %s is stuck awaiting authorization", name),
			Detail:   fmt.Sprintf(`{"server":"%s","needs_reauth":true}`, name),
			Status:   audit.StatusError,
			Source:   "health_checker",
		})
	}
}

// probeServer sends a ListTools request to verify the server is responsive.
func (m *Manager) probeServer(ctx context.Context, sc *serverConn) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := sc.session.ListTools(probeCtx, nil)
	return err
}

// handleServerFailure records the error and attempts a restart if allowed.
func (m *Manager) handleServerFailure(ctx context.Context, sc *serverConn, maxAttempts int, cooldown time.Duration, errMsg string) {
	m.mu.Lock()
	sc.lastError = errMsg
	sc.restartCount++

	if sc.restartCount > maxAttempts {
		sc.disabled = true
		m.mu.Unlock()
		m.logger.Error("MCP server disabled after max restart attempts",
			"server", sc.name, "attempts", sc.restartCount-1)
		return
	}

	attempt := sc.restartCount
	cfg := sc.cfg
	// Carry over the negotiated transport so restarts skip failed protocols
	// (e.g. don't retry Streamable HTTP for servers that only support legacy SSE).
	if sc.transport != "" && sc.transport != cfg.Transport {
		cfg.Transport = sc.transport
	}
	name := sc.name
	m.mu.Unlock()

	ctx, span := toolTracer.Start(ctx, "tool.restart", trace.WithAttributes(
		attribute.String("tool.server", name),
		attribute.Int("tool.restart.attempt", attempt),
		attribute.String("tool.restart.trigger", errMsg),
		attribute.String("tool.transport", cfg.Transport),
	))
	defer span.End()

	// Exponential backoff: 2^(attempt-1) seconds, capped at 60s.
	backoffSecs := 1 << (attempt - 1)
	if backoffSecs > 60 {
		backoffSecs = 60
	}
	span.SetAttributes(attribute.Int("tool.restart.backoff_secs", backoffSecs))
	m.logger.Info("restarting MCP server",
		"server", name, "attempt", attempt, "backoff_secs", backoffSecs)

	select {
	case <-time.After(time.Duration(backoffSecs) * time.Second):
	case <-ctx.Done():
		span.SetStatus(codes.Error, "context cancelled during backoff")
		return
	}

	// Close old session, re-register.
	_ = m.UnregisterServer(name)

	// Re-add the old entry so registerSSE sees isReregistration=true (needed
	// for OAuth tools) and so the tool stays visible if RegisterServer fails.
	// Keep sc.session as-is (closed but non-nil) so the health checker's
	// next cycle can probe it, fail, and retry via handleServerFailure.
	m.mu.Lock()
	m.servers[name] = sc
	m.mu.Unlock()

	if err := m.RegisterServer(ctx, name, cfg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		m.logger.Error("MCP server restart failed", "server", name, "attempt", attempt, "error", err)
		m.mu.Lock()
		sc.lastError = err.Error()
		m.mu.Unlock()
	} else {
		span.SetStatus(codes.Ok, "")
		m.logger.Info("MCP server restarted successfully", "server", name, "attempt", attempt)
		// RegisterServer creates a new serverConn — update health state.
		m.mu.Lock()
		if newSc, ok := m.servers[name]; ok {
			newSc.restartCount = attempt
			newSc.connectedAt = time.Now()
		}
		m.mu.Unlock()
	}
}

// schemaToMap converts a jsonschema.Schema to a generic map for the OpenAI tools API.
func schemaToMap(schema any) (map[string]any, error) {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshaling schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshaling schema to map: %w", err)
	}
	return m, nil
}
