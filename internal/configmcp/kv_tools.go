package configmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/kv"
)

const (
	defaultKVListMaxBytes       = 16384
	defaultKVListValueHeadBytes = 1024

	// kvListEnvelopeBytes budgets the JSON around the entries array: the
	// object braces plus the truncation flags and note.
	kvListEnvelopeBytes = 192

	// kvTruncationMarker is appended to a head-capped value.
	kvTruncationMarker = "\u2026"
)

// registerKVTools adds the five KV MCP tools to the server.
// Called from registerTools when a KVStore is available.
func (s *Server) registerKVTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "kv_get",
		Description: "Get a value from your key-value store. Returns null if the key doesn't exist or has expired. Keys are conventionally namespaced (`cache:*`, `log:*`, `pref:*`, `state:*`, or anything that fits the use case).",
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
		Description: "Store a key-value pair. Overwrites any existing value. Use ttl to set an expiry. Use a `prefix:subkey` shape so kv_list stays useful; see system prompt for namespace conventions.",
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
		Name: "kv_list",
		Description: fmt.Sprintf(
			"List keys in your key-value store, optionally filtered by prefix. Prefix-filter to scan a namespace (e.g. `log:heartbeat:`). "+
				"Every key is always returned, but values are size-capped: at most %d bytes per value and %d bytes of values per call. "+
				"Past that, entries come back with `value_omitted: true` and the response carries `truncated` plus `omitted_values` - kv_get those keys individually. "+
				"Pass `keys_only: true` when you only need the namespace.",
			s.kvListValueHeadBytes(), s.kvListMaxBytes()),
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"prefix":    {"type": "string", "description": "Optional prefix filter (e.g. 'lock:' to list all locks)"},
				"keys_only": {"type": "boolean", "description": "Return keys and metadata without any values. Cheapest way to scan a namespace."}
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
	tier := s.tier()
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
	tier := s.tier()
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
		Prefix   string `json:"prefix"`
		KeysOnly bool   `json:"keys_only"`
	}
	if req.Params.Arguments != nil {
		_ = json.Unmarshal(req.Params.Arguments, &input)
	}

	entries, err := s.deps.KVStore.List(ctx, s.deps.AgentName, input.Prefix)
	if err != nil {
		return toolError(fmt.Sprintf("kv_list failed: %v", err)), nil
	}

	out, omitted, headCapped := s.buildKVListRows(entries, input.KeysOnly)

	resp := map[string]any{"entries": out}
	switch {
	case omitted > 0:
		resp["truncated"] = true
		resp["omitted_values"] = omitted
		resp["note"] = fmt.Sprintf("value budget of %d bytes reached: %d value(s) omitted. Use kv_get on those keys for their contents.", s.kvListMaxBytes(), omitted)
	case headCapped > 0:
		resp["truncated"] = true
		resp["note"] = fmt.Sprintf("%d value(s) cut to the first %d bytes. Use kv_get on those keys for their full contents.", headCapped, s.kvListValueHeadBytes())
	}

	body, _ := json.Marshal(resp)
	return toolText(string(body)), nil
}

// kvListRow is one entry of a kv_list response. Value is a pointer so a stored
// empty string stays distinguishable from a value that was left out.
type kvListRow struct {
	Key            string  `json:"key"`
	Value          *string `json:"value,omitempty"`
	ValueTruncated bool    `json:"value_truncated,omitempty"`
	ValueOmitted   bool    `json:"value_omitted,omitempty"`
	ExpiresAt      *string `json:"expires_at,omitempty"`
	UpdatedAt      string  `json:"updated_at"`
}

// buildKVListRows renders entries under the response size caps. Keys and
// metadata are always complete; values are what gets dropped, from the tail
// onwards, so listing a namespace stays reliable however large it grows.
func (s *Server) buildKVListRows(entries []kv.Entry, keysOnly bool) (rows []kvListRow, omitted, headCapped int) {
	maxBytes := s.kvListMaxBytes()
	headBytes := s.kvListValueHeadBytes()

	rows = make([]kvListRow, len(entries))
	used := kvListEnvelopeBytes
	budgetSpent := false

	for i, e := range entries {
		row := kvListRow{Key: e.Key, UpdatedAt: e.UpdatedAt.Format(time.RFC3339)}
		if e.ExpiresAt != nil {
			exp := e.ExpiresAt.Format(time.RFC3339)
			row.ExpiresAt = &exp
		}

		if keysOnly {
			used += kvRowBytes(row)
			rows[i] = row
			continue
		}

		val := e.Value
		cut := false
		if len(val) > headBytes {
			val = truncateUTF8(val, headBytes) + kvTruncationMarker
			cut = true
		}
		row.Value = &val
		row.ValueTruncated = cut

		cost := kvRowBytes(row)
		if budgetSpent || used+cost > maxBytes {
			budgetSpent = true
			row.Value = nil
			row.ValueTruncated = false
			row.ValueOmitted = true
			omitted++
			used += kvRowBytes(row)
			rows[i] = row
			continue
		}

		if cut {
			headCapped++
		}
		used += cost
		rows[i] = row
	}
	return rows, omitted, headCapped
}

// kvRowBytes is the serialised cost of one row, including its comma.
// Marshalling cannot fail for this struct; the guard just keeps an
// unmeasurable row from reading as free budget.
func kvRowBytes(row kvListRow) int {
	b, err := json.Marshal(row)
	if err != nil {
		n := len(row.Key) + 64
		if row.Value != nil {
			n += len(*row.Value)
		}
		return n
	}
	return len(b) + 1
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (s *Server) kvListMaxBytes() int {
	if s.deps.KVListMaxBytes <= 0 {
		return defaultKVListMaxBytes
	}
	return s.deps.KVListMaxBytes
}

func (s *Server) kvListValueHeadBytes() int {
	if s.deps.KVListValueHeadBytes <= 0 {
		return defaultKVListValueHeadBytes
	}
	return s.deps.KVListValueHeadBytes
}

func (s *Server) handleKVSetNX(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tier := s.tier()
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
