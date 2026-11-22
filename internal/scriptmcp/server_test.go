package scriptmcp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	if !strings.Contains(extractText(result), "top-level") {
		t.Errorf("error should say code is a top-level argument: %s", extractText(result))
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
	want := strings.Repeat("y", 10) + truncationMarker(10)
	if got := extractText(result); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRunJavaScript_OutputAtCapUnchanged(t *testing.T) {
	deps := defaultDeps()
	deps.MaxOutputChars = 10
	session := newTestServer(t, deps)

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `return "yyyyyyyyyy";`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "yyyyyyyyyy" {
		t.Errorf("output = %q, want 10 bytes with no marker", got)
	}
}

func TestRunJavaScript_OutputTruncatedOnRuneBoundary(t *testing.T) {
	deps := defaultDeps()
	deps.MaxOutputChars = 10 // falls inside the fourth 3-byte "€"
	session := newTestServer(t, deps)

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return input;`,
		"input": strings.Repeat("€", 100),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	got := extractText(result)
	if !utf8.ValidString(got) {
		t.Errorf("output is not valid UTF-8: %.200q", got)
	}
	if want := strings.Repeat("€", 3) + truncationMarker(10); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRunJavaScript_ThrowInputCapped(t *testing.T) {
	deps := defaultDeps()
	deps.MaxOutputChars = 100
	session := newTestServer(t, deps)

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `throw input;`,
		"input": strings.Repeat("x", 5000),
	})
	if !result.IsError {
		t.Fatalf("expected IsError, got %q", extractText(result))
	}
	got := extractText(result)
	if !strings.Contains(got, truncationMarker(100)) {
		t.Errorf("error text has no truncation marker: %.200q", got)
	}
	if limit := len("javascript error: ") + 100 + len(truncationMarker(100)); len(got) > limit {
		t.Errorf("error text length = %d, want at most %d", len(got), limit)
	}
}

func TestRunJavaScript_ThrowMultiByteCappedValidUTF8(t *testing.T) {
	deps := defaultDeps()
	deps.MaxOutputChars = 100 // falls inside a 3-byte "€"
	session := newTestServer(t, deps)

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `throw input;`,
		"input": strings.Repeat("€", 1000),
	})
	if !result.IsError {
		t.Fatalf("expected IsError, got %q", extractText(result))
	}
	got := extractText(result)
	if !utf8.ValidString(got) {
		t.Errorf("error text is not valid UTF-8: %.200q", got)
	}
	if !strings.Contains(got, truncationMarker(100)) {
		t.Errorf("error text has no truncation marker: %.200q", got)
	}
}

func TestRunJavaScript_ThrowCappedKeepsDecodeHint(t *testing.T) {
	deps := defaultDeps()
	deps.MaxOutputChars = 100
	session := newTestServer(t, deps)

	inner, err := json.Marshal(map[string]string{"a": strings.Repeat("x", 5000)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `throw input.a;`,
		"input": string(inner),
	})
	if !result.IsError {
		t.Fatalf("expected IsError, got %q", extractText(result))
	}
	got := extractText(result)
	marker := strings.Index(got, truncationMarker(100))
	hint := strings.Index(got, "remove JSON.parse(input)")
	if marker < 0 || hint < 0 || hint < marker {
		t.Errorf("want marker then decode hint, got marker at %d, hint at %d: %.200q", marker, hint, got)
	}
}

func TestRunJavaScript_ShortErrorUnchanged(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code": `throw new Error("boom");`,
	})
	if !result.IsError {
		t.Fatalf("expected IsError, got %q", extractText(result))
	}
	got := extractText(result)
	if !strings.HasPrefix(got, "javascript error: ") || !strings.Contains(got, "boom") {
		t.Errorf("error text = %q, want the javascript error with boom", got)
	}
	if strings.Contains(got, "[truncated") {
		t.Errorf("short error has a truncation marker: %q", got)
	}
}

func TestRunJavaScript_ZeroCapReturnsWholeError(t *testing.T) {
	deps := defaultDeps()
	deps.MaxOutputChars = 0
	session := newTestServer(t, deps)

	long := strings.Repeat("x", 20000)
	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `throw input;`,
		"input": long,
	})
	if !result.IsError {
		t.Fatalf("expected IsError, got %q", extractText(result))
	}
	got := extractText(result)
	if !strings.Contains(got, long) || strings.Contains(got, "[truncated") {
		t.Errorf("want the whole thrown string and no marker, got %d bytes", len(got))
	}
}

// runFormatProbe runs code that throws or returns a value whose formatting
// runs script JS after RunString. Before #616 T2 each of these crashed the
// whole process; now each must come back as an IsError result.
func runFormatProbe(t *testing.T, code string) string {
	t.Helper()
	deps := defaultDeps()
	deps.Timeout = 200 * time.Millisecond // the looping probes stop here
	session := newTestServer(t, deps)

	start := time.Now()
	result := callTool(t, session, "run_javascript", map[string]any{"code": code})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("call took %v, want it bounded by the timeout", elapsed)
	}
	if !result.IsError {
		t.Fatalf("expected IsError, got %.200q", extractText(result))
	}
	return extractText(result)
}

func TestRunJavaScript_ThrownLoopingToStringReturnsError(t *testing.T) {
	runFormatProbe(t, `throw {toString: function(){ while(true){} }}`)
}

func TestRunJavaScript_ThrownThrowingToStringReturnsError(t *testing.T) {
	runFormatProbe(t, `throw {toString: function(){ throw new Error("x") }}`)
}

func TestRunJavaScript_ThrowingGetterResultReturnsError(t *testing.T) {
	runFormatProbe(t, `return {get a(){ throw new Error("boom") }}`)
}

func TestRunJavaScript_LoopingGetterResultReturnsError(t *testing.T) {
	runFormatProbe(t, `return {get a(){ while(true){} }}`)
}

func TestRunJavaScript_CyclicResultReturnsError(t *testing.T) {
	got := runFormatProbe(t, `var a = {}; a.self = a; return a;`)
	if !strings.Contains(got, "could not be encoded as JSON") {
		t.Errorf("error text = %.200q, want the JSON encoding error", got)
	}
}

func TestRunJavaScript_DeepNestedResultReturnsError(t *testing.T) {
	runFormatProbe(t, `var a = []; for (var i = 0; i < 1000000; i++) { a = [a]; } return a;`)
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

func TestNewSemaphore(t *testing.T) {
	if NewSemaphore(0) != nil {
		t.Error("NewSemaphore(0) should be nil (unbounded)")
	}
	if NewSemaphore(-1) != nil {
		t.Error("NewSemaphore(-1) should be nil (unbounded)")
	}
	sem := NewSemaphore(3)
	if cap(sem) != 3 {
		t.Errorf("cap = %d, want 3", cap(sem))
	}
}

func TestRunJavaScript_SemaphoreBlocksWhenFull(t *testing.T) {
	deps := defaultDeps()
	deps.Sem = NewSemaphore(1)
	deps.Sem <- struct{}{} // occupy the only slot so the call must wait

	// Call the handler directly: passing a cancelled context through the MCP
	// session would be rejected by the transport before reaching the handler.
	srv := New(deps)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the call can never acquire a slot

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "run_javascript",
		Arguments: json.RawMessage(`{"code":"return 1;"}`),
	}}
	result, err := srv.handleRunJavaScript(ctx, req)
	if err != nil {
		t.Fatalf("handler returned err: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when waiting on a full semaphore with cancelled context")
	}
	if !strings.Contains(extractText(result), "concurrency slot") {
		t.Errorf("error should mention the concurrency slot: %s", extractText(result))
	}
}

func TestRunJavaScript_AgentSemaphoreBlocksWhenFull(t *testing.T) {
	deps := defaultDeps()
	deps.Sem = NewSemaphore(8)      // global pool has room
	deps.AgentSem = NewSemaphore(1) // but this agent's slot is taken
	deps.AgentSem <- struct{}{}

	srv := New(deps)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "run_javascript",
		Arguments: json.RawMessage(`{"code":"return 1;"}`),
	}}
	result, err := srv.handleRunJavaScript(ctx, req)
	if err != nil {
		t.Fatalf("handler returned err: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when the per-agent semaphore is full")
	}
	// The global slot must not have been consumed when the per-agent cap blocks.
	if len(deps.Sem) != 0 {
		t.Errorf("global semaphore should be untouched, len = %d", len(deps.Sem))
	}
}

func TestRunJavaScript_BothSemaphoresReleased(t *testing.T) {
	deps := defaultDeps()
	deps.Sem = NewSemaphore(2)
	deps.AgentSem = NewSemaphore(1)
	session := newTestServer(t, deps)

	for i := 0; i < 3; i++ {
		result := callTool(t, session, "run_javascript", map[string]any{"code": `return 1;`})
		if result.IsError {
			t.Fatalf("call %d errored: %s", i, extractText(result))
		}
	}
	if len(deps.Sem) != 0 || len(deps.AgentSem) != 0 {
		t.Errorf("semaphores should be empty: global=%d agent=%d", len(deps.Sem), len(deps.AgentSem))
	}
}

func TestRunJavaScript_SemaphoreSlotReleased(t *testing.T) {
	deps := defaultDeps()
	deps.Sem = NewSemaphore(1)
	session := newTestServer(t, deps)

	// Two sequential calls must both succeed: the first must release its slot.
	for i := 0; i < 2; i++ {
		result := callTool(t, session, "run_javascript", map[string]any{"code": `return 1;`})
		if result.IsError {
			t.Fatalf("call %d errored: %s", i, extractText(result))
		}
	}
	if len(deps.Sem) != 0 {
		t.Errorf("semaphore should be empty after calls, len = %d", len(deps.Sem))
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

func TestRunJavaScript_HoistsCodeFromObjectInput(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"input": json.RawMessage(`{"b":1,"code":"return Object.keys(input).join(',');","a":2}`),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	// The code key is gone and the remaining keys keep their order.
	if got := extractText(result); got != "b,a" {
		t.Errorf("output = %q, want %q", got, "b,a")
	}
}

func TestRunJavaScript_HoistsCodeFromStringInput(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"input": `{"code":"return Object.keys(input).join(',') + '=' + input.n * 2;","n":21}`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "n=42" {
		t.Errorf("output = %q, want %q", got, "n=42")
	}
}

func TestRunJavaScript_TopLevelCodeWinsOverInputCode(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return "top:" + input.code;`,
		"input": map[string]any{"code": "data"},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "top:data" {
		t.Errorf("output = %q, want %q", got, "top:data")
	}
}

func TestRunJavaScript_MissingCodeNamesTopLevel(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"input": map[string]any{"n": 1},
	})
	if !result.IsError {
		t.Fatalf("expected error, got: %s", extractText(result))
	}
	text := extractText(result)
	if !strings.Contains(text, "top-level") || !strings.Contains(text, "next to `input`") {
		t.Errorf("error should say code is top-level, next to input: %s", text)
	}
}

func TestRunJavaScript_NonStringInputCodeNotHoisted(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"input": map[string]any{"code": 5},
	})
	if !result.IsError {
		t.Fatalf("expected error, got: %s", extractText(result))
	}
	text := extractText(result)
	if !strings.Contains(text, "top-level") || !strings.Contains(text, "`input.code`") {
		t.Errorf("error should name top-level code and the bad input.code: %s", text)
	}
}

func TestRunJavaScript_StringInputObjectDecoded(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return input.name;`,
		"input": " {\"name\":\"alpha\"}\n",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "alpha" {
		t.Errorf("output = %q, want %q", got, "alpha")
	}
}

func TestRunJavaScript_StringInputArrayDecoded(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return input.length;`,
		"input": `[1,2,3]`,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "3" {
		t.Errorf("output = %q, want %q", got, "3")
	}
}

func TestRunJavaScript_StringInputBrokenJSONRefused(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return "SCRIPT-RAN";`,
		"input": `{"name":"alpha"`,
	})
	if !result.IsError {
		t.Fatalf("expected error, got: %s", extractText(result))
	}
	text := extractText(result)
	if strings.Contains(text, "SCRIPT-RAN") {
		t.Fatalf("script must not run on broken input: %s", text)
	}
	if !strings.Contains(text, "JSON value, not a string") || !strings.Contains(text, "byte 15") {
		t.Errorf("error should ask for a JSON value and give the position: %s", text)
	}
}

func TestRunJavaScript_StringInputPlainTextStaysString(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return typeof input + ":" + input;`,
		"input": "hello {world}",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "string:hello {world}" {
		t.Errorf("output = %q, want %q", got, "string:hello {world}")
	}
}

func TestRunJavaScript_StringInputNumberStaysString(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `return typeof input + ":" + input;`,
		"input": "123",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	if got := extractText(result); got != "string:123" {
		t.Errorf("output = %q, want %q", got, "string:123")
	}
}

func TestRunJavaScript_JSONParseOnDecodedInputHints(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `var d = JSON.parse(input); return d.a;`,
		"input": `{"a":1}`,
	})
	if !result.IsError {
		t.Fatalf("expected error, got: %s", extractText(result))
	}
	text := extractText(result)
	if !strings.Contains(text, "javascript error") || !strings.Contains(text, "remove JSON.parse(input)") {
		t.Errorf("error should carry the decode hint: %s", text)
	}
}

func TestRunJavaScript_UndecodedInputErrorHasNoHint(t *testing.T) {
	session := newTestServer(t, defaultDeps())

	result := callTool(t, session, "run_javascript", map[string]any{
		"code":  `throw new Error("boom");`,
		"input": map[string]any{"a": 1},
	})
	if !result.IsError {
		t.Fatalf("expected error, got: %s", extractText(result))
	}
	if strings.Contains(extractText(result), "JSON.parse(input)") {
		t.Errorf("hint must only follow a decoded string input: %s", extractText(result))
	}
}

func TestRunJavaScript_StringInputSizeCheckedRaw(t *testing.T) {
	deps := defaultDeps()
	deps.MaxInputBytes = 30
	session := newTestServer(t, deps)

	// No top-level code: the size check must still run first, on the raw bytes.
	result := callTool(t, session, "run_javascript", map[string]any{
		"input": `{"code":"return 1;","pad":"xxxxxxxx"}`,
	})
	if !result.IsError {
		t.Fatalf("expected error, got: %s", extractText(result))
	}
	if !strings.Contains(extractText(result), "exceeds") {
		t.Errorf("error should mention size: %s", extractText(result))
	}
}

// callHandlerRaw sends arguments byte-for-byte. The MCP client re-encodes
// arguments with HTML escaping, which would hide key-growth bugs.
func callHandlerRaw(t *testing.T, deps Deps, args string) *mcp.CallToolResult {
	t.Helper()
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "run_javascript",
		Arguments: json.RawMessage(args),
	}}
	result, err := New(deps).handleRunJavaScript(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned err: %v", err)
	}
	return result
}

func TestRunJavaScript_HoistKeepsRawKeyBytes(t *testing.T) {
	deps := defaultDeps()
	deps.MaxInputBytes = 2000
	key := strings.Repeat("<", 1500)

	result := callHandlerRaw(t, deps,
		`{"code":"","input":{"code":"return __denkeeper_input_json.length","`+key+`":1}}`)
	if result.IsError {
		t.Fatalf("near-cap input with HTML characters in a key should run: %s", extractText(result))
	}
	n, err := strconv.Atoi(extractText(result))
	if err != nil {
		t.Fatalf("output %q is not a length: %v", extractText(result), err)
	}
	if n > deps.MaxInputBytes {
		t.Errorf("VM saw %d bytes of input JSON, cap is %d", n, deps.MaxInputBytes)
	}
}

func TestRunJavaScript_DecodedInputSizeRechecked(t *testing.T) {
	deps := defaultDeps()
	deps.MaxInputBytes = 2000
	// Each invalid UTF-8 byte decodes to U+FFFD: 1 raw byte becomes 3.
	invalid := strings.Repeat("\xff", 1000)

	result := callHandlerRaw(t, deps,
		`{"code":"return __denkeeper_input_json.length","input":"[\"`+invalid+`\"]"}`)
	if !result.IsError {
		t.Fatalf("decoded input over the cap must be refused, got: %s", extractText(result))
	}
	if !strings.Contains(extractText(result), "exceeds") {
		t.Errorf("error should mention size: %s", extractText(result))
	}
}
