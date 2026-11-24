package tool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// fixedResultServer builds an MCP server whose tool returns res verbatim. The
// raw AddTool is used on purpose: the typed one fills a text block from the
// output value, which would hide the structured-only case.
func fixedResultServer(label, toolName string, res *mcp.CallToolResult) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: label, Version: "v1"}, nil)
	srv.AddTool(&mcp.Tool{Name: toolName, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return res, nil })
	return srv
}

// registerFixedSSE serves srv over HTTP and registers it on m as an external
// (sse) server.
func registerFixedSSE(t *testing.T, m *Manager, name string, srv *mcp.Server) {
	t.Helper()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	// This cleanup runs before the manager's Close (cleanups are LIFO), while
	// the client still holds its SSE stream open; Close alone would block.
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})
	if err := m.RegisterServer(context.Background(), name, config.ToolConfig{
		Transport: "sse", URL: ts.URL, AllowLoopback: true,
	}); err != nil {
		t.Fatalf("RegisterServer %q: %v", name, err)
	}
}

// registerFixedSession connects srv in process and registers it the way
// main.go registers config-MCP and friends.
func registerFixedSession(t *testing.T, m *Manager, name string, srv *mcp.Server) {
	t.Helper()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), serverT, nil); err != nil {
		t.Fatalf("connecting server %q: %v", name, err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "denkeeper-test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("connecting client %q: %v", name, err)
	}
	if err := m.RegisterSession(context.Background(), name, session); err != nil {
		t.Fatalf("RegisterSession %q: %v", name, err)
	}
}

func newStructuredManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func execResult(t *testing.T, m *Manager, name string) (Result, error) {
	t.Helper()
	return m.ExecuteResult(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: name, Arguments: `{}`},
	})
}

// assertStructured compares got with want as decoded JSON values, so key
// order and spacing do not matter.
func assertStructured(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("Structured is nil, want %s", want)
	}
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("Structured is not JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("Structured = %s, want %s", got, want)
	}
}

func textBlocks(texts ...string) []mcp.Content {
	out := make([]mcp.Content, 0, len(texts))
	for _, s := range texts {
		out = append(out, &mcp.TextContent{Text: s})
	}
	return out
}

func TestExecuteResult_TextOnlyHasNoStructured(t *testing.T) {
	m := newStructuredManager(t)
	registerFixedSSE(t, m, "plain", fixedResultServer("plain", "say", &mcp.CallToolResult{
		Content: textBlocks("hello"),
	}))

	res, err := execResult(t, m, "say")
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	if res.Text != "hello" {
		t.Errorf("Text = %q, want %q", res.Text, "hello")
	}
	if res.Structured != nil {
		t.Errorf("Structured = %s, want nil for a text-only result", res.Structured)
	}
}

func TestExecuteResult_StructuredAndTextKeepsTheTextAsToday(t *testing.T) {
	m := newStructuredManager(t)
	registerFixedSSE(t, m, "both", fixedResultServer("both", "lookup", &mcp.CallToolResult{
		Content:           textBlocks("first", "second"),
		StructuredContent: map[string]any{"id": "abc", "count": 2},
	}))

	res, err := execResult(t, m, "lookup")
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	if res.Text != "first\nsecond" {
		t.Errorf("Text = %q, want the text blocks joined by a newline", res.Text)
	}
	assertStructured(t, res.Structured, `{"id":"abc","count":2}`)
}

func TestExecuteResult_StructuredOnlyLeavesTextEmpty(t *testing.T) {
	m := newStructuredManager(t)
	registerFixedSSE(t, m, "bare", fixedResultServer("bare", "count", &mcp.CallToolResult{
		Content:           []mcp.Content{},
		StructuredContent: map[string]any{"n": 7},
	}))

	res, err := execResult(t, m, "count")
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	if res.Text != "" {
		t.Errorf("Text = %q, want empty: nothing is synthesised from the structured value", res.Text)
	}
	assertStructured(t, res.Structured, `{"n":7}`)
}

func TestExecute_ReturnsExecuteResultText(t *testing.T) {
	m := newStructuredManager(t)
	registerFixedSSE(t, m, "plain", fixedResultServer("plain", "say", &mcp.CallToolResult{
		Content: textBlocks("hello"),
	}))
	registerFixedSSE(t, m, "both", fixedResultServer("both", "lookup", &mcp.CallToolResult{
		Content: textBlocks("first", "second"), StructuredContent: map[string]any{"id": "abc"},
	}))
	registerFixedSSE(t, m, "bare", fixedResultServer("bare", "count", &mcp.CallToolResult{
		Content: []mcp.Content{}, StructuredContent: map[string]any{"n": 7},
	}))

	for _, name := range []string{"say", "lookup", "count"} {
		call := llm.ToolCall{Function: llm.FunctionCall{Name: name, Arguments: `{}`}}
		text, err := m.Execute(context.Background(), call)
		if err != nil {
			t.Fatalf("Execute %s: %v", name, err)
		}
		res, err := m.ExecuteResult(context.Background(), call)
		if err != nil {
			t.Fatalf("ExecuteResult %s: %v", name, err)
		}
		if text != res.Text {
			t.Errorf("%s: Execute = %q, ExecuteResult().Text = %q", name, text, res.Text)
		}
	}
}

func TestExecuteResult_IsErrorKeepsStructured(t *testing.T) {
	m := newStructuredManager(t)
	registerFixedSSE(t, m, "reject", fixedResultServer("reject", "update", &mcp.CallToolResult{
		IsError:           true,
		Content:           textBlocks("missing field 'cron'"),
		StructuredContent: map[string]any{"field": "cron"},
	}))

	res, err := execResult(t, m, "update")
	var re *RejectionError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v (%T), want *RejectionError", err, err)
	}
	if res.Text != "missing field 'cron'" {
		t.Errorf("Text = %q, want the error text", res.Text)
	}
	assertStructured(t, res.Structured, `{"field":"cron"}`)
}

func TestExecuteResult_ParentToolReturnsParentStructured(t *testing.T) {
	parent := newStructuredManager(t)
	registerFixedSSE(t, parent, "shared", fixedResultServer("shared", "lookup", &mcp.CallToolResult{
		Content: textBlocks("from parent"), StructuredContent: map[string]any{"owner": "parent"},
	}))
	child := newStructuredManager(t)
	child.AdoptFrom(parent)

	res, err := execResult(t, child, "lookup")
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	if res.Text != "from parent" {
		t.Errorf("Text = %q, want the parent's text", res.Text)
	}
	assertStructured(t, res.Structured, `{"owner":"parent"}`)
}

func TestExecuteResult_SessionServerCarriesStructured(t *testing.T) {
	m := newStructuredManager(t)
	registerFixedSession(t, m, "config-test", fixedResultServer("config-test", "kv_peek", &mcp.CallToolResult{
		Content: textBlocks("ok"), StructuredContent: map[string]any{"keys": []any{"a", "b"}},
	}))

	res, err := execResult(t, m, "kv_peek")
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	assertStructured(t, res.Structured, `{"keys":["a","b"]}`)
}

func TestExecuteResult_QualifiedCollidingNameCarriesStructured(t *testing.T) {
	m := newStructuredManager(t)
	registerFixedSSE(t, m, "alpha", fixedResultServer("alpha", "search", &mcp.CallToolResult{
		Content: textBlocks("alpha"), StructuredContent: map[string]any{"from": "alpha"},
	}))
	registerFixedSSE(t, m, "beta", fixedResultServer("beta", "search", &mcp.CallToolResult{
		Content: textBlocks("beta"), StructuredContent: map[string]any{"from": "beta"},
	}))

	res, err := execResult(t, m, "beta__search")
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	if res.Text != "beta" {
		t.Errorf("Text = %q, want beta's", res.Text)
	}
	assertStructured(t, res.Structured, `{"from":"beta"}`)
}

func TestExecuteResult_LargeIntegerLosesPrecision(t *testing.T) {
	// The SDK decodes structuredContent into Go values, so a JSON integer
	// becomes a float64 before the manager re-encodes it. 2^53 + 1 does not
	// survive that. This pins the limit: callers must not treat a 64-bit id
	// from Structured as exact.
	m := newStructuredManager(t)
	registerFixedSSE(t, m, "ids", fixedResultServer("ids", "task", &mcp.CallToolResult{
		Content:           textBlocks("task"),
		StructuredContent: json.RawMessage(`{"id":9007199254740993}`),
	}))

	res, err := execResult(t, m, "task")
	if err != nil {
		t.Fatalf("ExecuteResult: %v", err)
	}
	if got, want := string(res.Structured), `{"id":9007199254740992}`; got != want {
		t.Errorf("Structured = %s, want %s (2^53+1 rounded to 2^53 by float64)", got, want)
	}
}
