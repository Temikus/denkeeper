package tool

import (
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
