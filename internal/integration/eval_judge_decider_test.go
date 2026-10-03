//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/llm"
)

// preferringDecider reads the blinded item it is handed and names whichever
// side contains marker with probability p, or a tie at the same probability
// when neither does. It is the decision-model counterpart of judgePrefers.
type preferringDecider struct {
	marker string
	p      float64

	mu    sync.Mutex
	calls int
}

func (d *preferringDecider) Decide(_ context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	item, ok := req.State.(*eval.BlindedItem)
	if !ok {
		return nil, fmt.Errorf("state is %T, want *eval.BlindedItem", req.State)
	}
	choice := eval.WinnerTie
	switch {
	case strings.Contains(item.ResponseA.Response, d.marker):
		choice = eval.WinnerA
	case strings.Contains(item.ResponseB.Response, d.marker):
		choice = eval.WinnerB
	}
	answers := make(map[string]llm.Answer, len(req.Questions))
	for id := range req.Questions {
		answers[id] = llm.Answer{Type: llm.QuestionChoice, Choice: choice, Confidence: d.p,
			Probabilities: map[string]float64{choice: d.p}}
	}
	return &llm.DecisionResponse{Model: req.Model, Answers: answers, CostUSD: 0.0002,
		Usage: llm.TokenUsage{Prompt: 1500, Completion: 5, Total: 1505}}, nil
}

func (d *preferringDecider) callCount() int { d.mu.Lock(); defer d.mu.Unlock(); return d.calls }

// thoroughResponder makes the candidate's answers distinguishable so a judge
// has something to prefer.
func thoroughResponder(req llm.ChatRequest) (*llm.ChatResponse, error) {
	content := "the answer"
	if req.Model == "candidate-model" {
		content = "the thorough answer"
	}
	return &llm.ChatResponse{
		Content:      content,
		TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
		Model:        req.Model,
		FinishReason: "stop",
	}, nil
}

func runJudgeableEval(t *testing.T, h *Harness) int64 {
	t.Helper()
	seedEvalSet(t, h, "regression", "first question", "second question")
	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "regression",
		"base_agent": "default",
		"k":          1,
		"cost_cap":   10.0,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "candidate", "llm_model": "candidate-model"},
		},
	})
	if run := evalRunStatus(t, h, runID); run.Status != eval.StatusDone {
		t.Fatalf("status = %q, want %q", run.Status, eval.StatusDone)
	}
	return runID
}

func judgeRun(t *testing.T, h *Harness, runID int64) eval.JudgePass {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/eval/runs/%d/judge", runID), nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("judge: status %d: %s", rec.Code, rec.Body.String())
	}
	var pass eval.JudgePass
	DecodeJSON(t, rec, &pass)
	awaitJudging(t, h, runID)
	return pass
}

// A decider alone judges a finished run end to end: its verdicts are ordinary
// verdicts under judge_decider, feed the same tally, and bill judge_cost.
func TestEvalRun_JudgeDeciderAloneProducesAnUpgradeVerdict(t *testing.T) {
	prov := &preferringDecider{marker: "thorough", p: 0.95}
	h := evalHarness(t, &HarnessOpts{
		EvalJudgeDecider: prov,
		Responder:        thoroughResponder,
	})
	runID := runJudgeableEval(t, h)

	pass := judgeRun(t, h, runID)
	if pass.Items != 4 || pass.Decider != "jev" || pass.Model != "" {
		t.Fatalf("pass = %+v, want 4 items on the decider alone", pass)
	}
	if len(pass.JudgeIdents) != 1 || pass.JudgeIdents[0] != eval.JudgeDecider {
		t.Errorf("judge_idents = %v, want [%s]", pass.JudgeIdents, eval.JudgeDecider)
	}

	summary := evalSummary(t, h, runID)
	vv := summary.Verdicts[0]
	if vv.Judgment.JudgedPairs != 2 || vv.Verdict != eval.VerdictUpgrade {
		t.Fatalf("judgment = %+v, verdict %q (%s); want 2 judged pairs and an upgrade", vv.Judgment, vv.Verdict, vv.Reason)
	}
	if len(vv.Judgment.JudgeIdents) != 1 || vv.Judgment.JudgeIdents[0] != eval.JudgeDecider || vv.Judgment.MixedPairs != 0 {
		t.Errorf("judge_idents = %v, mixed = %d; want the decider alone", vv.Judgment.JudgeIdents, vv.Judgment.MixedPairs)
	}
	verdicts, err := h.EvalStore.ListVerdicts(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	for _, v := range verdicts {
		if v.JudgeIdent != eval.JudgeDecider || v.RubricVersion != eval.RubricVersion {
			t.Errorf("verdict %d = %s/%s", v.ID, v.JudgeIdent, v.RubricVersion)
		}
	}
	run, err := h.EvalStore.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.JudgeCost <= 0 {
		t.Errorf("judge_cost = %v, want decider spend recorded", run.JudgeCost)
	}
	// Nothing fell through: the mock LLM served the samples only.
	if h.MockLLM.CallCount() != 4 {
		t.Errorf("LLM calls = %d, want the 4 samples and no judge completions", h.MockLLM.CallCount())
	}
}

// The cascade: an uncertain decider hands the item to the judge model, and
// the pass records nothing under the decider.
func TestEvalRun_UncertainJudgeDeciderFallsThroughToTheModel(t *testing.T) {
	prov := &preferringDecider{marker: "thorough", p: 0.6}
	h := evalHarness(t, &HarnessOpts{
		EvalJudgeModel:   "judge-model",
		EvalJudgeDecider: prov,
		Responder:        thoroughResponder,
	})
	runID := runJudgeableEval(t, h)
	h.judgePrefers(t, "thorough")

	pass := judgeRun(t, h, runID)
	if want := []string{eval.JudgeDecider, eval.JudgeInternal}; strings.Join(pass.JudgeIdents, ",") != strings.Join(want, ",") {
		t.Errorf("judge_idents = %v, want %v", pass.JudgeIdents, want)
	}
	if prov.callCount() != 4 {
		t.Errorf("decider asked %d times, want once per item", prov.callCount())
	}
	verdicts, err := h.EvalStore.ListVerdicts(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	if len(verdicts) != 4 {
		t.Fatalf("recorded %d verdicts, want 4 from the model", len(verdicts))
	}
	for _, v := range verdicts {
		if v.JudgeIdent != eval.JudgeInternal {
			t.Errorf("verdict %d judge_ident = %q, want the model's after an abstention", v.ID, v.JudgeIdent)
		}
	}
	if vv := evalSummary(t, h, runID).Verdicts[0]; vv.Verdict != eval.VerdictUpgrade {
		t.Errorf("verdict = %q (%s), want an upgrade from the model's calls", vv.Verdict, vv.Reason)
	}
}
