package eval

import (
	"context"
	"testing"
	"time"
)

// scheduledPrompt is a header the scheduler wrote on 2026-07-07, six hours
// after the schedule's previous fire.
const scheduledPrompt = "[Scheduled: digest | 2026-07-07T12:00:00Z UTC | 2026-W28 | last run 2026-07-07T06:00:00Z (6h ago)]"

// scheduledAtAsOf is scheduledPrompt moved to scheduledAsOf, keeping the gap.
const scheduledAtAsOf = "[Scheduled: digest | 2026-09-01T08:00:00Z UTC | 2026-W36 | last run 2026-09-01T02:00:00Z (6h ago)]"

var scheduledAsOf = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

// runAsOf runs every task of the fixture's set under asOf with two variants
// and k=1, and waits for the run to finish and pair.
func runAsOf(t *testing.T, f *runnerFixture, asOf time.Time) *Run {
	t.Helper()
	run, _, err := f.store.CreateRun(context.Background(), Run{
		TaskSetID: f.setID, BaseAgent: "pamela", K: 1, CostCap: 10, AsOf: asOf,
	}, twoVariants())
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if got := waitForTerminal(t, f.store, run.ID); got.Status != StatusDone {
		t.Fatalf("status = %q, want %q (error %q)", got.Status, StatusDone, got.Error)
	}
	return run
}

func TestPromptForRun_ScheduledHeaderMovesToAsOf(t *testing.T) {
	if got := PromptForRun(scheduledPrompt, scheduledAsOf); got != scheduledAtAsOf {
		t.Errorf("got %q, want %q", got, scheduledAtAsOf)
	}
}

func TestPromptForRun_ZeroAsOfLeavesPrompt(t *testing.T) {
	if got := PromptForRun(scheduledPrompt, time.Time{}); got != scheduledPrompt {
		t.Errorf("got %q, want the stored prompt", got)
	}
}

func TestRunner_ScheduledTaskSentAtRunAsOf(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	task := f.addTasks(t, scheduledPrompt)[0]
	runAsOf(t, f, scheduledAsOf)

	for _, text := range f.engine.seenTexts() {
		if text != scheduledAtAsOf {
			t.Errorf("engine got %q, want %q", text, scheduledAtAsOf)
		}
	}
	stored, err := f.store.getTaskByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("getTaskByID: %v", err)
	}
	if stored.Prompt != scheduledPrompt {
		t.Errorf("stored prompt = %q, want the original %q", stored.Prompt, scheduledPrompt)
	}
}

func TestRunner_ChatTaskSentVerbatim(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	const prompt = "What changed in [Scheduled: digest] since Monday?"
	f.addTasks(t, prompt)
	runAsOf(t, f, scheduledAsOf)

	for _, text := range f.engine.seenTexts() {
		if text != prompt {
			t.Errorf("engine got %q, want %q", text, prompt)
		}
	}
}

func TestGetBlindedItem_ScheduledPromptMatchesWhatWasSent(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, scheduledPrompt)
	run := runAsOf(t, f, scheduledAsOf)

	pending, err := f.store.ListPending(context.Background(), run.ID, 0, 0)
	if err != nil || len(pending) == 0 {
		t.Fatalf("ListPending: %d items, %v", len(pending), err)
	}
	item, err := f.store.GetBlindedItem(context.Background(), pending[0].ItemID)
	if err != nil {
		t.Fatalf("GetBlindedItem: %v", err)
	}
	if item.Prompt != scheduledAtAsOf {
		t.Errorf("blinded prompt = %q, want %q", item.Prompt, scheduledAtAsOf)
	}
}

func TestListPending_ScheduledPromptMatchesWhatWasSent(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, scheduledPrompt)
	run := runAsOf(t, f, scheduledAsOf)

	pending, err := f.store.ListPending(context.Background(), run.ID, 0, 0)
	if err != nil || len(pending) == 0 {
		t.Fatalf("ListPending: %d items, %v", len(pending), err)
	}
	for _, p := range pending {
		if p.Prompt != scheduledAtAsOf {
			t.Errorf("pending prompt = %q, want %q", p.Prompt, scheduledAtAsOf)
		}
	}
}

func TestPairDetails_ScheduledTaskPromptMatchesWhatWasSent(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, scheduledPrompt)
	run := runAsOf(t, f, scheduledAsOf)

	view, err := f.store.PairDetails(context.Background(), run.ID, 0)
	if err != nil || len(view.Pairs) == 0 {
		t.Fatalf("PairDetails: %v", err)
	}
	if got := view.Pairs[0].TaskPrompt; got != scheduledAtAsOf {
		t.Errorf("task prompt = %q, want %q", got, scheduledAtAsOf)
	}
}

func TestSummarize_ScheduledTaskPromptMatchesWhatWasSent(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, scheduledPrompt)
	run := runAsOf(t, f, scheduledAsOf)

	sum, err := f.store.Summarize(context.Background(), run.ID, SummaryOpts{})
	if err != nil || len(sum.PerTask) != 1 {
		t.Fatalf("Summarize: %v", err)
	}
	if got := sum.PerTask[0].Prompt; got != scheduledAtAsOf {
		t.Errorf("per-task prompt = %q, want %q", got, scheduledAtAsOf)
	}
}
