package eval

import (
	"context"
	"testing"
	"time"
)

// summaryFixture builds a run with two tasks and two variants and lets a test
// write sample rows by hand, so the aggregation maths is exercised against
// exactly known inputs.
type summaryFixture struct {
	store    *Store
	run      *Run
	tasks    []Task
	variants []Variant
}

func newSummaryFixture(t *testing.T, k int) *summaryFixture {
	t.Helper()
	store := newTestStore(t)
	set := mustTaskSet(t, store, "set")
	tasks := []Task{*mustTask(t, store, set.ID, "first"), *mustTask(t, store, set.ID, "second")}
	run, variants, err := store.CreateRun(context.Background(), Run{
		TaskSetID: set.ID, BaseAgent: "pamela", K: k, CostCap: 2.0, AsOf: time.Now().UTC(),
	}, []Variant{{Name: "incumbent"}, {Name: "candidate", Overlay: `{"llm_model":"kimi-k3"}`}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return &summaryFixture{store: store, run: run, tasks: tasks, variants: variants}
}

func (f *summaryFixture) add(t *testing.T, smp Sample) {
	t.Helper()
	smp.RunID = f.run.ID
	if smp.Status == "" {
		smp.Status = SampleOK
	}
	if _, err := f.store.AddSample(context.Background(), smp); err != nil {
		t.Fatalf("AddSample: %v", err)
	}
}

func (f *summaryFixture) summarize(t *testing.T, floor float64) *Summary {
	t.Helper()
	sum, err := f.store.Summarize(context.Background(), f.run.ID,
		SummaryOpts{CompletenessFloor: floor})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	return sum
}

func variantByName(t *testing.T, sum *Summary, name string) VariantMetrics {
	t.Helper()
	for _, v := range sum.Variants {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("variant %q missing from summary", name)
	return VariantMetrics{}
}

func TestSummary_RatesAndDeltas(t *testing.T) {
	f := newSummaryFixture(t, 1)
	inc, cand := f.variants[0], f.variants[1]

	// Incumbent: 8 executed tool calls, 1 rejected, 1 failed. Plus cached and
	// suppressed calls, which must not enter the denominator.
	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[0].ID, Rounds: 2, Cost: 0.10, LatencyMs: 1000,
		OutcomeOK: 3, OutcomeRejected: 1, OutcomeCached: 5, OutcomeSuppressed: 7})
	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[1].ID, Rounds: 4, Cost: 0.30, LatencyMs: 3000,
		OutcomeOK: 3, OutcomeFailed: 1})

	// Candidate: costlier, more rounds, no tool faults.
	f.add(t, Sample{VariantID: cand.ID, TaskID: f.tasks[0].ID, Rounds: 3, Cost: 0.20, LatencyMs: 1500,
		OutcomeOK: 4})
	f.add(t, Sample{VariantID: cand.ID, TaskID: f.tasks[1].ID, Rounds: 7, Cost: 0.60, LatencyMs: 4500,
		OutcomeOK: 4, OutcomeDenied: 0})

	sum := f.summarize(t, 0.8)

	incM := variantByName(t, sum, "incumbent")
	if incM.ToolCalls != 8 {
		t.Errorf("tool_calls = %d, want 8 — cached and suppressed calls executed nothing and must be excluded", incM.ToolCalls)
	}
	if incM.RejectedRate != 0.125 {
		t.Errorf("rejected_rate = %v, want 1/8", incM.RejectedRate)
	}
	if incM.FailedRate != 0.125 {
		t.Errorf("failed_rate = %v, want 1/8", incM.FailedRate)
	}
	if incM.MeanRounds != 3 {
		t.Errorf("mean_rounds = %v, want 3", incM.MeanRounds)
	}
	if incM.MeanCostPerTask != 0.20 {
		t.Errorf("mean_cost_per_task = %v, want 0.40/2 tasks", incM.MeanCostPerTask)
	}
	if incM.MeanLatencyMs != 2000 {
		t.Errorf("mean_latency_ms = %v, want 2000", incM.MeanLatencyMs)
	}

	candM := variantByName(t, sum, "candidate")
	if candM.RejectedRate != 0 || candM.FailedRate != 0 {
		t.Errorf("candidate rates = %v/%v, want 0/0", candM.RejectedRate, candM.FailedRate)
	}
	if candM.Overlay.Model != "kimi-k3" {
		t.Errorf("overlay model = %q, want it decoded into the scorecard", candM.Overlay.Model)
	}

	if sum.BaselineVariant != "incumbent" {
		t.Errorf("baseline = %q, want the first variant by creation order", sum.BaselineVariant)
	}
	if len(sum.PerTask) != 2 {
		t.Fatalf("per_task has %d entries, want 2", len(sum.PerTask))
	}
	first := sum.PerTask[0]
	if first.Variants[0].DeltaCost != 0 || first.Variants[0].DeltaRounds != 0 {
		t.Errorf("baseline row carries deltas %+v, want zeros", first.Variants[0])
	}
	if got := first.Variants[1].DeltaCost; got < 0.099 || got > 0.101 {
		t.Errorf("candidate delta_cost = %v, want +0.10", got)
	}
	if got := first.Variants[1].DeltaRounds; got != 1 {
		t.Errorf("candidate delta_rounds = %v, want +1", got)
	}
	if got := first.Variants[1].DeltaLatency; got != 500 {
		t.Errorf("candidate delta_latency_ms = %v, want +500", got)
	}
}

func TestSummary_WrapupCountUsesStopReasonSlugs(t *testing.T) {
	f := newSummaryFixture(t, 2)
	inc := f.variants[0]

	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[0].ID, StopReason: "max_rounds"})
	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[0].ID, KIndex: 1, StopReason: "repeated_calls"})
	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[1].ID, StopReason: ""})
	// stop_requested is a cancellation, not the model flailing, so it must not
	// count as a wrap-up.
	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[1].ID, KIndex: 1, StopReason: "stop_requested"})

	sum := f.summarize(t, 0.8)
	if got := variantByName(t, sum, "incumbent").WrapupCount; got != 2 {
		t.Errorf("wrapup_count = %d, want 2 (max_rounds + repeated_calls only)", got)
	}
}

func TestSummary_FailedSamplesExcludedFromMeans(t *testing.T) {
	f := newSummaryFixture(t, 1)
	inc := f.variants[0]

	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[0].ID, Rounds: 2, Cost: 0.10, LatencyMs: 100})
	f.add(t, Sample{VariantID: inc.ID, TaskID: f.tasks[1].ID, Status: SampleFailed,
		Error: "provider timeout", Rounds: 99, Cost: 9.99, LatencyMs: 99999})

	m := variantByName(t, f.summarize(t, 0.8), "incumbent")
	if m.SamplesOK != 1 || m.SamplesFailed != 1 {
		t.Fatalf("sample counts ok/failed = %d/%d, want 1/1", m.SamplesOK, m.SamplesFailed)
	}
	if m.MeanRounds != 2 {
		t.Errorf("mean_rounds = %v, want 2 — a failed sample carries no measurement", m.MeanRounds)
	}
	if m.MeanLatencyMs != 100 {
		t.Errorf("mean_latency_ms = %v, want 100", m.MeanLatencyMs)
	}
}

func TestSummary_CompletenessFloorMarksInconclusive(t *testing.T) {
	f := newSummaryFixture(t, 2) // 2 tasks × 2 variants × k=2 = 8 expected

	for _, v := range f.variants {
		for _, task := range f.tasks {
			f.add(t, Sample{VariantID: v.ID, TaskID: task.ID, Rounds: 1})
		}
	}
	// 4 of 8 landed: half, below the 0.8 floor.
	sum := f.summarize(t, 0.8)
	if sum.Completeness.SamplesExpected != 8 {
		t.Fatalf("samples_expected = %d, want tasks×variants×k = 8", sum.Completeness.SamplesExpected)
	}
	if sum.Completeness.SamplesOK != 4 {
		t.Fatalf("samples_ok = %d, want 4", sum.Completeness.SamplesOK)
	}
	if sum.Completeness.Ratio != 0.5 {
		t.Errorf("ratio = %v, want 0.5", sum.Completeness.Ratio)
	}
	if sum.Completeness.Conclusive {
		t.Error("run below the floor reported as conclusive")
	}
	if sum.Completeness.Floor != 0.8 {
		t.Errorf("floor = %v, want it echoed back", sum.Completeness.Floor)
	}
}

func TestSummary_CompletenessAtFloorIsConclusive(t *testing.T) {
	f := newSummaryFixture(t, 1) // 2 tasks × 2 variants × k=1 = 4 expected

	for _, v := range f.variants {
		for _, task := range f.tasks {
			f.add(t, Sample{VariantID: v.ID, TaskID: task.ID, Rounds: 1})
		}
	}
	sum := f.summarize(t, 1.0)
	if !sum.Completeness.Conclusive {
		t.Errorf("ratio %v at floor %v reported inconclusive; the floor is inclusive",
			sum.Completeness.Ratio, sum.Completeness.Floor)
	}
}

func TestSummary_EmptyRunReportsZeroesNotErrors(t *testing.T) {
	f := newSummaryFixture(t, 1)
	sum := f.summarize(t, 0.8)

	if len(sum.Variants) != 2 {
		t.Fatalf("got %d variant rows, want 2 even with no samples", len(sum.Variants))
	}
	for _, v := range sum.Variants {
		if v.RejectedRate != 0 || v.MeanRounds != 0 || v.SamplesOK != 0 {
			t.Errorf("variant %q = %+v, want zeroes", v.Name, v)
		}
	}
	if sum.Completeness.Conclusive {
		t.Error("a run with no samples cannot be conclusive")
	}
}

func TestSummary_CarriesRunIdentity(t *testing.T) {
	f := newSummaryFixture(t, 3)
	sum := f.summarize(t, 0.8)

	if sum.RunID != f.run.ID || sum.BaseAgent != "pamela" || sum.TaskSet != "set" || sum.K != 3 {
		t.Errorf("summary identity = %+v, want run %d / pamela / set / k=3", sum, f.run.ID)
	}
	if sum.CostCap != 2.0 {
		t.Errorf("cost_cap = %v, want 2.0", sum.CostCap)
	}
}
