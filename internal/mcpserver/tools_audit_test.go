package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
)

func auditReadCtx() context.Context {
	return withScopes(context.Background(), []string{"audit:read"})
}

// auditServer wires a Server backed by an in-memory audit store seeded with
// the given events.
func auditServer(t *testing.T, events ...audit.Event) *Server {
	t.Helper()
	store, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating audit store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, e := range events {
		if err := store.Insert(context.Background(), e); err != nil {
			t.Fatalf("seeding audit event: %v", err)
		}
	}
	return &Server{deps: Deps{AuditStore: store, Logger: testLogger()}}
}

func TestAuditEvents_ListsAndFilters(t *testing.T) {
	s := auditServer(t,
		audit.Event{Timestamp: time.Now().UTC(), Category: audit.CategoryToolCall, Agent: "a", Status: audit.StatusOK, Summary: "ran tool"},
		audit.Event{Timestamp: time.Now().UTC(), Category: audit.CategorySkill, Agent: "b", Status: audit.StatusError, Summary: "skill failed"},
	)

	// No filter: both events.
	res, _, err := s.handleAuditEvents(auditReadCtx(), nil, auditEventsInput{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}
	var all audit.ListResult
	if err := json.Unmarshal([]byte(toolResultText(res)), &all); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if all.Total != 2 || len(all.Events) != 2 {
		t.Fatalf("expected 2 events, got total=%d len=%d", all.Total, len(all.Events))
	}

	// Category filter narrows to one.
	res, _, _ = s.handleAuditEvents(auditReadCtx(), nil, auditEventsInput{Category: audit.CategorySkill})
	var filtered audit.ListResult
	if err := json.Unmarshal([]byte(toolResultText(res)), &filtered); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if filtered.Total != 1 || len(filtered.Events) != 1 || filtered.Events[0].Category != audit.CategorySkill {
		t.Fatalf("category filter failed: %+v", filtered)
	}
}

func TestAuditEvents_RequiresScope(t *testing.T) {
	s := auditServer(t)
	res, _, _ := s.handleAuditEvents(context.Background(), nil, auditEventsInput{})
	if !res.IsError {
		t.Error("expected scope error without audit:read")
	}
}

func TestAuditEvents_NilStore(t *testing.T) {
	s := &Server{deps: Deps{Logger: testLogger()}}
	res, _, _ := s.handleAuditEvents(auditReadCtx(), nil, auditEventsInput{})
	if !res.IsError {
		t.Error("expected error when audit store is not configured")
	}
}

func TestAuditEvents_InvalidSince(t *testing.T) {
	s := auditServer(t)
	res, _, _ := s.handleAuditEvents(auditReadCtx(), nil, auditEventsInput{Since: "not-a-time"})
	if !res.IsError {
		t.Error("expected error for invalid since")
	}
}

func TestAuditEvents_InvalidUntil(t *testing.T) {
	s := auditServer(t)
	res, _, _ := s.handleAuditEvents(auditReadCtx(), nil, auditEventsInput{Until: "not-a-time"})
	if !res.IsError {
		t.Error("expected error for invalid until")
	}
}

func TestAuditSummary_Aggregates(t *testing.T) {
	s := auditServer(t,
		audit.Event{Timestamp: time.Now().UTC(), Category: audit.CategoryToolCall, Status: audit.StatusOK, Summary: "x"},
		audit.Event{Timestamp: time.Now().UTC(), Category: audit.CategoryToolCall, Status: audit.StatusError, Summary: "y"},
		audit.Event{Timestamp: time.Now().UTC(), Category: audit.CategorySkill, Status: audit.StatusOK, Summary: "z"},
	)

	res, _, err := s.handleAuditSummary(auditReadCtx(), nil, auditSummaryInput{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}
	var stats audit.Stats
	if err := json.Unmarshal([]byte(toolResultText(res)), &stats); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("expected total 3, got %d", stats.Total)
	}
	if stats.ByCategory[audit.CategoryToolCall] != 2 {
		t.Errorf("expected 2 tool_call events, got %d", stats.ByCategory[audit.CategoryToolCall])
	}
	if stats.ByStatus[audit.StatusOK] != 2 {
		t.Errorf("expected 2 ok events, got %d", stats.ByStatus[audit.StatusOK])
	}
}

func TestAuditSummary_RequiresScope(t *testing.T) {
	s := auditServer(t)
	res, _, _ := s.handleAuditSummary(context.Background(), nil, auditSummaryInput{})
	if !res.IsError {
		t.Error("expected scope error without audit:read")
	}
}

func TestAuditSummary_NilStore(t *testing.T) {
	s := &Server{deps: Deps{Logger: testLogger()}}
	res, _, _ := s.handleAuditSummary(auditReadCtx(), nil, auditSummaryInput{})
	if !res.IsError {
		t.Error("expected error when audit store is not configured")
	}
}

func TestAuditSummary_InvalidSince(t *testing.T) {
	s := auditServer(t)
	res, _, _ := s.handleAuditSummary(auditReadCtx(), nil, auditSummaryInput{Since: "not-a-time"})
	if !res.IsError {
		t.Error("expected error for invalid since")
	}
}
