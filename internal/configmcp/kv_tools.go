package configmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerKVTools adds the five KV MCP tools to the server.
// Called from registerTools when a KVStore is available.
func (s *Server) registerKVTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "kv_get",
		Description: "Get a value from your key-value store. Returns null if the key doesn't exist or has expired.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {"type": "string", "description": "The key to look up"}
			},
			"required": ["key"]
		}`),
	}, s.handleKVGet)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "kv_set",
		Description: "Store a key-value pair. Overwrites any existing value. Use ttl to set an expiry.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"key":   {"type": "string", "description": "The key to store"},
				"value": {"type": "string", "description": "The value to store (max 64KB)"},
				"ttl":   {"type": "string", "description": "Optional TTL as a Go duration string (e.g. '5m', '24h'). Omit or empty for no expiry."}
			},
			"required": ["key", "value"]
		}`),
	}, s.handleKVSet)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "kv_delete",
		Description: "Delete a key from your key-value store. No error if the key doesn't exist.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {"type": "string", "description": "The key to delete"}
			},
			"required": ["key"]
		}`),
	}, s.handleKVDelete)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "kv_list",
		Description: "List keys in your key-value store, optionally filtered by prefix.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"prefix": {"type": "string", "description": "Optional prefix filter (e.g. 'lock:' to list all locks)"}
			}
		}`),
	}, s.handleKVList)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "kv_set_nx",
		Description: "Set a key only if it doesn't already exist (atomic). Returns whether the key was set. Use this to acquire locks.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"key":   {"type": "string", "description": "The key to set"},
				"value": {"type": "string", "description": "The value to store"},
				"ttl":   {"type": "string", "description": "Optional TTL (e.g. '5m'). Strongly recommended for locks."}
			},
			"required": ["key", "value"]
		}`),
	}, s.handleKVSetNX)
}

func (s *Server) handleKVGet(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Key) == "" {
		return toolError("key is required"), nil
	}

	val, ok, err := s.deps.KVStore.Get(ctx, s.deps.AgentName, input.Key)
	if err != nil {
		return toolError(fmt.Sprintf("kv_get failed: %v", err)), nil
	}
	if !ok {
		return toolText(`{"value": null}`), nil
	}

	resp, _ := json.Marshal(map[string]string{"value": val})
	return toolText(string(resp)), nil
}

func (s *Server) handleKVSet(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("kv_set is not available in restricted mode"), nil
	}

	var input struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		TTL   string `json:"ttl"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Key) == "" {
		return toolError("key is required"), nil
	}

	ttl, err := parseTTL(input.TTL)
	if err != nil {
		return toolError(err.Error()), nil
	}

	if err := s.deps.KVStore.Set(ctx, s.deps.AgentName, input.Key, input.Value, ttl); err != nil {
		return toolError(fmt.Sprintf("kv_set failed: %v", err)), nil
	}

	return toolText(`{"ok": true}`), nil
}

func (s *Server) handleKVDelete(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("kv_delete is not available in restricted mode"), nil
	}

	var input struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Key) == "" {
		return toolError("key is required"), nil
	}

	if err := s.deps.KVStore.Delete(ctx, s.deps.AgentName, input.Key); err != nil {
		return toolError(fmt.Sprintf("kv_delete failed: %v", err)), nil
	}

	return toolText(`{"ok": true}`), nil
}

func (s *Server) handleKVList(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Prefix string `json:"prefix"`
	}
	if req.Params.Arguments != nil {
		_ = json.Unmarshal(req.Params.Arguments, &input)
	}

	entries, err := s.deps.KVStore.List(ctx, s.deps.AgentName, input.Prefix)
	if err != nil {
		return toolError(fmt.Sprintf("kv_list failed: %v", err)), nil
	}

	type entry struct {
		Key       string  `json:"key"`
		Value     string  `json:"value"`
		ExpiresAt *string `json:"expires_at,omitempty"`
		UpdatedAt string  `json:"updated_at"`
	}

	out := make([]entry, len(entries))
	for i, e := range entries {
		out[i] = entry{
			Key:       e.Key,
			Value:     e.Value,
			UpdatedAt: e.UpdatedAt.Format(time.RFC3339),
		}
		if e.ExpiresAt != nil {
			s := e.ExpiresAt.Format(time.RFC3339)
			out[i].ExpiresAt = &s
		}
	}

	resp, _ := json.Marshal(map[string]any{"entries": out})
	return toolText(string(resp)), nil
}

func (s *Server) handleKVSetNX(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("kv_set_nx is not available in restricted mode"), nil
	}

	var input struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		TTL   string `json:"ttl"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Key) == "" {
		return toolError("key is required"), nil
	}

	ttl, err := parseTTL(input.TTL)
	if err != nil {
		return toolError(err.Error()), nil
	}

	acquired, err := s.deps.KVStore.SetNX(ctx, s.deps.AgentName, input.Key, input.Value, ttl)
	if err != nil {
		return toolError(fmt.Sprintf("kv_set_nx failed: %v", err)), nil
	}

	resp, _ := json.Marshal(map[string]bool{"acquired": acquired})
	return toolText(string(resp)), nil
}

func parseTTL(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid ttl %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("ttl must be non-negative, got %s", s)
	}
	return d, nil
}
