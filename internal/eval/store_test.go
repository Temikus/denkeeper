package eval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustTaskSet(t *testing.T, s *Store, name string) *TaskSet {
	t.Helper()
	set, err := s.CreateTaskSet(context.Background(), name, "")
	if err != nil {
		t.Fatalf("creating task set %q: %v", name, err)
	}
	return set
}

func mustTask(t *testing.T, s *Store, setID int64, prompt string) *Task {
	t.Helper()
	task, err := s.AddTask(context.Background(), setID, Task{Prompt: prompt, Category: CategoryChat})
	if err != nil {
		t.Fatalf("adding task: %v", err)
	}
	return task
}

func TestStore_TaskSetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateTaskSet(ctx, "regression", "the standing set")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateTaskSet returned a zero id")
	}

	got, err := s.GetTaskSet(ctx, "regression")
	if err != nil {
		t.Fatalf("GetTaskSet: %v", err)
	}
	if got.Description != "the standing set" {
		t.Errorf("description = %q, want %q", got.Description, "the standing set")
	}

	mustTask(t, s, created.ID, "hello")
	mustTask(t, s, created.ID, "goodbye")

	sets, err := s.ListTaskSets(ctx)
	if err != nil {
		t.Fatalf("ListTaskSets: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("ListTaskSets returned %d sets, want 1", len(sets))
	}
	if sets[0].TaskCount != 2 {
		t.Errorf("task_count = %d, want 2 — the list must carry the count, not just the row", sets[0].TaskCount)
	}
}

func TestStore_TaskSetNameUnique(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustTaskSet(t, s, "dupe")

	_, err := s.CreateTaskSet(ctx, "dupe", "")
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("second create error = %v, want ErrNameTaken", err)
	}
}

func TestStore_GetTaskSetMissingIsNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetTaskSet(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound so handlers can classify a 404", err)
	}
}

func TestStore_UpdateTaskSetRenames(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustTaskSet(t, s, "old")

	newName := "new"
	desc := "renamed"
	if _, err := s.UpdateTaskSet(ctx, "old", &newName, &desc); err != nil {
		t.Fatalf("UpdateTaskSet: %v", err)
	}
	if _, err := s.GetTaskSet(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old name still resolves: %v", err)
	}
	got, err := s.GetTaskSet(ctx, "new")
	if err != nil {
		t.Fatalf("GetTaskSet(new): %v", err)
	}
	if got.Description != "renamed" {
		t.Errorf("description = %q, want %q", got.Description, "renamed")
	}
}

func TestStore_DeleteSetRefusedWhenRunsExist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "referenced")
	mustTask(t, s, set.ID, "hi")

	if _, _, err := s.CreateRun(ctx, Run{
		TaskSetID: set.ID, BaseAgent: "pamela", K: 1, CostCap: 1, AsOf: time.Now().UTC(),
	}, []Variant{{Name: "incumbent"}, {Name: "candidate"}}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	err := s.DeleteTaskSet(ctx, "referenced")
	if !errors.Is(err, ErrTaskSetInUse) {
		t.Fatalf("delete error = %v, want ErrTaskSetInUse — deleting would orphan the run's samples", err)
	}
	if _, err := s.GetTaskSet(ctx, "referenced"); err != nil {
		t.Errorf("refused delete still removed the set: %v", err)
	}
}

func TestStore_DeleteSetRemovesTasks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "throwaway")
	mustTask(t, s, set.ID, "hi")

	if err := s.DeleteTaskSet(ctx, "throwaway"); err != nil {
		t.Fatalf("DeleteTaskSet: %v", err)
	}
	tasks, err := s.ListTasks(ctx, set.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("%d task(s) survived the set deletion", len(tasks))
	}
}

func TestStore_AddTaskRejectsUnknownCategory(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")

	_, err := s.AddTask(context.Background(), set.ID, Task{Prompt: "x", Category: "freeform"})
	if err == nil {
		t.Fatal("AddTask accepted an unknown category; categories are a closed set")
	}
}

func TestStore_UpdateTaskPatchesOnlyGivenFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	task, err := s.AddTask(ctx, set.ID, Task{
		Prompt: "original", Category: CategoryToolHeavy, Notes: "keep me",
		PinnedHistory: `[{"role":"user","content":"before"}]`,
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	prompt := "edited"
	got, err := s.UpdateTask(ctx, set.ID, task.ID, TaskPatch{Prompt: &prompt})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if got.Prompt != "edited" {
		t.Errorf("prompt = %q, want %q", got.Prompt, "edited")
	}
	if got.Notes != "keep me" {
		t.Errorf("notes = %q, want it preserved by a prompt-only patch", got.Notes)
	}
	if got.Category != CategoryToolHeavy {
		t.Errorf("category = %q, want it preserved", got.Category)
	}
	if !strings.Contains(got.PinnedHistory, "before") {
		t.Errorf("pinned_history = %q, want it preserved", got.PinnedHistory)
	}
}

func TestStore_GetTaskIsScopedToItsSet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	setA := mustTaskSet(t, s, "a")
	setB := mustTaskSet(t, s, "b")
	task := mustTask(t, s, setA.ID, "in a")

	if _, err := s.GetTask(ctx, setB.ID, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task reachable through the wrong set: %v", err)
	}
}

func TestStore_DeleteTaskMissingIsNotFound(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")
	if err := s.DeleteTask(context.Background(), set.ID, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestStore_CreateRunPersistsVariants(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")

	run, variants, err := s.CreateRun(ctx, Run{
		TaskSetID: set.ID, BaseAgent: "pamela", K: 3, CostCap: 2.0, AsOf: time.Now().UTC(),
	}, []Variant{
		{Name: "incumbent"},
		{Name: "candidate", Overlay: `{"llm_model":"kimi-k3"}`},
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.Status != StatusPending {
		t.Errorf("status = %q, want %q — the runner flips it, not the store", run.Status, StatusPending)
	}
	if len(variants) != 2 {
		t.Fatalf("got %d variants, want 2", len(variants))
	}
	if variants[0].Overlay != "{}" {
		t.Errorf("empty overlay stored as %q, want %q", variants[0].Overlay, "{}")
	}

	listed, err := s.ListVariants(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListVariants: %v", err)
	}
	if listed[0].Name != "incumbent" {
		t.Errorf("first variant = %q, want the creation-order baseline %q", listed[0].Name, "incumbent")
	}
}

func TestStore_ListRunsFiltersByStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	vs := []Variant{{Name: "a"}, {Name: "b"}}

	r1, _, err := s.CreateRun(ctx, Run{TaskSetID: set.ID, BaseAgent: "p", K: 1, CostCap: 1, AsOf: time.Now()}, vs)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, _, err := s.CreateRun(ctx, Run{TaskSetID: set.ID, BaseAgent: "p", K: 1, CostCap: 1, AsOf: time.Now()}, vs); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := s.FinishRun(ctx, r1.ID, StatusDone, ""); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	done, err := s.ListRuns(ctx, 0, StatusDone)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(done) != 1 || done[0].ID != r1.ID {
		t.Fatalf("status filter returned %+v, want only run %d", done, r1.ID)
	}

	all, err := s.ListRuns(ctx, set.ID, "")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d runs for the set, want 2", len(all))
	}
	if all[0].ID < all[1].ID {
		t.Error("runs must come back newest first")
	}
}

func TestStore_FinishRunStampsFinishedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	run, _, err := s.CreateRun(ctx, Run{TaskSetID: set.ID, BaseAgent: "p", K: 1, CostCap: 1, AsOf: time.Now()},
		[]Variant{{Name: "a"}, {Name: "b"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if err := s.FinishRun(ctx, run.ID, StatusCapped, "cost cap reached"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	got, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != StatusCapped || got.Error != "cost cap reached" {
		t.Errorf("run = %q/%q, want capped with the error preserved", got.Status, got.Error)
	}
	if got.FinishedAt == nil {
		t.Error("finished_at is nil after FinishRun")
	}
}

func TestStore_AddRunCostAccumulates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	run, _, err := s.CreateRun(ctx, Run{TaskSetID: set.ID, BaseAgent: "p", K: 1, CostCap: 5, AsOf: time.Now()},
		[]Variant{{Name: "a"}, {Name: "b"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.AddRunCost(ctx, run.ID, 0.25); err != nil {
			t.Fatalf("AddRunCost: %v", err)
		}
	}
	got, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.CostSpent < 0.749 || got.CostSpent > 0.751 {
		t.Errorf("cost_spent = %v, want 0.75", got.CostSpent)
	}
}

func TestStore_SampleRoundTripKeepsOutcomeSplit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")
	task := mustTask(t, s, set.ID, "prompt")
	run, variants, err := s.CreateRun(ctx, Run{TaskSetID: set.ID, BaseAgent: "p", K: 1, CostCap: 1, AsOf: time.Now()},
		[]Variant{{Name: "a"}, {Name: "b"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if _, err := s.AddSample(ctx, Sample{
		RunID: run.ID, VariantID: variants[0].ID, TaskID: task.ID, KIndex: 0,
		Status: SampleOK, Response: "answer", Rounds: 2, StopReason: "max_rounds",
		Upstream:  "Fireworks",
		OutcomeOK: 3, OutcomeRejected: 1, OutcomeFailed: 2, OutcomeDenied: 1,
		OutcomeCached: 4, OutcomeSuppressed: 5,
		TokensPrompt: 100, TokensCompletion: 20, Cost: 0.5, LatencyMs: 1200,
	}); err != nil {
		t.Fatalf("AddSample: %v", err)
	}

	samples, err := s.ListSamples(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(samples))
	}
	got := samples[0]
	if got.OutcomeCached != 4 || got.OutcomeSuppressed != 5 {
		t.Errorf("cached/suppressed = %d/%d, want 4/5 — they must not fold into failed",
			got.OutcomeCached, got.OutcomeSuppressed)
	}
	if got.OutcomeFailed != 2 {
		t.Errorf("outcome_failed = %d, want 2", got.OutcomeFailed)
	}
	if got.Trace != "[]" {
		t.Errorf("trace = %q, want the empty-array default", got.Trace)
	}
	if got.StopReason != "max_rounds" {
		t.Errorf("stop_reason = %q, want the slug", got.StopReason)
	}
	if got.Upstream != "Fireworks" {
		t.Errorf("upstream = %q, want Fireworks", got.Upstream)
	}

	n, err := s.CountSamples(ctx, run.ID)
	if err != nil {
		t.Fatalf("CountSamples: %v", err)
	}
	if n != 1 {
		t.Errorf("CountSamples = %d, want 1", n)
	}
}

func TestStore_JSONLRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	src := mustTaskSet(t, s, "source")

	if _, err := s.AddTask(ctx, src.ID, Task{
		Prompt: "what is the weather", Category: CategoryToolHeavy,
		Tags: `["weather"]`, Notes: "should call a tool",
		PinnedHistory: `[{"role":"user","content":"hi"}]`,
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if _, err := s.AddTask(ctx, src.ID, Task{Prompt: "say hello", Category: CategoryChat}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	var buf strings.Builder
	if err := s.ExportJSONL(ctx, src.ID, &buf); err != nil {
		t.Fatalf("ExportJSONL: %v", err)
	}

	dst := mustTaskSet(t, s, "destination")
	n, err := s.ImportJSONL(ctx, dst.ID, strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("ImportJSONL: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported %d tasks, want 2", n)
	}

	orig, err := s.ListTasks(ctx, src.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	copied, err := s.ListTasks(ctx, dst.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for i := range orig {
		if orig[i].Prompt != copied[i].Prompt || orig[i].Category != copied[i].Category ||
			orig[i].Notes != copied[i].Notes || orig[i].Tags != copied[i].Tags ||
			orig[i].PinnedHistory != copied[i].PinnedHistory {
			t.Errorf("task %d did not survive the round trip:\n orig %+v\n copy %+v", i, orig[i], copied[i])
		}
	}
}

func TestStore_ImportJSONLIsAllOrNone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")

	body := `{"prompt":"good one","category":"chat"}
{"prompt":"also good","category":"chat"}
{"prompt":"bad","category":"nonsense"}
`
	_, err := s.ImportJSONL(ctx, set.ID, strings.NewReader(body))
	if err == nil {
		t.Fatal("ImportJSONL accepted an invalid category")
	}
	var impErr *ImportError
	if !errors.As(err, &impErr) {
		t.Fatalf("error = %v, want an *ImportError naming the line", err)
	}
	if impErr.Line != 3 {
		t.Errorf("reported line %d, want 3", impErr.Line)
	}

	tasks, err := s.ListTasks(ctx, set.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("%d task(s) imported despite the failure; import must be all-or-none", len(tasks))
	}
}

func TestStore_ImportJSONLRejectsEmptyPrompt(t *testing.T) {
	s := newTestStore(t)
	set := mustTaskSet(t, s, "set")
	_, err := s.ImportJSONL(context.Background(), set.ID, strings.NewReader(`{"prompt":"  "}`))
	if err == nil {
		t.Fatal("ImportJSONL accepted a blank prompt")
	}
}

func TestStore_ImportJSONLDefaultsCategoryToChat(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	set := mustTaskSet(t, s, "set")

	if _, err := s.ImportJSONL(ctx, set.ID, strings.NewReader(`{"prompt":"plain"}`)); err != nil {
		t.Fatalf("ImportJSONL: %v", err)
	}
	tasks, err := s.ListTasks(ctx, set.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if tasks[0].Category != CategoryChat {
		t.Errorf("category = %q, want the %q default", tasks[0].Category, CategoryChat)
	}
}
