package eval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
)

// The blinding canary. It serialises a judge-visible payload built from a
// fixture stuffed with identity strings, then greps the JSON for every one of
// them. It fails the day a new field on eval_samples starts riding along.
func TestGetBlindedItem_LeaksNoIdentity(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryToolHeavy}, "incumbent", "kimi-k3-candidate")
	ctx := context.Background()
	inc, cand := f.variants[0], f.variants[1]
	task := f.tasks[0]

	trace, err := json.Marshal([]agent.ToolCallRecord{{
		ConversationID: SampleConvID(f.run.ID, task.ID, 0, cand.ID),
		ToolName:       "kv_get",
		ServerName:     "kv",
		Round:          1,
		DurationMs:     4242,
		Outcome:        "ok",
		Arguments:      `{"key":"log:today"}`,
		Result:         "nothing yet",
	}})
	if err != nil {
		t.Fatalf("marshalling trace: %v", err)
	}

	// Neutral response text: the fixture must not smuggle a forbidden string in
	// through the one field that is legitimately verbatim.
	f.addSample(t, Sample{VariantID: inc.ID, TaskID: task.ID, KIndex: 0,
		Response: "nothing logged today", Rounds: 2, Cost: 0.1234, LatencyMs: 5150,
		TokensPrompt: 900, TokensCompletion: 210})
	f.addSample(t, Sample{VariantID: cand.ID, TaskID: task.ID, KIndex: 0,
		Response: "no entries for today", Rounds: 3, Cost: 0.9876, LatencyMs: 7373,
		TokensPrompt: 950, TokensCompletion: 260, Trace: string(trace)})
	f.createPairs(t)

	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range items {
		blinded, err := f.store.GetBlindedItem(ctx, it.ID)
		if err != nil {
			t.Fatalf("GetBlindedItem: %v", err)
		}
		raw, err := json.Marshal(blinded)
		if err != nil {
			t.Fatalf("marshalling blinded item: %v", err)
		}
		payload := string(raw)

		// Variant names, the eval conversation id (which names the variant),
		// and the cost/latency/token figures that would let a judge fingerprint
		// a model must all be absent.
		for _, forbidden := range []string{
			"incumbent", "kimi-k3-candidate", "kimi-k3", "llm_model", "overlay",
			"eval:", "0.1234", "0.9876", "5150", "7373", "4242",
			"cost", "latency", "tokens", "variant",
		} {
			if strings.Contains(payload, forbidden) {
				t.Errorf("item %d payload leaks %q:\n%s", it.ID, forbidden, payload)
			}
		}
		// It must still carry the things a judge needs.
		if blinded.Prompt == "" || blinded.Notes == "" || blinded.Category != CategoryToolHeavy {
			t.Errorf("item %d lost its task context: %+v", it.ID, blinded)
		}
		if blinded.ResponseA.Response == "" || blinded.ResponseB.Response == "" {
			t.Errorf("item %d is missing a response: %+v", it.ID, blinded)
		}
	}
}

func TestGetBlindedItem_AppliesPresentationOrder(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.addSample(t, Sample{VariantID: f.variants[0].ID, TaskID: f.tasks[0].ID, Response: "first side"})
	f.addSample(t, Sample{VariantID: f.variants[1].ID, TaskID: f.tasks[0].ID, Response: "second side"})
	f.createPairs(t)

	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	seen := make(map[string]string, 2)
	for _, it := range items {
		blinded, err := f.store.GetBlindedItem(ctx, it.ID)
		if err != nil {
			t.Fatalf("GetBlindedItem: %v", err)
		}
		seen[it.PresentationOrder] = blinded.ResponseA.Response
	}
	if seen[OrderAB] == seen[OrderBA] {
		t.Fatalf("both orders show %q as Response A — the swap did not happen", seen[OrderAB])
	}
}

func TestRecordVerdict_MarksItemJudged(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.fillGrid(t)
	f.createPairs(t)
	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	if _, err := f.store.RecordVerdict(ctx, Verdict{
		ItemID: items[0].ID, Winner: WinnerA, JudgeIdent: "claude-code",
		Dimensions: `{"task_success":"a"}`, Notes: "A answered the whole question",
	}); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}

	got, err := f.store.GetItem(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Status != ItemJudged {
		t.Errorf("item status = %q, want %q", got.Status, ItemJudged)
	}
	pending, err := f.store.ListPending(ctx, f.run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("got %d pending items, want 1 (the other order)", len(pending))
	}
}

// The operator's calibration mark sits alongside the judge's verdict rather
// than replacing it, and does not retire the item: an item only the operator
// has seen is still outstanding judge work.
func TestRecordVerdict_OperatorMarkCoexistsAndLeavesItemPending(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.fillGrid(t)
	f.createPairs(t)
	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	if _, err := f.store.RecordVerdict(ctx, Verdict{
		ItemID: items[0].ID, Winner: WinnerB, JudgeIdent: JudgeOperator,
	}); err != nil {
		t.Fatalf("RecordVerdict(operator): %v", err)
	}
	got, err := f.store.GetItem(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Status != ItemPending {
		t.Errorf("an operator mark alone set status to %q, want %q", got.Status, ItemPending)
	}

	if _, err := f.store.RecordVerdict(ctx, Verdict{
		ItemID: items[0].ID, Winner: WinnerA, JudgeIdent: "claude-code",
	}); err != nil {
		t.Fatalf("RecordVerdict(judge): %v", err)
	}
	verdicts, err := f.store.ListVerdicts(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	if len(verdicts) != 2 {
		t.Fatalf("got %d verdicts, want 2 (judge and operator side by side)", len(verdicts))
	}
}

// Re-judging overwrites the judge's own earlier call rather than stacking, so a
// retrying headless judge cannot inflate the tally.
func TestRecordVerdict_RejudgeOverwritesSameJudge(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.fillGrid(t)
	f.createPairs(t)
	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	for _, winner := range []string{WinnerA, WinnerTie, WinnerB} {
		if _, err := f.store.RecordVerdict(ctx, Verdict{
			ItemID: items[0].ID, Winner: winner, JudgeIdent: "claude-code",
		}); err != nil {
			t.Fatalf("RecordVerdict(%s): %v", winner, err)
		}
	}
	verdicts, err := f.store.ListVerdicts(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("got %d verdicts after three calls, want 1", len(verdicts))
	}
	if verdicts[0].Winner != WinnerB {
		t.Errorf("winner = %q, want the last call's %q", verdicts[0].Winner, WinnerB)
	}
}

func TestRecordVerdict_RejectsUnknownWinnerAndItem(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.fillGrid(t)
	f.createPairs(t)
	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	if _, err := f.store.RecordVerdict(ctx, Verdict{ItemID: items[0].ID, Winner: "maybe"}); err == nil {
		t.Error("RecordVerdict accepted an invalid winner")
	}
	if _, err := f.store.RecordVerdict(ctx, Verdict{ItemID: 9999, Winner: WinnerA}); err == nil {
		t.Error("RecordVerdict accepted an unknown item")
	}
}

func TestListPending_SampleNDrawsASubset(t *testing.T) {
	f := newPairFixture(t, 5, []string{CategoryChat, CategoryToolHeavy})
	ctx := context.Background()
	f.fillGrid(t)
	f.createPairs(t)

	all, err := f.store.ListPending(ctx, f.run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(all) != 20 {
		t.Fatalf("got %d pending items, want 20 (2 tasks x k=5 x 2 orders)", len(all))
	}
	sub, err := f.store.ListPending(ctx, f.run.ID, 0, 6)
	if err != nil {
		t.Fatalf("ListPending(sample_n): %v", err)
	}
	if len(sub) != 6 {
		t.Fatalf("sample_n=6 returned %d items", len(sub))
	}
	for _, it := range sub {
		if it.Prompt == "" {
			t.Errorf("pending item %d has no prompt", it.ItemID)
		}
	}
}
