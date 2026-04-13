package tool

import (
	"context"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
)

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
