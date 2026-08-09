package tool

// Tests for OAuth token cleanup across tool lifecycle transitions (issue #284):
// tokens are keyed by tool name alone, so any removal path that skips cleanup
// leaves a row that a later same-named tool would silently adopt.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// recordingTokenDeleter records store-level token deletions.
type recordingTokenDeleter struct {
	deleted []string
	err     error
}

func (d *recordingTokenDeleter) Delete(toolName string) error {
	d.deleted = append(d.deleted, toolName)
	return d.err
}

// clearRecordingHandler is an oauthHandler that records ClearToken calls.
type clearRecordingHandler struct {
	fakeOAuthHandler
	clearCalls int
	clearErr   error
}

func (h *clearRecordingHandler) ClearToken() error {
	h.clearCalls++
	return h.clearErr
}

func newOAuthTestManager(deleter *recordingTokenDeleter) *Manager {
	m := NewManager(testLogger())
	m.SetOAuthSupport(&OAuthSupport{
		HandlerFactory: func(name string, cfg config.ToolConfig, httpClient *http.Client) (oauthHandler, any, error) {
			return &fakeOAuthHandler{}, nil, nil
		},
		CallbackURL: "http://localhost:8080/api/v1/tools/oauth/callback",
		TokenStore:  deleter,
	})
	return m
}

func TestCleanupOAuthToken_LiveHandler_ClearsViaHandler(t *testing.T) {
	deleter := &recordingTokenDeleter{}
	m := newOAuthTestManager(deleter)
	handler := &clearRecordingHandler{}
	m.servers["fastmail"] = &serverConn{
		name:         "fastmail",
		transport:    "sse",
		cfg:          config.ToolConfig{Auth: "oauth"},
		oauthHandler: handler,
	}

	m.CleanupOAuthToken("fastmail")

	if handler.clearCalls != 1 {
		t.Errorf("ClearToken calls = %d, want 1", handler.clearCalls)
	}
	if len(deleter.deleted) != 0 {
		t.Errorf("store deletions = %v, want none (handler already cleared)", deleter.deleted)
	}
}

func TestCleanupOAuthToken_DisabledTool_DeletesFromStore(t *testing.T) {
	// The issue #284 gap: RegisterDisabled builds a serverConn without an
	// oauthHandler, so cleanup used to skip the persisted token entirely.
	deleter := &recordingTokenDeleter{}
	m := newOAuthTestManager(deleter)
	m.RegisterDisabled("fastmail", config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/mcp",
		Auth:      "oauth",
	}, "disabled by user", false)

	m.CleanupOAuthToken("fastmail")

	if !slices.Contains(deleter.deleted, "fastmail") {
		t.Errorf("store deletions = %v, want fastmail", deleter.deleted)
	}
}

func TestCleanupOAuthToken_UnregisteredName_DeletesFromStore(t *testing.T) {
	deleter := &recordingTokenDeleter{}
	m := newOAuthTestManager(deleter)

	m.CleanupOAuthToken("ghost")

	if !slices.Contains(deleter.deleted, "ghost") {
		t.Errorf("store deletions = %v, want ghost", deleter.deleted)
	}
}

func TestCleanupOAuthToken_HandlerError_FallsBackToStore(t *testing.T) {
	deleter := &recordingTokenDeleter{}
	m := newOAuthTestManager(deleter)
	handler := &clearRecordingHandler{clearErr: errors.New("clear failed")}
	m.servers["fastmail"] = &serverConn{
		name:         "fastmail",
		transport:    "sse",
		cfg:          config.ToolConfig{Auth: "oauth"},
		oauthHandler: handler,
	}

	m.CleanupOAuthToken("fastmail")

	if handler.clearCalls != 1 {
		t.Errorf("ClearToken calls = %d, want 1", handler.clearCalls)
	}
	if !slices.Contains(deleter.deleted, "fastmail") {
		t.Errorf("store deletions = %v, want fastmail as fallback", deleter.deleted)
	}
}

func TestCleanupOAuthToken_NoOAuthSupport_NoPanic(t *testing.T) {
	m := NewManager(testLogger())
	m.RegisterDisabled("fastmail", config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/mcp",
		Auth:      "oauth",
	}, "disabled by user", false)

	m.CleanupOAuthToken("fastmail")
	m.CleanupOAuthToken("ghost")
}

// newOAuthLifecycleMgr builds a LifecycleManager whose Manager has OAuth
// support with a recording token deleter and a tokenless handler factory
// (so SSE OAuth registrations short-circuit to pending_auth, no network).
func newOAuthLifecycleMgr(t *testing.T) (*LifecycleManager, *recordingTokenDeleter) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	seed := "[tools]\n[tools.fastmail]\ntransport = \"sse\"\nurl = \"https://mcp.example.com/mcp\"\nauth = \"oauth\"\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}
	deleter := &recordingTokenDeleter{}
	return NewLifecycleManager(newOAuthTestManager(deleter), cfgPath, 5, testLogger()), deleter
}

func TestRemoveTool_DisabledOAuthTool_DeletesStoredToken(t *testing.T) {
	lm, deleter := newOAuthLifecycleMgr(t)
	lm.toolMgr.RegisterDisabled("fastmail", config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/mcp",
		Auth:      "oauth",
	}, "disabled by user", false)

	if err := lm.RemoveTool(context.Background(), "fastmail"); err != nil {
		t.Fatalf("RemoveTool: %v", err)
	}

	if !slices.Contains(deleter.deleted, "fastmail") {
		t.Errorf("store deletions = %v, want fastmail", deleter.deleted)
	}
	if _, ok := lm.toolMgr.ServerInfo("fastmail"); ok {
		t.Error("server still registered after RemoveTool")
	}
}

func TestAddTool_PurgesOrphanedTokenBeforeRegister(t *testing.T) {
	// A token row left behind by a deleted tool must not be adopted by a new
	// tool created under the same name.
	lm, deleter := newOAuthLifecycleMgr(t)

	err := lm.AddTool(context.Background(), "fastmail", config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/mcp",
		Auth:      "oauth",
	})
	if err != nil {
		t.Fatalf("AddTool: %v", err)
	}

	if !slices.Contains(deleter.deleted, "fastmail") {
		t.Errorf("store deletions = %v, want fastmail purged before register", deleter.deleted)
	}
}

func TestUpdateTool_OAuthIdentityChanged_DeletesToken(t *testing.T) {
	lm, deleter := newOAuthLifecycleMgr(t)
	lm.toolMgr.RegisterDisabled("fastmail", config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/mcp",
		Auth:      "oauth",
	}, "disabled by user", false)

	err := lm.UpdateTool(context.Background(), "fastmail", config.ToolConfig{
		Transport: "sse",
		URL:       "https://other.example.com/mcp", // different server
		Auth:      "oauth",
	})
	if err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}

	if !slices.Contains(deleter.deleted, "fastmail") {
		t.Errorf("store deletions = %v, want fastmail (identity changed)", deleter.deleted)
	}
}

func TestUpdateTool_OAuthIdentityUnchanged_KeepsToken(t *testing.T) {
	lm, deleter := newOAuthLifecycleMgr(t)
	cfg := config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/mcp",
		Auth:      "oauth",
	}
	lm.toolMgr.RegisterDisabled("fastmail", cfg, "disabled by user", false)

	updated := cfg
	updated.DisabledTools = []string{"noisy_tool"} // non-identity change
	if err := lm.UpdateTool(context.Background(), "fastmail", updated); err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}

	if len(deleter.deleted) != 0 {
		t.Errorf("store deletions = %v, want none (identity unchanged)", deleter.deleted)
	}
}

func TestRemovePlugin_DeletesOrphanedToken(t *testing.T) {
	lm, deleter := newOAuthLifecycleMgr(t)
	lm.TrackPlugin("fastmail", config.PluginConfig{Type: "subprocess", Command: "/usr/bin/test"})

	if err := lm.RemovePlugin(context.Background(), "fastmail"); err != nil {
		t.Fatalf("RemovePlugin: %v", err)
	}

	if !slices.Contains(deleter.deleted, "fastmail") {
		t.Errorf("store deletions = %v, want fastmail", deleter.deleted)
	}
}

func TestOAuthIdentityChanged(t *testing.T) {
	base := config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/mcp",
		Auth:      "oauth",
		ClientID:  "id-1",
		Scopes:    []string{"read"},
	}

	if oauthIdentityChanged(base, base) {
		t.Error("identical configs reported as changed")
	}

	neither := config.ToolConfig{Transport: "stdio", Command: "/usr/bin/tool"}
	if oauthIdentityChanged(neither, neither) {
		t.Error("non-OAuth configs reported as changed")
	}

	mutations := map[string]func(c *config.ToolConfig){
		"auth off":      func(c *config.ToolConfig) { c.Auth = "" },
		"client id":     func(c *config.ToolConfig) { c.ClientID = "id-2" },
		"client secret": func(c *config.ToolConfig) { c.ClientSecret = "hunter2" },
		"url":           func(c *config.ToolConfig) { c.URL = "https://other.example.com/mcp" },
		"scopes":        func(c *config.ToolConfig) { c.Scopes = []string{"read", "write"} },
	}
	for label, mutate := range mutations {
		changed := base
		mutate(&changed)
		if !oauthIdentityChanged(base, changed) {
			t.Errorf("%s: change not detected", label)
		}
	}

	// Transition into OAuth purges rows orphaned by pre-cleanup versions.
	if !oauthIdentityChanged(neither, base) {
		t.Error("transition into OAuth not detected")
	}
}
