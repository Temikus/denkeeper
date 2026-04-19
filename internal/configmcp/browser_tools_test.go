package configmcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/browser"
	"github.com/Temikus/denkeeper/internal/configmcp"
)

func newBrowserTestServer(t *testing.T, profileDir string, tier string) *mcp.ClientSession {
	t.Helper()
	ps := browser.NewProfileService(profileDir, newTestLogger(t))
	session, _ := newTestServer(t, func(deps *configmcp.Deps) {
		deps.BrowserProfiles = ps
		deps.PermissionTier = func() string { return tier }
	})
	return session
}

func TestBrowserProfileList_Empty(t *testing.T) {
	dir := t.TempDir()
	session := newBrowserTestServer(t, dir, "autonomous")

	text, isErr := callTool(t, session, "browser_profile_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var resp struct {
		Profiles []json.RawMessage `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(resp.Profiles))
	}
}

func TestBrowserProfileList_WithProfiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent-a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-a", "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	session := newBrowserTestServer(t, dir, "autonomous")
	text, isErr := callTool(t, session, "browser_profile_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var resp struct {
		Profiles []struct {
			Agent string `json:"agent"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Profiles) != 1 || resp.Profiles[0].Agent != "agent-a" {
		t.Errorf("unexpected profiles: %s", text)
	}
}

func TestBrowserProfileInfo_Found(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "test-agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "f.bin"), make([]byte, 512), 0o600); err != nil {
		t.Fatal(err)
	}

	session := newBrowserTestServer(t, dir, "autonomous")
	// Omit agent arg — should default to "test-agent" (the AgentName in deps).
	text, isErr := callTool(t, session, "browser_profile_info", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var info struct {
		Agent     string `json:"agent"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if info.Agent != "test-agent" {
		t.Errorf("expected agent 'test-agent', got %q", info.Agent)
	}
	if info.SizeBytes < 512 {
		t.Errorf("expected size >= 512, got %d", info.SizeBytes)
	}
}

func TestBrowserProfileInfo_NotFound(t *testing.T) {
	dir := t.TempDir()
	session := newBrowserTestServer(t, dir, "autonomous")

	text, isErr := callTool(t, session, "browser_profile_info", map[string]any{"agent": "ghost"})
	if !isErr {
		t.Fatalf("expected error, got: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text)
	}
}

func TestBrowserProfileClear_Autonomous(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "clearme")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "data"), []byte("bye"), 0o600); err != nil {
		t.Fatal(err)
	}

	session := newBrowserTestServer(t, dir, "autonomous")
	text, isErr := callTool(t, session, "browser_profile_clear", map[string]any{"agent": "clearme"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "Done") {
		t.Errorf("expected 'Done' in response, got: %s", text)
	}

	// Directory should exist but be empty.
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		t.Fatalf("directory should exist: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %d entries", len(entries))
	}
}

func TestBrowserProfileClear_Restricted(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "test-agent"), 0o700); err != nil {
		t.Fatal(err)
	}

	session := newBrowserTestServer(t, dir, "restricted")
	text, isErr := callTool(t, session, "browser_profile_clear", map[string]any{})
	if !isErr {
		t.Fatalf("expected error for restricted tier, got: %s", text)
	}
	if !strings.Contains(text, "restricted") {
		t.Errorf("expected 'restricted' in error, got: %s", text)
	}
}

func TestBrowserProfileDelete_AlwaysRequiresApproval(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "deleteme")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Autonomous tier without approval manager — falls back to immediate execution.
	session := newBrowserTestServer(t, dir, "autonomous")
	text, isErr := callTool(t, session, "browser_profile_delete", map[string]any{"agent": "deleteme"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	// Directory should be gone (no approval manager = fallback to immediate).
	if _, err := os.Stat(agentDir); !os.IsNotExist(err) {
		t.Errorf("expected directory to be removed")
	}
}

func TestBrowserProfileDelete_Restricted(t *testing.T) {
	dir := t.TempDir()
	session := newBrowserTestServer(t, dir, "restricted")

	text, isErr := callTool(t, session, "browser_profile_delete", map[string]any{"agent": "x"})
	if !isErr {
		t.Fatalf("expected error for restricted tier, got: %s", text)
	}
	if !strings.Contains(text, "restricted") {
		t.Errorf("expected 'restricted' in error, got: %s", text)
	}
}

func TestBrowserProfileDelete_Autonomous_ForceApproval(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "test-agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	approvalMgr := approval.NewManager(store, newTestLogger(t))

	ps := browser.NewProfileService(dir, newTestLogger(t))
	session, _ := newTestServer(t, func(deps *configmcp.Deps) {
		deps.BrowserProfiles = ps
		deps.PermissionTier = func() string { return "autonomous" }
		deps.Approvals = approvalMgr
	})

	resolveAsync(t, approvalMgr, true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text, isErr, callErr := callToolCtx(ctx, session, "browser_profile_delete", map[string]any{})
	if callErr != nil {
		t.Fatalf("callToolCtx: %v", callErr)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	// Even in autonomous mode, delete requires approval (forceApproval=true).
	if !strings.Contains(text, "approved") {
		t.Errorf("expected approved message, got: %s", text)
	}

	// Directory should be gone after approval.
	if _, statErr := os.Stat(agentDir); !os.IsNotExist(statErr) {
		t.Error("expected directory to be deleted after approval")
	}
}

func TestBrowserProfileClear_Supervised_BlocksUntilApproved(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "test-agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "data"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	approvalMgr := approval.NewManager(store, newTestLogger(t))

	ps := browser.NewProfileService(dir, newTestLogger(t))
	session, _ := newTestServer(t, func(deps *configmcp.Deps) {
		deps.BrowserProfiles = ps
		deps.PermissionTier = func() string { return "supervised" }
		deps.Approvals = approvalMgr
	})

	resolveAsync(t, approvalMgr, true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text, isErr, callErr := callToolCtx(ctx, session, "browser_profile_clear", map[string]any{})
	if callErr != nil {
		t.Fatalf("callToolCtx: %v", callErr)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "approved") {
		t.Errorf("expected approved message, got: %s", text)
	}
}

func TestBrowserProfileDelete_Supervised_BlocksUntilApproved(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "test-agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	approvalMgr := approval.NewManager(store, newTestLogger(t))

	ps := browser.NewProfileService(dir, newTestLogger(t))
	session, _ := newTestServer(t, func(deps *configmcp.Deps) {
		deps.BrowserProfiles = ps
		deps.PermissionTier = func() string { return "supervised" }
		deps.Approvals = approvalMgr
	})

	resolveAsync(t, approvalMgr, true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text, isErr, callErr := callToolCtx(ctx, session, "browser_profile_delete", map[string]any{})
	if callErr != nil {
		t.Fatalf("callToolCtx: %v", callErr)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "approved") {
		t.Errorf("expected approved message, got: %s", text)
	}

	// Directory should be gone after approval.
	if _, statErr := os.Stat(agentDir); !os.IsNotExist(statErr) {
		t.Error("expected directory to be deleted after approval")
	}
}

func TestBrowserProfileInfo_ExplicitAgent(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "other-agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "f.bin"), make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}

	session := newBrowserTestServer(t, dir, "autonomous")
	text, isErr := callTool(t, session, "browser_profile_info", map[string]any{"agent": "other-agent"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var info struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if info.Agent != "other-agent" {
		t.Errorf("expected agent 'other-agent', got %q", info.Agent)
	}
}

func TestBrowserToolsDisabledWhenNilService(t *testing.T) {
	// Create server without BrowserProfiles — browser tools should not be registered.
	session, _ := newTestServer(t, func(deps *configmcp.Deps) {
		deps.BrowserProfiles = nil
	})

	// Attempting to call the tool should return an SDK-level error (unknown tool).
	argsMap := map[string]any{}
	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "browser_profile_list",
		Arguments: argsMap,
	})
	if err == nil {
		t.Error("expected error when browser tools are not registered")
	}
}
