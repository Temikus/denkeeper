package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

var (
	toolTracer   = otel.Tracer("denkeeper.tool")
	toolMeter    = otel.Meter("denkeeper.tool")
	toolDuration metric.Float64Histogram
)

func init() {
	toolDuration, _ = toolMeter.Float64Histogram("denkeeper.tool.duration",
		metric.WithDescription("Tool execution latency in seconds"),
		metric.WithUnit("s"))
}

// serverConn tracks a connected MCP server subprocess and its session.
type serverConn struct {
	name      string
	command   string            // binary path (empty for in-process or SSE sessions)
	args      []string          // command-line arguments
	transport string            // "stdio", "sse", or "" (in-process)
	url       string            // remote server URL (SSE only)
	cfg       config.ToolConfig // stored for restart
	session   *mcp.ClientSession

	// Health monitoring state.
	connectedAt  time.Time // when the server was last successfully connected
	restartCount int       // consecutive restart attempts
	lastError    string    // most recent failure message
	disabled     bool      // true when restarts exhausted

	// OAuth state (nil for non-OAuth tools).
	oauthHandler oauthHandler
}

// oauthHandler is an internal interface satisfied by oauth.Handler.
// It abstracts the concrete type so the manager can reference it without
// importing the build-tag-gated oauth package.
type oauthHandler interface {
	ToolName() string
	HasToken() bool
	ClearToken() error
	Close()
}

// OAuthSupport holds OAuth infrastructure injected into the Manager.
type OAuthSupport struct {
	// HandlerFactory creates an oauth.Handler for a tool. This is set from
	// a build-tag-gated init in manager_oauth.go.
	HandlerFactory OAuthHandlerFactory
	CallbackURL    string
}

// OAuthHandlerFactory creates an OAuthHandler and its corresponding
// auth.OAuthHandler for use with StreamableClientTransport.
// The second return value is the transport-compatible handler.
type OAuthHandlerFactory func(name string, cfg config.ToolConfig, httpClient *http.Client) (oauthHandler, any, error)

// ServerStatus exposes metadata about a registered MCP server.
type ServerStatus struct {
	Name         string           `json:"name"`
	Command      string           `json:"command,omitempty"`
	Args         []string         `json:"-"`          // excluded from JSON (may contain secrets)
	ArgsCount    int              `json:"args_count"` // safe count for display
	ToolNames    []string         `json:"tool_names"`
	Status       string           `json:"status"` // "connected", "restarting", "error", "disabled"
	Transport    string           `json:"transport,omitempty"`
	URL          string           `json:"url,omitempty"` // redacted
	RestartCount int              `json:"restart_count,omitempty"`
	LastError    string           `json:"last_error,omitempty"`
	UptimeSecs   float64          `json:"uptime_secs,omitempty"`
	AuthType     string           `json:"auth_type,omitempty"` // "oauth" or ""
	OAuthStatus  *OAuthStatusInfo `json:"oauth_status,omitempty"`
}

// OAuthStatusInfo is a non-sensitive view of OAuth state for API responses.
type OAuthStatusInfo struct {
	HasToken    bool `json:"has_token"`
	NeedsReauth bool `json:"needs_reauth"`
}

// Manager manages MCP tool server connections and tool execution.
type Manager struct {
	mu       sync.RWMutex
	parent   *Manager               // optional parent for delegated lookups (set by AdoptFrom)
	servers  map[string]*serverConn // keyed by config name (e.g. "web-search")
	toolMap  map[string]*serverConn // keyed by MCP tool name → owning server
	toolDefs []llm.ToolDef          // cached OpenAI-format tool definitions
	mcpCfg   config.MCPConfig       // global MCP settings
	logger   *slog.Logger
	oauth    *OAuthSupport // nil if OAuth not configured
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
		logger:  logger,
	}
	if len(mcpCfg) > 0 {
		m.mcpCfg = mcpCfg[0]
	}
	return m
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
	// Inherit the current process environment and overlay tool-specific vars.
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

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
	}

	m.mu.Lock()
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
		oauthHandler: oh,
	}

	m.mu.Lock()
	// Close the old session if we're overwriting (e.g. OAuth reconnect).
	if old, exists := m.servers[name]; exists && old.session != nil {
		_ = old.session.Close()
	}
	m.servers[name] = sc
	m.mu.Unlock()

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
			oauthHandler: handler,
		}
		m.mu.Lock()
		m.servers[name] = sc
		m.mu.Unlock()
		m.logger.Info("oauth: tool registered pending authorization",
			slog.String("tool", name))
		return handler, true, nil
	}

	return handler, false, nil
}

// discoverTools calls ListTools on the server's session and populates the
// manager's toolMap and toolDefs. Called by both RegisterServer and RegisterSession.
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

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, tool := range result.Tools {
		// Convert InputSchema (*jsonschema.Schema) to map[string]any for OpenAI format.
		params, err := schemaToMap(tool.InputSchema)
		if err != nil {
			m.logger.Warn("skipping tool with unparseable schema",
				"server", sc.name, "tool", tool.Name, "error", err)
			continue
		}

		m.toolMap[tool.Name] = sc
		m.toolDefs = append(m.toolDefs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  params,
			},
		})
		m.logger.Debug("discovered tool", "server", sc.name, "tool", tool.Name)
	}

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
// including those from the parent manager (if any).
func (m *Manager) ToolDefs() []llm.ToolDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.parent == nil {
		return m.toolDefs
	}
	parentDefs := m.parent.ToolDefs()
	if len(m.toolDefs) == 0 {
		return parentDefs
	}
	merged := make([]llm.ToolDef, 0, len(parentDefs)+len(m.toolDefs))
	merged = append(merged, parentDefs...)
	merged = append(merged, m.toolDefs...)
	return merged
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

// ServerToolDefs returns tool definitions for a specific server.
// Returns false if the server is not registered.
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
// Returns an empty string if the tool is not found.
func (m *Manager) ToolServer(toolName string) string {
	m.mu.RLock()
	sc, ok := m.toolMap[toolName]
	m.mu.RUnlock()
	if !ok {
		return ""
	}
	return sc.name
}

// Execute runs a single tool call and returns the text result.
// If the tool is not found locally, it delegates to the parent manager.
func (m *Manager) Execute(ctx context.Context, call llm.ToolCall) (string, error) {
	m.mu.RLock()
	sc, ok := m.toolMap[call.Function.Name]
	parent := m.parent
	m.mu.RUnlock()
	if !ok {
		if parent != nil {
			return parent.Execute(ctx, call)
		}
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}

	serverName := sc.name
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

	if sc.session == nil {
		err := fmt.Errorf("tool %q is not connected (OAuth authorization required)", call.Function.Name)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	var arguments map[string]any
	if call.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
			err = fmt.Errorf("parsing tool arguments: %w", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return "", err
		}
	}

	result, err := sc.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      call.Function.Name,
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
		err = fmt.Errorf("tool %q returned error: %s", call.Function.Name, text)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return text, err
	}

	span.SetStatus(codes.Ok, "")
	return text, nil
}

// UnregisterServer stops the MCP server for the given config name,
// removes its tools from the tool map, and closes the connection.
// Returns an error if the server is not registered.
// CleanupOAuthToken removes the OAuth token for a tool, if any.
// Called during tool removal to avoid leaving orphaned tokens.
func (m *Manager) CleanupOAuthToken(name string) {
	m.mu.RLock()
	sc, ok := m.servers[name]
	m.mu.RUnlock()

	if ok && sc.oauthHandler != nil {
		if err := sc.oauthHandler.ClearToken(); err != nil {
			m.logger.Warn("oauth: failed to clean up token on removal",
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

func (m *Manager) UnregisterServer(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sc, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("server %q is not registered", name)
	}

	// Remove tool definitions contributed by this server.
	var remaining []llm.ToolDef
	for _, td := range m.toolDefs {
		if owner, exists := m.toolMap[td.Function.Name]; exists && owner == sc {
			delete(m.toolMap, td.Function.Name)
			continue
		}
		remaining = append(remaining, td)
	}
	m.toolDefs = remaining

	delete(m.servers, name)

	// Best-effort session cleanup. The server is already removed from the map,
	// so returning an error here would leave callers in an inconsistent state
	// (entry gone but error returned). Log and move on.
	if sc.session != nil {
		if err := sc.session.Close(); err != nil {
			m.logger.Warn("error closing MCP session during unregister",
				"server", name, "error", err)
		}
	}
	return nil
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
		return fmt.Errorf("server %q is not registered", name)
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
	m.mu.RLock()
	sc, ok := m.servers[name]
	parent := m.parent
	m.mu.RUnlock()

	if !ok {
		if parent != nil {
			return parent.ServerInfo(name)
		}
		return ServerStatus{}, false
	}

	var toolNames []string
	for tn, owner := range m.toolMap {
		if owner == sc {
			toolNames = append(toolNames, tn)
		}
	}

	var displayURL string
	if sc.url != "" {
		displayURL = redactURL(sc.url)
	}

	status := "connected"
	if sc.disabled {
		status = "disabled"
	} else if sc.session == nil && sc.cfg.Auth == "oauth" {
		// Registered but not yet connected — waiting for OAuth authorization.
		status = "pending_auth"
	} else if sc.lastError != "" {
		status = "error"
	}

	var uptimeSecs float64
	if !sc.connectedAt.IsZero() {
		uptimeSecs = time.Since(sc.connectedAt).Seconds()
	}

	ss := ServerStatus{
		Name:         sc.name,
		Command:      sc.command,
		Args:         sc.args,
		ArgsCount:    len(sc.args),
		ToolNames:    toolNames,
		Status:       status,
		Transport:    sc.transport,
		URL:          displayURL,
		RestartCount: sc.restartCount,
		LastError:    sc.lastError,
		UptimeSecs:   uptimeSecs,
	}

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

	return ss, true
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

// Close shuts down all MCP server connections and OAuth handlers.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for name, sc := range m.servers {
		if sc.oauthHandler != nil {
			sc.oauthHandler.Close()
		}
		if sc.session != nil {
			if err := sc.session.Close(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("closing MCP server %q: %w", name, err)
			}
		}
	}
	return firstErr
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

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.checkServers(ctx, maxAttempts, cooldown)
			}
		}
	}()
}

// checkServers probes each registered server and restarts any that are unresponsive.
func (m *Manager) checkServers(ctx context.Context, maxAttempts int, cooldown time.Duration) {
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
		if !ok || sc.disabled || sc.transport == "" || sc.session == nil {
			// Skip in-process servers, disabled servers, and OAuth-pending tools.
			continue
		}

		probeCtx, probeSpan := toolTracer.Start(ctx, "tool.health_check", trace.WithAttributes(
			attribute.String("tool.server", name),
			attribute.String("tool.transport", sc.transport),
		))
		if err := m.probeServer(probeCtx, sc); err != nil {
			probeSpan.RecordError(err)
			probeSpan.SetStatus(codes.Error, err.Error())
			m.logger.Warn("MCP server health check failed", "server", name, "error", err)
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
			sc.lastError = ""
			m.mu.Unlock()
		}
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
