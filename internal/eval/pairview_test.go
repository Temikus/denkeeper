package eval

import (
	"context"
	"testing"
)

// pairDetails runs the unblinded view over the fixture's run.
func (f *pairFixture) pairDetails(t *testing.T, taskID int64) *PairView {
	t.Helper()
	view, err := f.store.PairDetails(context.Background(), f.run.ID, taskID)
	if err != nil {
		t.Fatalf("PairDetails: %v", err)
	}
	return view
}

// onlyPair asserts the view holds exactly one pair and returns it.
func onlyPair(t *testing.T, view *PairView) PairDetail {
	t.Helper()
	if len(view.Pairs) != 1 {
		t.Fatalf("got %d pairs, want 1", len(view.Pairs))
	}
	return view.Pairs[0]
}

// judgeItem records one judge verdict on one item, naming a variant.
func (f *pairFixture) judgeItem(t *testing.T, it JudgmentItem, variant int64, ident string) {
	t.Helper()
	pair, err := f.store.GetPair(context.Background(), it.PairID)
	if err != nil {
		t.Fatalf("GetPair: %v", err)
	}
	assign, err := DecodeAssignment(pair.Assignment)
	if err != nil {
		t.Fatalf("DecodeAssignment: %v", err)
	}
	w := WinnerTie
	if variant != 0 {
		w = letterFor(assign, it.PresentationOrder, variant)
	}
	if _, err := f.store.RecordVerdict(context.Background(), Verdict{
		ItemID: it.ID, Winner: w, JudgeIdent: ident, RubricVersion: "v1",
	}); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}
}

func (f *pairFixture) items(t *testing.T) []JudgmentItem {
	t.Helper()
	items, err := f.store.ListItems(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	return items
}

// Both presentation orders naming the candidate is the only shape that scores a
// win, and the presented letters differ between the two orders — which is the
// resolution doing its work rather than passing the letter through.
func TestPairDetails_AgreeingOrdersResolveToTheCandidate(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	f.cleanRun(t)
	f.createPairs(t)
	cand := f.variants[1]
	f.judgeRun(t, func(Pair) int64 { return cand.ID })

	pair := onlyPair(t, f.pairDetails(t, 0))
	if pair.Outcome != PairOutcomeWin {
		t.Fatalf("outcome = %q, want %q", pair.Outcome, PairOutcomeWin)
	}
	if len(pair.Items) != 2 {
		t.Fatalf("got %d items, want both presentation orders", len(pair.Items))
	}
	for _, it := range pair.Items {
		if len(it.Verdicts) != 1 {
			t.Fatalf("item %d has %d verdicts, want 1", it.ItemID, len(it.Verdicts))
		}
		if got := it.Verdicts[0].WinnerVariant; got != cand.Name {
			t.Errorf("winner_variant = %q on order %s, want %q", got, it.PresentationOrder, cand.Name)
		}
	}
	if pair.Items[0].Verdicts[0].Winner == pair.Items[1].Verdicts[0].Winner {
		t.Error("both orders carry the same presented letter — the swap did not happen")
	}
}

func TestPairDetails_AgreeingOnTheBaselineIsALoss(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	f.cleanRun(t)
	f.createPairs(t)
	base := f.variants[0]
	f.judgeRun(t, func(Pair) int64 { return base.ID })

	pair := onlyPair(t, f.pairDetails(t, 0))
	if pair.Outcome != PairOutcomeLoss {
		t.Fatalf("outcome = %q, want %q", pair.Outcome, PairOutcomeLoss)
	}
	for _, it := range pair.Items {
		if got := it.Verdicts[0].WinnerVariant; got != base.Name {
			t.Errorf("winner_variant = %q, want the baseline %q", got, base.Name)
		}
	}
}

// Naming the same *presented* letter in both orders means the judge tracked
// position, not quality. The view must say tie, exactly as the tally does.
func TestPairDetails_DisagreementBetweenOrdersIsATie(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.cleanRun(t)
	f.createPairs(t)

	for _, it := range f.items(t) {
		if _, err := f.store.RecordVerdict(ctx, Verdict{
			ItemID: it.ID, Winner: WinnerA, JudgeIdent: "claude-code",
		}); err != nil {
			t.Fatalf("RecordVerdict: %v", err)
		}
	}

	pair := onlyPair(t, f.pairDetails(t, 0))
	if pair.Outcome != PairOutcomeTie {
		t.Fatalf("outcome = %q, want %q when the two orders disagree", pair.Outcome, PairOutcomeTie)
	}
}

func TestPairDetails_HalfJudgedPairIsPending(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	f.cleanRun(t)
	f.createPairs(t)
	items := f.items(t)
	f.judgeItem(t, items[0], f.variants[1].ID, "claude-code")

	pair := onlyPair(t, f.pairDetails(t, 0))
	if pair.Outcome != PairOutcomePending {
		t.Fatalf("outcome = %q, want %q with one order unjudged", pair.Outcome, PairOutcomePending)
	}
	var judged int
	for _, it := range pair.Items {
		judged += len(it.Verdicts)
	}
	if judged != 1 {
		t.Errorf("listed %d verdicts, want the single one recorded", judged)
	}
}

// The operator's calibration marks are ordinary verdict rows. They belong in
// the view — the operator wants to see their own call next to the judge's — but
// they must never move the outcome.
func TestPairDetails_OperatorVerdictIsListedButDoesNotDecide(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	f.cleanRun(t)
	f.createPairs(t)
	items := f.items(t)
	base, cand := f.variants[0], f.variants[1]

	// The judge works only one order; the operator marks both, disagreeing.
	f.judgeItem(t, items[0], cand.ID, "claude-code")
	for _, it := range items {
		f.judgeItem(t, it, base.ID, JudgeOperator)
	}

	pair := onlyPair(t, f.pairDetails(t, 0))
	if pair.Outcome != PairOutcomePending {
		t.Fatalf("outcome = %q, want %q — an operator mark is not judge work",
			pair.Outcome, PairOutcomePending)
	}
	var operatorRows int
	for _, it := range pair.Items {
		for _, v := range it.Verdicts {
			if v.JudgeIdent == JudgeOperator {
				operatorRows++
				if v.WinnerVariant != base.Name {
					t.Errorf("operator winner_variant = %q, want %q", v.WinnerVariant, base.Name)
				}
			}
		}
	}
	if operatorRows != 2 {
		t.Errorf("listed %d operator verdicts, want 2", operatorRows)
	}
}

// Each side must name the variant that produced it and the sample it produced,
// so the results view can fetch the two transcripts.
func TestPairDetails_SidesNameTheirVariantAndSample(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	byVariant := make(map[int64]int64, len(f.variants))
	for _, v := range f.variants {
		smp := f.addSample(t, Sample{
			VariantID: v.ID, TaskID: f.tasks[0].ID, KIndex: 0,
			Response: "an answer", Rounds: 2, Cost: 0.1, OutcomeOK: 5,
		})
		byVariant[v.ID] = smp.ID
	}
	f.createPairs(t)

	view := f.pairDetails(t, 0)
	if view.BaselineVariant != f.variants[0].Name {
		t.Errorf("baseline_variant = %q, want %q", view.BaselineVariant, f.variants[0].Name)
	}
	pair := onlyPair(t, view)
	if pair.Baseline.VariantID != f.variants[0].ID || pair.Candidate.VariantID != f.variants[1].ID {
		t.Fatalf("sides = %+v / %+v, want baseline then candidate", pair.Baseline, pair.Candidate)
	}
	if pair.Baseline.SampleID != byVariant[f.variants[0].ID] {
		t.Errorf("baseline sample = %d, want %d", pair.Baseline.SampleID, byVariant[f.variants[0].ID])
	}
	if pair.Candidate.SampleID != byVariant[f.variants[1].ID] {
		t.Errorf("candidate sample = %d, want %d", pair.Candidate.SampleID, byVariant[f.variants[1].ID])
	}
	if pair.TaskPrompt != f.tasks[0].Prompt || pair.Category != f.tasks[0].Category {
		t.Errorf("task = %q/%q, want %q/%q", pair.TaskPrompt, pair.Category,
			f.tasks[0].Prompt, f.tasks[0].Category)
	}
}

func TestPairDetails_TaskFilterNarrowsToOneTask(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat, CategoryToolHeavy})
	f.cleanRun(t)
	f.createPairs(t)

	if got := len(f.pairDetails(t, 0).Pairs); got != 2 {
		t.Fatalf("unfiltered pairs = %d, want 2", got)
	}
	view := f.pairDetails(t, f.tasks[1].ID)
	pair := onlyPair(t, view)
	if pair.TaskID != f.tasks[1].ID {
		t.Errorf("task_id = %d, want %d", pair.TaskID, f.tasks[1].ID)
	}
}

func TestPairDetails_UnjudgedRunListsEveryPairAsPending(t *testing.T) {
	f := newPairFixture(t, 2, []string{CategoryChat})
	f.cleanRun(t)
	f.createPairs(t)

	view := f.pairDetails(t, 0)
	if len(view.Pairs) != 2 {
		t.Fatalf("got %d pairs, want one per k", len(view.Pairs))
	}
	for _, p := range view.Pairs {
		if p.Outcome != PairOutcomePending {
			t.Errorf("pair %d outcome = %q, want %q", p.PairID, p.Outcome, PairOutcomePending)
		}
		if p.Items == nil {
			t.Errorf("pair %d items is null, want an empty array the UI can iterate", p.PairID)
		}
	}
	if view.Pairs[0].K == view.Pairs[1].K {
		t.Error("both pairs report the same k, want one per sample index")
	}
}

func TestPairDetails_UnknownRunIsNotFound(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.PairDetails(context.Background(), 9999, 0); err == nil {
		t.Fatal("PairDetails on an unknown run returned no error")
	}
}

func TestPairDetails_VerdictCarriesDimensionsAndRubricVersion(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.cleanRun(t)
	f.createPairs(t)
	items := f.items(t)

	if _, err := f.store.RecordVerdict(ctx, Verdict{
		ItemID: items[0].ID, Winner: WinnerA, JudgeIdent: "claude-code",
		Dimensions:    `{"task_success":"a","length":"b"}`,
		Notes:         "A answered the whole question",
		RubricVersion: "v1",
	}); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}

	pair := onlyPair(t, f.pairDetails(t, 0))
	v := pair.Items[0].Verdicts[0]
	if v.RubricVersion != "v1" {
		t.Errorf("rubric_version = %q, want v1", v.RubricVersion)
	}
	if v.Dimensions["task_success"] != "a" || v.Dimensions["length"] != "b" {
		t.Errorf("dimensions = %v, want the recorded pair", v.Dimensions)
	}
	if v.Notes != "A answered the whole question" {
		t.Errorf("notes = %q, want the recorded text", v.Notes)
	}
}

// A tie names no variant: an empty winner_variant is the honest rendering, not
// a guess at whichever side sorted first.
func TestPairDetails_TieVerdictNamesNoVariant(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	f.cleanRun(t)
	f.createPairs(t)
	f.judgeRun(t, func(Pair) int64 { return 0 })

	pair := onlyPair(t, f.pairDetails(t, 0))
	if pair.Outcome != PairOutcomeTie {
		t.Fatalf("outcome = %q, want %q", pair.Outcome, PairOutcomeTie)
	}
	for _, it := range pair.Items {
		if it.Verdicts[0].Winner != WinnerTie || it.Verdicts[0].WinnerVariant != "" {
			t.Errorf("tie verdict = %+v, want winner tie and no variant", it.Verdicts[0])
		}
	}
}
