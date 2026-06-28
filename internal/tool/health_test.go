package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
)

// captureEmitter records audit events for assertions.
type captureEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *captureEmitter) Emit(_ context.Context, event audit.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *captureEmitter) byAction(action string) []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []audit.Event
	for _, e := range c.events {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// fakeOAuthHandler is a minimal oauthHandler whose token state can be toggled
// to simulate authorization completing (or breaking) between health ticks.
type fakeOAuthHandler struct {
	hasToken atomic.Bool
}

func (f *fakeOAuthHandler) ToolName() string                                { return "fake-oauth" }
func (f *fakeOAuthHandler) HasToken() bool                                  { return f.hasToken.Load() }
func (f *fakeOAuthHandler) ClearToken() error                               { return nil }
func (f *fakeOAuthHandler) Close()                                          {}
func (f *fakeOAuthHandler) InitiateOAuth(_ context.Context, _ string) error { return nil }

func TestCheckServers_OAuthPending_WarnsOnceAcrossTicks(t *testing.T) {
	m := NewManager(testLogger())
	auditor := &captureEmitter{}
	m.Auditor = auditor
	m.servers["fastmail"] = &serverConn{
		name:      "fastmail",
		transport: "sse",
		cfg:       config.ToolConfig{Auth: "oauth"},
		// session nil + no oauthHandler => pending_auth (tokenless).
	}

	// Three ticks: the debounce should hold at exactly one audit event.
	for i := 0; i < 3; i++ {
		m.checkServers(context.Background(), 3, 5*time.Minute, 3)
	}

	events := auditor.byAction("oauth_pending")
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 oauth_pending audit across ticks, got %d", len(events))
	}
	if events[0].Category != audit.CategoryMCP || events[0].Source != "health_checker" {
		t.Errorf("event metadata = {category:%q source:%q}, want {mcp health_checker}",
			events[0].Category, events[0].Source)
	}
	if !strings.Contains(events[0].Detail, `"server":"fastmail"`) {
		t.Errorf("Detail = %q, want server fastmail", events[0].Detail)
	}
}

func TestCheckServers_OAuthPending_ResetsAndRewarnsAfterTokenCycles(t *testing.T) {
	m := NewManager(testLogger())
	auditor := &captureEmitter{}
	m.Auditor = auditor
	handler := &fakeOAuthHandler{}
	m.servers["fastmail"] = &serverConn{
		name:         "fastmail",
		transport:    "sse",
		cfg:          config.ToolConfig{Auth: "oauth"},
		oauthHandler: handler,
	}

	// Tokenless: first tick warns, second tick is debounced.
	m.checkServers(context.Background(), 3, 5*time.Minute, 3)
	m.checkServers(context.Background(), 3, 5*time.Minute, 3)
	if got := len(auditor.byAction("oauth_pending")); got != 1 {
		t.Fatalf("after initial pending ticks, oauth_pending count = %d, want 1", got)
	}

	// Token appears (authorization completed): tick clears the warned flag.
	handler.hasToken.Store(true)
	m.checkServers(context.Background(), 3, 5*time.Minute, 3)
	if got := len(auditor.byAction("oauth_pending")); got != 1 {
		t.Fatalf("after token appears, oauth_pending count = %d, want 1 (no new warn)", got)
	}

	// Token disappears again (tool broke): the tool re-warns exactly once.
	handler.hasToken.Store(false)
	m.checkServers(context.Background(), 3, 5*time.Minute, 3)
	m.checkServers(context.Background(), 3, 5*time.Minute, 3)
	if got := len(auditor.byAction("oauth_pending")); got != 2 {
		t.Fatalf("after token disappears, oauth_pending count = %d, want 2 (re-warned once)", got)
	}
}

func TestCheckServers_NonOAuthSessionNil_NoWarn(t *testing.T) {
	m := NewManager(testLogger())
	auditor := &captureEmitter{}
	m.Auditor = auditor
	// In-process server: session nil, transport "", not an OAuth tool.
	m.servers["in-process"] = &serverConn{
		name: "in-process",
		cfg:  config.ToolConfig{},
	}

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	if got := len(auditor.byAction("oauth_pending")); got != 0 {
		t.Errorf("non-OAuth session==nil server emitted %d oauth_pending events, want 0", got)
	}
}

func TestServerStatus_Connected(t *testing.T) {
	m := NewManager(testLogger())
	m.servers["test"] = &serverConn{
		name:        "test",
		transport:   "stdio",
		command:     "/usr/bin/echo",
		connectedAt: time.Now().Add(-5 * time.Minute),
	}

	status, ok := m.ServerInfo("test")
	if !ok {
		t.Fatal("expected server to be found")
	}
	if status.Status != "connected" {
		t.Errorf("Status = %q, want %q", status.Status, "connected")
	}
	if status.UptimeSecs < 290 {
		t.Errorf("UptimeSecs = %f, want >= 290", status.UptimeSecs)
	}
}

func TestServerStatus_Error(t *testing.T) {
	m := NewManager(testLogger())
	m.servers["test"] = &serverConn{
		name:        "test",
		transport:   "stdio",
		lastError:   "connection lost",
		connectedAt: time.Now(),
	}

	status, ok := m.ServerInfo("test")
	if !ok {
		t.Fatal("expected server to be found")
	}
	if status.Status != "error" {
		t.Errorf("Status = %q, want %q", status.Status, "error")
	}
	if status.LastError != "connection lost" {
		t.Errorf("LastError = %q, want %q", status.LastError, "connection lost")
	}
}

func TestServerStatus_Connecting(t *testing.T) {
	m := NewManager(testLogger())
	m.RegisterPending("test-remote", config.ToolConfig{
		Transport: "sse",
		URL:       "http://localhost:9999/sse",
	}, "connection refused")

	status, ok := m.ServerInfo("test-remote")
	if !ok {
		t.Fatal("expected server to be found")
	}
	if status.Status != "connecting" {
		t.Errorf("Status = %q, want %q", status.Status, "connecting")
	}
	if status.LastError != "connection refused" {
		t.Errorf("LastError = %q, want %q", status.LastError, "connection refused")
	}
	if status.Transport != "sse" {
		t.Errorf("Transport = %q, want %q", status.Transport, "sse")
	}
}

func TestServerStatus_Disabled(t *testing.T) {
	m := NewManager(testLogger())
	m.servers["test"] = &serverConn{
		name:         "test",
		transport:    "stdio",
		disabled:     true,
		restartCount: 4,
		lastError:    "too many failures",
	}

	status, ok := m.ServerInfo("test")
	if !ok {
		t.Fatal("expected server to be found")
	}
	if status.Status != "disabled" {
		t.Errorf("Status = %q, want %q", status.Status, "disabled")
	}
	if status.RestartCount != 4 {
		t.Errorf("RestartCount = %d, want 4", status.RestartCount)
	}
	if status.Enabled {
		t.Error("expected Enabled = false for restart-exhausted server")
	}
}

func TestStartHealthChecker_DisabledByConfig(t *testing.T) {
	disabled := false
	m := NewManager(testLogger(), config.MCPConfig{AutoRestart: &disabled})

	// Should return immediately without starting a goroutine.
	// We can't easily test that no goroutine was started, but we verify
	// it doesn't panic.
	ctx, cancel := t.Context(), func() {}
	_ = cancel
	m.StartHealthChecker(ctx, time.Hour)
}

func TestRestartServer_NotRegistered(t *testing.T) {
	m := NewManager(testLogger())
	err := m.RestartServer(t.Context(), "no-such-server")
	if err == nil {
		t.Fatal("expected error for unregistered server, got nil")
	}
}

func TestHandleServerFailure_MaxAttempts(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name:         "test",
		transport:    "stdio",
		restartCount: 3, // already at max
	}
	m.servers["test"] = sc

	m.handleServerFailure(t.Context(), sc, 3, 5*time.Minute, "test error")

	if !sc.disabled {
		t.Error("expected server to be disabled after max attempts")
	}
	if sc.restartCount != 4 {
		t.Errorf("restartCount = %d, want 4", sc.restartCount)
	}
}

func TestRestartServer_SSE_Success(t *testing.T) {
	ts := startStreamableServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	cfg := config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}

	err := m.RegisterServer(context.Background(), "test-sse", cfg)
	if err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	err = m.RestartServer(context.Background(), "test-sse")
	if err != nil {
		t.Fatalf("RestartServer failed: %v", err)
	}

	info, ok := m.ServerInfo("test-sse")
	if !ok {
		t.Fatal("server should be registered after restart")
	}
	if info.Status != "connected" {
		t.Errorf("Status = %q, want %q", info.Status, "connected")
	}
	if len(info.ToolNames) != 1 || info.ToolNames[0] != "greet" {
		t.Errorf("ToolNames = %v, want [greet]", info.ToolNames)
	}
}

// TestCheckServers_ResetsRestartCountAfterCooldown covers the bug where
// restartCount drifted monotonically across intermittent failures because the
// reset path only triggered on an error→healthy transition. A healthy probe
// on a server that was never cleared should reset the counter once the server
// has been connected longer than the cooldown.
func TestCheckServers_ResetsRestartCountAfterCooldown(t *testing.T) {
	ts := startStreamableServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	cfg := config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}

	if err := m.RegisterServer(context.Background(), "test-sse", cfg); err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	m.mu.Lock()
	sc := m.servers["test-sse"]
	// Simulate a prior failure that bumped the counter and an old connection
	// timestamp beyond the cooldown. lastError is empty — this is the state
	// after a successful restart, which previously skipped the reset path.
	sc.restartCount = 2
	sc.connectedAt = time.Now().Add(-10 * time.Minute)
	sc.lastError = ""
	m.mu.Unlock()

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	m.mu.RLock()
	got := sc.restartCount
	m.mu.RUnlock()
	if got != 0 {
		t.Errorf("restartCount = %d after healthy probe past cooldown, want 0", got)
	}
}

func TestCheckServers_DoesNotResetWithinCooldown(t *testing.T) {
	ts := startStreamableServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	cfg := config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}

	if err := m.RegisterServer(context.Background(), "test-sse", cfg); err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	m.mu.Lock()
	sc := m.servers["test-sse"]
	sc.restartCount = 1
	sc.connectedAt = time.Now().Add(-1 * time.Minute) // still within cooldown
	sc.lastError = ""
	m.mu.Unlock()

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	m.mu.RLock()
	got := sc.restartCount
	m.mu.RUnlock()
	if got != 1 {
		t.Errorf("restartCount = %d after healthy probe within cooldown, want 1 (reset too eagerly)", got)
	}
}

// startFlakyStreamableServer starts a streamable MCP server whose handler can
// be switched to return 500s, making health probes fail without closing the
// httptest server (mid-test Close would block on the persistent MCP stream).
func startFlakyStreamableServer(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	server := newTestMCPServer()
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var failing atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "injected failure", http.StatusInternalServerError)
			return
		}
		inner.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, &failing
}

// failingSSEServer registers an SSE server, attaches a capture emitter, then
// makes the upstream fail so health probes error. restartCount is pre-set past
// maxAttempts so handleServerFailure disables instead of sleeping on backoff.
func failingSSEServer(t *testing.T) (*Manager, *serverConn, *captureEmitter) {
	t.Helper()
	ts, failing := startFlakyStreamableServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 2})
	cfg := config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}
	if err := m.RegisterServer(context.Background(), "test-sse", cfg); err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	auditor := &captureEmitter{}
	m.Auditor = auditor

	failing.Store(true) // probes now fail with HTTP 500

	m.mu.Lock()
	sc := m.servers["test-sse"]
	sc.restartCount = 3
	m.mu.Unlock()
	return m, sc, auditor
}

func TestCheckServers_RemoteFailureBelowThreshold_NoAudit(t *testing.T) {
	m, sc, auditor := failingSSEServer(t)

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	m.mu.RLock()
	failures := sc.probeFailures
	m.mu.RUnlock()
	if failures != 1 {
		t.Errorf("probeFailures = %d, want 1", failures)
	}
	if events := auditor.byAction("health_fail"); len(events) != 0 {
		t.Errorf("expected no health_fail audit below threshold, got %d", len(events))
	}
}

func TestCheckServers_RemoteFailureAtThreshold_Audits(t *testing.T) {
	m, sc, auditor := failingSSEServer(t)

	m.mu.Lock()
	sc.probeFailures = 2 // this probe is the 3rd consecutive failure
	m.mu.Unlock()

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	events := auditor.byAction("health_fail")
	if len(events) != 1 {
		t.Fatalf("expected 1 health_fail audit at threshold, got %d", len(events))
	}
	if !strings.Contains(events[0].Detail, `"consecutive_failures":3`) {
		t.Errorf("Detail = %q, want consecutive_failures:3", events[0].Detail)
	}
}

func TestCheckServers_StdioFailureAuditsImmediately(t *testing.T) {
	m, sc, auditor := failingSSEServer(t)

	// Reuse the failing SSE session but mark the server as stdio: a dead
	// local subprocess is meaningful on the first failure.
	m.mu.Lock()
	sc.transport = "stdio"
	m.mu.Unlock()

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	if events := auditor.byAction("health_fail"); len(events) != 1 {
		t.Fatalf("expected health_fail audit on first stdio failure, got %d", len(events))
	}
}

func TestCheckServers_HealthyProbeResetsProbeFailures(t *testing.T) {
	ts := startStreamableServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	cfg := config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}
	if err := m.RegisterServer(context.Background(), "test-sse", cfg); err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	m.mu.Lock()
	sc := m.servers["test-sse"]
	sc.probeFailures = 2
	m.mu.Unlock()

	m.checkServers(context.Background(), 3, 5*time.Minute, 3)

	m.mu.RLock()
	got := sc.probeFailures
	m.mu.RUnlock()
	if got != 0 {
		t.Errorf("probeFailures = %d after healthy probe, want 0", got)
	}
}

func TestRestartServer_SSE_RecoveryOnFailure(t *testing.T) {
	ts := startStreamableServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 2})
	cfg := config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}

	err := m.RegisterServer(context.Background(), "test-sse", cfg)
	if err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}

	// Forcibly close client connections before shutting down the test server,
	// otherwise ts.Close() blocks waiting for the long-lived SSE connection
	// (no http.Client.Timeout to kill it).
	ts.CloseClientConnections()
	ts.Close()

	err = m.RestartServer(context.Background(), "test-sse")
	if err == nil {
		t.Fatal("expected error from RestartServer after remote server shutdown")
	}

	// The tool should still be visible with error status, not lost.
	info, ok := m.ServerInfo("test-sse")
	if !ok {
		t.Fatal("server should still be registered after failed restart")
	}
	if info.Status != "error" {
		t.Errorf("Status = %q, want %q", info.Status, "error")
	}
	if info.LastError == "" {
		t.Error("LastError should be set after failed restart")
	}
}

func TestRegisterDisabled_UserDisabled(t *testing.T) {
	m := NewManager(testLogger())
	cfg := config.ToolConfig{
		Command: "/usr/bin/tool",
	}

	m.RegisterDisabled("my-tool", cfg, "disabled by user", false)

	status, ok := m.ServerInfo("my-tool")
	if !ok {
		t.Fatal("expected server to be found")
	}
	if status.Status != "disabled" {
		t.Errorf("Status = %q, want %q", status.Status, "disabled")
	}
	if status.Enabled {
		t.Error("expected Enabled = false")
	}
	if status.ConfigError != "" {
		t.Errorf("ConfigError = %q, want empty", status.ConfigError)
	}
}

func TestRegisterDisabled_ConfigError(t *testing.T) {
	m := NewManager(testLogger())
	cfg := config.ToolConfig{
		Transport: "sse",
	}

	m.RegisterDisabled("bad-tool", cfg, "url is required for sse transport", true)

	status, ok := m.ServerInfo("bad-tool")
	if !ok {
		t.Fatal("expected server to be found")
	}
	if status.Status != "config_error" {
		t.Errorf("Status = %q, want %q", status.Status, "config_error")
	}
	if status.Enabled {
		t.Error("expected Enabled = false")
	}
	if status.ConfigError != "url is required for sse transport" {
		t.Errorf("ConfigError = %q, want %q", status.ConfigError, "url is required for sse transport")
	}
}

func TestServerStatus_UserDisabledTakesPriority(t *testing.T) {
	m := NewManager(testLogger())
	m.servers["test"] = &serverConn{
		name:         "test",
		transport:    "stdio",
		userDisabled: true,
		lastError:    "some error",
	}

	status, ok := m.ServerInfo("test")
	if !ok {
		t.Fatal("expected server to be found")
	}
	if status.Status != "disabled" {
		t.Errorf("Status = %q, want %q (userDisabled should take priority)", status.Status, "disabled")
	}
}
