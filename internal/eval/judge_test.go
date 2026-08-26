package eval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
)

// judgeProvider is a hand-written llm.Provider so the judge runs through a
// real llm.Router: the model override, the cost tracker and the no-tools
// request shape are all exercised as production wires them, not stubbed.
type judgeProvider struct {
	mu       sync.Mutex
	requests []llm.ChatRequest
	// reply builds the completion for call n; nil replies with a valid verdict.
	reply func(n int) (*llm.ChatResponse, error)
	// costPerCall is reported back as the provider's own cost, which is what a
	// nil pricing registry bills.
	costPerCall float64
}

func (p *judgeProvider) Name() string                        { return "mock" }
func (p *judgeProvider) HealthCheck(_ context.Context) error { return nil }
func (p *judgeProvider) requestCount() int                   { p.mu.Lock(); defer p.mu.Unlock(); return len(p.requests) }
func (p *judgeProvider) allRequests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.ChatRequest(nil), p.requests...)
}

func (p *judgeProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	n := len(p.requests)
	p.requests = append(p.requests, req)
	reply, cost := p.reply, p.costPerCall
	p.mu.Unlock()

	if reply != nil {
		return reply(n)
	}
	return &llm.ChatResponse{
		Content: `{"winner":"a","dimensions":{"task_success":"a","tool_path":"tie",` +
			`"persona_fit":"a","length":"b"},"notes":"A answered the whole question."}`,
		Model:      req.Model,
		CostUSD:    cost,
		TokensUsed: llm.TokenUsage{Prompt: 100, Completion: 20, Total: 120},
	}, nil
}

// judgeFixture is a terminal run whose grid is filled and paired, plus the
// engine the judge resolves its router from.
type judgeFixture struct {
	*pairFixture
	provider *judgeProvider
	engine   *mockEngine
}

func newJudgeFixture(t *testing.T) *judgeFixture {
	t.Helper()
	f := newPairFixture(t, 1, []string{CategoryChat, CategoryToolHeavy})
	f.fillGrid(t)
	f.createPairs(t)
	if err := f.store.FinishRun(context.Background(), f.run.ID, StatusDone, ""); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	provider := &judgeProvider{}
	engine := newMockEngine()
	engine.router.RegisterProvider(provider)
	return &judgeFixture{pairFixture: f, provider: provider, engine: engine}
}

func (f *judgeFixture) judge(t *testing.T, cfg JudgeConfig) *Judge {
	t.Helper()
	if cfg.MaxCost == 0 {
		cfg.MaxCost = 2.0
	}
	j := NewJudge(f.store, func(name string) (Engine, bool) {
		if name != f.engine.name {
			return nil, false
		}
		return f.engine, true
	}, nil, cfg, testLogger())
	t.Cleanup(j.Shutdown)
	return j
}

// awaitPass blocks until the judging pass finishes, so assertions read a
// settled queue rather than a racing one.
func (f *judgeFixture) awaitPass(t *testing.T, j *Judge) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !j.IsActive(f.run.ID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("judging pass did not finish within 5s")
}

func (f *judgeFixture) verdicts(t *testing.T) []Verdict {
	t.Helper()
	out, err := f.store.ListVerdicts(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	return out
}

// --- Availability ---

func TestJudge_UnconfiguredIsUnavailable(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{})

	if j.Available() {
		t.Error("a judge with no model must not report itself available")
	}
	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); !errors.Is(err, ErrJudgeNotConfigured) {
		t.Fatalf("Start error = %v, want ErrJudgeNotConfigured", err)
	}
	if f.provider.requestCount() != 0 {
		t.Error("an unconfigured judge must not reach the provider")
	}
	// The MCP path is untouched: the queue is still there for Claude Code.
	pending, err := f.store.ListPending(context.Background(), f.run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) == 0 {
		t.Error("the judging queue must survive an unconfigured internal judge")
	}
}

func TestJudge_RejectsRunThatIsStillRunning(t *testing.T) {
	f := newJudgeFixture(t)
	if err := f.store.SetRunStatus(context.Background(), f.run.ID, StatusRunning, ""); err != nil {
		t.Fatalf("SetRunStatus: %v", err)
	}
	j := f.judge(t, JudgeConfig{Model: "judge-model"})

	_, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if !errors.Is(err, ErrRunNotTerminal) {
		t.Fatalf("Start error = %v, want ErrRunNotTerminal", err)
	}
}

func TestJudge_RejectsUnregisteredProvider(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model", Provider: "ghost"})

	_, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("Start error = %v, want it to name the unknown provider", err)
	}
}

// --- The happy path ---

func TestJudge_RecordsVerdictsUnderItsOwnIdentAndRubric(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model", MaxConcurrent: 2})

	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if pass.Items == 0 {
		t.Fatal("the pass took no items from a fully paired run")
	}
	if pass.JudgeIdent != JudgeInternal || pass.RubricVersion != RubricVersion {
		t.Errorf("pass = %+v, want ident %q and rubric %q", pass, JudgeInternal, RubricVersion)
	}
	f.awaitPass(t, j)

	verdicts := f.verdicts(t)
	if len(verdicts) != pass.Items {
		t.Fatalf("recorded %d verdicts, want %d", len(verdicts), pass.Items)
	}
	for _, v := range verdicts {
		if v.JudgeIdent != JudgeInternal {
			t.Errorf("verdict %d judge_ident = %q, want %q", v.ID, v.JudgeIdent, JudgeInternal)
		}
		if v.RubricVersion != RubricVersion {
			t.Errorf("verdict %d rubric_version = %q, want %q", v.ID, v.RubricVersion, RubricVersion)
		}
		if !strings.Contains(v.Dimensions, DimToolPath) {
			t.Errorf("verdict %d lost its dimensions: %q", v.ID, v.Dimensions)
		}
	}
	// A judge verdict flips the item, unlike an operator calibration mark.
	pending, err := f.store.ListPending(context.Background(), f.run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("%d items still pending after a full pass", len(pending))
	}
}

// The internal judge must feed the same win rate the MCP judge does — one
// derivation of the tally, not two.
func TestJudge_VerdictsFeedTheSameAggregation(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model"})
	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	summary, err := f.store.Summarize(context.Background(), f.run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(summary.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1 candidate", len(summary.Verdicts))
	}
	jd := summary.Verdicts[0].Judgment
	if jd.JudgedPairs != jd.Pairs || jd.Pairs == 0 {
		t.Fatalf("judgment = %+v, want every pair judged", jd)
	}
	if len(jd.RubricVersions) != 1 || jd.RubricVersions[0] != RubricVersion {
		t.Errorf("rubric_versions = %v, want [%s]", jd.RubricVersions, RubricVersion)
	}
}

// A judge that names the same presented letter in both orders tracked
// position, and the pair must record a tie — the position-bias control has to
// bite the internal judge exactly as it bites the MCP one.
func TestJudge_AlwaysAnsweringAYieldsTies(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model"})
	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	summary, err := f.store.Summarize(context.Background(), f.run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	jd := summary.Verdicts[0].Judgment
	if jd.Wins != 0 || jd.Losses != 0 || jd.Ties != jd.JudgedPairs {
		t.Errorf("judgment = %+v, want every pair a tie", jd)
	}
}

func TestJudge_SampleNDrawsASubsetOfTheQueue(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model"})

	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{SampleN: 2})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if pass.Items != 2 {
		t.Fatalf("pass took %d items, want the 2 drawn", pass.Items)
	}
	f.awaitPass(t, j)
	if got := len(f.verdicts(t)); got != 2 {
		t.Errorf("recorded %d verdicts, want 2", got)
	}
}

func TestJudge_EmptyQueueIsNotAnError(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model"})
	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	f.awaitPass(t, j)

	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if pass.Items != 0 {
		t.Errorf("pass over an empty queue took %d items", pass.Items)
	}
	if j.IsActive(f.run.ID) {
		t.Error("an empty pass must not register as active")
	}
}

// --- Blinding ---

// The internal judge's own blinding canary, mirroring
// TestGetBlindedItem_LeaksNoIdentity one layer further out: the thing that
// actually reaches the model is the prompt, so the prompt is what gets grepped.
func TestJudge_PromptCarriesNoIdentity(t *testing.T) {
	// Neither variant may be called "incumbent": the rubric itself uses the
	// word to explain the comparison, and a canary that trips on its own prose
	// tests nothing.
	f := &judgeFixture{pairFixture: newPairFixture(t, 1, []string{CategoryToolHeavy},
		"alpha-9x7", "kimi-k3-candidate")}
	for _, task := range f.tasks {
		f.addSample(t, Sample{VariantID: f.variants[0].ID, TaskID: task.ID, KIndex: 0,
			Response: "nothing logged today", Rounds: 2, Cost: 0.1234, LatencyMs: 5150,
			TokensPrompt: 900, TokensCompletion: 210, Upstream: "Fireworks"})
		f.addSample(t, Sample{VariantID: f.variants[1].ID, TaskID: task.ID, KIndex: 0,
			Response: "no entries for today", Rounds: 3, Cost: 0.9876, LatencyMs: 7373,
			TokensPrompt: 950, TokensCompletion: 260, Upstream: "Together"})
	}
	f.createPairs(t)
	if err := f.store.FinishRun(context.Background(), f.run.ID, StatusDone, ""); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	f.provider = &judgeProvider{}
	f.engine = newMockEngine()
	f.engine.router.RegisterProvider(f.provider)

	j := f.judge(t, JudgeConfig{Model: "judge-model"})
	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	reqs := f.provider.allRequests()
	if len(reqs) == 0 {
		t.Fatal("the judge sent nothing")
	}
	for i, req := range reqs {
		var sb strings.Builder
		for _, m := range req.Messages {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
		payload := sb.String()
		for _, forbidden := range []string{
			"alpha-9x7", "kimi-k3-candidate", "kimi-k3", "llm_model", "overlay",
			"eval:", "0.1234", "0.9876", "5150", "7373",
			"Fireworks", "Together", "upstream",
		} {
			if strings.Contains(payload, forbidden) {
				t.Errorf("request %d leaks %q", i, forbidden)
			}
		}
		if !strings.Contains(payload, "prompt "+CategoryToolHeavy) {
			t.Errorf("request %d lost the task prompt", i)
		}
	}
}

// The hard Stage D constraint, restated for the internal judge: it must be
// unable to unblind its own queue. The MCP judge is held to it by
// TestEvalTools_JudgeSurfaceCannotUnblindAPair pinning its tool set at five
// names; the internal judge is held to it by having no tool set at all — every
// request goes out with no tool definitions, so there is nothing for it to
// call the unblinded pair view with.
func TestJudge_SendsNoToolDefinitions(t *testing.T) {
	f := newJudgeFixture(t)
	f.engine.router.SetTools(func() []llm.ToolDef {
		return []llm.ToolDef{{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        "eval_run_pairs",
				Description: "would unblind the queue",
			},
		}}
	})
	j := f.judge(t, JudgeConfig{Model: "judge-model"})
	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	reqs := f.provider.allRequests()
	if len(reqs) == 0 {
		t.Fatal("the judge sent nothing")
	}
	for i, req := range reqs {
		if len(req.Tools) != 0 {
			t.Errorf("request %d carried %d tool definitions; the judge must be one-shot", i, len(req.Tools))
		}
	}
}

func TestJudge_UsesTheConfiguredModel(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model"})
	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	for i, req := range f.provider.allRequests() {
		if req.Model != "judge-model" {
			t.Errorf("request %d ran on %q, want judge-model", i, req.Model)
		}
	}
	// The overlay is a clone: the agent's own router keeps its model.
	if got := f.engine.router.DefaultModel(); got != "test-model" {
		t.Errorf("the agent router was retargeted to %q", got)
	}
}

// --- Cost ---

func TestJudge_RecordsCostApartFromSampleSpend(t *testing.T) {
	f := newJudgeFixture(t)
	f.provider.costPerCall = 0.01
	j := f.judge(t, JudgeConfig{Model: "judge-model"})

	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	run, err := f.store.GetRun(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	want := 0.01 * float64(pass.Items)
	if diff := run.JudgeCost - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("judge_cost = %v, want %v", run.JudgeCost, want)
	}
	// Judging is a separate budget: it must not show up as sample spend, which
	// would read as a run that blew its cap.
	if run.CostSpent != 0 {
		t.Errorf("cost_spent = %v, want the judging pass to leave it alone", run.CostSpent)
	}
}

func TestJudge_StopsDispatchingAtItsCostCap(t *testing.T) {
	f := newJudgeFixture(t)
	f.provider.costPerCall = 0.05
	// One item's worth of budget: the cap is checked before each dispatch, so
	// the first item runs and the rest never start.
	j := f.judge(t, JudgeConfig{Model: "judge-model", MaxCost: 0.05, MaxConcurrent: 1})

	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	got := len(f.verdicts(t))
	if got == 0 {
		t.Fatal("the cap stopped the pass before it judged anything")
	}
	if got >= pass.Items {
		t.Fatalf("recorded %d of %d verdicts; the cap never bit", got, pass.Items)
	}
	// Partial work is kept: the rest of the queue stays pending for a later
	// pass rather than being discarded.
	pending, err := f.store.ListPending(context.Background(), f.run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != pass.Items-got {
		t.Errorf("%d items pending, want %d", len(pending), pass.Items-got)
	}
}

// A second pass gets a fresh budget: spend is measured from the pass's own
// baseline, not from the cost tracker's running total for the key.
func TestJudge_SecondPassIsNotBornOverBudget(t *testing.T) {
	f := newJudgeFixture(t)
	f.provider.costPerCall = 0.02
	j := f.judge(t, JudgeConfig{Model: "judge-model", MaxCost: 0.03, MaxConcurrent: 1})

	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	f.awaitPass(t, j)
	first := len(f.verdicts(t))

	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	f.awaitPass(t, j)
	if second := len(f.verdicts(t)); second <= first {
		t.Errorf("second pass judged nothing new (%d then %d)", first, second)
	}
}

// --- Failure handling ---

func TestJudge_UnreadableReplyLeavesTheItemPending(t *testing.T) {
	f := newJudgeFixture(t)
	f.provider.reply = func(int) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "I would rather not say."}, nil
	}
	j := f.judge(t, JudgeConfig{Model: "judge-model"})

	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	if got := len(f.verdicts(t)); got != 0 {
		t.Errorf("recorded %d verdicts from unreadable replies", got)
	}
	pending, err := f.store.ListPending(context.Background(), f.run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != pass.Items {
		t.Errorf("%d items pending, want all %d back on the queue", len(pending), pass.Items)
	}
}

func TestJudge_ProviderErrorFailsOnlyThatItem(t *testing.T) {
	f := newJudgeFixture(t)
	f.provider.reply = func(n int) (*llm.ChatResponse, error) {
		if n == 0 {
			return nil, errors.New("upstream is having a day")
		}
		return &llm.ChatResponse{Content: `{"winner":"tie"}`}, nil
	}
	j := f.judge(t, JudgeConfig{Model: "judge-model", MaxConcurrent: 1})

	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)

	if got := len(f.verdicts(t)); got != pass.Items-1 {
		t.Errorf("recorded %d verdicts, want %d — one item should have failed alone", got, pass.Items-1)
	}
}

func TestJudge_StopCancelsAnActivePass(t *testing.T) {
	f := newJudgeFixture(t)
	release := make(chan struct{})
	var once sync.Once
	f.provider.reply = func(int) (*llm.ChatResponse, error) {
		<-release
		return &llm.ChatResponse{Content: `{"winner":"tie"}`}, nil
	}
	j := f.judge(t, JudgeConfig{Model: "judge-model", MaxConcurrent: 1})
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	if _, err := j.Start(context.Background(), f.run.ID, JudgeOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !j.IsActive(f.run.ID) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !j.Stop(f.run.ID) {
		t.Fatal("Stop reported no active pass")
	}
	once.Do(func() { close(release) })
	f.awaitPass(t, j)

	if got := len(f.verdicts(t)); got > 1 {
		t.Errorf("a stopped pass recorded %d verdicts; only the in-flight one should land", got)
	}
}

// --- Rubric ---

// The internal judge and the /judge-eval skill must grade under the same
// rubric revision, or a results view naming one version would be describing
// two different policies.
func TestRubricVersion_MatchesTheSkillFile(t *testing.T) {
	raw, err := os.ReadFile("../../.claude/skills/judge-eval/SKILL.md")
	if err != nil {
		t.Fatalf("reading the judging skill: %v", err)
	}
	want := fmt.Sprintf("Rubric version: **%s**", RubricVersion)
	if !strings.Contains(string(raw), want) {
		t.Errorf("the skill file does not carry %q — bump one to match the other", want)
	}
}

func TestJudgeSystemPrompt_NamesEveryDimension(t *testing.T) {
	for _, d := range Dimensions() {
		if !strings.Contains(judgeSystemPrompt, d) {
			t.Errorf("the rubric never mentions the %q dimension", d)
		}
	}
}

// --- Reply parsing ---

func TestParseJudgeCall_AcceptsAFencedObject(t *testing.T) {
	call, err := parseJudgeCall("Sure:\n```json\n{\"winner\":\"B\",\"notes\":\"tighter\"}\n```\n")
	if err != nil {
		t.Fatalf("parseJudgeCall: %v", err)
	}
	if call.Winner != WinnerB || call.Notes != "tighter" {
		t.Errorf("call = %+v", call)
	}
}

func TestParseJudgeCall_RejectsAnUnknownDimension(t *testing.T) {
	_, err := parseJudgeCall(`{"winner":"a","dimensions":{"task_sucess":"a"}}`)
	if err == nil || !strings.Contains(err.Error(), "task_sucess") {
		t.Fatalf("error = %v, want the typo named rather than silently dropped", err)
	}
}

func TestParseJudgeCall_RejectsAnInvalidWinner(t *testing.T) {
	if _, err := parseJudgeCall(`{"winner":"neither"}`); err == nil {
		t.Fatal("expected an error for an invalid winner")
	}
	if _, err := parseJudgeCall(`{"winner":"a","dimensions":{"length":"c"}}`); err == nil {
		t.Fatal("expected an error for an invalid dimension winner")
	}
}

func TestParseJudgeCall_RejectsProseWithNoObject(t *testing.T) {
	if _, err := parseJudgeCall("I cannot judge this."); err == nil {
		t.Fatal("expected an error when the reply carries no JSON")
	}
}

func TestBuildJudgeMessages_IsSystemThenUser(t *testing.T) {
	msgs, err := buildJudgeMessages(&BlindedItem{ItemID: 7, Prompt: "what is the time"})
	if err != nil {
		t.Fatalf("buildJudgeMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("messages = %+v", msgs)
	}
	if !strings.Contains(msgs[1].Content, "what is the time") {
		t.Error("the item never made it into the user message")
	}
}

// The judge's ident is not the operator's: its verdicts count toward the win
// rate and flip items, which is exactly what a calibration mark must not do.
func TestJudgeInternal_IsNotTheOperatorIdent(t *testing.T) {
	if JudgeInternal == JudgeOperator {
		t.Fatal("the internal judge must not share the operator's calibration identity")
	}
}
