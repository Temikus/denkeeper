package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

type collisionToolArgs struct {
	Query string `json:"query"`
}

// startCollisionServer serves an MCP server whose tools each return
// "<label>:<tool>", so a result identifies which server actually ran the call.
func startCollisionServer(t *testing.T, label string, tools ...string) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: label, Version: "v1"}, nil)
	for _, name := range tools {
		result := label + ":" + name
		mcp.AddTool(server, &mcp.Tool{
			Name:        name,
			Description: "tool " + name,
		}, func(_ context.Context, _ *mcp.CallToolRequest, _ collisionToolArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: result}},
			}, nil, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// registerCollisionServer connects m to ts under the given server name.
func registerCollisionServer(t *testing.T, m *Manager, name string, ts *httptest.Server, disabled ...string) {
	t.Helper()
	if err := m.RegisterServer(context.Background(), name, config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
		DisabledTools: disabled,
	}); err != nil {
		t.Fatalf("RegisterServer(%s): %v", name, err)
	}
}

// syncBuffer is a concurrency-safe io.Writer for slog capture.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// callTool executes a tool by its advertised name.
func callTool(t *testing.T, m *Manager, name string) (string, error) {
	t.Helper()
	return m.Execute(context.Background(), llm.ToolCall{
		ID:       "call-1",
		Function: llm.FunctionCall{Name: name, Arguments: `{"query":"x"}`},
	})
}

func hasTool(defs []llm.ToolDef, name string) bool {
	for _, td := range defs {
		if td.Function.Name == name {
			return true
		}
	}
	return false
}

// newCollisionManager returns a manager plus the buffer its logs land in.
//
// Call it *after* startCollisionServer: cleanups run last-registered-first, and
// the manager's sessions must close before the httptest servers do —
// httptest.Server.Close blocks on the standalone SSE stream an MCP
// streamable-HTTP session holds open.
func newCollisionManager(t *testing.T) (*Manager, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	m := NewManager(slog.New(slog.NewTextHandler(logs, nil)), config.MCPConfig{RequestTimeoutSecs: 10})
	t.Cleanup(func() { _ = m.Close() })
	return m, logs
}

func TestDiscoverTools_CollisionQualifiesBothOwners(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch", "only_a")
	tsB := startCollisionServer(t, "b", "fetch")
	m, logs := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)
	registerCollisionServer(t, m, "b", tsB)

	defs := m.ToolDefs()
	if !hasTool(defs, "a__fetch") || !hasTool(defs, "b__fetch") {
		t.Errorf("ToolDefs() = %v, want both a__fetch and b__fetch", toolNames(defs))
	}
	if hasTool(defs, "fetch") {
		t.Errorf("ToolDefs() = %v, want no bare fetch under collision", toolNames(defs))
	}
	// The non-colliding tool keeps its bare name.
	if !hasTool(defs, "only_a") {
		t.Errorf("ToolDefs() = %v, want unique tool only_a untouched", toolNames(defs))
	}

	out := logs.String()
	if !strings.Contains(out, "collision") {
		t.Errorf("logs do not mention the collision: %s", out)
	}
	for _, want := range []string{"a__fetch", "b__fetch"} {
		if !strings.Contains(out, want) {
			t.Errorf("collision log does not name %q: %s", want, out)
		}
	}
}

// TestDiscoverTools_QualifiedNameShadowedByLiteralToolName covers a server
// whose tool is literally named "<other-server>__<tool>": the first claim wins
// and the later definition is dropped rather than advertised against a
// definition that routes somewhere else.
func TestDiscoverTools_QualifiedNameShadowedByLiteralToolName(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch")
	tsB := startCollisionServer(t, "b", "fetch")
	tsC := startCollisionServer(t, "c", "a__fetch")
	m, logs := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)
	registerCollisionServer(t, m, "b", tsB)
	registerCollisionServer(t, m, "c", tsC)

	var count int
	for _, name := range toolNames(m.ToolDefs()) {
		if name == "a__fetch" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ToolDefs() advertises a__fetch %d times, want exactly 1: %v", count, toolNames(m.ToolDefs()))
	}

	got, err := callTool(t, m, "a__fetch")
	if err != nil {
		t.Fatalf("Execute(a__fetch): %v", err)
	}
	if got != "a:fetch" {
		t.Errorf("Execute(a__fetch) = %q, want a:fetch (the first claim)", got)
	}
	if !strings.Contains(logs.String(), "claimed twice") {
		t.Errorf("expected a warning about the double claim, got: %s", logs.String())
	}
}

func TestExecute_QualifiedNameRoutesToDeclaredServer(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)
	registerCollisionServer(t, m, "b", tsB)

	got, err := callTool(t, m, "a__fetch")
	if err != nil {
		t.Fatalf("Execute(a__fetch): %v", err)
	}
	if got != "a:fetch" {
		t.Errorf("Execute(a__fetch) = %q, want a:fetch", got)
	}

	got, err = callTool(t, m, "b__fetch")
	if err != nil {
		t.Fatalf("Execute(b__fetch): %v", err)
	}
	if got != "b:fetch" {
		t.Errorf("Execute(b__fetch) = %q, want b:fetch", got)
	}
}

func TestExecute_AmbiguousBareNameErrors(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)
	registerCollisionServer(t, m, "b", tsB)

	_, err := callTool(t, m, "fetch")
	if err == nil {
		t.Fatal("Execute(fetch) succeeded; a colliding bare name must never be routed")
	}
	if !errors.Is(err, ErrAmbiguousTool) {
		t.Errorf("error = %v, want wrapped ErrAmbiguousTool", err)
	}
	for _, want := range []string{`"a"`, `"b"`, "a__fetch", "b__fetch"} {
		if !strings.Contains(err.Error(), strings.Trim(want, `"`)) {
			t.Errorf("error %q does not mention %s", err.Error(), want)
		}
	}
}

// TestExecute_UniqueBareNameUnchanged locks the back-compat contract: with no
// collision, nothing is qualified and calls route exactly as before.
func TestExecute_UniqueBareNameUnchanged(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch", "only_a")
	m, _ := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)

	defs := m.ToolDefs()
	want := []string{"fetch", "only_a"}
	got := toolNames(defs)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ToolDefs() = %v, want %v (bare names, discovery order)", got, want)
	}
	if defs[0].Function.Description != "tool fetch" || defs[0].Type != "function" {
		t.Errorf("def = %+v, want the unmodified discovered definition", defs[0])
	}

	result, err := callTool(t, m, "fetch")
	if err != nil {
		t.Fatalf("Execute(fetch): %v", err)
	}
	if result != "a:fetch" {
		t.Errorf("Execute(fetch) = %q, want a:fetch", result)
	}
	if server := m.ToolServer("fetch"); server != "a" {
		t.Errorf("ToolServer(fetch) = %q, want a", server)
	}
}

func TestUnregisterServer_CollisionSurvivorReclaimsBareName(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch", "only_a")
	tsB := startCollisionServer(t, "b", "fetch")
	m, logs := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)

	// Snapshot the collision-free payload, then collide and un-collide: the
	// advertised payload must return to exactly what it was.
	before, err := json.Marshal(m.ToolDefs())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	registerCollisionServer(t, m, "b", tsB)
	if err := m.UnregisterServer("b"); err != nil {
		t.Fatalf("UnregisterServer(b): %v", err)
	}

	after, err := json.Marshal(m.ToolDefs())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("ToolDefs() after collision cleared = %s, want %s", after, before)
	}

	got, err := callTool(t, m, "fetch")
	if err != nil {
		t.Fatalf("Execute(fetch) after survivor reclaimed the name: %v", err)
	}
	if got != "a:fetch" {
		t.Errorf("Execute(fetch) = %q, want a:fetch", got)
	}
	if !strings.Contains(logs.String(), "collision cleared") {
		t.Errorf("expected an Info log about the cleared collision, got: %s", logs.String())
	}
}

// TestUnregisterServer_CollisionOwnerRemovedFirstLeavesNoOrphan covers the old
// corruption case: removing the *first*-registered collider used to strand the
// other server's definition (advertised but unroutable) or drop the owner's
// map entry outright.
func TestUnregisterServer_CollisionOwnerRemovedFirstLeavesNoOrphan(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch", "only_a")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)
	registerCollisionServer(t, m, "b", tsB)

	if err := m.UnregisterServer("a"); err != nil {
		t.Fatalf("UnregisterServer(a): %v", err)
	}

	defs := m.ToolDefs()
	if got := toolNames(defs); len(got) != 1 || got[0] != "fetch" {
		t.Fatalf("ToolDefs() = %v, want exactly [fetch] (no orphans from the removed server)", got)
	}

	got, err := callTool(t, m, "fetch")
	if err != nil {
		t.Fatalf("Execute(fetch): %v", err)
	}
	if got != "b:fetch" {
		t.Errorf("Execute(fetch) = %q, want b:fetch (the survivor)", got)
	}
	if _, err := callTool(t, m, "only_a"); err == nil {
		t.Error("Execute(only_a) succeeded after its server was unregistered")
	}
}

// TestExecute_QualifiedNameStillRoutesAfterCollisionClears keeps a name written
// while the collision existed (an auto-approve rule, an in-flight call) working
// once the other server goes away.
func TestExecute_QualifiedNameStillRoutesAfterCollisionClears(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)
	registerCollisionServer(t, m, "b", tsB)
	if err := m.UnregisterServer("b"); err != nil {
		t.Fatalf("UnregisterServer(b): %v", err)
	}

	got, err := callTool(t, m, "a__fetch")
	if err != nil {
		t.Fatalf("Execute(a__fetch) after the collision cleared: %v", err)
	}
	if got != "a:fetch" {
		t.Errorf("Execute(a__fetch) = %q, want a:fetch", got)
	}
	// Resolvable, but no longer advertised — the bare name is back.
	if hasTool(m.ToolDefs(), "a__fetch") {
		t.Error("a__fetch should not be advertised once the collision cleared")
	}
}

func TestEnabledToolDefs_DisabledCheckUsesOwningServer(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch", "only_a")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA, "fetch")
	registerCollisionServer(t, m, "b", tsB)

	defs := m.ToolDefs()
	if hasTool(defs, "a__fetch") {
		t.Errorf("ToolDefs() = %v, want a__fetch filtered (disabled on its own server)", toolNames(defs))
	}
	if !hasTool(defs, "b__fetch") {
		t.Errorf("ToolDefs() = %v, want b__fetch advertised (not disabled on b)", toolNames(defs))
	}
	if !hasTool(defs, "only_a") {
		t.Errorf("ToolDefs() = %v, want only_a advertised", toolNames(defs))
	}
	if _, err := callTool(t, m, "a__fetch"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("Execute(a__fetch) error = %v, want a disabled error", err)
	}
}

func TestIsIdempotent_QualifiedNameUsesOwningServerConfig(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	if err := m.RegisterServer(context.Background(), "a", config.ToolConfig{
		Transport:     "sse",
		URL:           tsA.URL,
		AllowLoopback: true,
		Idempotent:    boolPtr(true),
	}); err != nil {
		t.Fatalf("RegisterServer(a): %v", err)
	}
	registerCollisionServer(t, m, "b", tsB)

	if !m.IsIdempotent("a__fetch") {
		t.Error("IsIdempotent(a__fetch) = false, want true (a opts in server-wide)")
	}
	if m.IsIdempotent("b__fetch") {
		t.Error("IsIdempotent(b__fetch) = true, want false (b does not opt in)")
	}
	if m.IsIdempotent("fetch") {
		t.Error("IsIdempotent(fetch) = true, want false (ambiguous names are never memoized)")
	}
	if server := m.ToolServer("fetch"); server != "" {
		t.Errorf("ToolServer(fetch) = %q, want empty for an ambiguous name", server)
	}
}

func TestDiscoverTools_CollisionEmitsAuditEvent(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch", "only_a")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	auditor := &captureEmitter{}
	m.Auditor = auditor

	registerCollisionServer(t, m, "a", tsA)
	if events := auditor.byAction("tool_name_collision"); len(events) != 0 {
		t.Fatalf("collision-free registration emitted %d events, want 0", len(events))
	}

	registerCollisionServer(t, m, "b", tsB)

	events := auditor.byAction("tool_name_collision")
	if len(events) != 1 {
		t.Fatalf("got %d tool_name_collision events, want exactly 1", len(events))
	}
	ev := events[0]
	if ev.Category != audit.CategoryMCP || ev.Status != audit.StatusError {
		t.Errorf("event = {category:%q status:%q}, want {mcp error}", ev.Category, ev.Status)
	}
	if !strings.Contains(ev.Detail, `"tool":"fetch"`) || !strings.Contains(ev.Detail, "a__fetch") {
		t.Errorf("Detail = %q, want the tool and its qualified names", ev.Detail)
	}
}

// TestManager_CollisionRaceUnderTeardown exercises the registry under -race:
// calls and an unregistration of a colliding server run concurrently.
func TestManager_CollisionRaceUnderTeardown(t *testing.T) {
	tsA := startCollisionServer(t, "a", "fetch")
	tsB := startCollisionServer(t, "b", "fetch")
	m, _ := newCollisionManager(t)
	registerCollisionServer(t, m, "a", tsA)
	registerCollisionServer(t, m, "b", tsB)

	var wg sync.WaitGroup
	for _, name := range []string{"a__fetch", "b__fetch", "fetch"} {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Either outcome is fine; a panic or a torn read is not.
				_, _ = callTool(t, m, name)
				_ = m.ToolDefs()
				_ = m.ToolServer(name)
			}()
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = m.UnregisterServer("b")
	}()
	wg.Wait()

	got, err := callTool(t, m, "fetch")
	if err != nil {
		t.Fatalf("Execute(fetch) after teardown: %v", err)
	}
	if got != "a:fetch" {
		t.Errorf("Execute(fetch) = %q, want a:fetch", got)
	}
}
