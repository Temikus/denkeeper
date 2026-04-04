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

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
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

	// Health monitoring state.
	connectedAt  time.Time // when the server was last successfully connected
	restartCount int       // consecutive restart attempts
	lastError    string    // most recent failure message
	disabled     bool      // true when restarts exhausted
}

// ServerStatus exposes metadata about a registered MCP server.
type ServerStatus struct {
	Name         string   `json:"name"`
	Command      string   `json:"command,omitempty"`
	Args         []string `json:"-"`          // excluded from JSON (may contain secrets)
	ArgsCount    int      `json:"args_count"` // safe count for display
	ToolNames    []string `json:"tool_names"`
	Status       string   `json:"status"` // "connected", "restarting", "error", "disabled"
	Transport    string   `json:"transport,omitempty"`
	URL          string   `json:"url,omitempty"` // redacted
	RestartCount int      `json:"restart_count,omitempty"`
	LastError    string   `json:"last_error,omitempty"`
	UptimeSecs   float64  `json:"uptime_secs,omitempty"`
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

	switch transport {
	case "stdio":
		return m.registerStdio(ctx, name, cfg)
	case "sse":
		return m.registerSSE(ctx, name, cfg)
	default:
		return fmt.Errorf("unsupported transport %q for MCP server %q", transport, name)
	}
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

// registerSSE connects to a remote MCP server over Streamable HTTP (SSE).
func (m *Manager) registerSSE(ctx context.Context, name string, cfg config.ToolConfig) error {
	// Resolve ${VAR} placeholders in URL and header values (with denylist).
	resolvedURL, err := resolveEnvPlaceholders(cfg.URL, cfg.Env)
	if err != nil {
		return fmt.Errorf("resolving URL for MCP server %q: %w", name, err)
	}

	// SSRF validation.
	if err := validateToolURL(resolvedURL, m.mcpCfg.URLAllowlist); err != nil {
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
	baseRT := http.DefaultTransport
	rt := http.RoundTripper(&redirectCheckingRoundTripper{
		base:      baseRT,
		allowlist: m.mcpCfg.URLAllowlist,
	})
	if len(resolvedHeaders) > 0 {
		rt = &headerRoundTripper{base: rt, headers: resolvedHeaders}
	}

	timeout := time.Duration(m.mcpCfg.RequestTimeoutSecs) * time.Second
	if cfg.RequestTimeoutSecs > 0 {
		timeout = time.Duration(cfg.RequestTimeoutSecs) * time.Second
	}

	httpClient := &http.Client{
		Transport: rt,
		Timeout:   timeout,
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	sseTransport := &mcp.StreamableClientTransport{
		Endpoint:   resolvedURL,
		HTTPClient: httpClient,
	}

	session, err := client.Connect(ctx, sseTransport, nil)
	if err != nil {
		return fmt.Errorf("connecting to remote MCP server %q at %s: %w", name, redactURL(resolvedURL), err)
	}

	sc := &serverConn{
		name:        name,
		transport:   "sse",
		url:         resolvedURL,
		cfg:         cfg,
		session:     session,
		connectedAt: time.Now(),
	}

	m.mu.Lock()
	m.servers[name] = sc
	m.mu.Unlock()

	return m.discoverTools(ctx, sc)
}

// discoverTools calls ListTools on the server's session and populates the
// manager's toolMap and toolDefs. Called by both RegisterServer and RegisterSession.
func (m *Manager) discoverTools(ctx context.Context, sc *serverConn) error {
	result, err := sc.session.ListTools(ctx, nil)
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

	var arguments map[string]any
	if call.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
			return "", fmt.Errorf("parsing tool arguments: %w", err)
		}
	}

	result, err := sc.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      call.Function.Name,
		Arguments: arguments,
	})
	if err != nil {
		return "", fmt.Errorf("calling tool %q: %w", call.Function.Name, err)
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

	if result.IsError {
		return text, fmt.Errorf("tool %q returned error: %s", call.Function.Name, text)
	}

	return text, nil
}

// UnregisterServer stops the MCP server for the given config name,
// removes its tools from the tool map, and closes the connection.
// Returns an error if the server is not registered.
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

	if sc.session != nil {
		if err := sc.session.Close(); err != nil {
			return fmt.Errorf("closing MCP server %q: %w", name, err)
		}
	}
	return nil
}

// RestartServer stops and re-registers an MCP server using its stored config.
// It resets the server's health state (disabled flag, error, restart count).
func (m *Manager) RestartServer(ctx context.Context, name string) error {
	m.mu.RLock()
	sc, ok := m.servers[name]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("server %q is not registered", name)
	}
	cfg := sc.cfg
	m.mu.RUnlock()

	if err := m.UnregisterServer(name); err != nil {
		return fmt.Errorf("stopping server %q: %w", name, err)
	}

	if err := m.RegisterServer(ctx, name, cfg); err != nil {
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
	} else if sc.lastError != "" {
		status = "error"
	}

	var uptimeSecs float64
	if !sc.connectedAt.IsZero() {
		uptimeSecs = time.Since(sc.connectedAt).Seconds()
	}

	return ServerStatus{
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
	}, true
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

// Close shuts down all MCP server connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for name, sc := range m.servers {
		if err := sc.session.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("closing MCP server %q: %w", name, err)
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
		if !ok || sc.disabled || sc.transport == "" {
			// Skip in-process servers and disabled servers.
			continue
		}

		if err := m.probeServer(ctx, sc); err != nil {
			m.logger.Warn("MCP server health check failed", "server", name, "error", err)
			m.handleServerFailure(ctx, sc, maxAttempts, cooldown, err.Error())
		} else if sc.lastError != "" {
			// Server recovered — reset health state.
			m.mu.Lock()
			if !sc.connectedAt.IsZero() && time.Since(sc.connectedAt) > cooldown {
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
	name := sc.name
	m.mu.Unlock()

	// Exponential backoff: 2^(attempt-1) seconds, capped at 60s.
	backoffSecs := 1 << (attempt - 1)
	if backoffSecs > 60 {
		backoffSecs = 60
	}
	m.logger.Info("restarting MCP server",
		"server", name, "attempt", attempt, "backoff_secs", backoffSecs)

	select {
	case <-time.After(time.Duration(backoffSecs) * time.Second):
	case <-ctx.Done():
		return
	}

	// Close old session, re-register.
	_ = m.UnregisterServer(name)
	if err := m.RegisterServer(ctx, name, cfg); err != nil {
		m.logger.Error("MCP server restart failed", "server", name, "attempt", attempt, "error", err)
		m.mu.Lock()
		// Re-add a placeholder so the next health check can retry.
		sc.lastError = err.Error()
		m.servers[name] = sc
		m.mu.Unlock()
	} else {
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
