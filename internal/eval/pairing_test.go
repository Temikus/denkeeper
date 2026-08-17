package eval

import (
	"context"
	"testing"
	"time"
)

// pairFixture builds a run whose samples a test writes by hand, so pairing is
// exercised against an exactly known grid — including the holes a capped or
// stopped run leaves behind.
type pairFixture struct {
	store    *Store
	run      *Run
	tasks    []Task
	variants []Variant
}

func newPairFixture(t *testing.T, k int, taskCategories []string, variantNames ...string) *pairFixture {
	t.Helper()
	store := newTestStore(t)
	set := mustTaskSet(t, store, "set")

	tasks := make([]Task, 0, len(taskCategories))
	for i, cat := range taskCategories {
		task, err := store.AddTask(context.Background(), set.ID, Task{
			Prompt:   "prompt " + cat,
			Category: cat,
			Notes:    "what good looks like",
		})
		if err != nil {
			t.Fatalf("adding task %d: %v", i, err)
		}
		tasks = append(tasks, *task)
	}

	if len(variantNames) == 0 {
		variantNames = []string{"incumbent", "candidate"}
	}
	specs := make([]Variant, 0, len(variantNames))
	for _, name := range variantNames {
		v := Variant{Name: name}
		if name != "incumbent" {
			v.Overlay = `{"llm_model":"kimi-k3"}`
		}
		specs = append(specs, v)
	}
	run, variants, err := store.CreateRun(context.Background(), Run{
		TaskSetID: set.ID, BaseAgent: "pamela", K: k, CostCap: 2.0, AsOf: time.Now().UTC(),
	}, specs)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return &pairFixture{store: store, run: run, tasks: tasks, variants: variants}
}

// addSample writes one sample row and returns it.
func (f *pairFixture) addSample(t *testing.T, smp Sample) Sample {
	t.Helper()
	smp.RunID = f.run.ID
	if smp.Status == "" {
		smp.Status = SampleOK
	}
	out, err := f.store.AddSample(context.Background(), smp)
	if err != nil {
		t.Fatalf("AddSample: %v", err)
	}
	return *out
}

// fillGrid writes an ok sample for every (task, k, variant) cell.
func (f *pairFixture) fillGrid(t *testing.T) {
	t.Helper()
	for _, task := range f.tasks {
		for k := 0; k < f.run.K; k++ {
			for _, v := range f.variants {
				f.addSample(t, Sample{
					VariantID: v.ID, TaskID: task.ID, KIndex: k,
					Response: v.Name + " answered", Rounds: 2, Cost: 0.01,
				})
			}
		}
	}
}

func (f *pairFixture) createPairs(t *testing.T) int {
	t.Helper()
	n, err := f.store.CreatePairs(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("CreatePairs: %v", err)
	}
	return n
}

func TestCreatePairs_FullGridPairsEveryTaskAndK(t *testing.T) {
	f := newPairFixture(t, 2, []string{CategoryChat, CategoryToolHeavy})
	f.fillGrid(t)

	if n := f.createPairs(t); n != 4 {
		t.Fatalf("CreatePairs = %d pairs, want 4 (2 tasks x k=2)", n)
	}

	items, err := f.store.ListItems(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 8 {
		t.Fatalf("got %d judgment items, want 8 (2 orders per pair)", len(items))
	}

	// Every pair must carry exactly one item of each presentation order —
	// that is the whole position-bias control.
	orders := make(map[int64]map[string]int)
	for _, it := range items {
		if orders[it.PairID] == nil {
			orders[it.PairID] = make(map[string]int)
		}
		orders[it.PairID][it.PresentationOrder]++
		if it.Status != ItemPending {
			t.Errorf("item %d starts %q, want %q", it.ID, it.Status, ItemPending)
		}
	}
	for pairID, counts := range orders {
		if counts[OrderAB] != 1 || counts[OrderBA] != 1 {
			t.Errorf("pair %d has orders %v, want one of each", pairID, counts)
		}
	}
}

func TestCreatePairs_SkipsCellsWithMissingOrFailedSamples(t *testing.T) {
	// A capped run: task 0 completed both sides at k=0, task 0's k=1 candidate
	// failed, and task 1 never got dispatched at all.
	f := newPairFixture(t, 2, []string{CategoryChat, CategoryToolHeavy})
	inc, cand := f.variants[0], f.variants[1]
	t0 := f.tasks[0]

	f.addSample(t, Sample{VariantID: inc.ID, TaskID: t0.ID, KIndex: 0, Response: "a"})
	f.addSample(t, Sample{VariantID: cand.ID, TaskID: t0.ID, KIndex: 0, Response: "b"})
	f.addSample(t, Sample{VariantID: inc.ID, TaskID: t0.ID, KIndex: 1, Response: "a"})
	f.addSample(t, Sample{VariantID: cand.ID, TaskID: t0.ID, KIndex: 1,
		Status: SampleFailed, Error: "provider timeout"})

	if n := f.createPairs(t); n != 1 {
		t.Fatalf("CreatePairs = %d, want 1 — only the fully-completed cell is pairable", n)
	}
	pairs, err := f.store.ListPairs(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("ListPairs: %v", err)
	}
	if pairs[0].TaskID != t0.ID || pairs[0].KIndex != 0 {
		t.Errorf("paired (task %d, k %d), want (task %d, k 0)", pairs[0].TaskID, pairs[0].KIndex, t0.ID)
	}
}

func TestCreatePairs_IsIdempotent(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	f.fillGrid(t)

	if n := f.createPairs(t); n != 1 {
		t.Fatalf("first CreatePairs = %d, want 1", n)
	}
	if n := f.createPairs(t); n != 0 {
		t.Fatalf("second CreatePairs = %d, want 0 — a re-run must not double the queue", n)
	}
	got, err := f.store.CountPairs(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("CountPairs: %v", err)
	}
	if got != 1 {
		t.Fatalf("run holds %d pairs after two finalizations, want 1", got)
	}
}

// With more than two variants every candidate pairs against the baseline
// rather than round-robin against each other: the question the decision rule
// answers is "is this candidate an upgrade on the incumbent".
func TestCreatePairs_ThreeVariantsPairAgainstBaselineOnly(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat}, "incumbent", "candidate-a", "candidate-b")
	f.fillGrid(t)

	if n := f.createPairs(t); n != 2 {
		t.Fatalf("CreatePairs = %d, want 2 (N-1 pair sets, not round-robin)", n)
	}
	pairs, err := f.store.ListPairs(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("ListPairs: %v", err)
	}
	baseline := f.variants[0].ID
	for _, p := range pairs {
		assign, err := DecodeAssignment(p.Assignment)
		if err != nil {
			t.Fatalf("DecodeAssignment: %v", err)
		}
		if assign.A != baseline && assign.B != baseline {
			t.Errorf("pair %d compares %d vs %d, neither of which is the baseline %d",
				p.ID, assign.A, assign.B, baseline)
		}
	}
}

func TestCreatePairs_AssignmentMatchesSampleOrder(t *testing.T) {
	// Whichever way the coin lands, sample_a must belong to the variant the
	// assignment names as A — a mismatch would unblind every verdict backwards.
	f := newPairFixture(t, 4, []string{CategoryChat})
	f.fillGrid(t)
	f.createPairs(t)

	ctx := context.Background()
	pairs, err := f.store.ListPairs(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListPairs: %v", err)
	}
	for _, p := range pairs {
		assign, err := DecodeAssignment(p.Assignment)
		if err != nil {
			t.Fatalf("DecodeAssignment: %v", err)
		}
		first, err := f.store.GetSample(ctx, p.SampleA)
		if err != nil {
			t.Fatalf("GetSample: %v", err)
		}
		second, err := f.store.GetSample(ctx, p.SampleB)
		if err != nil {
			t.Fatalf("GetSample: %v", err)
		}
		if first.VariantID != assign.A {
			t.Errorf("pair %d: sample_a is variant %d but assignment says %d",
				p.ID, first.VariantID, assign.A)
		}
		if second.VariantID != assign.B {
			t.Errorf("pair %d: sample_b is variant %d but assignment says %d",
				p.ID, second.VariantID, assign.B)
		}
	}
}

func TestVariantFor_UnblindsThroughPresentationOrder(t *testing.T) {
	assign := Assignment{A: 10, B: 20}
	cases := []struct {
		order, winner string
		want          int64
	}{
		{OrderAB, WinnerA, 10},
		{OrderAB, WinnerB, 20},
		{OrderBA, WinnerA, 20}, // shown first was the pair's B
		{OrderBA, WinnerB, 10},
		{OrderAB, WinnerTie, 0},
		{OrderBA, WinnerTie, 0},
	}
	for _, c := range cases {
		if got := VariantFor(assign, c.order, c.winner); got != c.want {
			t.Errorf("VariantFor(order=%s, winner=%s) = %d, want %d", c.order, c.winner, got, c.want)
		}
	}
}
