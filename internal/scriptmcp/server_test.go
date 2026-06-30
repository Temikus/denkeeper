package scriptmcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func defaultDeps() Deps {
	return Deps{
		Enabled:        true,
		Timeout:        2 * time.Second,
		MaxOutputChars: 16000,
		MaxInputBytes:  262144,
		PermissionTier: func() string { return "autonomous" },
	}
}

func newTestServer(t *testing.T, deps Deps) *mcp.ClientSession {
	t.Helper()
	srv := New(deps)
	session, err := srv.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("call tool %q: %v", name, err)
	}
	return result
}

func extractText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func TestRunJavaScript_FormatsDeterministically(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	code := `var lines = input.items.map(function(x, i){ return (i+1) + ". " + x; });
		return lines.join("\n");`
	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  code,
		"input": map[string]any{"items": []string{"alpha", "beta", "gamma"}},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	want := "1. alpha\n2. beta\n3. gamma"
	if got := extractText(result); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRunJavaScript_ReturnsJSONForObjects(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return {count: input.length, first: input[0]};`,
		"input": []int{10, 20, 30},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	got := extractText(result)
	if !strings.Contains(got, `"count":3`) || !strings.Contains(got, `"first":10`) {
		t.Errorf("output = %q, want JSON object with count and first", got)
	}
}

func TestRunJavaScript_StringPassthrough(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `return "hello world";`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	// A string must not be double-encoded (no surrounding quotes).
	if got := extractText(result); got != "hello world" {
		t.Errorf("output = %q, want %q", got, "hello world")
	}
}

func TestRunJavaScript_TimeoutInterrupts(t *testing.T) {
	deps := defaultDeps()
	deps.Timeout = 100 * time.Millisecond
	session := newTestServer(t, deps)

	start := time.Now()
	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `while(true){}`,
	})
	elapsed := time.Since(start)
	if !result.IsError {
		t.Fatalf("expected error for infinite loop, got: %s", extractText(result))
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestRunJavaScript_SyntaxErrorSurfaced(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `return (((;`,
	})
	if !result.IsError {
		t.Fatal("expected error for invalid JavaScript")
	}
	if !strings.Contains(extractText(result), "javascript error") {
		t.Errorf("error should mention javascript error: %s", extractText(result))
	}
}

func TestRunJavaScript_NoNetworkOrFS(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	for _, global := range []string{`fetch("http://x")`, `require("fs")`, `process.exit(0)`} {
		result := callTool(t, session, "run_javascript", map[string]any{
			"code": "return " + global + ";",
		})
		if !result.IsError {
			t.Errorf("expected error for host global %q, got: %s", global, extractText(result))
		}
	}
}

func TestRunJavaScript_RestrictedTierBlocked(t *testing.T) {
	deps := defaultDeps()
	deps.PermissionTier = func() string { return "restricted" }
	session := newTestServer(t, deps)

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `return 1;`,
	})
	if !result.IsError {
		t.Fatal("expected error for restricted tier")
	}
	if !strings.Contains(extractText(result), "restricted") {
		t.Errorf("error should mention restricted: %s", extractText(result))
	}
}

func TestRunJavaScript_InputTooLarge(t *testing.T) {
	deps := defaultDeps()
	deps.MaxInputBytes = 16
	session := newTestServer(t, deps)

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return input;`,
		"input": strings.Repeat("x", 100),
	})
	if !result.IsError {
		t.Fatal("expected error for oversized input")
	}
	if !strings.Contains(extractText(result), "exceeds") {
		t.Errorf("error should mention size: %s", extractText(result))
	}
}

func TestRunJavaScript_EmptyCode(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{"code": ""})
	if !result.IsError {
		t.Fatal("expected error for empty code")
	}
}

func TestRunJavaScript_NullInputWhenOmitted(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `return input === null ? "is-null" : "not-null";`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "is-null" {
		t.Errorf("output = %q, want %q", got, "is-null")
	}
}

func TestRunJavaScript_NoReturnYieldsEmpty(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	// A snippet with no return value evaluates to undefined → empty string.
	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `var x = 1 + 1;`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "" {
		t.Errorf("output = %q, want empty string", got)
	}
}

func TestRunJavaScript_NullReturnYieldsEmpty(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `return null;`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "" {
		t.Errorf("output = %q, want empty string", got)
	}
}

func TestRunJavaScript_OutputTruncated(t *testing.T) {
	deps := defaultDeps()
	deps.MaxOutputChars = 10
	session := newTestServer(t, deps)

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `var s = ""; for (var i = 0; i < 100; i++) s += "y"; return s;`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); len(got) != 10 {
		t.Errorf("output length = %d, want 10", len(got))
	}
}

func TestRunJavaScript_DisabledNotRegistered(t *testing.T) {
	deps := defaultDeps()
	deps.Enabled = false
	session := newTestServer(t, deps)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 0 {
		t.Fatalf("expected 0 tools when disabled, got %d", len(tools.Tools))
	}
}

func TestRunJavaScript_EnabledRegistersTool(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools.Tools))
	}
	if tools.Tools[0].Name != "run_javascript" {
		t.Errorf("tool name = %q, want %q", tools.Tools[0].Name, "run_javascript")
	}
}
