//go:build integration

package integration

import (
	"net/http"
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
