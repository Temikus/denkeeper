package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// toolDecisionProvider answers every question with the probability configured
// for the call's tool, and fails for tools in fail.
type toolDecisionProvider struct {
	mu           sync.Mutex
	p            map[string]float64
	fail         map[string]bool
	calls        int
	userRequests map[string]string // tool → user_request seen
}

func (f *toolDecisionProvider) Decide(_ context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	b, err := json.Marshal(req.State)
	if err != nil {
		return nil, err
	}
	var state struct {
		Tool struct {
			Name string `json:"name"`
		} `json:"tool"`
		UserRequest string `json:"user_request"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.userRequests == nil {
		f.userRequests = map[string]string{}
	}
	f.userRequests[state.Tool.Name] = state.UserRequest
	if f.fail[state.Tool.Name] {
		return nil, errors.New("upstream 502")
	}
	p, ok := f.p[state.Tool.Name]
	if !ok {
		p = 0.99
	}
	answers := make(map[string]llm.Answer, len(req.Questions))
	for id := range req.Questions {
		answers[id] = llm.Answer{Type: llm.QuestionNoul, Noul: p}
	}
	return &llm.DecisionResponse{Model: req.Model, Answers: answers, CostUSD: 0.0001}, nil
}

type replayFixture struct {
	audit  *audit.SQLiteStore
	memory *agent.SQLiteMemoryStore
	prov   *toolDecisionProvider
	now    time.Time
}

func newReplayFixture(t *testing.T) *replayFixture {
	t.Helper()
	as, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatalf("audit store: %v", err)
	}
	ms, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("memory store: %v", err)
	}
	t.Cleanup(func() { _ = as.Close(); _ = ms.Close() })
	return &replayFixture{audit: as, memory: ms, prov: &toolDecisionProvider{}, now: time.Now()}
}

// afterMessages is a review age that lands after any message the test stored,
// clear of created_at's one-second resolution.
const afterMessages = -30 * time.Second

// review inserts a supervisor verdict for agent "default", age before now.
func (fx *replayFixture) review(t *testing.T, tool, decision, convID string, age time.Duration) {
	t.Helper()
	detail, _ := json.Marshal(map[string]any{
		"tool": tool, "arguments": `{"q":"` + tool + `"}`, "decision": decision, "supervisor": "guard",
	})
	fx.insert(t, audit.Event{
		Timestamp: fx.now.Add(-age), Category: audit.CategorySupervisor, Action: "review", Agent: "default",
		Summary: decision + " " + tool, Detail: string(detail), Source: "supervisor:guard", ConversationID: convID,
	})
}

func (fx *replayFixture) insert(t *testing.T, ev audit.Event) {
	t.Helper()
	if err := fx.audit.Insert(context.Background(), ev); err != nil {
		t.Fatalf("inserting audit event: %v", err)
	}
}

func (fx *replayFixture) opts() replayOpts {
	return replayOpts{
		Agent: "default", Since: fx.now.Add(-24 * time.Hour), Until: fx.now.Add(time.Minute),
		ApproveAt: 0.95, DenyAt: 0.05, Limit: 500, Show: 20, Concurrency: 4, ContextMessages: 5, Format: "text",
	}
}

func (fx *replayFixture) run(t *testing.T, opts replayOpts) *replayReport {
	t.Helper()
	d := llm.NewDecider(llm.DeciderConfig{Name: "jev", Model: "typesafe/jev-1.13"}, fx.prov, nil)
	report, err := replayDecisions(context.Background(), io.Discard, fx.audit, fx.memory, d, opts)
	if err != nil {
		t.Fatalf("replayDecisions: %v", err)
	}
	return report
}

// mixedReviews seeds eight verdicts covering every matrix cell the tests read,
// one failing call, and four events a replay must pass over.
func mixedReviews(t *testing.T, fx *replayFixture) {
	t.Helper()
	ctx := context.Background()
	convID, err := fx.memory.GetOrCreateConversation(ctx, "telegram", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.memory.AddMessage(ctx, convID, agent.StoredMessage{Role: "user", Content: "look this up"}); err != nil {
		t.Fatal(err)
	}

	fx.prov.p = map[string]float64{"safe": 0.99, "risky": 0.99, "odd": 0.97, "blocked": 0.02, "unsure": 0.5, "bad": 0.02}
	fx.prov.fail = map[string]bool{"boom": true}

	fx.review(t, "safe", "APPROVE", convID, afterMessages)
	fx.review(t, "safe", "APPROVE", "", time.Minute)
	fx.review(t, "blocked", "APPROVE", "", 2*time.Minute)
	fx.review(t, "odd", "ESCALATE", "", 3*time.Minute)
	fx.review(t, "risky", "DENY", "", 4*time.Minute)
	fx.review(t, "unsure", "APPROVE", "", 5*time.Minute)
	fx.review(t, "bad", "DENY", "", 6*time.Minute)
	fx.review(t, "boom", "APPROVE", "", 7*time.Minute)

	// Passed over: a failed review, a decider shadow event, another agent's
	// review, and a review outside the window.
	fx.review(t, "errored", "error", "", 8*time.Minute)
	fx.insert(t, audit.Event{
		Timestamp: fx.now.Add(-9 * time.Minute), Category: audit.CategorySupervisor, Action: "review", Agent: "default",
		Summary: "SHADOW", Detail: `{"tool":"safe","arguments":"{}","decision":"shadow"}`, Source: "decider:jev",
	})
	fx.insert(t, audit.Event{
		Timestamp: fx.now.Add(-10 * time.Minute), Category: audit.CategorySupervisor, Action: "review", Agent: "other",
		Summary: "APPROVE", Detail: `{"tool":"safe","arguments":"{}","decision":"APPROVE"}`, Source: "supervisor:guard",
	})
	fx.review(t, "ancient", "APPROVE", "", 48*time.Hour)
	// Counted as unreadable.
	fx.insert(t, audit.Event{
		Timestamp: fx.now.Add(-11 * time.Minute), Category: audit.CategorySupervisor, Action: "review", Agent: "default",
		Summary: "?", Detail: "not json", Source: "supervisor:guard",
	})
}

func TestReplayDecisions_AgreementMatrix(t *testing.T) {
	fx := newReplayFixture(t)
	mixedReviews(t, fx)
	r := fx.run(t, fx.opts())

	if r.Reviews != 8 || r.Replayed != 7 {
		t.Errorf("reviews/replayed = %d/%d, want 8/7", r.Reviews, r.Replayed)
	}
	if fx.prov.calls != 8 {
		t.Errorf("decider calls = %d, want 8 (ignored events must not be sent)", fx.prov.calls)
	}
	want := map[string]map[string]int{
		"APPROVE":  {"APPROVE": 2, "DENY": 1, "ESCALATE": 1},
		"ESCALATE": {"APPROVE": 1},
		"DENY":     {"APPROVE": 1, "DENY": 1},
	}
	for dv, row := range want {
		for sv, n := range row {
			if got := r.Matrix[dv][sv]; got != n {
				t.Errorf("matrix[decider %s][supervisor %s] = %d, want %d", dv, sv, got, n)
			}
		}
	}
	// A failed call is skipped, never a verdict.
	if r.Skipped["provider_error"] != 1 || r.Skipped["unreadable"] != 1 {
		t.Errorf("skipped = %v, want provider_error 1, unreadable 1", r.Skipped)
	}
	if math.Abs(r.CostUSD-0.0007) > 1e-9 {
		t.Errorf("cost = %v, want 0.0007", r.CostUSD)
	}
	if r.Decider != "jev" || r.Model != "typesafe/jev-1.13" {
		t.Errorf("decider/model = %q/%q", r.Decider, r.Model)
	}
}

func TestReplayDecisions_RebuildsUserRequestFromHistory(t *testing.T) {
	fx := newReplayFixture(t)
	mixedReviews(t, fx)
	r := fx.run(t, fx.opts())

	// Only the newest "safe" review has a conversation; the other "safe" call
	// may overwrite the recorded request, so check through the count.
	if r.WithoutUserRequest != 6 {
		t.Errorf("without user request = %d, want 6 of 7 replayed", r.WithoutUserRequest)
	}

	solo := newReplayFixture(t)
	ctx := context.Background()
	convID, _ := solo.memory.GetOrCreateConversation(ctx, "telegram", "1")
	if _, err := solo.memory.AddMessage(ctx, convID, agent.StoredMessage{Role: "user", Content: "look this up"}); err != nil {
		t.Fatal(err)
	}
	solo.review(t, "safe", "APPROVE", convID, afterMessages)
	solo.run(t, solo.opts())
	if got := solo.prov.userRequests["safe"]; got != "look this up" {
		t.Errorf("user_request sent to decider = %q, want the stored user message", got)
	}
}

func TestReplayDecisions_ThresholdSweep(t *testing.T) {
	fx := newReplayFixture(t)
	mixedReviews(t, fx)
	r := fx.run(t, fx.opts())

	approve := map[float64]replaySweepRow{}
	for _, row := range r.ApproveSweep {
		approve[row.At] = row
	}
	// p=0.99: safe x2 and risky (supervisor denied).
	if got := approve[0.99]; got.Decided != 3 || got.Disagreed != 1 {
		t.Errorf("approve_at 0.99 = %+v, want decided 3, disagreed 1", got)
	}
	// 0.95 also admits odd (p=0.97, supervisor escalated).
	if got := approve[0.95]; got.Decided != 4 || got.Disagreed != 2 {
		t.Errorf("approve_at 0.95 = %+v, want decided 4, disagreed 2", got)
	}

	deny := map[float64]replaySweepRow{}
	for _, row := range r.DenySweep {
		deny[row.At] = row
	}
	if got := deny[0.01]; got.Decided != 0 {
		t.Errorf("deny_at 0.01 = %+v, want decided 0", got)
	}
	// p=0.02: blocked (supervisor approved) and bad.
	if got := deny[0.05]; got.Decided != 2 || got.Disagreed != 1 {
		t.Errorf("deny_at 0.05 = %+v, want decided 2, disagreed 1", got)
	}
}

func TestReplayDecisions_SweepIncludesChosenThresholds(t *testing.T) {
	fx := newReplayFixture(t)
	fx.review(t, "safe", "APPROVE", "", time.Minute)
	opts := fx.opts()
	opts.ApproveAt, opts.DenyAt = 0.93, 0.07
	r := fx.run(t, opts)

	hasAt := func(rows []replaySweepRow, at float64) bool {
		for _, row := range rows {
			if row.At == at {
				return true
			}
		}
		return false
	}
	if !hasAt(r.ApproveSweep, 0.93) || !hasAt(r.DenySweep, 0.07) {
		t.Errorf("sweeps %v / %v must include the chosen 0.93 / 0.07", r.ApproveSweep, r.DenySweep)
	}
}

func TestReplayDecisions_DisagreementsUnsafeFirst(t *testing.T) {
	fx := newReplayFixture(t)
	mixedReviews(t, fx)
	opts := fx.opts()
	opts.Show = 2
	r := fx.run(t, opts)

	if r.DisagreementCount != 3 || len(r.Disagreements) != 2 {
		t.Fatalf("disagreements = %d shown of %d, want 2 of 3", len(r.Disagreements), r.DisagreementCount)
	}
	// "blocked" is newer than both, but a decider approval the supervisor
	// refused outranks a decider denial.
	if r.Disagreements[0].Tool != "risky" || r.Disagreements[1].Tool != "odd" {
		t.Errorf("order = %s, %s; want risky (approve vs DENY), odd (approve vs ESCALATE)",
			r.Disagreements[0].Tool, r.Disagreements[1].Tool)
	}
	if d := r.Disagreements[0]; d.Decider != "APPROVE" || d.Supervisor != "DENY" || !strings.Contains(d.Arguments, "risky") {
		t.Errorf("disagreement = %+v", d)
	}
}

func TestReplayDecisions_PagesPastOneAuditPage(t *testing.T) {
	fx := newReplayFixture(t)
	events := make([]audit.Event, 450)
	for i := range events {
		events[i] = audit.Event{
			Timestamp: fx.now.Add(-time.Duration(i+1) * time.Second), Category: audit.CategorySupervisor, Action: "review",
			Agent: "default", Summary: "APPROVE", Source: "supervisor:guard",
			Detail: fmt.Sprintf(`{"tool":"t%d","arguments":"{}","decision":"APPROVE"}`, i),
		}
	}
	if err := fx.audit.InsertBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}

	opts := fx.opts()
	opts.Limit = 420
	r := fx.run(t, opts)
	if r.Reviews != 420 || fx.prov.calls != 420 {
		t.Errorf("reviews/calls = %d/%d, want 420/420 (limit applies across pages)", r.Reviews, fx.prov.calls)
	}

	all := newReplayFixture(t)
	if err := all.audit.InsertBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if r := all.run(t, all.opts()); r.Reviews != 450 {
		t.Errorf("reviews = %d, want all 450", r.Reviews)
	}
}

func TestReplayReport_TextOutput(t *testing.T) {
	fx := newReplayFixture(t)
	mixedReviews(t, fx)
	var buf bytes.Buffer
	if err := fx.run(t, fx.opts()).writeText(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`Replayed 7 of 8 supervisor reviews for agent "default" through decider "jev"`,
		"Skipped: provider_error 1, unreadable 1",
		"Agreement at approve_at=0.95 deny_at=0.05",
		"Threshold sweep",
		"Disagreements (3 of 3 shown)",
		"Indicative only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReplayReport_TextOutputNoReviews(t *testing.T) {
	fx := newReplayFixture(t)
	var buf bytes.Buffer
	if err := fx.run(t, fx.opts()).writeText(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No supervisor reviews found") {
		t.Errorf("output = %q", buf.String())
	}
	if fx.prov.calls != 0 {
		t.Errorf("decider calls = %d, want 0", fx.prov.calls)
	}
}

func TestReplayReport_JSONOutput(t *testing.T) {
	fx := newReplayFixture(t)
	mixedReviews(t, fx)
	b, err := json.Marshal(fx.run(t, fx.opts()))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Replayed int                       `json:"replayed"`
		Matrix   map[string]map[string]int `json:"matrix"`
		Sweep    []replaySweepRow          `json:"approve_sweep"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Replayed != 7 || got.Matrix["APPROVE"]["DENY"] != 1 || len(got.Sweep) == 0 {
		t.Errorf("json = %s", b)
	}
}

func replayTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Agents = []config.AgentInstanceConfig{
		{Name: "default", SupervisorDecider: "jev", SupervisorDeciderApproveAt: 0.9, SupervisorDeciderDenyAt: 0.1, SupervisorContextMessages: 8},
		{Name: "plain"},
	}
	cfg.LLM.Deciders = []config.DeciderConfig{{Name: "jev", Provider: "or", Model: "m"}, {Name: "alt", Provider: "or", Model: "m2"}}
	return cfg
}

func TestResolveReplayOpts_DefaultsFromAgent(t *testing.T) {
	now := time.Now()
	opts, dc, err := resolveReplayOpts(replayTestConfig(), replayFlags{agent: "default", format: "text", limit: 500, concurrency: 4}, now)
	if err != nil {
		t.Fatalf("resolveReplayOpts: %v", err)
	}
	if dc.Name != "jev" || opts.ApproveAt != 0.9 || opts.DenyAt != 0.1 || opts.ContextMessages != 8 {
		t.Errorf("decider=%q opts=%+v, want the agent's configured values", dc.Name, opts)
	}
	if want := now.Add(-replayDefaultWindow); !opts.Since.Equal(want) {
		t.Errorf("since = %v, want %v", opts.Since, want)
	}
}

func TestResolveReplayOpts_FlagsOverrideAgent(t *testing.T) {
	f := replayFlags{agent: "plain", decider: "alt", format: "json", limit: 10, concurrency: 0, approveAt: 0.99, since: "2026-09-01"}
	opts, dc, err := resolveReplayOpts(replayTestConfig(), f, time.Now())
	if err != nil {
		t.Fatalf("resolveReplayOpts: %v", err)
	}
	if dc.Name != "alt" || opts.ApproveAt != 0.99 || opts.DenyAt != config.DefaultDeciderDenyAt {
		t.Errorf("decider=%q approve=%v deny=%v", dc.Name, opts.ApproveAt, opts.DenyAt)
	}
	if opts.Concurrency != 1 || opts.ContextMessages != agent.DefaultSupervisorContextMessages {
		t.Errorf("concurrency=%d context=%d, want 1 and the engine default", opts.Concurrency, opts.ContextMessages)
	}
	if opts.Since.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("since = %v", opts.Since)
	}
}

// resolveReplayErr resolves the default agent's flags after mutate and returns
// the error text.
func resolveReplayErr(t *testing.T, mutate func(*replayFlags)) string {
	t.Helper()
	f := replayFlags{agent: "default", format: "text", limit: 500}
	mutate(&f)
	_, _, err := resolveReplayOpts(replayTestConfig(), f, time.Now())
	if err == nil {
		t.Fatal("resolveReplayOpts succeeded, want an error")
	}
	return err.Error()
}

func TestResolveReplayOpts_UnknownAgentListsAgents(t *testing.T) {
	got := resolveReplayErr(t, func(f *replayFlags) { f.agent = "nope" })
	if want := `agent "nope" not found in config (agents: default, plain)`; !strings.Contains(got, want) {
		t.Errorf("err = %q, want it to contain %q", got, want)
	}
}

func TestResolveReplayOpts_NoDeciderListsDeciders(t *testing.T) {
	got := resolveReplayErr(t, func(f *replayFlags) { f.agent = "plain" })
	if want := "pass --decider (configured [[llm.deciders]]: jev, alt)"; !strings.Contains(got, want) {
		t.Errorf("err = %q, want it to contain %q", got, want)
	}
}

func TestResolveReplayOpts_NoDecidersConfigured(t *testing.T) {
	cfg := replayTestConfig()
	cfg.LLM.Deciders = nil
	_, _, err := resolveReplayOpts(cfg, replayFlags{agent: "plain", format: "text", limit: 500}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no [[llm.deciders]] configured") {
		t.Errorf("err = %v, want it to say no deciders are configured", err)
	}
}

func TestResolveReplayOpts_UnknownDecider(t *testing.T) {
	got := resolveReplayErr(t, func(f *replayFlags) { f.decider = "ghost" })
	if want := `decider "ghost" not found`; !strings.Contains(got, want) {
		t.Errorf("err = %q, want it to contain %q", got, want)
	}
}

func TestResolveReplayOpts_InvertedThresholds(t *testing.T) {
	got := resolveReplayErr(t, func(f *replayFlags) { f.approveAt, f.denyAt = 0.2, 0.5 })
	if want := "0 < deny-at < approve-at < 1"; !strings.Contains(got, want) {
		t.Errorf("err = %q, want it to contain %q", got, want)
	}
}

func TestResolveReplayOpts_BadSince(t *testing.T) {
	got := resolveReplayErr(t, func(f *replayFlags) { f.since = "yesterday" })
	if want := "want 2006-01-02 or RFC3339"; !strings.Contains(got, want) {
		t.Errorf("err = %q, want it to contain %q", got, want)
	}
}

func TestResolveReplayOpts_BadFormat(t *testing.T) {
	got := resolveReplayErr(t, func(f *replayFlags) { f.format = "yaml" })
	if want := `unknown format "yaml"`; !strings.Contains(got, want) {
		t.Errorf("err = %q, want it to contain %q", got, want)
	}
}

func TestResolveReplayOpts_ZeroLimit(t *testing.T) {
	got := resolveReplayErr(t, func(f *replayFlags) { f.limit = 0 })
	if want := "--limit must be positive"; !strings.Contains(got, want) {
		t.Errorf("err = %q, want it to contain %q", got, want)
	}
}

func TestPercentileMs_NearestRank(t *testing.T) {
	ds := make([]time.Duration, 20)
	for i := range ds {
		ds[i] = time.Duration(20-i) * 10 * time.Millisecond // unsorted: 200ms..10ms
	}
	if p50, p95 := percentileMs(ds, 50), percentileMs(ds, 95); p50 != 100 || p95 != 190 {
		t.Errorf("p50/p95 = %d/%d, want 100/190", p50, p95)
	}
	if got := percentileMs(nil, 95); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
}
