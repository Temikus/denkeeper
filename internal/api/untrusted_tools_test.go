package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/tool"
)

// toolsWriteKey can manage tools but is not the operator.
func toolsWriteKey() config.APIKeyConfig {
	return config.APIKeyConfig{
		Name:   "tools-bot",
		Key:    "dk-test-key",
		Scopes: []string{"tools:read", "tools:write", "approvals:write"},
	}
}

func postJSON(srv *Server, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func TestAddTool_NonAdminStdioWithoutAllowlist_Forbidden(t *testing.T) {
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(testConfig(toolsWriteKey()), deps, testLogger())

	rec := postJSON(srv, "/api/v1/tools", `{"name":"x","command":"/bin/sh","args":["-c","id"]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "allowed_commands") {
		t.Errorf("body = %s, want it to name allowed_commands", rec.Body)
	}
}

func TestAddTool_NonAdminCommandOffAllowlist_Rejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/denkeeper.toml"
	_ = os.WriteFile(cfgPath, []byte("[telegram]\ntoken = \"test\"\n"), 0o600)
	mgr := tool.NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10, AllowedCommands: []string{"npx"}})
	deps := testDeps()
	deps.LifecycleMgr = tool.NewLifecycleManager(mgr, cfgPath, 50, testLogger())
	srv := New(testConfig(toolsWriteKey()), deps, testLogger())

	rec := postJSON(srv, "/api/v1/tools", `{"name":"x","command":"/bin/sh"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "allowed_commands") {
		t.Fatalf("status = %d, body %s; want 400 naming allowed_commands", rec.Code, rec.Body)
	}
}

func TestAddPlugin_NonAdminSubprocess_Forbidden(t *testing.T) {
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(testConfig(toolsWriteKey()), deps, testLogger())

	for _, body := range []string{
		`{"name":"p","type":"subprocess","command":"/bin/sh"}`,
		`{"name":"p","type":"docker","image":"x:1","network":"host"}`,
	} {
		if rec := postJSON(srv, "/api/v1/plugins", body); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403; body %s", body, rec.Code, rec.Body)
		}
	}
}

// An admin key is the operator: the plugin checks do not stop it. Without a
// "tools" capability AddPlugin spawns nothing, so the add itself succeeds.
func TestAddPlugin_AdminSubprocess_NotForbidden(t *testing.T) {
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postJSON(srv, "/api/v1/plugins", `{"name":"p","type":"subprocess","command":"/nonexistent/binary"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body)
	}
}

// allowToolEnvRefsFor runs the REST tool gate directly for a non-admin key,
// so an update can be checked against a stored stdio config without spawning.
func allowToolEnvRefsFor(t *testing.T, cfg config.ToolConfig, prev *config.ToolConfig) *httptest.ResponseRecorder {
	t.Helper()
	deps := testDeps()
	deps.LifecycleMgr = testLifecycleMgr(t)
	srv := New(testConfig(toolsWriteKey()), deps, testLogger())
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tools/x", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	if srv.allowToolEnvRefs(rec, req, cfg, prev) {
		rec.Code = http.StatusOK
	}
	return rec
}

// A new env on an operator's stdio tool reaches the respawned child
// (NODE_OPTIONS=--require ... runs other code), so it is gated like a new
// command line while no allowlist is set.
func TestUpdateTool_NonAdminStdioEnvChangeWithoutAllowlist_Forbidden(t *testing.T) {
	prev := config.ToolConfig{Command: "npx", Args: []string{"pkg@1.0.0"}, Env: map[string]string{"A": "1"}}
	cfg := prev
	cfg.Env = map[string]string{"A": "1", "NODE_OPTIONS": "--require=/tmp/x.js"}

	rec := allowToolEnvRefsFor(t, cfg, &prev)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "env") {
		t.Fatalf("status = %d, body %s; want 403 naming env", rec.Code, rec.Body)
	}
}

func TestUpdateTool_NonAdminStdioUnchangedSpawn_Allowed(t *testing.T) {
	prev := config.ToolConfig{Command: "npx", Args: []string{"pkg@1.0.0"}, Env: map[string]string{"A": "1"}}
	cfg := prev
	cfg.Env = map[string]string{"A": "1"}
	cfg.RequestTimeoutSecs = 42

	if rec := allowToolEnvRefsFor(t, cfg, &prev); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want an unchanged command line and env allowed", rec.Code, rec.Body)
	}
}

func TestCreateAutoApprove_HumanOnlyTool_Rejected(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDeps(), testLogger())
	for _, name := range []string{"tool_add", "plugin_add", "config-default__tool_add"} {
		rec := postJSON(srv, "/api/v1/auto-approve", `{"agent":"default","tool":"`+name+`","scope":"permanent"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body %s", name, rec.Code, rec.Body)
		}
	}
}
