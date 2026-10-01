package approval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Temikus/denkeeper/internal/audit"
)

const auditTestToolSummary = `Execute tool "web_fetch" with args: {}`

func newAuditedManager(t *testing.T) (*Manager, *recordingAuditEmitter) {
	t.Helper()
	m := newTestManager(t)
	emitter := &recordingAuditEmitter{}
	m.Auditor = emitter
	return m, emitter
}

func submitWithAction(t *testing.T, m *Manager, kind ActionKind, summary string, action ActionFunc) *Request {
	t.Helper()
	req, err := m.Submit(context.Background(), "default", kind,
		summary, "payload", "123", "telegram", "conv1", action)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return req
}

func noopAction(_ context.Context, _ string) error { return nil }

func failingAction(_ context.Context, _ string) error { return errors.New("disk full") }

func eventsByAction(events []audit.Event, action string) []audit.Event {
	var out []audit.Event
	for _, e := range events {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

func TestManager_Resolve_ActionFails_AuditsError(t *testing.T) {
	m, emitter := newAuditedManager(t)
	req := submitWithAction(t, m, ActionKindUserUpdate, "summary", failingAction)

	_, err := m.Resolve(context.Background(), req.ID, true, "operator")
	if !errors.Is(err, ErrActionFailed) {
		t.Fatalf("Resolve error = %v, want ErrActionFailed", err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(emitter.events))
	}
	event := emitter.events[0]
	if event.Action != "approve" {
		t.Errorf("audit action = %q, want %q", event.Action, "approve")
	}
	if event.Status != audit.StatusError {
		t.Errorf("audit status = %q, want %q", event.Status, audit.StatusError)
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
		t.Fatalf("detail %q: %v", event.Detail, err)
	}
	if detail["error"] != "disk full" {
		t.Errorf("detail.error = %q, want %q", detail["error"], "disk full")
	}
}

func TestManager_Resolve_AuditCarriesAgentAndConversation(t *testing.T) {
	m, emitter := newAuditedManager(t)
	req := submitWithAction(t, m, ActionKindUserUpdate, "summary", noopAction)

	if _, err := m.Resolve(context.Background(), req.ID, false, "operator"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(emitter.events))
	}
	if got := emitter.events[0]; got.Agent != "default" || got.ConversationID != "conv1" {
		t.Errorf("agent/conversation = %q/%q, want default/conv1", got.Agent, got.ConversationID)
	}
}

func TestManager_ResolveByCallback_Approve_Audits(t *testing.T) {
	m, emitter := newAuditedManager(t)
	req := submitWithAction(t, m, ActionKindUserUpdate, "summary", noopAction)

	if _, err := m.ResolveByCallback(context.Background(), req.CallbackData+":approve", "telegram"); err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(emitter.events))
	}
	event := emitter.events[0]
	if event.Category != audit.CategoryApproval || event.Action != "approve" || event.Status != audit.StatusOK {
		t.Errorf("event = %s/%s/%s, want approval/approve/ok", event.Category, event.Action, event.Status)
	}
	wantSummary := "Approval " + req.ID + " approved (by telegram)"
	if event.Summary != wantSummary {
		t.Errorf("audit summary = %q, want %q", event.Summary, wantSummary)
	}
}

func TestManager_ResolveByCallback_Deny_Audits(t *testing.T) {
	m, emitter := newAuditedManager(t)
	req := submitWithAction(t, m, ActionKindUserUpdate, "summary", noopAction)

	if _, err := m.ResolveByCallback(context.Background(), req.CallbackData+":deny", "telegram"); err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(emitter.events))
	}
	if event := emitter.events[0]; event.Action != "deny" || event.Status != audit.StatusDenied {
		t.Errorf("event = %s/%s, want deny/denied", event.Action, event.Status)
	}
}

func TestManager_ResolveByCallback_ActionFails_AuditsError(t *testing.T) {
	m, emitter := newAuditedManager(t)
	req := submitWithAction(t, m, ActionKindUserUpdate, "summary", failingAction)

	_, err := m.ResolveByCallback(context.Background(), req.CallbackData+":approve", "telegram")
	if !errors.Is(err, ErrActionFailed) {
		t.Fatalf("ResolveByCallback error = %v, want ErrActionFailed", err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(emitter.events))
	}
	if event := emitter.events[0]; event.Action != "approve" || event.Status != audit.StatusError {
		t.Errorf("event = %s/%s, want approve/error", event.Action, event.Status)
	}
}

func TestManager_ResolveByCallback_ApproveAlways_AuditsRuleCreation(t *testing.T) {
	m, emitter := newAuditedManager(t)
	req := submitWithAction(t, m, ActionKindToolCall, auditTestToolSummary, noopAction)

	if _, err := m.ResolveByCallback(context.Background(), req.CallbackData+":approve_always", "telegram"); err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}
	if got := len(eventsByAction(emitter.events, "approve")); got != 1 {
		t.Errorf("got %d approve events, want 1", got)
	}
	rules := eventsByAction(emitter.events, "create_rule")
	if len(rules) != 1 {
		t.Fatalf("got %d create_rule events, want 1 (events: %+v)", len(rules), emitter.events)
	}
	event := rules[0]
	if event.Agent != "default" || event.Summary != "web_fetch" || event.Source != "telegram" {
		t.Errorf("event agent/summary/source = %q/%q/%q, want default/web_fetch/telegram",
			event.Agent, event.Summary, event.Source)
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
		t.Fatalf("detail %q: %v", event.Detail, err)
	}
	if detail["scope"] != string(ScopePermanent) || detail["tool"] != "web_fetch" {
		t.Errorf("detail = %v, want scope=permanent tool=web_fetch", detail)
	}
}

func TestManager_ResolveByCallback_ApproveSession_AuditsRuleCreation(t *testing.T) {
	m, emitter := newAuditedManager(t)
	req := submitWithAction(t, m, ActionKindToolCall, auditTestToolSummary, noopAction)

	if _, err := m.ResolveByCallback(context.Background(), req.CallbackData+":approve_session", "telegram"); err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}
	rules := eventsByAction(emitter.events, "create_rule")
	if len(rules) != 1 {
		t.Fatalf("got %d create_rule events, want 1", len(rules))
	}
	if rules[0].ConversationID != "conv1" {
		t.Errorf("conversation = %q, want conv1", rules[0].ConversationID)
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(rules[0].Detail), &detail); err != nil {
		t.Fatalf("detail %q: %v", rules[0].Detail, err)
	}
	if detail["scope"] != string(ScopeSession) {
		t.Errorf("detail.scope = %q, want session", detail["scope"])
	}
}

// Rules created outside the callback path (REST, WebSocket) go through the
// same Manager methods, so they are audited too.
func TestManager_AddPermanentRule_Audits(t *testing.T) {
	m, emitter := newAuditedManager(t)

	if _, err := m.AddPermanentRule(context.Background(), "default", "web_fetch", "api"); err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}
	rules := eventsByAction(emitter.events, "create_rule")
	if len(rules) != 1 {
		t.Fatalf("got %d create_rule events, want 1", len(rules))
	}
	if rules[0].Source != "api" || rules[0].Status != audit.StatusOK {
		t.Errorf("source/status = %q/%q, want api/ok", rules[0].Source, rules[0].Status)
	}
}
