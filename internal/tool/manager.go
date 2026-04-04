package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/llm"
)

// serverConn tracks a connected MCP server subprocess and its session.
type serverConn struct {
	name    string
	command string   // binary path (empty for in-process sessions)
	args    []string // command-line arguments
	session *mcp.ClientSession
}

// ServerStatus exposes metadata about a registered MCP server.
type ServerStatus struct {
	Name      string
	Command   string
	Args      []string
	ToolNames []string
	Status    string // "connected", "error", "stopped"
}

// Manager manages MCP tool server connections and tool execution.
type Manager struct {
	mu       sync.RWMutex
	parent   *Manager              // optional parent for delegated lookups (set by AdoptFrom)
	servers  map[string]*serverConn // keyed by config name (e.g. "web-search")
	toolMap  map[string]*serverConn // keyed by MCP tool name → owning server
	toolDefs []llm.ToolDef          // cached OpenAI-format tool definitions
	logger   *slog.Logger
}

// NewManager creates a manager with no servers registered.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		servers: make(map[string]*serverConn),
		toolMap: make(map[string]*serverConn),
		logger:  logger,
	}
}

// RegisterServer spawns an MCP server subprocess, connects to it over stdio,
// and discovers its available tools.
func (m *Manager) RegisterServer(ctx context.Context, name, command string, args []string, env map[string]string) error {
	cmd := exec.Command(command, args...) // #nosec G204 -- MCP tool servers are spawned from config-declared commands
	// Inherit the current process environment and overlay tool-specific vars.
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	transport := &mcp.CommandTransport{Command: cmd}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connecting to MCP server %q: %w", name, err)
	}

	sc := &serverConn{name: name, command: command, args: args, session: session}

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

	return ServerStatus{
		Name:      sc.name,
		Command:   sc.command,
		Args:      sc.args,
		ToolNames: toolNames,
		Status:    "connected",
	}, true
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
