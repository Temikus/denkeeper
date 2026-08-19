package eval

import (
	"context"
	"testing"
	"time"
)

func mustCategorisedTask(t *testing.T, s *Store, setID int64, prompt, category string) *Task {
	t.Helper()
	task, err := s.AddTask(context.Background(), setID, Task{Prompt: prompt, Category: category})
	if err != nil {
		t.Fatalf("adding %s task: %v", category, err)
	}
	return task
}

// categoriesOf resolves drawn ids back to their categories, so a draw can be
// checked for balance rather than only for size.
func categoriesOf(tasks []Task, drawn TaskIDList) map[string]int {
	byID := make(map[int64]string, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t.Category
	}
	counts := make(map[string]int)
	for _, id := range drawn {
		counts[byID[id]]++
	}
	return counts
}

func seedCategorised(t *testing.T, s *Store, setID int64, perCategory int, categories ...string) []Task {
	t.Helper()
	out := make([]Task, 0, perCategory*len(categories))
	for _, cat := range categories {
		for i := 0; i < perCategory; i++ {
			out = append(out, *mustCategorisedTask(t, s, setID, cat+" prompt", cat))
		}
	}
	return out
}

func TestDrawStratified_BalancesAcrossCategories(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 4, CategoryChat, CategorySkillCommand, CategoryToolHeavy)

	drawn := DrawStratified(tasks, 5)
	if len(drawn) != 5 {
		t.Fatalf("drew %d tasks, want 5", len(drawn))
	}
	seen := make(map[int64]struct{}, len(drawn))
	for _, id := range drawn {
		if _, dup := seen[id]; dup {
			t.Fatalf("task %d drawn twice", id)
		}
		seen[id] = struct{}{}
	}
	counts := categoriesOf(tasks, drawn)
	if len(counts) != 3 {
		t.Fatalf("counts = %v, want all three categories represented", counts)
	}
	for cat, n := range counts {
		if n < 1 || n > 2 {
			t.Errorf("category %s drew %d of 5, want counts within one of each other", cat, n)
		}
	}
}

func TestDrawStratified_TakesTheRemainderFromLiveCategories(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")
	tasks := []Task{*mustCategorisedTask(t, s, set.ID, "lonely", CategoryChat)}
	tasks = append(tasks, seedCategorised(t, s, set.ID, 5, CategoryToolHeavy)...)

	drawn := DrawStratified(tasks, 4)
	counts := categoriesOf(tasks, drawn)
	if counts[CategoryChat] != 1 {
		t.Errorf("chat drew %d, want its only task", counts[CategoryChat])
	}
	if counts[CategoryToolHeavy] != 3 {
		t.Errorf("tool_heavy drew %d, want the remaining 3 once chat is exhausted", counts[CategoryToolHeavy])
	}
}

func TestDrawStratified_SampleAtOrAboveTaskCountIsTheWholeSet(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 2, CategoryChat, CategoryScheduled)

	for _, n := range []int{len(tasks), len(tasks) + 10} {
		if drawn := DrawStratified(tasks, n); drawn != nil {
			t.Errorf("DrawStratified(%d of %d) = %v, want nil meaning the whole set", n, len(tasks), drawn)
		}
	}
}

func TestDrawStratified_ZeroIsTheWholeSet(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 3, CategoryChat)

	if drawn := DrawStratified(tasks, 0); drawn != nil {
		t.Errorf("DrawStratified(0) = %v, want nil meaning the whole set", drawn)
	}
}

func TestDrawStratified_ReturnsIDsInCreationOrder(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 4, CategoryChat, CategoryToolHeavy)

	drawn := DrawStratified(tasks, 5)
	for i := 1; i < len(drawn); i++ {
		if drawn[i-1] >= drawn[i] {
			t.Fatalf("drawn ids %v are not ascending — the pin must read in creation order", drawn)
		}
	}
}

// --- RunTasks ---

func pinnedRun(t *testing.T, s *Store, setID int64, k int, pin TaskIDList) *Run {
	t.Helper()
	run, _, err := s.CreateRun(context.Background(), Run{
		TaskSetID: setID, BaseAgent: "pamela", K: k, CostCap: 2.0,
		AsOf: time.Now().UTC(), TaskIDs: pin,
	}, []Variant{{Name: "incumbent"}, {Name: "candidate"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return run
}

func TestRunTasks_UnpinnedRunCoversTheWholeSet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	seedCategorised(t, s, set.ID, 3, CategoryChat)

	run := pinnedRun(t, s, set.ID, 1, nil)
	got, err := s.RunTasks(ctx, run)
	if err != nil {
		t.Fatalf("RunTasks: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("RunTasks returned %d tasks, want the whole set of 3", len(got))
	}
	if run.TaskCount != 3 {
		t.Errorf("task_count = %d, want the set size of 3", run.TaskCount)
	}
	if run.TaskIDs != nil {
		t.Errorf("task_ids = %v, want nil on an unpinned run", run.TaskIDs)
	}
}

func TestRunTasks_PinSurvivesALaterTaskAdd(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 3, CategoryChat)

	run := pinnedRun(t, s, set.ID, 1, TaskIDList{tasks[0].ID, tasks[1].ID})
	if run.TaskCount != 2 {
		t.Errorf("task_count = %d, want the pinned 2", run.TaskCount)
	}

	// The set grows after the run was created — the run must not notice.
	mustCategorisedTask(t, s, set.ID, "added later", CategoryToolHeavy)

	reread, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	got, err := s.RunTasks(ctx, reread)
	if err != nil {
		t.Fatalf("RunTasks: %v", err)
	}
	if len(got) != 2 || got[0].ID != tasks[0].ID || got[1].ID != tasks[1].ID {
		t.Fatalf("RunTasks = %v, want exactly the two pinned tasks", taskIDsOf(got))
	}
	if reread.TaskCount != 2 {
		t.Errorf("task_count = %d after the set grew, want the pinned 2", reread.TaskCount)
	}
}

func TestRunTasks_SkipsAPinnedTaskDeletedAfterTheRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 3, CategoryChat)

	run := pinnedRun(t, s, set.ID, 1, TaskIDList{tasks[0].ID, tasks[1].ID})
	if err := s.DeleteTask(ctx, set.ID, tasks[1].ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	got, err := s.RunTasks(ctx, run)
	if err != nil {
		t.Fatalf("RunTasks: %v", err)
	}
	if len(got) != 1 || got[0].ID != tasks[0].ID {
		t.Fatalf("RunTasks = %v, want only the surviving pinned task", taskIDsOf(got))
	}
}

func taskIDsOf(tasks []Task) []int64 {
	out := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

// --- The denominator the pin exists to stabilise ---

func TestSummarize_SamplesExpectedCountsOnlyPinnedTasks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 4, CategoryChat)

	run := pinnedRun(t, s, set.ID, 1, TaskIDList{tasks[0].ID, tasks[1].ID})
	sum, err := s.Summarize(ctx, run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.Completeness.SamplesExpected != 4 {
		t.Errorf("samples_expected = %d, want 2 pinned tasks × 2 variants × k=1 = 4",
			sum.Completeness.SamplesExpected)
	}
}

// A task added to the set after a run was created used to inflate
// samples_expected retroactively, which could drag a finished run's ratio
// under the completeness floor and flip its verdict to inconclusive. The pin
// is what stops that.
func TestSummarize_LaterTaskAddCannotFlipAFinishedRunToInconclusive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 2, CategoryChat)

	run := pinnedRun(t, s, set.ID, 1, TaskIDList{tasks[0].ID, tasks[1].ID})
	variants, err := s.ListVariants(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListVariants: %v", err)
	}
	for _, task := range tasks {
		for _, v := range variants {
			if _, err := s.AddSample(ctx, Sample{
				RunID: run.ID, VariantID: v.ID, TaskID: task.ID,
				Status: SampleOK, Rounds: 1, Cost: 0.01,
			}); err != nil {
				t.Fatalf("AddSample: %v", err)
			}
		}
	}

	before, err := s.Summarize(ctx, run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !before.Completeness.Conclusive || before.Completeness.Ratio != 1 {
		t.Fatalf("completeness = %+v, want a complete, conclusive run", before.Completeness)
	}

	// Curation continues after the run finished.
	for i := 0; i < 6; i++ {
		mustCategorisedTask(t, s, set.ID, "added later", CategoryChat)
	}

	after, err := s.Summarize(ctx, run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if after.Completeness.SamplesExpected != before.Completeness.SamplesExpected {
		t.Errorf("samples_expected moved from %d to %d after tasks were added to the set",
			before.Completeness.SamplesExpected, after.Completeness.SamplesExpected)
	}
	if !after.Completeness.Conclusive {
		t.Error("a finished, complete run went inconclusive because the set grew underneath it")
	}
}

func TestCreatePairs_PairsOnlyPinnedTasks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	tasks := seedCategorised(t, s, set.ID, 3, CategoryChat)

	run := pinnedRun(t, s, set.ID, 1, TaskIDList{tasks[0].ID})
	variants, err := s.ListVariants(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListVariants: %v", err)
	}
	// Samples exist for an unpinned task too — a stale row from an earlier
	// shape must not create judging work this run never asked for.
	for _, task := range tasks[:2] {
		for _, v := range variants {
			if _, err := s.AddSample(ctx, Sample{
				RunID: run.ID, VariantID: v.ID, TaskID: task.ID, Status: SampleOK,
			}); err != nil {
				t.Fatalf("AddSample: %v", err)
			}
		}
	}

	n, err := s.CreatePairs(ctx, run.ID)
	if err != nil {
		t.Fatalf("CreatePairs: %v", err)
	}
	if n != 1 {
		t.Fatalf("created %d pairs, want 1 — only the pinned task is in the run", n)
	}
}
