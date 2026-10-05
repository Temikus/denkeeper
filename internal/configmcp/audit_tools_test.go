package configmcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/configmcp"
)

type approvalAuditResponse struct {
	Events []struct {
		ID       int64          `json:"id"`
		Category string         `json:"category"`
		Status   string         `json:"status"`
		Source   string         `json:"source"`
		Summary  string         `json:"summary"`
		Detail   map[string]any `json:"detail"`
	} `json:"events"`
	Total int `json:"total"`
}

// newAuditServer serves approval_audit for "test-agent" over a real audit store.
func newAuditServer(t *testing.T) (*mcp.ClientSession, *audit.SQLiteStore) {
	t.Helper()
	store, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatalf("audit store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session, _ := newTestServer(t, func(d *configmcp.Deps) { d.AuditStore = store })
	return session, store
}

func insertAudit(t *testing.T, store *audit.SQLiteStore, ev audit.Event) {
	t.Helper()
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if err := store.Insert(context.Background(), ev); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}
}

func callApprovalAudit(t *testing.T, session *mcp.ClientSession, args map[string]any) approvalAuditResponse {
	t.Helper()
	text, isErr := callTool(t, session, "approval_audit", args)
	if isErr {
		t.Fatalf("approval_audit error: %s", text)
	}
	var resp approvalAuditResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode approval_audit: %v\n%s", err, text)
	}
	return resp
}

func TestApprovalAudit_NotRegisteredWithoutStore(t *testing.T) {
	session, _ := newTestServer(t, nil)
	assertToolRegistered(t, session, "approval_audit", false)
}

// The view never returns another agent's events, nor categories outside
// supervisor/approval, whatever the caller asks for.
func TestApprovalAudit_OnlyOwnAgentAndCategories(t *testing.T) {
	session, store := newAuditServer(t)
	insertAudit(t, store, audit.Event{Category: audit.CategorySupervisor, Action: "review", Agent: "test-agent", Status: audit.StatusError, Source: "supervisor:argus", Summary: "ERROR run_javascript: mine"})
	insertAudit(t, store, audit.Event{Category: audit.CategoryApproval, Action: "deny", Agent: "test-agent", Status: audit.StatusDenied, Source: "operator", Summary: "Approval x denied"})
	insertAudit(t, store, audit.Event{Category: audit.CategorySupervisor, Action: "review", Agent: "other-agent", Status: audit.StatusError, Source: "supervisor:argus", Summary: "ERROR run_javascript: theirs"})
	insertAudit(t, store, audit.Event{Category: audit.CategorySupervisor, Action: "review", Agent: "test-agent#dryrun", Status: audit.StatusError, Source: "dryrun", Summary: "ERROR run_javascript: dry run"})
	insertAudit(t, store, audit.Event{Category: audit.CategoryToolCall, Action: "execute", Agent: "test-agent", Status: audit.StatusOK, Source: "engine", Summary: "run_javascript"})

	resp := callApprovalAudit(t, session, map[string]any{"search": "run_javascript"})
	if resp.Total != 1 || len(resp.Events) != 1 || resp.Events[0].Summary != "ERROR run_javascript: mine" {
		t.Errorf("events = %+v (total %d), want only this agent's supervisor event", resp.Events, resp.Total)
	}

	resp = callApprovalAudit(t, session, map[string]any{})
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2 (own supervisor + approval events only)", resp.Total)
	}
	for _, ev := range resp.Events {
		if strings.Contains(ev.Summary, "theirs") || ev.Category == audit.CategoryToolCall {
			t.Errorf("leaked event %+v", ev)
		}
	}
}

func TestApprovalAudit_RejectsOtherCategory(t *testing.T) {
	session, _ := newAuditServer(t)
	if text, isErr := callTool(t, session, "approval_audit", map[string]any{"category": "llm"}); !isErr {
		t.Errorf("category=llm accepted: %s", text)
	}
}

// The cause is what the agent needs; the bulky arguments are dropped so a
// large payload cannot push it out of view.
func TestApprovalAudit_KeepsCauseDropsArguments(t *testing.T) {
	session, store := newAuditServer(t)
	detail, _ := json.Marshal(map[string]any{
		"tool": "run_javascript", "decision": "error", "cause": "cost_limit",
		"reason": "hard limit exceeded", "arguments": strings.Repeat("x", 50000),
	})
	insertAudit(t, store, audit.Event{Category: audit.CategorySupervisor, Action: "review", Agent: "test-agent", Status: audit.StatusError, Source: "supervisor:argus", Summary: "ERROR run_javascript", Detail: string(detail)})

	text, _ := callTool(t, session, "approval_audit", map[string]any{"status": "error"})
	if len(text) > 4000 {
		t.Errorf("response is %d bytes, want the arguments dropped", len(text))
	}
	var resp approvalAuditResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].Detail["cause"] != "cost_limit" || resp.Events[0].Detail["tool"] != "run_javascript" {
		t.Fatalf("events = %+v, want detail.cause and detail.tool", resp.Events)
	}
	if _, ok := resp.Events[0].Detail["arguments"]; ok {
		t.Error("detail.arguments returned")
	}
}

// A shadow verdict never affects the call, so the agent must not see it.
func TestApprovalAudit_HidesShadowVerdict(t *testing.T) {
	session, store := newAuditServer(t)
	detail, _ := json.Marshal(map[string]any{
		"tool": "web_fetch", "stage": "decider", "mode": "shadow",
		"decision": "shadow", "would_decide": "DENY",
	})
	insertAudit(t, store, audit.Event{Category: audit.CategorySupervisor, Action: "review", Agent: "test-agent", Status: audit.StatusOK, Source: "decider:jev", Summary: "SHADOW web_fetch", Detail: string(detail)})

	resp := callApprovalAudit(t, session, nil)
	if len(resp.Events) != 1 || resp.Events[0].Detail["stage"] != "decider" || resp.Events[0].Detail["mode"] != "shadow" {
		t.Fatalf("events = %+v, want detail.stage and detail.mode", resp.Events)
	}
	if _, ok := resp.Events[0].Detail["would_decide"]; ok {
		t.Error("detail.would_decide returned")
	}
}

func TestApprovalAudit_LimitDefaultsAndCaps(t *testing.T) {
	session, store := newAuditServer(t)
	for i := range 120 {
		insertAudit(t, store, audit.Event{Category: audit.CategoryApproval, Action: "deny", Agent: "test-agent", Status: audit.StatusDenied, Source: "operator", Summary: fmt.Sprintf("Approval %d denied", i)})
	}
	if resp := callApprovalAudit(t, session, map[string]any{}); len(resp.Events) != 20 || resp.Total != 120 {
		t.Errorf("default: %d events (total %d), want 20 of 120", len(resp.Events), resp.Total)
	}
	if resp := callApprovalAudit(t, session, map[string]any{"limit": 500}); len(resp.Events) != 100 {
		t.Errorf("limit=500: %d events, want the cap of 100", len(resp.Events))
	}
}

func TestApprovalAudit_DaysWindow(t *testing.T) {
	session, store := newAuditServer(t)
	insertAudit(t, store, audit.Event{Category: audit.CategoryApproval, Action: "deny", Agent: "test-agent", Status: audit.StatusDenied, Summary: "old", Timestamp: time.Now().UTC().AddDate(0, 0, -10)})
	insertAudit(t, store, audit.Event{Category: audit.CategoryApproval, Action: "deny", Agent: "test-agent", Status: audit.StatusDenied, Summary: "new"})

	resp := callApprovalAudit(t, session, map[string]any{"days": 3})
	if resp.Total != 1 || resp.Events[0].Summary != "new" {
		t.Errorf("events = %+v, want only the recent one", resp.Events)
	}
}
