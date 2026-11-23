package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
		TokensPrompt: 900, TokensCompletion: 210, Upstream: "Fireworks"})
	f.addSample(t, Sample{VariantID: cand.ID, TaskID: task.ID, KIndex: 0,
		Response: "no entries for today", Rounds: 3, Cost: 0.9876, LatencyMs: 7373,
		TokensPrompt: 950, TokensCompletion: 260, Trace: string(trace), Upstream: "Together"})
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
			"Fireworks", "Together", "upstream",
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

// judgedFixture is a one-pair run (two items) with pairs created and no
// verdicts yet.
func judgedFixture(t *testing.T) (*pairFixture, []JudgmentItem) {
	t.Helper()
	f := newPairFixture(t, 1, []string{CategoryChat})
	f.fillGrid(t)
	f.createPairs(t)
	items, err := f.store.ListItems(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	return f, items
}

func mustRecord(t *testing.T, s *Store, itemID int64, winner, ident string) {
	t.Helper()
	if _, err := s.RecordVerdict(context.Background(), Verdict{
		ItemID: itemID, Winner: winner, JudgeIdent: ident,
	}); err != nil {
		t.Fatalf("RecordVerdict(%d, %s, %s): %v", itemID, winner, ident, err)
	}
}

func verdictsBy(t *testing.T, s *Store, runID int64, ident string) []Verdict {
	t.Helper()
	all, err := s.ListVerdicts(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	var out []Verdict
	for _, v := range all {
		if v.JudgeIdent == ident {
			out = append(out, v)
		}
	}
	return out
}

func TestRecordVerdict_OperatorMarkOnJudgedItemIsRefused(t *testing.T) {
	f, items := judgedFixture(t)
	mustRecord(t, f.store, items[0].ID, WinnerA, "claude-code")

	_, err := f.store.RecordVerdict(context.Background(), Verdict{
		ItemID: items[0].ID, Winner: WinnerB, JudgeIdent: JudgeOperator,
	})
	if !errors.Is(err, ErrMarkNotBlind) {
		t.Fatalf("err = %v, want ErrMarkNotBlind", err)
	}
	if !strings.Contains(err.Error(), "already has a judge verdict") {
		t.Errorf("err = %q, want it to say the item already has a judge verdict", err)
	}
	if ops := verdictsBy(t, f.store, f.run.ID, JudgeOperator); len(ops) != 0 {
		t.Errorf("stored %d operator rows, want 0", len(ops))
	}
}

func TestRecordVerdict_OperatorMayChangeMarkWhilePending(t *testing.T) {
	f, items := judgedFixture(t)
	mustRecord(t, f.store, items[0].ID, WinnerA, JudgeOperator)
	mustRecord(t, f.store, items[0].ID, WinnerB, JudgeOperator)

	ops := verdictsBy(t, f.store, f.run.ID, JudgeOperator)
	if len(ops) != 1 {
		t.Fatalf("got %d operator rows, want 1", len(ops))
	}
	if ops[0].Winner != WinnerB {
		t.Errorf("operator winner = %q, want the second call's %q", ops[0].Winner, WinnerB)
	}
}

func TestRecordVerdict_FullyJudgedRunTakesNoVerdicts(t *testing.T) {
	f, items := judgedFixture(t)
	for _, it := range items {
		mustRecord(t, f.store, it.ID, WinnerA, "claude-code")
	}

	_, err := f.store.RecordVerdict(context.Background(), Verdict{
		ItemID: items[0].ID, Winner: WinnerB, JudgeIdent: "claude-code",
	})
	if !errors.Is(err, ErrJudgingClosed) {
		t.Fatalf("err = %v, want ErrJudgingClosed", err)
	}
	if !strings.Contains(err.Error(), "takes no more verdicts") {
		t.Errorf("err = %q, want it to say the run takes no more verdicts", err)
	}
	// A different judge identity is refused too: the run is closed, not the ident.
	if _, err := f.store.RecordVerdict(context.Background(), Verdict{
		ItemID: items[1].ID, Winner: WinnerB, JudgeIdent: "another-judge",
	}); !errors.Is(err, ErrJudgingClosed) {
		t.Errorf("second judge: err = %v, want ErrJudgingClosed", err)
	}
	for _, v := range verdictsBy(t, f.store, f.run.ID, "claude-code") {
		if v.Winner != WinnerA {
			t.Errorf("item %d winner = %q, want the stored %q", v.ItemID, v.Winner, WinnerA)
		}
	}
}

// Re-judging stays open while any item of the run is pending, so a retrying
// headless judge keeps its overwrite.
func TestRecordVerdict_RejudgeAllowedWhileRunHasPendingItems(t *testing.T) {
	f, items := judgedFixture(t)
	mustRecord(t, f.store, items[0].ID, WinnerA, "claude-code")
	mustRecord(t, f.store, items[0].ID, WinnerB, "claude-code")

	got := verdictsBy(t, f.store, f.run.ID, "claude-code")
	if len(got) != 1 || got[0].Winner != WinnerB {
		t.Errorf("verdicts = %+v, want one row with winner %q", got, WinnerB)
	}
}

func TestPendingItems_CountsOneRunOnly(t *testing.T) {
	f, items := judgedFixture(t)
	ctx := context.Background()

	// A second run in the same store, left unjudged.
	other, variants, err := f.store.CreateRun(ctx, Run{
		TaskSetID: f.run.TaskSetID, BaseAgent: "pamela", K: 1, CostCap: 2.0, AsOf: time.Now().UTC(),
	}, []Variant{{Name: "incumbent"}, {Name: "candidate", Overlay: `{"llm_model":"kimi-k3"}`}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	for _, v := range variants {
		if _, err := f.store.AddSample(ctx, Sample{
			RunID: other.ID, VariantID: v.ID, TaskID: f.tasks[0].ID, Status: SampleOK, Response: "x",
		}); err != nil {
			t.Fatalf("AddSample: %v", err)
		}
	}
	if _, err := f.store.CreatePairs(ctx, other.ID); err != nil {
		t.Fatalf("CreatePairs: %v", err)
	}

	for _, it := range items {
		mustRecord(t, f.store, it.ID, WinnerA, "claude-code")
	}
	if n, err := f.store.PendingItems(ctx, f.run.ID); err != nil || n != 0 {
		t.Errorf("PendingItems(judged run) = %d, %v; want 0", n, err)
	}
	if n, err := f.store.PendingItems(ctx, other.ID); err != nil || n != 2 {
		t.Errorf("PendingItems(other run) = %d, %v; want 2", n, err)
	}
}

func TestPendingItems_FallsToZeroOnlyOnLastJudgeVerdict(t *testing.T) {
	f, items := judgedFixture(t)
	ctx := context.Background()
	pending := func() int {
		t.Helper()
		n, err := f.store.PendingItems(ctx, f.run.ID)
		if err != nil {
			t.Fatalf("PendingItems: %v", err)
		}
		return n
	}

	if n := pending(); n != 2 {
		t.Fatalf("fresh run: %d pending, want 2", n)
	}
	for _, it := range items {
		mustRecord(t, f.store, it.ID, WinnerA, JudgeOperator)
	}
	if n := pending(); n != 2 {
		t.Errorf("after operator marks: %d pending, want 2", n)
	}
	mustRecord(t, f.store, items[0].ID, WinnerA, "claude-code")
	if n := pending(); n != 1 {
		t.Errorf("after one judge verdict: %d pending, want 1", n)
	}
	mustRecord(t, f.store, items[1].ID, WinnerA, "claude-code")
	if n := pending(); n != 0 {
		t.Errorf("after the last judge verdict: %d pending, want 0", n)
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
