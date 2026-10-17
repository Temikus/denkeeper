package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scriptmcp"
)

func TestDecideSupervisorOutcome_PrivateDataInLocalToolApproves(t *testing.T) {
	a := allAnswers(0.97)
	a[deciderQSendsExternal] = llm.Answer{Noul: 0.02}
	a[deciderQPrivateData] = llm.Answer{Noul: 0.98}
	scores, _, _ := deciderChecks(a)
	if got := scores[deciderCheckNoLeak]; got < 0.98 {
		t.Errorf("no_leak = %v, want >= 0.98 for private data that stays local", got)
	}
	if got, reason := decideSupervisorOutcome(a, 0.95, 0.05); got != supervisorApprove {
		t.Errorf("= %s %q, want APPROVE", got, reason)
	}
}

func TestDecideSupervisorOutcome_PrivateDataSentOutDenies(t *testing.T) {
	a := allAnswers(0.97)
	a[deciderQSendsExternal] = llm.Answer{Noul: 0.98}
	a[deciderQPrivateData] = llm.Answer{Noul: 0.98}
	got, reason := decideSupervisorOutcome(a, 0.95, 0.05)
	if got != supervisorDeny || !strings.Contains(reason, "private data out") {
		t.Errorf("= %s %q, want DENY naming the leak", got, reason)
	}
}

func TestDecideSupervisorOutcome_SecretInArgumentsDenies(t *testing.T) {
	a := allAnswers(0.97)
	a[deciderQSecrets] = llm.Answer{Noul: 0.99}
	got, reason := decideSupervisorOutcome(a, 0.95, 0.05)
	if got != supervisorDeny || reason != "decider: arguments contain a credential (p=0.01)" {
		t.Errorf("= %s %q, want DENY naming the credential", got, reason)
	}
}

func TestDecideSupervisorOutcome_MissingHalfOfLeakEscalates(t *testing.T) {
	a := allAnswers(0.99)
	delete(a, deciderQPrivateData)
	got, reason := decideSupervisorOutcome(a, 0.95, 0.05)
	if got != supervisorEscalate || reason != "decider: no answer for private_data" {
		t.Errorf("= %s %q, want ESCALATE naming private_data", got, reason)
	}
}

func TestPairShadowReviews_MissingLeakHalfLeavesNoLeakUnscored(t *testing.T) {
	answers := allScores(0.99)
	delete(answers, deciderQSendsExternal)
	events := []audit.Event{
		shadowEvent(t, 0, "c1", "echo", `{}`, answers),
		supervisorEvent(t, time.Second, "c1", "echo", `{}`, "APPROVE", nil),
	}
	reviews, _ := PairShadowReviews(events, "jev")
	if len(reviews) != 1 {
		t.Fatalf("got %d reviews, want 1", len(reviews))
	}
	r := reviews[0]
	if r.MinScore != nil || r.Lowest != "" {
		t.Errorf("min = %v on %q, want none", r.MinScore, r.Lowest)
	}
	if _, ok := r.Scores[deciderCheckNoLeak]; ok {
		t.Errorf("no_leak scored without sends_external: %v", r.Scores)
	}
	if r.Scores[deciderCheckNoInjection] != 0.99 {
		t.Errorf("no_injection = %v, want 0.99", r.Scores[deciderCheckNoInjection])
	}
}

// The no-network sentence sits past character 300 of run_javascript's
// description, so the default cap must keep it for the sends_external question.
func TestGatherSupervisorInput_DefaultCapKeepsRunJavascriptDescription(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, nil, nil)
	defer h.teardown()

	srv := scriptmcp.New(scriptmcp.Deps{Enabled: true, Timeout: time.Second, PermissionTier: func() string { return "autonomous" }})
	session, err := srv.Connect(context.Background())
	if err != nil {
		t.Fatalf("connecting script MCP: %v", err)
	}
	if err := h.engine.tools.RegisterSession(context.Background(), "script-default", session); err != nil {
		t.Fatalf("registering script MCP: %v", err)
	}

	tc := llm.ToolCall{ID: "1", Type: "function", Function: llm.FunctionCall{Name: "run_javascript", Arguments: `{"code":"return 1"}`}}
	desc := h.engine.gatherSupervisorInput(context.Background(), tc, "c1").deciderState().Tool.Description
	if !strings.Contains(desc, "No network or filesystem access") {
		t.Errorf("decider sees description %q, want the no-network sentence", desc)
	}
}

// Reviews scored with the old bundled safety question are left out, so the
// calibration chart only shows scores from the split checks.
func TestPairShadowReviews_SkipsPreSplitReviews(t *testing.T) {
	old := shadowEvent(t, 0, "c1", "echo", `{}`, map[string]float64{
		deciderQAligned: 0.99, deciderQLegacySafeArgs: 0.4, deciderQScoped: 0.97,
	})
	current := shadowEvent(t, 2*time.Second, "c1", "echo", `{}`, allScores(0.9))
	old.ID, current.ID = 1, 2
	events := []audit.Event{
		old,
		supervisorEvent(t, time.Second, "c1", "echo", `{}`, "APPROVE", nil),
		current,
		supervisorEvent(t, 3*time.Second, "c1", "echo", `{}`, "APPROVE", nil),
	}
	reviews, failed := PairShadowReviews(events, "jev")
	if len(reviews) != 1 || failed != 0 {
		t.Fatalf("got %d reviews, %d failed; want 1, 0", len(reviews), failed)
	}
	if r := reviews[0]; r.AuditID != current.ID || r.MinScore == nil || *r.MinScore != 0.9 || r.Supervisor != "APPROVE" {
		t.Errorf("review = %+v, want the split-check review at 0.9, paired", r)
	}
}
