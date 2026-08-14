package tool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// gatedServer is an MCP server whose single "slow_op" tool blocks until the
// test releases it, so a tool call can be held in flight across a teardown.
type gatedServer struct {
	entered     chan struct{} // receives once per call, when the handler starts
	release     chan struct{} // closed to let parked handlers return
	releaseOnce sync.Once
}

func newGatedServer() *gatedServer {
	return &gatedServer{
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
}

// releaseAll unparks every handler. Safe to call more than once, so a test can
// release explicitly and still rely on the cleanup as a backstop.
func (g *gatedServer) releaseAll() {
	g.releaseOnce.Do(func() { close(g.release) })
}

func (g *gatedServer) handle(ctx context.Context, _ *mcp.CallToolRequest, _ sayHiParams) (*mcp.CallToolResult, any, error) {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "slow done"}},
	}, nil, nil
}

// waitEntered blocks until a slow_op handler has started executing.
func (g *gatedServer) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the slow tool call to start")
	}
}

// startGatedServer registers a gated MCP server over Streamable HTTP and
// returns its control handle plus the server URL.
func startGatedServer(t *testing.T, toolName string) (*gatedServer, string) {
	t.Helper()
	g := newGatedServer()
	srv := mcp.NewServer(&mcp.Implementation{Name: "gated-server", Version: "v1.0.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: toolName, Description: "a slow operation"}, g.handle)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return g, ts.URL
}

// drainManager builds a manager with the given drain ceiling and registers a
// gated server under name/toolName.
//
// Cleanups run LIFO, and the order matters: unpark the handlers, then close the
// manager (so its sessions are gone), then close the HTTP server. Closing
// httptest while a session is still connected blocks for the whole test.
func drainManager(t *testing.T, name, toolName, drainTimeout string) (*Manager, *gatedServer) {
	t.Helper()
	g, url := startGatedServer(t, toolName)
	m := NewManager(testLogger(), config.MCPConfig{
		RequestTimeoutSecs: 10,
		DrainTimeout:       drainTimeout,
	})
	cfg := config.ToolConfig{Transport: "sse", URL: url, AllowLoopback: true}
	if err := m.RegisterServer(context.Background(), name, cfg); err != nil {
		t.Fatalf("RegisterServer(%s) failed: %v", name, err)
	}
	t.Cleanup(func() { _ = m.Close() })
	t.Cleanup(g.releaseAll)
	return m, g
}

func callToolCall(name string) llm.ToolCall {
	return llm.ToolCall{Function: llm.FunctionCall{Name: name, Arguments: `{"name":"x"}`}}
}

// TestUnregisterServer_DrainsInFlightCall is the item-defining case: a call
// already executing when teardown starts must be allowed to finish, and the
// transport must not close until it has.
func TestUnregisterServer_DrainsInFlightCall(t *testing.T) {
	m, g := drainManager(t, "slow-srv", "slow_op", "30s")

	callDone := make(chan error, 1)
	var callText string
	go func() {
		text, err := m.Execute(context.Background(), callToolCall("slow_op"))
		callText = text
		callDone <- err
	}()
	g.waitEntered(t)

	unregisterDone := make(chan struct{})
	go func() {
		if err := m.UnregisterServer("slow-srv"); err != nil {
			t.Errorf("UnregisterServer: %v", err)
		}
		close(unregisterDone)
	}()

	// The unregister must be parked in phase 2 while the call runs. Give it a
	// moment to get there, then confirm it has not closed the session yet.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-unregisterDone:
		t.Fatal("UnregisterServer returned while a tool call was still in flight — the call was not drained")
	default:
	}

	g.releaseAll()

	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("in-flight call should have completed successfully, got: %v", err)
		}
		if callText != "slow done" {
			t.Errorf("call result = %q, want %q", callText, "slow done")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the drained call to complete")
	}

	select {
	case <-unregisterDone:
	case <-time.After(5 * time.Second):
		t.Fatal("UnregisterServer did not finish after the drained call completed")
	}
}

// TestExecute_DuringDrainRefusedFast checks that teardown stops the server
// being offered immediately: a call arriving mid-drain is refused with the
// sentinel, far inside the drain window rather than blocking on it.
func TestExecute_DuringDrainRefusedFast(t *testing.T) {
	m, g := drainManager(t, "slow-srv", "slow_op", "30s")

	// Hold the conn so we can still reach it after phase 1 removes it.
	m.mu.RLock()
	sc := m.servers["slow-srv"]
	m.mu.RUnlock()

	go func() { _, _ = m.Execute(context.Background(), callToolCall("slow_op")) }()
	g.waitEntered(t)

	go func() { _ = m.UnregisterServer("slow-srv") }()

	// Wait for phase 1 to land.
	deadline := time.Now().Add(5 * time.Second)
	for sc.drainStatus() != drainStateDraining {
		if time.Now().After(deadline) {
			t.Fatal("server never entered the draining state")
		}
		time.Sleep(time.Millisecond)
	}

	// Phase 1 unpublishes the tool, so a fresh lookup 404s. Go straight at the
	// draining conn to prove the refusal is the drain sentinel, not the
	// unknown-tool path, and that it does not wait out the drain window.
	m.mu.Lock()
	m.toolMap["slow_op"] = sc
	m.mu.Unlock()

	start := time.Now()
	_, err := m.Execute(context.Background(), callToolCall("slow_op"))
	elapsed := time.Since(start)

	if !errors.Is(err, ErrServerDraining) {
		t.Fatalf("Execute during drain: err = %v, want wrapped ErrServerDraining", err)
	}
	if elapsed > time.Second {
		t.Errorf("refusal took %v — should be immediate, not bounded by the drain window", elapsed)
	}
	if !strings.Contains(err.Error(), "slow-srv") {
		t.Errorf("error %q should name the server", err)
	}

	g.releaseAll()
}

// TestUnregisterServer_ForcedCloseAfterTimeout checks the ceiling: a call that
// outlives drain_timeout does not hold teardown open forever, and the forced
// close is recorded exactly once with the in-flight count.
func TestUnregisterServer_ForcedCloseAfterTimeout(t *testing.T) {
	m, g := drainManager(t, "slow-srv", "slow_op", "50ms")
	auditor := &captureEmitter{}
	m.Auditor = auditor

	go func() { _, _ = m.Execute(context.Background(), callToolCall("slow_op")) }()
	g.waitEntered(t)

	forced := make(chan struct{})
	go func() {
		_ = m.UnregisterServer("slow-srv")
		close(forced)
	}()

	// The audit event fires when the window expires, well before the call
	// (and therefore session.Close) finishes.
	deadline := time.Now().Add(5 * time.Second)
	for len(auditor.byAction("forced_close")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no forced_close audit event after the drain window expired")
		}
		time.Sleep(5 * time.Millisecond)
	}

	g.releaseAll()
	select {
	case <-forced:
	case <-time.After(5 * time.Second):
		t.Fatal("UnregisterServer did not return after the forced close")
	}

	events := auditor.byAction("forced_close")
	if len(events) != 1 {
		t.Fatalf("forced_close events = %d, want exactly 1", len(events))
	}
	e := events[0]
	if e.Category != audit.CategoryMCP {
		t.Errorf("Category = %q, want %q", e.Category, audit.CategoryMCP)
	}
	if e.Status != audit.StatusError {
		t.Errorf("Status = %q, want %q", e.Status, audit.StatusError)
	}
	if !strings.Contains(e.Detail, `"server":"slow-srv"`) {
		t.Errorf("Detail = %q, want the server name", e.Detail)
	}
	if !strings.Contains(e.Detail, `"in_flight":1`) {
		t.Errorf("Detail = %q, want in_flight 1", e.Detail)
	}
	if !strings.Contains(e.Detail, `"drain_timeout":"50ms"`) {
		t.Errorf("Detail = %q, want the drain window", e.Detail)
	}
}

// TestUnregisterServer_DoesNotBlockOtherServers is the reason the drain moved
// off the write lock: one server's teardown must not stall anyone else's tool
// calls or the tool-definition lookup every LLM request performs.
func TestUnregisterServer_DoesNotBlockOtherServers(t *testing.T) {
	// Started before the manager so its cleanup runs last — the manager's
	// sessions must be closed before any httptest server is.
	tsB := startStreamableServer(t)

	m, gA := drainManager(t, "srv-a", "slow_op", "30s")

	// A second, healthy server on the same manager.
	cfgB := config.ToolConfig{Transport: "sse", URL: tsB.URL, AllowLoopback: true}
	if err := m.RegisterServer(context.Background(), "srv-b", cfgB); err != nil {
		t.Fatalf("RegisterServer(srv-b) failed: %v", err)
	}

	go func() { _, _ = m.Execute(context.Background(), callToolCall("slow_op")) }()
	gA.waitEntered(t)

	go func() { _ = m.UnregisterServer("srv-a") }()
	time.Sleep(100 * time.Millisecond) // let phase 2 park on the drain

	// Both of these would block for the whole drain window if teardown still
	// held the write lock across session.Close.
	unblocked := make(chan error, 1)
	go func() {
		if defs := m.ToolDefs(); len(defs) == 0 {
			unblocked <- fmt.Errorf("ToolDefs() returned nothing while srv-a drained")
			return
		}
		_, err := m.Execute(context.Background(), callToolCall("greet"))
		unblocked <- err
	}()

	select {
	case err := <-unblocked:
		if err != nil {
			t.Fatalf("work against the healthy server failed during srv-a's drain: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ToolDefs()/Execute against srv-b blocked behind srv-a's drain")
	}

	gA.releaseAll()
}

// TestServerInfo_ConcurrentWithUnregister hammers the dashboard's status path
// against registration churn. ServerInfo builds its whole status — including
// the serverConn health fields handleServerFailure writes — and must do so
// under the read lock; releasing early races teardown's rebuild of the
// advertised index and the health fields alongside it.
func TestServerInfo_ConcurrentWithUnregister(t *testing.T) {
	m := NewManager(testLogger())

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = m.ServerInfo("churn")
				_ = m.ToolNames()
			}
		}()
	}

	// Writer: register and tear down the same name repeatedly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			// Register the way discoverTools does: per-server tool list plus
			// discovery order, then rebuild the advertised projection from it.
			sc := &serverConn{name: "churn", command: "/bin/true"}
			sc.tools = []llm.ToolDef{{
				Type:     "function",
				Function: llm.FunctionDef{Name: "churn_tool"},
			}}
			m.mu.Lock()
			m.servers["churn"] = sc
			m.discoveryOrder = append(m.discoveryOrder, "churn")
			m.rebuildToolIndex()
			// Health fields ServerInfo reads, written the way
			// handleServerFailure writes them.
			sc.lastError = "boom"
			sc.restartCount++
			m.mu.Unlock()
			_ = m.UnregisterServer("churn")
		}
		close(stop)
	}()

	wg.Wait()
}

// TestServerStatus_DrainingSurfaced covers the new status value. It is reached
// through the same re-insert handleServerFailure performs after unregistering
// a failed server, which is where an operator actually sees it.
func TestServerStatus_DrainingSurfaced(t *testing.T) {
	m, g := drainManager(t, "slow-srv", "slow_op", "30s")

	m.mu.RLock()
	sc := m.servers["slow-srv"]
	m.mu.RUnlock()

	go func() { _, _ = m.Execute(context.Background(), callToolCall("slow_op")) }()
	g.waitEntered(t)

	go func() { _ = m.UnregisterServer("slow-srv") }()

	deadline := time.Now().Add(5 * time.Second)
	for sc.drainStatus() != drainStateDraining {
		if time.Now().After(deadline) {
			t.Fatal("server never entered the draining state")
		}
		time.Sleep(time.Millisecond)
	}

	// Mirror handleServerFailure: the conn is put back so the health checker
	// can keep an eye on it while teardown finishes.
	m.mu.Lock()
	m.servers["slow-srv"] = sc
	m.mu.Unlock()

	info, ok := m.ServerInfo("slow-srv")
	if !ok {
		t.Fatal("ServerInfo should find the re-inserted conn")
	}
	if info.Status != "draining" {
		t.Errorf("Status during drain = %q, want draining", info.Status)
	}

	g.releaseAll()

	// Once teardown completes the conn stops reporting draining, so a server
	// stuck in the map after a failed re-registration doesn't read as draining
	// forever.
	deadline = time.Now().Add(5 * time.Second)
	for sc.drainStatus() != drainStateClosed {
		if time.Now().After(deadline) {
			t.Fatal("server never left the draining state")
		}
		time.Sleep(time.Millisecond)
	}
	info, _ = m.ServerInfo("slow-srv")
	if info.Status == "draining" {
		t.Error("Status should no longer be draining once the transport is closed")
	}
}

// TestCheckServers_SkipsDrainingServer guards the health checker against
// racing teardown into a redundant second unregister of the same conn.
func TestCheckServers_SkipsDrainingServer(t *testing.T) {
	m := NewManager(testLogger())
	auditor := &captureEmitter{}
	m.Auditor = auditor
	sc := &serverConn{name: "gone", transport: "stdio"}
	sc.beginDrain()
	m.servers["gone"] = sc

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	if got := len(auditor.byAction("health_fail")); got != 0 {
		t.Errorf("draining server produced %d health_fail events, want 0", got)
	}
	if sc.restartCount != 0 {
		t.Errorf("draining server restartCount = %d, want 0", sc.restartCount)
	}
}

// TestCheckServers_ProbesClosedConn pins the other half of that skip: a
// *closed* conn must still be probed.
//
// handleServerFailure re-inserts the conn it just unregistered — closed session
// and all — precisely so the next probe fails and drives another restart
// attempt. Widening the skip above to "any non-live conn" would strand such a
// server forever: never probed, so restartCount stops climbing, so
// max_restart_attempts never trips and it never reaches the disabled state that
// makes the failure visible. This test fails if the condition is ever tidied
// back to `!= drainStateLive`.
func TestCheckServers_ProbesClosedConn(t *testing.T) {
	ts := startStreamableServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	auditor := &captureEmitter{}
	m.Auditor = auditor

	cfg := config.ToolConfig{Transport: "sse", URL: ts.URL, AllowLoopback: true}
	if err := m.RegisterServer(context.Background(), "flaky", cfg); err != nil {
		t.Fatalf("RegisterServer failed: %v", err)
	}
	m.mu.RLock()
	sc := m.servers["flaky"]
	m.mu.RUnlock()

	// Tear down, then put the closed conn back exactly as handleServerFailure
	// does when re-registration fails.
	if err := m.UnregisterServer("flaky"); err != nil {
		t.Fatalf("UnregisterServer: %v", err)
	}
	if got := sc.drainStatus(); got != drainStateClosed {
		t.Fatalf("drainStatus after teardown = %v, want closed", got)
	}
	m.mu.Lock()
	m.servers["flaky"] = sc
	m.mu.Unlock()

	// maxAttempts=0 makes handleServerFailure disable on the first failure and
	// return, so the probe is exercised without a restart backoff.
	m.checkServers(context.Background(), 0, 5*time.Minute, 1)

	if got := len(auditor.byAction("health_fail")); got != 1 {
		t.Fatalf("closed conn produced %d health_fail events, want 1 — it was not probed", got)
	}
	if sc.restartCount != 1 {
		t.Errorf("restartCount = %d, want 1 (the probe failure must drive a retry)", sc.restartCount)
	}
	if !sc.disabled {
		t.Error("server should be disabled after exhausting restart attempts")
	}
}

// lifecycleForDrain wires a LifecycleManager over a manager holding one gated
// server, with a config file the disable/restart paths can persist to.
func lifecycleForDrain(t *testing.T, name, toolName string) (*LifecycleManager, *gatedServer) {
	t.Helper()
	m, g := drainManager(t, name, toolName, "30s")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("[telegram]\ntoken = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Persist the tool so the disable path has a [tools.*] section to flip.
	cfg, ok := m.ServerToolConfig(name)
	if !ok {
		t.Fatalf("registered server %q has no stored config", name)
	}
	if err := addToolToConfig(cfgPath, name, cfg); err != nil {
		t.Fatalf("seeding tool config: %v", err)
	}
	return NewLifecycleManager(m, cfgPath, 5, testLogger()), g
}

// TestLifecycleManager_DisableDrainsInFlightCall proves the lifecycle paths
// inherit drain behaviour for free: they all funnel through UnregisterServer,
// so none of them needs its own handling.
func TestLifecycleManager_DisableDrainsInFlightCall(t *testing.T) {
	lm, g := lifecycleForDrain(t, "slow-srv", "slow_op")

	callDone := make(chan error, 1)
	go func() {
		_, err := lm.ToolManager().Execute(context.Background(), callToolCall("slow_op"))
		callDone <- err
	}()
	g.waitEntered(t)

	disabled := make(chan error, 1)
	go func() { disabled <- lm.DisableTool(context.Background(), "slow-srv") }()

	time.Sleep(100 * time.Millisecond)
	select {
	case <-disabled:
		t.Fatal("DisableTool returned without draining the in-flight call")
	default:
	}

	g.releaseAll()

	select {
	case err := <-callDone:
		if err != nil {
			t.Errorf("in-flight call should have survived the disable, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the drained call")
	}
	select {
	case err := <-disabled:
		if err != nil {
			t.Fatalf("DisableTool: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DisableTool did not finish after the drained call completed")
	}

	info, ok := lm.ToolManager().ServerInfo("slow-srv")
	if !ok {
		t.Fatal("disabled tool should still be listed")
	}
	if info.Status != "disabled" {
		t.Errorf("Status after disable = %q, want disabled", info.Status)
	}
}

// TestLifecycleManager_RestartDrainsInFlightCall covers the restart path, which
// re-registers the same name and so must not begin before the old transport is
// fully closed.
func TestLifecycleManager_RestartDrainsInFlightCall(t *testing.T) {
	lm, g := lifecycleForDrain(t, "slow-srv", "slow_op")

	callDone := make(chan error, 1)
	go func() {
		_, err := lm.ToolManager().Execute(context.Background(), callToolCall("slow_op"))
		callDone <- err
	}()
	g.waitEntered(t)

	restarted := make(chan error, 1)
	go func() { restarted <- lm.RestartTool(context.Background(), "slow-srv") }()

	time.Sleep(100 * time.Millisecond)
	select {
	case <-restarted:
		t.Fatal("RestartTool returned without draining the in-flight call")
	default:
	}

	g.releaseAll()

	select {
	case err := <-callDone:
		if err != nil {
			t.Errorf("in-flight call should have survived the restart, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the drained call")
	}
	select {
	case err := <-restarted:
		if err != nil {
			t.Fatalf("RestartTool: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RestartTool did not finish after the drained call completed")
	}

	// The restarted server is live again and serving its tool.
	if _, err := lm.ToolManager().Execute(context.Background(), callToolCall("slow_op")); err != nil {
		t.Errorf("tool should be callable after restart, got: %v", err)
	}
}

// TestClose_DrainsInFlightCalls checks that shutdown runs phase 1 for every
// server (so nothing new is admitted) and still waits out running work.
func TestClose_DrainsInFlightCalls(t *testing.T) {
	m, g := drainManager(t, "slow-srv", "slow_op", "30s")

	callDone := make(chan error, 1)
	go func() {
		_, err := m.Execute(context.Background(), callToolCall("slow_op"))
		callDone <- err
	}()
	g.waitEntered(t)

	closeDone := make(chan struct{})
	go func() {
		_ = m.Close()
		close(closeDone)
	}()

	time.Sleep(100 * time.Millisecond)
	select {
	case <-closeDone:
		t.Fatal("Close returned without draining the in-flight call")
	default:
	}

	// Phase 1 ran: the manager offers nothing any more.
	if defs := m.ToolDefs(); len(defs) != 0 {
		t.Errorf("ToolDefs() during shutdown = %d defs, want 0", len(defs))
	}

	g.releaseAll()

	select {
	case err := <-callDone:
		if err != nil {
			t.Errorf("in-flight call should have completed during shutdown drain, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the drained call")
	}
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish after the drained call completed")
	}

	// The whole derived index must be empty, not just the parts a hand-reset
	// would remember: owners and localOf left populated would point at
	// torn-down conns.
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.servers) != 0 || len(m.toolMap) != 0 || len(m.owners) != 0 ||
		len(m.localOf) != 0 || len(m.discoveryOrder) != 0 || len(m.toolDefs) != 0 {
		t.Errorf("registry not fully cleared after Close: servers=%d toolMap=%d owners=%d localOf=%d order=%d defs=%d",
			len(m.servers), len(m.toolMap), len(m.owners), len(m.localOf),
			len(m.discoveryOrder), len(m.toolDefs))
	}
}
