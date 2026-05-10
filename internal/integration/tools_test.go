//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// toolHarness returns a harness with LifecycleManager enabled and a temp config file.
func toolHarness(t *testing.T) *Harness {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return NewHarness(t, &HarnessOpts{
		ConfigPath:       cfgPath,
		WithLifecycleMgr: true,
	})
}

func TestTools_ListEmpty(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	tools, ok := resp["tools"].([]any)
	if !ok {
		t.Fatalf("tools is not an array: %v", resp)
	}
	if len(tools) != 0 {
		t.Errorf("tools count = %d, want 0", len(tools))
	}
}

func TestTools_ListWithoutLifecycleMgr_Returns503(t *testing.T) {
	// Default harness has no LifecycleMgr.
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestTools_AddMissingName_Returns400(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"command": "echo",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestTools_AddMissingCommand_Returns400(t *testing.T) {
	h := toolHarness(t)

	// stdio transport requires command.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"name": "test-tool",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestTools_AddSSEMissingURL_Returns400(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"name":      "sse-tool",
		"transport": "sse",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestTools_AddInvalidName_Returns400(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"name":    "invalid name!",
		"command": "echo",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestTools_GetNotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTools_DeleteNotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/tools/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTools_HealthNotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/nonexistent/health", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTools_RestartNotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools/nonexistent/restart", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTools_DefsNotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/nonexistent/defs", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTools_EnableNotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools/nonexistent/enable", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTools_DisableNotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools/nonexistent/disable", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ---------------------------------------------------------------------------
// Happy-path CRUD tests (Phase 2 — require test MCP server)
// ---------------------------------------------------------------------------

// addTestTool adds the echo tool from the test MCP server. The tool connects
// synchronously, so it's already in "connected" state when this returns.
// Registers a cleanup to remove the tool and close the MCP connection before
// the httptest.Server shuts down.
func addTestTool(t *testing.T, h *Harness, ts *httptest.Server) {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"name":           "echo-tool",
		"transport":      "sse",
		"url":            ts.URL,
		"allow_loopback": true,
	}))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add tool: status = %d, want 200/201; body: %s", rec.Code, rec.Body.String())
	}
	// Remove the tool before httptest.Server.Close() to avoid connection leaks.
	t.Cleanup(func() {
		h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/tools/echo-tool", nil))
	})
}

func TestTools_AddSSEAndList(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	addTestTool(t, h, ts)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	tools := resp["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "echo-tool" {
		t.Errorf("name = %v, want echo-tool", tool["name"])
	}
	if tool["status"] != "connected" {
		t.Errorf("status = %v, want connected", tool["status"])
	}
}

func TestTools_GetRegistered(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	addTestTool(t, h, ts)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["name"] != "echo-tool" {
		t.Errorf("name = %v, want echo-tool", resp["name"])
	}
	if resp["transport"] != "sse" {
		t.Errorf("transport = %v, want sse", resp["transport"])
	}
	if resp["url"] != ts.URL {
		t.Errorf("url = %v, want %s", resp["url"], ts.URL)
	}
}

func TestTools_DeleteRegistered(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	addTestTool(t, h, ts)

	rec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/tools/echo-tool", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	// Verify it's gone.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404", rec.Code)
	}
}

func TestTools_HealthConnected(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	addTestTool(t, h, ts)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["status"] != "connected" {
		t.Errorf("status = %v, want connected", resp["status"])
	}
}

func TestTools_DefsRegistered(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	addTestTool(t, h, ts)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool/defs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("defs: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	tools, ok := resp["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatal("expected at least one tool definition")
	}
	// The echo tool should be present.
	found := false
	for _, d := range tools {
		def := d.(map[string]any)
		if def["name"] == "echo" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("echo tool not found in defs: %v", tools)
	}
}

func TestTools_RestartRegistered(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	addTestTool(t, h, ts)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools/echo-tool/restart", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("restart: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// Restart is synchronous — verify immediately.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health after restart: status = %d", rec.Code)
	}
	var health map[string]any
	DecodeJSON(t, rec, &health)
	if health["status"] != "connected" {
		t.Errorf("status after restart = %v, want connected", health["status"])
	}
}

func TestTools_UpdateDisabledTools(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	addTestTool(t, h, ts)

	// Disable the "echo" tool.
	rec := h.Do(h.AuthedRequest(http.MethodPut, "/api/v1/tools/echo-tool/disabled-tools", map[string]any{
		"disabled_tools": []string{"echo"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var status map[string]any
	DecodeJSON(t, rec, &status)
	if int(status["enabled_count"].(float64)) != 0 {
		t.Errorf("enabled_count = %v, want 0", status["enabled_count"])
	}
	if int(status["total_tool_count"].(float64)) != 1 {
		t.Errorf("total_tool_count = %v, want 1", status["total_tool_count"])
	}

	// Defs endpoint should include the disabled flag.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool/defs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("defs: status = %d", rec.Code)
	}
	var defsResp map[string]any
	DecodeJSON(t, rec, &defsResp)
	tools := defsResp["tools"].([]any)
	for _, d := range tools {
		def := d.(map[string]any)
		if def["name"] == "echo" {
			if def["disabled"] != true {
				t.Errorf("echo tool disabled = %v, want true", def["disabled"])
			}
		}
	}

	// Re-enable by clearing the list.
	rec = h.Do(h.AuthedRequest(http.MethodPut, "/api/v1/tools/echo-tool/disabled-tools", map[string]any{
		"disabled_tools": []string{},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	DecodeJSON(t, rec, &status)
	if int(status["enabled_count"].(float64)) != 1 {
		t.Errorf("enabled_count after re-enable = %v, want 1", status["enabled_count"])
	}
}

func TestTools_UpdateDisabledTools_NotFound(t *testing.T) {
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPut, "/api/v1/tools/nonexistent/disabled-tools", map[string]any{
		"disabled_tools": []string{"x"},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
