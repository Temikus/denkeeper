package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
)

// decisionStub is a hand-written llm.DecisionProvider. By default it names
// response_a on every question with probability p; answer overrides one
// question's reply, err fails the call, and delay makes it slow.
type decisionStub struct {
	mu       sync.Mutex
	requests []llm.DecisionRequest
	p        float64
	cost     float64
	err      error
	delay    time.Duration
	// model is what the response reports back; empty echoes the request.
	model string
	// answer, when set, builds the reply for question id (nil keeps default).
	answer func(id string) *llm.Answer
}

func (d *decisionStub) Decide(ctx context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	d.mu.Lock()
	d.requests = append(d.requests, req)
	p, cost, err, delay, model, answer := d.p, d.cost, d.err, d.delay, d.model, d.answer
	d.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = req.Model
	}
	answers := make(map[string]llm.Answer, len(req.Questions))
	for id := range req.Questions {
		if answer != nil {
			if a := answer(id); a != nil {
				answers[id] = *a
				continue
			}
		}
		answers[id] = choiceAnswer(WinnerA, p)
	}
	return &llm.DecisionResponse{Model: model, Answers: answers, CostUSD: cost,
		Usage: llm.TokenUsage{Prompt: 2000, Completion: 5, Total: 2005}}, nil
}

func (d *decisionStub) requestCount() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.requests) }
func (d *decisionStub) allRequests() []llm.DecisionRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]llm.DecisionRequest(nil), d.requests...)
}

// choiceAnswer names choice with probability p, the remainder split evenly.
func choiceAnswer(choice string, p float64) llm.Answer {
	probs := map[string]float64{}
	for _, c := range []string{WinnerA, WinnerB, WinnerTie} {
		probs[c] = (1 - p) / 2
	}
	probs[choice] = p
	return llm.Answer{Type: llm.QuestionChoice, Choice: choice, Probabilities: probs, Confidence: p}
}

// decider binds the stub the way main.go binds a started client, billing to
// the fixture engine's tracker so judge and decider spend share one ledger.
func (f *judgeFixture) decider(stub *decisionStub, timeout time.Duration) *llm.Decider {
	return llm.NewDecider(llm.DeciderConfig{
		Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", Timeout: timeout, MaxInputTokens: 30000,
	}, stub, f.engine.tracker)
}

func (f *judgeFixture) pending(t *testing.T) int {
	t.Helper()
	pending, err := f.store.ListPending(context.Background(), f.run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	return len(pending)
}

func (f *judgeFixture) runPass(t *testing.T, j *Judge) *JudgePass {
	t.Helper()
	pass, err := j.Start(context.Background(), f.run.ID, JudgeOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.awaitPass(t, j)
	return pass
}

func identCounts(verdicts []Verdict) map[string]int {
	out := map[string]int{}
	for _, v := range verdicts {
		out[v.JudgeIdent]++
	}
	return out
}

// --- Questions ---

func TestJudgeDeciderQuestions_AreValidAndCoverEveryDimension(t *testing.T) {
	qs := judgeDeciderQuestions()
	if err := llm.ValidateQuestions(qs); err != nil {
		t.Fatalf("ValidateQuestions: %v", err)
	}
	if _, ok := qs[deciderWinnerQuestion]; !ok {
		t.Error("no winner question")
	}
	for _, dim := range Dimensions() {
		q, ok := qs[dim]
		if !ok {
			t.Errorf("no question for dimension %q", dim)
			continue
		}
		if q.Type != llm.QuestionChoice || len(q.Choices) != 3 {
			t.Errorf("dimension %q is %s with %d choices, want a three-way choice", dim, q.Type, len(q.Choices))
		}
	}
	if len(qs) != len(Dimensions())+1 {
		t.Errorf("%d questions, want winner plus %d dimensions", len(qs), len(Dimensions()))
	}
}

// --- deciderCall ---

func TestDeciderCall_RecordsAtExactlyTheThreshold(t *testing.T) {
	answers := map[string]llm.Answer{deciderWinnerQuestion: choiceAnswer(WinnerB, 0.8)}
	call, ok, err := deciderCall(answers, 0.8)
	if err != nil || !ok {
		t.Fatalf("deciderCall = %v, %v, %v; want a recorded call at p == record_at", call, ok, err)
	}
	if call.Winner != WinnerB || !strings.Contains(call.Notes, "winner b (p=0.80)") {
		t.Errorf("call = %+v", call)
	}
}

func TestDeciderCall_AbstainsBelowTheThreshold(t *testing.T) {
	answers := map[string]llm.Answer{deciderWinnerQuestion: choiceAnswer(WinnerA, 0.79)}
	_, ok, err := deciderCall(answers, 0.8)
	if err != nil || ok {
		t.Fatalf("ok = %v, err = %v; want an abstention", ok, err)
	}
}

func TestDeciderCall_FallsBackToConfidenceWithoutProbabilities(t *testing.T) {
	answers := map[string]llm.Answer{deciderWinnerQuestion: {Type: llm.QuestionChoice, Choice: WinnerTie, Confidence: 0.9}}
	call, ok, err := deciderCall(answers, 0.8)
	if err != nil || !ok || call.Winner != WinnerTie {
		t.Fatalf("deciderCall = %v, %v, %v; want tie recorded on confidence alone", call, ok, err)
	}
}

func TestDeciderCall_OmitsUncertainDimensions(t *testing.T) {
	answers := map[string]llm.Answer{
		deciderWinnerQuestion: choiceAnswer(WinnerA, 0.95),
		DimTaskSuccess:        choiceAnswer(WinnerA, 0.91),
		DimToolPath:           choiceAnswer(WinnerTie, 0.61),
		DimLength:             choiceAnswer(WinnerB, 0.85),
	}
	call, ok, err := deciderCall(answers, 0.8)
	if err != nil || !ok {
		t.Fatalf("deciderCall = %v, %v", ok, err)
	}
	if call.Dimensions[DimTaskSuccess] != WinnerA || call.Dimensions[DimLength] != WinnerB {
		t.Errorf("dimensions = %v, want the confident ones kept", call.Dimensions)
	}
	if _, kept := call.Dimensions[DimToolPath]; kept {
		t.Errorf("dimensions = %v, want tool_path omitted at p=0.61", call.Dimensions)
	}
	if _, kept := call.Dimensions[DimPersonaFit]; kept {
		t.Errorf("dimensions = %v, want an unanswered dimension absent", call.Dimensions)
	}
	if !strings.Contains(call.Notes, "tool_path omitted (tie p=0.61)") {
		t.Errorf("notes = %q, want the omission explained", call.Notes)
	}
}

func TestDeciderCall_RejectsAnUnknownOption(t *testing.T) {
	answers := map[string]llm.Answer{deciderWinnerQuestion: {Type: llm.QuestionChoice, Choice: "both", Confidence: 1}}
	if _, _, err := deciderCall(answers, 0.8); err == nil {
		t.Fatal("an option outside a/b/tie must fail the item, not be stored")
	}
	answers = map[string]llm.Answer{
		deciderWinnerQuestion: choiceAnswer(WinnerA, 0.95),
		DimLength:             {Type: llm.QuestionChoice, Choice: "neither", Confidence: 1},
	}
	if _, _, err := deciderCall(answers, 0.8); err == nil {
		t.Fatal("an unknown dimension option must fail the item")
	}
}

func TestDeciderCall_MissingWinnerIsAnError(t *testing.T) {
	if _, _, err := deciderCall(map[string]llm.Answer{DimLength: choiceAnswer(WinnerA, 1)}, 0.8); err == nil {
		t.Fatal("no winner answer must be an error")
	}
}

// --- Decider-only passes ---

func TestJudge_DeciderOnlyIsAvailableAndRecordsUnderItsOwnIdent(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95, cost: 0.0001}
	j := f.judge(t, JudgeConfig{Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})
	if !j.Available() {
		t.Fatal("a decider alone must make the judge available")
	}

	pass := f.runPass(t, j)
	if pass.Decider != "jev" || pass.DeciderRecordAt != 0.8 || pass.Model != "" {
		t.Errorf("pass = %+v", pass)
	}
	if len(pass.JudgeIdents) != 1 || pass.JudgeIdents[0] != JudgeDecider {
		t.Errorf("judge_idents = %v, want [%s]", pass.JudgeIdents, JudgeDecider)
	}
	verdicts := f.verdicts(t)
	if len(verdicts) != pass.Items || pass.Items == 0 {
		t.Fatalf("recorded %d verdicts, want %d", len(verdicts), pass.Items)
	}
	for _, v := range verdicts {
		if v.JudgeIdent != JudgeDecider || v.RubricVersion != RubricVersion {
			t.Errorf("verdict %d = %s/%s, want %s/%s", v.ID, v.JudgeIdent, v.RubricVersion, JudgeDecider, RubricVersion)
		}
		if !strings.HasPrefix(v.Notes, "decider: winner a") {
			t.Errorf("verdict %d notes = %q, want the probabilities recorded", v.ID, v.Notes)
		}
	}
	if f.pending(t) != 0 {
		t.Error("decider verdicts must flip items to judged")
	}
	if f.provider.requestCount() != 0 {
		t.Error("no judge model is configured, yet the router was called")
	}
}

func TestJudge_DeciderAbstentionLeavesTheItemPendingWithoutAModel(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.6}
	j := f.judge(t, JudgeConfig{Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	if got := len(f.verdicts(t)); got != 0 {
		t.Errorf("recorded %d verdicts from uncertain answers", got)
	}
	if f.pending(t) != pass.Items {
		t.Errorf("%d pending, want all %d back on the queue for the MCP judge", f.pending(t), pass.Items)
	}
}

// --- The cascade ---

func TestJudge_DeciderAbstentionFallsThroughToTheModel(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.6}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	if want := []string{JudgeDecider, JudgeInternal}; strings.Join(pass.JudgeIdents, ",") != strings.Join(want, ",") {
		t.Errorf("judge_idents = %v, want %v", pass.JudgeIdents, want)
	}
	counts := identCounts(f.verdicts(t))
	if counts[JudgeInternal] != pass.Items || counts[JudgeDecider] != 0 {
		t.Errorf("verdicts by ident = %v, want every item escalated to the model", counts)
	}
	if stub.requestCount() != pass.Items {
		t.Errorf("decider asked %d times, want once per item", stub.requestCount())
	}
}

func TestJudge_ConfidentDeciderSkipsTheModel(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	counts := identCounts(f.verdicts(t))
	if counts[JudgeDecider] != pass.Items || counts[JudgeInternal] != 0 {
		t.Errorf("verdicts by ident = %v, want every item decided", counts)
	}
	if f.provider.requestCount() != 0 {
		t.Error("the model was asked although the decider settled every item")
	}
}

func TestJudge_OversizedItemFallsThroughToTheModel(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95}
	d := llm.NewDecider(llm.DeciderConfig{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13",
		Timeout: time.Second, MaxInputTokens: 10}, stub, f.engine.tracker)
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: d, DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	if stub.requestCount() != 0 {
		t.Error("an oversized item must never reach the decider's provider")
	}
	if counts := identCounts(f.verdicts(t)); counts[JudgeInternal] != pass.Items {
		t.Errorf("verdicts by ident = %v, want the model to take every item", counts)
	}
}

func TestJudge_DeciderErrorFallsThroughToTheModel(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{err: &llm.LLMError{StatusCode: 503, Message: "down"}}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	if counts := identCounts(f.verdicts(t)); counts[JudgeInternal] != pass.Items {
		t.Errorf("verdicts by ident = %v, want the model to take every item", counts)
	}
}

func TestJudge_DeciderTimeoutFallsThroughToTheModel(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95, delay: 200 * time.Millisecond}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, 10*time.Millisecond), DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	if counts := identCounts(f.verdicts(t)); counts[JudgeInternal] != pass.Items {
		t.Errorf("verdicts by ident = %v, want the model to take every item", counts)
	}
}

// A decider response from another model is nothing recorded, so it is a
// fall-through rather than a failed item — unlike the model stage, whose
// swapped completion fails the item because a verdict would otherwise be
// stamped with a model that did not produce it.
func TestJudge_DeciderModelSwapFallsThrough(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95, model: "some/other-classifier"}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	counts := identCounts(f.verdicts(t))
	if counts[JudgeDecider] != 0 || counts[JudgeInternal] != pass.Items {
		t.Errorf("verdicts by ident = %v, want nothing recorded under the decider", counts)
	}
}

func TestJudge_DeciderUnknownOptionFailsTheItem(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95, answer: func(id string) *llm.Answer {
		if id == deciderWinnerQuestion {
			return &llm.Answer{Type: llm.QuestionChoice, Choice: "both", Confidence: 1}
		}
		return nil
	}}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})

	pass := f.runPass(t, j)
	if got := len(f.verdicts(t)); got != 0 {
		t.Errorf("recorded %d verdicts from an invalid option", got)
	}
	if f.provider.requestCount() != 0 {
		t.Error("a rejected decider answer must fail the item, not hand it to the model")
	}
	if f.pending(t) != pass.Items {
		t.Errorf("%d pending, want all %d left for a later pass", f.pending(t), pass.Items)
	}
}

// --- Cost ---

func TestJudge_DeciderSpendCountsTowardTheCapAndTheRun(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95, cost: 0.05}
	j := f.judge(t, JudgeConfig{Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8, MaxCost: 0.05, MaxConcurrent: 1})

	pass := f.runPass(t, j)
	got := len(f.verdicts(t))
	if got == 0 || got >= pass.Items {
		t.Fatalf("recorded %d of %d verdicts; the cap must bite after the first decider call", got, pass.Items)
	}
	run, err := f.store.GetRun(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.JudgeCost <= 0 {
		t.Errorf("judge_cost = %v, want decider spend recorded on the run", run.JudgeCost)
	}
}

func TestJudge_DeciderSpendIsAttributedToTheEvalPseudoAgent(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95, cost: 0.01}
	j := f.judge(t, JudgeConfig{Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})
	f.runPass(t, j)

	want := JudgeAgentIdent(f.engine.name)
	var found bool
	for _, a := range f.engine.tracker.AgentCosts() {
		switch a.Agent {
		case want:
			found = true
		case "eval", f.engine.name, "jev", "or":
			t.Errorf("decider judge spend landed on %q", a.Agent)
		}
	}
	if !found {
		t.Errorf("no spend attributed to %q; got %+v", want, f.engine.tracker.AgentCosts())
	}
}

// The two stages bill sibling keys: the tracker keeps one provider per
// session for limit resolution, and the backends may sit on different ones.
func TestJudge_StagesBillSiblingSessionKeys(t *testing.T) {
	f := newJudgeFixture(t)
	f.provider.costPerCall = 0.01
	stub := &decisionStub{p: 0.6, cost: 0.001}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})
	f.runPass(t, j)

	var model, decider int
	for id, cost := range f.engine.tracker.AllSessionCosts() {
		switch {
		case !strings.HasPrefix(id, "eval:judge:") || cost == 0:
		case strings.HasSuffix(id, ":decider"):
			decider++
		default:
			model++
		}
	}
	if model != 1 || decider != 1 {
		t.Errorf("model keys = %d, decider keys = %d, want one of each", model, decider)
	}
}

func TestJudge_DeciderHardLimitCapsThePass(t *testing.T) {
	f := newJudgeFixture(t)
	stub := &decisionStub{p: 0.95, cost: 0.01}
	tracker := llm.NewCostTracker(llm.SessionLimits{Hard: 0.001}, nil)
	d := llm.NewDecider(llm.DeciderConfig{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13",
		Timeout: time.Second, MaxInputTokens: 30000}, stub, tracker)
	j := f.judge(t, JudgeConfig{Decider: d, DeciderRecordAt: 0.8, MaxConcurrent: 1})
	// The fixture engine's tracker has no limits; the decider's own stands in
	// for a [costs] hard limit on the shared one, already spent on this pass's
	// key so the very first call is refused.
	j.engines = func(string) (Engine, bool) {
		e := newMockEngine()
		e.router = llm.NewRouter("mock", "test-model", nil)
		return e, true
	}
	tracker.Record(deciderConvID(JudgeConvID(f.run.ID, 1)), 1.0)

	pass := f.runPass(t, j)
	if stub.requestCount() != 0 {
		t.Errorf("decider called %d times under a hard limit", stub.requestCount())
	}
	if f.pending(t) != pass.Items {
		t.Errorf("%d pending, want all %d: a hard limit records nothing", f.pending(t), pass.Items)
	}
	if f.provider.requestCount() != 0 {
		t.Error("the model stage ran after the budget was refused")
	}
}

// --- Blinding ---

// The decider twin of TestJudge_PromptCarriesNoIdentity: what reaches the
// provider is the marshalled state, so the state is what gets grepped.
func TestJudge_DeciderStateCarriesNoIdentity(t *testing.T) {
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

	stub := &decisionStub{p: 0.95}
	j := f.judge(t, JudgeConfig{Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8})
	f.runPass(t, j)

	reqs := stub.allRequests()
	if len(reqs) == 0 {
		t.Fatal("the decider was sent nothing")
	}
	for i, req := range reqs {
		body, err := json.Marshal(req.State)
		if err != nil {
			t.Fatalf("request %d state does not marshal: %v", i, err)
		}
		payload := string(body)
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
		// Same struct, same bytes: the decider sees exactly what the model
		// judge's user message carries, not a second rendering.
		if _, ok := req.State.(*BlindedItem); !ok {
			t.Errorf("request %d state is %T, want *BlindedItem", i, req.State)
		}
	}
}

// --- Aggregation ---

func TestJudgment_JudgeIdentsAndMixedPairs(t *testing.T) {
	f := newJudgeFixture(t)
	// The decider settles only the first item it sees of each pair: alternate
	// confident and uncertain answers, so each pair ends up with one decider
	// verdict and one model verdict.
	var n int
	var mu sync.Mutex
	stub := &decisionStub{answer: func(id string) *llm.Answer {
		mu.Lock()
		defer mu.Unlock()
		if id == deciderWinnerQuestion {
			n++
		}
		if n%2 == 1 {
			a := choiceAnswer(WinnerA, 0.95)
			return &a
		}
		a := choiceAnswer(WinnerA, 0.5)
		return &a
	}}
	j := f.judge(t, JudgeConfig{Model: "judge-model", Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.8, MaxConcurrent: 1})
	f.runPass(t, j)

	counts := identCounts(f.verdicts(t))
	if counts[JudgeDecider] == 0 || counts[JudgeInternal] == 0 {
		t.Fatalf("verdicts by ident = %v, want both backends to have recorded", counts)
	}
	summary, err := f.store.Summarize(context.Background(), f.run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	jd := summary.Verdicts[0].Judgment
	if want := []string{JudgeDecider, JudgeInternal}; strings.Join(jd.JudgeIdents, ",") != strings.Join(want, ",") {
		t.Errorf("judge_idents = %v, want %v sorted", jd.JudgeIdents, want)
	}
	if jd.JudgedPairs == 0 || jd.MixedPairs != jd.JudgedPairs {
		t.Errorf("judgment = %+v, want every judged pair reported as mixed", jd)
	}
}

func TestJudgment_SingleJudgeHasNoMixedPairs(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{Model: "judge-model"})
	f.runPass(t, j)

	summary, err := f.store.Summarize(context.Background(), f.run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	jd := summary.Verdicts[0].Judgment
	if jd.MixedPairs != 0 || len(jd.JudgeIdents) != 1 || jd.JudgeIdents[0] != JudgeInternal {
		t.Errorf("judgment = %+v, want one ident and no mixed pairs", jd)
	}
}

// --- Config ---

func TestJudge_SetConfigBindsADeciderWithoutARestart(t *testing.T) {
	f := newJudgeFixture(t)
	j := f.judge(t, JudgeConfig{})
	if j.Available() {
		t.Fatal("judge with nothing configured must start unavailable")
	}
	stub := &decisionStub{p: 0.95}
	j.SetConfig(JudgeConfig{Decider: f.decider(stub, time.Second), DeciderRecordAt: 0.9, MaxCost: 1.5})
	if !j.Available() {
		t.Fatal("a reloaded judge_decider must take effect without a restart")
	}
	pass := f.runPass(t, j)
	if pass.Items == 0 || len(f.verdicts(t)) != pass.Items {
		t.Errorf("the reloaded decider judged %d of %d", len(f.verdicts(t)), pass.Items)
	}
}

func TestJudgeDecider_IsNotTheOperatorOrModelIdent(t *testing.T) {
	if JudgeDecider == JudgeOperator || JudgeDecider == JudgeInternal {
		t.Fatalf("JudgeDecider %q collides with another identity", JudgeDecider)
	}
}

func TestDecisionErrorCauseInJudge_TooLargeIsNotAFailure(t *testing.T) {
	// Pin the cause string the judge switches on: a rename in llm would
	// silently turn every oversized item into a logged fall-through with no
	// too_large count.
	err := errors.Join(errors.New("decider"), llm.ErrDecisionTooLarge)
	if got := llm.DecisionErrorCause(err); got != "too_large" {
		t.Fatalf("cause = %q, want too_large", got)
	}
}
