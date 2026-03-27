package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/llm"
)

// serverConn tracks a connected MCP server subprocess and its session.
type serverConn struct {
	name    string
	session *mcp.ClientSession
}

// Manager manages MCP tool server connections and tool execution.
type Manager struct {
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
	cmd := exec.Command(command, args...)
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

	sc := &serverConn{name: name, session: session}
	m.servers[name] = sc
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
	m.servers[name] = sc
	return m.discoverTools(ctx, sc)
}

// AdoptFrom copies all registered tool connections from source into m.
// Both managers then share the same underlying *mcp.ClientSession pointers,
// which is safe for concurrent use. Use this to give per-agent managers
// access to shared external MCP servers without re-spawning subprocesses.
func (m *Manager) AdoptFrom(source *Manager) {
	for name, sc := range source.servers {
		m.servers[name] = sc
	}
	for toolName, sc := range source.toolMap {
		m.toolMap[toolName] = sc
	}
	m.toolDefs = append(m.toolDefs, source.toolDefs...)
}

// ToolDefs returns OpenAI-format tool definitions for all registered tools.
func (m *Manager) ToolDefs() []llm.ToolDef {
	return m.toolDefs
}

// ToolNames returns the names of all registered MCP tools.
func (m *Manager) ToolNames() []string {
	names := make([]string, len(m.toolDefs))
	for i, td := range m.toolDefs {
		names[i] = td.Function.Name
	}
	return names
}

// Execute runs a single tool call and returns the text result.
func (m *Manager) Execute(ctx context.Context, call llm.ToolCall) (string, error) {
	sc, ok := m.toolMap[call.Function.Name]
	if !ok {
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

// Close shuts down all MCP server connections.
func (m *Manager) Close() error {
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
