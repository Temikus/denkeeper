package approval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/audit"
)

const (
	tierArgs     = `{"name":"nightly","session_tier":"autonomous"}`
	harmlessArgs = `{"name":"nightly","schedule":"@daily"}`
)

// mustOK fails the test on a setup error.
func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// submitCall submits a tool_call request the way the engine does: the
// summary names the tool and the payload is the raw arguments.
func submitCall(t *testing.T, m *Manager, toolName, args string) *Request {
	t.Helper()
	req, err := m.Submit(context.Background(), "default", ActionKindToolCall,
		`Execute tool "`+toolName+`" with args: `+args, args, "123", "telegram", "conv1",
		func(_ context.Context, _ string) error { return nil })
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return req
}

func requireStatus(t *testing.T, m *Manager, id string, want Status) {
	t.Helper()
	got, err := m.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != want {
		t.Errorf("request %s status = %q, want %q", id, got.Status, want)
	}
}

func TestShouldAutoApprove_RulesSkipAHighRiskCall(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if err := m.AddSessionRule(ctx, "default", "schedule_update", "conv1", "test"); err != nil {
		t.Fatalf("AddSessionRule: %v", err)
	}
	if _, err := m.AddPermanentRule(ctx, "other", "schedule_update", "test"); err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}

	if ok, scope := m.ShouldAutoApprove(ctx, "default", "schedule_update", []byte(harmlessArgs), "conv1"); !ok || scope != ScopeSession {
		t.Errorf("session rule, harmless call: ok=%v scope=%q", ok, scope)
	}
	if ok, _ := m.ShouldAutoApprove(ctx, "default", "schedule_update", []byte(tierArgs), "conv1"); ok {
		t.Error("session rule approved a call that sets session_tier")
	}
	if ok, scope := m.ShouldAutoApprove(ctx, "other", "schedule_update", []byte(harmlessArgs), "conv9"); !ok || scope != ScopePermanent {
		t.Errorf("permanent rule, harmless call: ok=%v scope=%q", ok, scope)
	}
	if ok, _ := m.ShouldAutoApprove(ctx, "other", "schedule_update", []byte(tierArgs), "conv9"); ok {
		t.Error("permanent rule approved a call that sets session_tier")
	}
}

// The handler can read "soul" from a case-variant key the classifier would
// have read as "user"; the rule must not approve it.
func TestShouldAutoApprove_SessionRuleSkipsCaseVariantSoulCall(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	mustOK(t, m.AddSessionRule(ctx, "default", "persona_update", "conv1", "test"))

	if ok, _ := m.ShouldAutoApprove(ctx, "default", "persona_update", []byte(`{"section":"user","content":"x"}`), "conv1"); !ok {
		t.Error("session rule did not approve a plain user update")
	}
	if ok, _ := m.ShouldAutoApprove(ctx, "default", "persona_update", []byte(`{"section":"soul","Section":"user","content":"x"}`), "conv1"); ok {
		t.Error("session rule approved a case-variant soul rewrite")
	}
}

func TestShouldAutoApprove_ConfigRuleSkipsAHighRiskCall(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	m.SetConfigRules(ctx, map[string][]string{"default": {"set_fallback", "schedule_add"}})

	if ok, _ := m.ShouldAutoApprove(ctx, "default", "set_fallback", []byte(`{"rules":[]}`), "conv1"); ok {
		t.Error("config rule approved set_fallback")
	}
	if ok, _ := m.ShouldAutoApprove(ctx, "default", "schedule_add", []byte(tierArgs), "conv1"); ok {
		t.Error("config rule approved a tier-setting schedule_add")
	}
	if ok, scope := m.ShouldAutoApprove(ctx, "default", "schedule_add", []byte(harmlessArgs), "conv1"); !ok || scope != ScopeConfig {
		t.Errorf("config rule, harmless schedule_add: ok=%v scope=%q", ok, scope)
	}
}

func TestAddRule_AlwaysHighToolRefusedAndNothingWritten(t *testing.T) {
	m, emitter := newAuditedManager(t)
	ctx := context.Background()

	if err := m.AddSessionRule(ctx, "default", "set_fallback", "conv1", "test"); !errors.Is(err, ErrHighRisk) {
		t.Errorf("AddSessionRule err = %v, want ErrHighRisk", err)
	}
	if rule, err := m.AddPermanentRule(ctx, "default", "set_fallback", "test"); !errors.Is(err, ErrHighRisk) || rule != nil {
		t.Errorf("AddPermanentRule = %+v, %v; want nil, ErrHighRisk", rule, err)
	}
	rules, err := m.ListAutoApproveRules(ctx, "default")
	if err != nil {
		t.Fatalf("ListAutoApproveRules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("rules = %+v, want none", rules)
	}
	if n := countRefusedRules(t, emitter); n != 2 {
		t.Errorf("refused create_rule events = %d, want 2", n)
	}
}

func countRefusedRules(t *testing.T, emitter *recordingAuditEmitter) int {
	t.Helper()
	n := 0
	for _, ev := range emitter.events {
		if ev.Action != "create_rule" || ev.Status != audit.StatusDenied {
			continue
		}
		var detail map[string]string
		if err := json.Unmarshal([]byte(ev.Detail), &detail); err != nil {
			t.Fatalf("detail %q: %v", ev.Detail, err)
		}
		if detail["reason"] != "high_risk" {
			t.Errorf("refused create_rule reason = %q, want high_risk", detail["reason"])
		}
		n++
	}
	return n
}

func TestResolveByCallback_AlwaysOnHighRiskRequestRefusedAndStillPending(t *testing.T) {
	m, emitter := newAuditedManager(t)
	ctx := context.Background()
	req := submitCall(t, m, "schedule_update", tierArgs)

	if _, err := m.ResolveByCallback(ctx, req.CallbackData+":approve_always", "telegram"); !errors.Is(err, ErrHighRisk) {
		t.Fatalf("err = %v, want ErrHighRisk", err)
	}
	requireStatus(t, m, req.ID, StatusPending)
	if rules, _ := m.ListAutoApproveRules(ctx, "default"); len(rules) != 0 {
		t.Errorf("rules = %+v, want none", rules)
	}
	if n := countRefusedRules(t, emitter); n != 1 {
		t.Errorf("refused create_rule events = %d, want 1", n)
	}

	if _, err := m.ResolveByCallback(ctx, req.CallbackData+":approve", "telegram"); err != nil {
		t.Fatalf("plain approve: %v", err)
	}
	requireStatus(t, m, req.ID, StatusApproved)
}

func TestCheckStandingRule(t *testing.T) {
	m, emitter := newAuditedManager(t)
	ctx := context.Background()
	risky := submitCall(t, m, "set_fallback", `{"rules":[]}`)
	harmless := submitCall(t, m, "schedule_update", harmlessArgs)

	if err := m.CheckStandingRule(ctx, risky.ID, ScopePermanent, "api"); !errors.Is(err, ErrHighRisk) {
		t.Errorf("high-risk request: err = %v, want ErrHighRisk", err)
	}
	if err := m.CheckStandingRule(ctx, harmless.ID, ScopePermanent, "api"); err != nil {
		t.Errorf("harmless request: err = %v", err)
	}
	if err := m.CheckStandingRule(ctx, "missing", ScopeSession, "api"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing request: err = %v, want ErrNotFound", err)
	}
	requireStatus(t, m, risky.ID, StatusPending)
	if n := countRefusedRules(t, emitter); n != 1 {
		t.Errorf("refused create_rule events = %d, want 1", n)
	}
}

// A rule made from a harmless call must not approve a queued one that raises
// a schedule's tier.
func TestAddRule_AutoResolveSkipsHighRiskPending(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	risky := submitCall(t, m, "schedule_update", tierArgs)
	harmless := submitCall(t, m, "schedule_update", harmlessArgs)

	if _, err := m.AddPermanentRule(ctx, "default", "schedule_update", "test"); err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}
	requireStatus(t, m, risky.ID, StatusPending)
	requireStatus(t, m, harmless.ID, StatusApproved)
}

func TestGetAndList_CarryRisk(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	risky := submitCall(t, m, "schedule_update", tierArgs)
	harmless := submitCall(t, m, "schedule_update", harmlessArgs)
	if risky.Risk == nil || harmless.Risk != nil {
		t.Errorf("Submit risk = %+v / %+v", risky.Risk, harmless.Risk)
	}

	got, err := m.Get(ctx, risky.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Risk == nil || got.Risk.Flagged[0].Name != "session_tier" {
		t.Errorf("Get risk = %+v", got.Risk)
	}
	if got, _ := m.Get(ctx, harmless.ID); got.Risk != nil {
		t.Errorf("harmless Get risk = %+v", got.Risk)
	}

	list, err := m.List(ctx, StatusPending)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, r := range list {
		if (r.ID == risky.ID) != (r.Risk != nil) {
			t.Errorf("List %s risk = %+v", r.ID, r.Risk)
		}
	}
}

// A rule stored before this change stays, is listed as inert, and never
// applies.
func TestListAutoApproveRules_MarksStoredHighRiskRuleInert(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if _, err := m.autoStore.CreateAutoApproveRule(ctx, AutoApproveRule{
		ID: "old", AgentName: "default", ToolName: "set_fallback", Scope: ScopePermanent, CreatedBy: "test",
	}); err != nil {
		t.Fatalf("CreateAutoApproveRule: %v", err)
	}
	if _, err := m.AddPermanentRule(ctx, "default", "kv_get", "test"); err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}

	rules, err := m.ListAutoApproveRules(ctx, "default")
	if err != nil {
		t.Fatalf("ListAutoApproveRules: %v", err)
	}
	for _, r := range rules {
		if r.Inert != (r.ToolName == "set_fallback") {
			t.Errorf("rule %s inert = %v", r.ToolName, r.Inert)
		}
	}
	if ok, _ := m.ShouldAutoApprove(ctx, "default", "set_fallback", []byte(`{}`), "conv1"); ok {
		t.Error("stored set_fallback rule applied")
	}
}

func TestHandler_Resolve_HighRiskAutoPressSaysUseApproveOrDeny(t *testing.T) {
	h, mgr, _ := newTestHandler(t)
	req := submitCall(t, mgr, "set_fallback", `{"rules":[]}`)

	text, err := h.Resolve(context.Background(), req.CallbackData+":approve_session")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(text, "high-risk call and cannot be auto-approved") {
		t.Errorf("reply = %q", text)
	}
	requireStatus(t, mgr, req.ID, StatusPending)
}
