package mcpserver

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type kvGetInput struct {
	Agent string `json:"agent" jsonschema:"Agent namespace"`
	Key   string `json:"key" jsonschema:"Key to retrieve"`
}

type kvSetInput struct {
	Agent string `json:"agent" jsonschema:"Agent namespace"`
	Key   string `json:"key" jsonschema:"Key to set"`
	Value string `json:"value" jsonschema:"Value to store"`
	TTL   string `json:"ttl,omitempty" jsonschema:"Time-to-live (Go duration string e.g. 1h). Omit for no expiry."`
}

type kvListInput struct {
	Agent  string `json:"agent" jsonschema:"Agent namespace"`
	Prefix string `json:"prefix,omitempty" jsonschema:"Key prefix filter"`
}

type kvDeleteInput struct {
	Agent string `json:"agent" jsonschema:"Agent namespace"`
	Key   string `json:"key" jsonschema:"Key to delete"`
}

func (s *Server) registerKVTools() {
	if s.deps.KVStore == nil {
		return
	}

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "kv_get",
		Description: "Get a value from the agent KV store. Requires 'kv:read' scope.",
	}, s.handleKVGet)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "kv_set",
		Description: "Set a value in the agent KV store with optional TTL. Requires 'kv:write' scope.",
	}, s.handleKVSet)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "kv_list",
		Description: "List keys in the agent KV store with optional prefix filter. Requires 'kv:read' scope.",
	}, s.handleKVList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "kv_delete",
		Description: "Delete a key from the agent KV store. Requires 'kv:write' scope.",
	}, s.handleKVDelete)
}

func (s *Server) handleKVGet(ctx context.Context, _ *mcp.CallToolRequest, input kvGetInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "kv:read"); err != nil {
		return err, nil, nil
	}

	val, ok, err := s.deps.KVStore.Get(ctx, input.Agent, input.Key)
	if err != nil {
		return toolError("kv get failed: " + err.Error()), nil, nil
	}
	if !ok {
		return toolError("key not found"), nil, nil
	}
	r, jsonErr := toolJSON(map[string]string{"key": input.Key, "value": val})
	return r, nil, jsonErr
}

func (s *Server) handleKVSet(ctx context.Context, _ *mcp.CallToolRequest, input kvSetInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "kv:write"); err != nil {
		return err, nil, nil
	}

	var ttl time.Duration
	if input.TTL != "" {
		var err error
		ttl, err = time.ParseDuration(input.TTL)
		if err != nil {
			return toolError("invalid ttl: " + err.Error()), nil, nil
		}
	}

	if err := s.deps.KVStore.Set(ctx, input.Agent, input.Key, input.Value, ttl); err != nil {
		return toolError("kv set failed: " + err.Error()), nil, nil
	}
	return toolText("ok"), nil, nil
}

func (s *Server) handleKVList(ctx context.Context, _ *mcp.CallToolRequest, input kvListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "kv:read"); err != nil {
		return err, nil, nil
	}
	if strings.TrimSpace(input.Agent) == "" {
		return toolError("agent is required"), nil, nil
	}

	entries, err := s.deps.KVStore.List(ctx, input.Agent, input.Prefix)
	if err != nil {
		return toolError("kv list failed: " + err.Error()), nil, nil
	}

	r, jsonErr := toolJSON(entries)
	return r, nil, jsonErr
}

func (s *Server) handleKVDelete(ctx context.Context, _ *mcp.CallToolRequest, input kvDeleteInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "kv:write"); err != nil {
		return err, nil, nil
	}

	if err := s.deps.KVStore.Delete(ctx, input.Agent, input.Key); err != nil {
		return toolError("kv delete failed: " + err.Error()), nil, nil
	}
	return toolText("deleted"), nil, nil
}
