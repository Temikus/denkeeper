//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
)

func TestAudit_ListEmpty(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if len(resp.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(resp.Events))
	}
	if resp.Total != 0 {
		t.Errorf("expected total=0, got %d", resp.Total)
	}
}

func TestAudit_InsertAndList(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	ev := audit.Event{
		Timestamp:      time.Now().UTC(),
		Category:       audit.CategoryToolCall,
		Action:         "execute",
		Agent:          "default",
		Summary:        "Executed tool weather_lookup",
		Detail:         `{"tool":"weather_lookup","server":"weather-mcp"}`,
		Status:         audit.StatusOK,
		DurationMs:     150,
		Source:         "engine",
		ConversationID: "conv-1",
	}
	if err := h.AuditStore.Insert(ctx, ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if resp.Total != 1 {
		t.Fatalf("expected total=1, got %d", resp.Total)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(resp.Events))
	}
	if resp.Events[0].Category != audit.CategoryToolCall {
		t.Errorf("expected category %q, got %q", audit.CategoryToolCall, resp.Events[0].Category)
	}
	if resp.Events[0].Summary != "Executed tool weather_lookup" {
		t.Errorf("unexpected summary: %q", resp.Events[0].Summary)
	}
}

func TestAudit_FilterByCategory(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	now := time.Now().UTC()
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryToolCall, Action: "execute", Summary: "tool", Status: audit.StatusOK})
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryLLM, Action: "complete", Summary: "llm", Status: audit.StatusOK})
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryToolCall, Action: "execute", Summary: "tool2", Status: audit.StatusOK})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit?category=tool_call", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if resp.Total != 2 {
		t.Errorf("expected total=2, got %d", resp.Total)
	}
}

func TestAudit_FilterByStatus(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	now := time.Now().UTC()
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryToolCall, Action: "execute", Summary: "ok", Status: audit.StatusOK})
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryToolCall, Action: "execute", Summary: "err", Status: audit.StatusError})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit?status=error", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if resp.Total != 1 {
		t.Errorf("expected total=1, got %d", resp.Total)
	}
}

func TestAudit_Pagination(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		_ = h.AuditStore.Insert(ctx, audit.Event{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Category:  audit.CategoryLLM, Action: "complete", Summary: "event", Status: audit.StatusOK,
		})
	}

	// First page.
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit?limit=3&offset=0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if resp.Total != 10 {
		t.Errorf("expected total=10, got %d", resp.Total)
	}
	if len(resp.Events) != 3 {
		t.Errorf("expected 3 events, got %d", len(resp.Events))
	}

	// Second page.
	rec2 := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit?limit=3&offset=3", nil))
	var resp2 audit.ListResult
	DecodeJSON(t, rec2, &resp2)
	if len(resp2.Events) != 3 {
		t.Errorf("expected 3 events on page 2, got %d", len(resp2.Events))
	}
}

func TestAudit_Stats(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	now := time.Now().UTC()
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryToolCall, Action: "execute", Summary: "t1", Status: audit.StatusOK})
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryToolCall, Action: "execute", Summary: "t2", Status: audit.StatusError})
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryLLM, Action: "complete", Summary: "l1", Status: audit.StatusOK})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var stats audit.Stats
	DecodeJSON(t, rec, &stats)
	if stats.Total != 3 {
		t.Errorf("expected total=3, got %d", stats.Total)
	}
	if stats.ByCategory[audit.CategoryToolCall] != 2 {
		t.Errorf("expected 2 tool_call, got %d", stats.ByCategory[audit.CategoryToolCall])
	}
	if stats.ByStatus[audit.StatusOK] != 2 {
		t.Errorf("expected 2 ok, got %d", stats.ByStatus[audit.StatusOK])
	}
}

func TestAudit_Search(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	now := time.Now().UTC()
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryToolCall, Action: "execute", Summary: "Executed weather_lookup", Status: audit.StatusOK})
	_ = h.AuditStore.Insert(ctx, audit.Event{Timestamp: now, Category: audit.CategoryLLM, Action: "complete", Summary: "LLM call to claude", Status: audit.StatusOK})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit?search=weather", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if resp.Total != 1 {
		t.Errorf("expected total=1, got %d", resp.Total)
	}
}

func TestAudit_RequiresScope(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"chat"}, // no audit:read
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit", nil))
	// Scope enforcement returns 401 (insufficient scope on valid key).
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAudit_ToolCallDetailContainsArgumentsAndResult(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	detail := map[string]any{
		"tool":      "list-active-tasks",
		"server":    "todoist",
		"round":     1,
		"arguments": `{"filter":"today | overdue","limit":20}`,
		"result":    `[{"id":"1","content":"Review PR"},{"id":"2","content":"Water plants"}]`,
	}
	detailJSON, _ := json.Marshal(detail)

	ev := audit.Event{
		Timestamp:      time.Now().UTC(),
		Category:       audit.CategoryToolCall,
		Action:         "execute",
		Agent:          "default",
		Summary:        "list-active-tasks",
		Detail:         string(detailJSON),
		Status:         audit.StatusOK,
		DurationMs:     380,
		Source:         "engine",
		ConversationID: "conv-2",
	}
	if err := h.AuditStore.Insert(ctx, ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit?category=tool_call", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if resp.Total != 1 {
		t.Fatalf("expected total=1, got %d", resp.Total)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(resp.Events[0].Detail), &got); err != nil {
		t.Fatalf("failed to parse detail JSON: %v", err)
	}
	if _, ok := got["arguments"]; !ok {
		t.Error("detail missing 'arguments' field")
	}
	if _, ok := got["result"]; !ok {
		t.Error("detail missing 'result' field")
	}
	if got["tool"] != "list-active-tasks" {
		t.Errorf("expected tool 'list-active-tasks', got %q", got["tool"])
	}
	if got["server"] != "todoist" {
		t.Errorf("expected server 'todoist', got %q", got["server"])
	}
}

func TestAudit_ToolCallDetailTruncatedResult(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	detail := map[string]any{
		"tool":             "big-search",
		"server":           "search-mcp",
		"round":            1,
		"arguments":        `{"query":"test"}`,
		"result":           "truncated content here...",
		"result_truncated": true,
	}
	detailJSON, _ := json.Marshal(detail)

	ev := audit.Event{
		Timestamp: time.Now().UTC(),
		Category:  audit.CategoryToolCall,
		Action:    "execute",
		Summary:   "big-search",
		Detail:    string(detailJSON),
		Status:    audit.StatusOK,
		Source:    "engine",
	}
	if err := h.AuditStore.Insert(ctx, ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/audit?category=tool_call", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp audit.ListResult
	DecodeJSON(t, rec, &resp)
	if resp.Total != 1 {
		t.Fatalf("expected total=1, got %d", resp.Total)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(resp.Events[0].Detail), &got); err != nil {
		t.Fatalf("failed to parse detail JSON: %v", err)
	}
	if got["result_truncated"] != true {
		t.Error("expected result_truncated=true in detail")
	}
}
