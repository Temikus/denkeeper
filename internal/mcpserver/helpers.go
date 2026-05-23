package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ctxKey int

const (
	ctxKeyScopes ctxKey = iota
	ctxKeyName
)

func withScopes(ctx context.Context, scopes []string) context.Context {
	return context.WithValue(ctx, ctxKeyScopes, scopes)
}

func withKeyName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyName, name)
}

func scopesFromCtx(ctx context.Context) []string {
	v, _ := ctx.Value(ctxKeyScopes).([]string)
	return v
}

func keyNameFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyName).(string)
	return v
}

func hasScope(scopes []string, scope string) bool {
	for _, s := range scopes {
		if s == scope || s == "admin" {
			return true
		}
	}
	return false
}

// requireScope checks that the request context carries the given scope.
// Returns nil if the scope is present, or an MCP error result if missing.
func requireScope(ctx context.Context, scope string) *mcp.CallToolResult {
	if hasScope(scopesFromCtx(ctx), scope) {
		return nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("insufficient scope: requires %q", scope),
		}},
		IsError: true,
	}
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func toolJSON(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return toolText(string(b)), nil
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}
