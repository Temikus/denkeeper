//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
)

// TestEvalRun_JudgeDeciderLive judges one real run's blinded pairs with the
// real decider, the samples mocked. The verdicts are the model's, so the test
// checks they are well-formed and consistent rather than pinning them.
// Skipped without OPENROUTER_API_KEY.
func TestEvalRun_JudgeDeciderLive(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	h := evalHarness(t, &HarnessOpts{
		EvalJudgeDecider: openrouter.New(key),
		// Record everything the model answers: the point is to see the
		// probabilities, not to pass a bar chosen before seeing them.
		EvalJudgeDeciderRecordAt: 0.51,
		Responder:                thoroughResponder,
	})
	runID := runJudgeableEval(t, h)

	pass := judgeRun(t, h, runID)
	if pass.Items != 4 {
		t.Fatalf("pass took %d items, want 4", pass.Items)
	}
	verdicts, err := h.EvalStore.ListVerdicts(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	for _, v := range verdicts {
		t.Logf("item %d: winner=%s dims=%s notes=%q", v.ItemID, v.Winner, v.Dimensions, v.Notes)
		if v.JudgeIdent != eval.JudgeDecider || !eval.ValidWinner(v.Winner) {
			t.Errorf("verdict %d = %+v", v.ID, v)
		}
	}
	run, err := h.EvalStore.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	t.Logf("judge_cost = %v over %d verdicts", run.JudgeCost, len(verdicts))
	if len(verdicts) > 0 && run.JudgeCost <= 0 {
		t.Errorf("judge_cost = %v, want the provider's reported cost", run.JudgeCost)
	}
}
