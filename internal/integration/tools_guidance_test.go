//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
)

// TestTools_GuidanceSurvivesUnrelatedUpdate is the erasure canary: the
// dashboard's edit form never sends `guidance`, so a PUT that omits it must
// preserve what the operator wrote rather than silently blanking it.
func TestTools_GuidanceSurvivesUnrelatedUpdate(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	const guidance = "IDs come from the cached value, never the key."

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"name":           "echo-tool",
		"transport":      "sse",
		"url":            ts.URL,
		"allow_loopback": true,
		"guidance":       guidance,
	}))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add tool: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/tools/echo-tool", nil))
	})

	// A PUT with no guidance key at all — exactly what the UI sends.
	rec = h.Do(h.AuthedRequest(http.MethodPut, "/api/v1/tools/echo-tool", map[string]any{
		"transport":      "sse",
		"url":            ts.URL,
		"allow_loopback": true,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("update tool: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool", nil))
	DecodeJSON(t, rec, &resp)
	if resp["guidance"] != guidance {
		t.Errorf("guidance after an unrelated PUT = %v, want %q", resp["guidance"], guidance)
	}

	// An explicit empty string is a deliberate clear, and must take effect.
	rec = h.Do(h.AuthedRequest(http.MethodPut, "/api/v1/tools/echo-tool", map[string]any{
		"transport":      "sse",
		"url":            ts.URL,
		"allow_loopback": true,
		"guidance":       "",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing update: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	resp = map[string]any{}
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools/echo-tool", nil))
	DecodeJSON(t, rec, &resp)
	if _, ok := resp["guidance"]; ok {
		t.Errorf("guidance = %v after an explicit clear, want it gone", resp["guidance"])
	}
}

func TestTools_AddOversizeGuidance_Returns400(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"name":           "echo-tool",
		"transport":      "sse",
		"url":            ts.URL,
		"allow_loopback": true,
		"guidance":       strings.Repeat("x", 2001),
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestTools_ListIncludesGuidance(t *testing.T) {
	ts := startTestMCPServer(t)
	h := toolHarness(t)
	const guidance = "LIST-GUIDANCE-MARKER"

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tools", map[string]any{
		"name":           "echo-tool",
		"transport":      "sse",
		"url":            ts.URL,
		"allow_loopback": true,
		"guidance":       guidance,
	}))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add tool: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/tools/echo-tool", nil))
	})

	var resp map[string]any
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tools", nil))
	DecodeJSON(t, rec, &resp)
	tools := resp["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if got := tools[0].(map[string]any)["guidance"]; got != guidance {
		t.Errorf("guidance in list = %v, want %q", got, guidance)
	}
}
