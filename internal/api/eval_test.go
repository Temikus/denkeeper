package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/eval"
)

// evalReadOnlyKey carries eval:read but no eval:write, so it can list task
// sets but cannot create one or launch a run.
func evalReadOnlyKey() config.APIKeyConfig {
	return config.APIKeyConfig{
		Name:   "eval-readonly",
		Key:    "dk-eval-readonly",
		Scopes: []string{"eval:read"},
	}
}

// evalTestServer builds a server with a real in-memory eval store and runner
// over the standard test dispatcher.
func evalTestServer(t *testing.T, keys ...config.APIKeyConfig) (*Server, *eval.Store) {
	t.Helper()
	if len(keys) == 0 {
		keys = []config.APIKeyConfig{allScopesKey()}
	}
	deps := testDeps()
	store, err := eval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating eval store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dispatcher := deps.Dispatcher
	runner := eval.NewRunner(store, func(name string) (eval.Engine, bool) {
		e := dispatcher.Agent(name)
		if e == nil {
			return nil, false
		}
		return e, true
	}, nil, eval.Config{MaxConcurrent: 1, MaxCostPerRun: 2.0, DefaultK: 3, CompletenessFloor: 0.8}, testLogger())
	t.Cleanup(runner.Shutdown)

	deps.EvalStore = store
	deps.EvalRunner = runner
	deps.Config.Eval = config.EvalConfig{
		Audit: "full", MaxConcurrent: 1, MaxCostPerRun: 2.0, DefaultK: 3, CompletenessFloor: 0.8,
	}
	return New(testConfig(keys...), deps, testLogger()), store
}

// evalRequest issues an authenticated request against the eval routes.
func evalRequest(t *testing.T, srv *Server, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	var r *strings.Reader
	if body == "" {
		r = strings.NewReader("")
	} else {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func seedTaskSet(t *testing.T, store *eval.Store, name string, prompts ...string) *eval.TaskSet {
	t.Helper()
	set, err := store.CreateTaskSet(context.Background(), name, "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	for _, p := range prompts {
		if _, err := store.AddTask(context.Background(), set.ID,
			eval.Task{Prompt: p, Category: eval.CategoryChat}); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
	}
	return set
}

// --- Scope enforcement ---

func TestEvalTaskSets_ReadScopeCannotCreate(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey(), evalReadOnlyKey())

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets", `{"name":"x"}`, "dk-eval-readonly")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — creating a set needs eval:write", rec.Code)
	}
}

func TestEvalTaskSets_ReadScopeCanList(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey(), evalReadOnlyKey())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/task-sets", "", "dk-eval-readonly")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestEvalRuns_ReadScopeCannotStartARun(t *testing.T) {
	srv, store := evalTestServer(t, allScopesKey(), evalReadOnlyKey())
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"a"},{"name":"b"}]}`, "dk-eval-readonly")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a run spends real tokens", rec.Code)
	}
}

func TestEvalEndpoints_UnwiredStoreReturns503(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDeps(), testLogger())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/task-sets", "", "dk-test-key")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the eval subsystem is not wired", rec.Code)
	}
}

// --- Task sets ---

func TestEvalTaskSets_CreateAndGet(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets",
		`{"name":"regression","description":"the standing set"}`, "dk-test-key")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	rec = evalRequest(t, srv, http.MethodGet, "/api/v1/eval/task-sets/regression", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var detail evalTaskSetDetail
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Name != "regression" || detail.Description != "the standing set" {
		t.Errorf("set = %+v, want the created values", detail.TaskSet)
	}
	if detail.Tasks == nil {
		t.Error("tasks is null, want an empty array so the UI can iterate it")
	}
}

func TestEvalTaskSets_DuplicateNameIs409(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "dupe")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets", `{"name":"dupe"}`, "dk-test-key")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestEvalTaskSets_MissingNameIs400(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets", `{"description":"no name"}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalTaskSets_UnknownSetIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/task-sets/ghost", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 via the ErrNotFound sentinel", rec.Code)
	}
}

func TestEvalTaskSets_DeleteReferencedSetIs409(t *testing.T) {
	srv, store := evalTestServer(t)
	set := seedTaskSet(t, store, "referenced", "hi")
	if _, _, err := store.CreateRun(context.Background(), eval.Run{
		TaskSetID: set.ID, BaseAgent: "default", K: 1, CostCap: 1, AsOf: time.Now(),
	}, []eval.Variant{{Name: "a"}, {Name: "b"}}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodDelete, "/api/v1/eval/task-sets/referenced", "", "dk-test-key")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — the run's samples need their tasks", rec.Code)
	}
}

func TestEvalTaskSets_DeleteUnreferencedSetIs204(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "throwaway", "hi")

	rec := evalRequest(t, srv, http.MethodDelete, "/api/v1/eval/task-sets/throwaway", "", "dk-test-key")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
}

func TestEvalTaskSets_RenameRoundTrips(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "old")

	rec := evalRequest(t, srv, http.MethodPatch, "/api/v1/eval/task-sets/old", `{"name":"new"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/task-sets/new", "", "dk-test-key"); rec.Code != http.StatusOK {
		t.Errorf("renamed set not reachable: %d", rec.Code)
	}
}

// --- Tasks ---

func TestEvalTasks_CreateCarriesPinnedHistory(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set")

	body := `{"prompt":"continue","category":"tool_heavy",
	          "pinned_history":[{"role":"user","content":"earlier"}],
	          "tags":["regression"],"notes":"should call a tool",
	          "source_conversation_id":"chan:main"}`
	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets/set/tasks", body, "dk-test-key")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var task eval.Task
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(task.PinnedHistory, "earlier") {
		t.Errorf("pinned_history = %q, want the snippet stored verbatim", task.PinnedHistory)
	}
	if task.Category != eval.CategoryToolHeavy {
		t.Errorf("category = %q, want tool_heavy", task.Category)
	}
	if task.SourceConversationID != "chan:main" {
		t.Errorf("source_conversation_id = %q, want it preserved", task.SourceConversationID)
	}
}

func TestEvalTasks_CreateDefaultsCategoryToChat(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets/set/tasks",
		`{"prompt":"say hi"}`, "dk-test-key")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var task eval.Task
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if task.Category != eval.CategoryChat {
		t.Errorf("category = %q, want the chat default", task.Category)
	}
}

func TestEvalTasks_UnknownCategoryIs400(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets/set/tasks",
		`{"prompt":"x","category":"freeform"}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "tool_heavy") {
		t.Errorf("error %q should list the valid categories", rec.Body.String())
	}
}

func TestEvalTasks_MissingPromptIs400(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets/set/tasks", `{"notes":"x"}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalTasks_PatchPreservesUntouchedFields(t *testing.T) {
	srv, store := evalTestServer(t)
	set := seedTaskSet(t, store, "set")
	task, err := store.AddTask(context.Background(), set.ID, eval.Task{
		Prompt: "before", Category: eval.CategoryToolHeavy, Notes: "keep me"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/v1/eval/task-sets/set/tasks/%d", task.ID), `{"prompt":"after"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got eval.Task
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Prompt != "after" || got.Notes != "keep me" || got.Category != eval.CategoryToolHeavy {
		t.Errorf("task = %+v, want only the prompt changed", got)
	}
}

func TestEvalTasks_DeleteUnknownTaskIs404(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set")

	rec := evalRequest(t, srv, http.MethodDelete, "/api/v1/eval/task-sets/set/tasks/999", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalTasks_NonNumericIDIs400(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set")

	rec := evalRequest(t, srv, http.MethodDelete, "/api/v1/eval/task-sets/set/tasks/abc", "", "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// --- JSONL ---

func TestEvalTaskSets_ExportImportRoundTrip(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "source", "one", "two")
	seedTaskSet(t, store, "destination")

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/task-sets/source/export", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/jsonl" {
		t.Errorf("Content-Type = %q, want application/jsonl", ct)
	}

	rec = evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets/destination/import",
		rec.Body.String(), "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("import status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var result evalImportResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Imported != 2 {
		t.Errorf("imported = %d, want 2", result.Imported)
	}
}

func TestEvalTaskSets_ImportAllOrNoneNamesTheBadLine(t *testing.T) {
	srv, store := evalTestServer(t)
	set := seedTaskSet(t, store, "set")

	body := "{\"prompt\":\"ok\"}\n{\"prompt\":\"also ok\"}\n{\"prompt\":\"bad\",\"category\":\"nope\"}\n"
	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/task-sets/set/import", body, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "line 3") {
		t.Errorf("error %q should name the offending line", rec.Body.String())
	}

	tasks, err := store.ListTasks(context.Background(), set.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("%d task(s) imported despite the rejection; import must be all-or-none", len(tasks))
	}
}

// --- Runs ---

func TestEvalRuns_CreateRequiresTwoVariants(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"only"}]}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "at least 2 variants") {
		t.Errorf("error = %q, want it to say why", rec.Body.String())
	}
}

func TestEvalRuns_CreateRejectsUnknownAgent(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"ghost","variants":[{"name":"a"},{"name":"b"}]}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalRuns_CreateRejectsUnregisteredProvider(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"a"},{"name":"b","llm_provider":"nowhere"}]}`,
		"dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — an unknown provider must fail run creation, not every sample", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "nowhere") {
		t.Errorf("error = %q, want it to name the provider", rec.Body.String())
	}
}

func TestEvalRuns_CreateRejectsDuplicateVariantNames(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"a"},{"name":"a"}]}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalRuns_CreateRejectsEmptyTaskSet(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "empty")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"empty","base_agent":"default","variants":[{"name":"a"},{"name":"b"}]}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalRuns_CreateUnknownTaskSetIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"ghost","base_agent":"default","variants":[{"name":"a"},{"name":"b"}]}`, "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalRuns_CreateAppliesConfigDefaults(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"incumbent"},{"name":"candidate"}]}`,
		"dk-test-key")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created evalRunCreated
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.K != 3 {
		t.Errorf("k = %d, want the [eval] default_k of 3", created.K)
	}
	if created.CostCap != 2.0 {
		t.Errorf("cost_cap = %v, want the [eval] max_cost_per_run of 2.0", created.CostCap)
	}
	if len(created.Variants) != 2 || created.Variants[0].Name != "incumbent" {
		t.Errorf("variants = %+v, want the incumbent first", created.Variants)
	}
	if created.Variants[0].Overlay != `{}` {
		t.Errorf("incumbent overlay = %q, want the empty overlay meaning live config", created.Variants[0].Overlay)
	}
}

func TestEvalRuns_GetReportsProgressAndTotals(t *testing.T) {
	srv, store := evalTestServer(t)
	set := seedTaskSet(t, store, "set", "a", "b")
	run, _, err := store.CreateRun(context.Background(), eval.Run{
		TaskSetID: set.ID, BaseAgent: "default", K: 2, CostCap: 1, AsOf: time.Now(),
	}, []eval.Variant{{Name: "a"}, {Name: "b"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/eval/runs/%d", run.ID), "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var detail evalRunDetail
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.SamplesTotal != 8 {
		t.Errorf("samples_total = %d, want 2 tasks × 2 variants × k=2 = 8", detail.SamplesTotal)
	}
	if detail.SamplesDone != 0 {
		t.Errorf("samples_done = %d, want 0", detail.SamplesDone)
	}
	if len(detail.Variants) != 2 {
		t.Errorf("got %d variants, want 2", len(detail.Variants))
	}
}

func TestEvalRuns_GetUnknownRunIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/runs/999", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalRuns_GetNonNumericIDIs400(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/runs/abc", "", "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalRuns_StopTerminalRunIs409(t *testing.T) {
	srv, store := evalTestServer(t)
	set := seedTaskSet(t, store, "set", "a")
	run, _, err := store.CreateRun(context.Background(), eval.Run{
		TaskSetID: set.ID, BaseAgent: "default", K: 1, CostCap: 1, AsOf: time.Now(),
	}, []eval.Variant{{Name: "a"}, {Name: "b"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := store.FinishRun(context.Background(), run.ID, eval.StatusDone, ""); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/eval/runs/%d/stop", run.ID), "", "dk-test-key")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestEvalRuns_StopUnknownRunIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs/999/stop", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalRuns_ListFiltersByTaskSet(t *testing.T) {
	srv, store := evalTestServer(t)
	setA := seedTaskSet(t, store, "a", "x")
	seedTaskSet(t, store, "b", "y")
	if _, _, err := store.CreateRun(context.Background(), eval.Run{
		TaskSetID: setA.ID, BaseAgent: "default", K: 1, CostCap: 1, AsOf: time.Now(),
	}, []eval.Variant{{Name: "i"}, {Name: "c"}}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/runs?task_set=b", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var runs []eval.Run
	if err := json.NewDecoder(rec.Body).Decode(&runs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d runs for set b, want 0", len(runs))
	}
}

func TestEvalRuns_ListUnknownTaskSetIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/runs?task_set=ghost", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalRuns_SummaryReportsCompleteness(t *testing.T) {
	srv, store := evalTestServer(t)
	set := seedTaskSet(t, store, "set", "a")
	run, variants, err := store.CreateRun(context.Background(), eval.Run{
		TaskSetID: set.ID, BaseAgent: "default", K: 1, CostCap: 1, AsOf: time.Now(),
	}, []eval.Variant{{Name: "incumbent"}, {Name: "candidate"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AddSample(context.Background(), eval.Sample{
		RunID: run.ID, VariantID: variants[0].ID, TaskID: 1, Status: eval.SampleOK, Rounds: 2,
	}); err != nil {
		t.Fatalf("AddSample: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/eval/runs/%d/summary", run.ID), "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var summary eval.Summary
	if err := json.NewDecoder(rec.Body).Decode(&summary); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if summary.Completeness.SamplesExpected != 2 {
		t.Errorf("samples_expected = %d, want 2", summary.Completeness.SamplesExpected)
	}
	if summary.Completeness.Conclusive {
		t.Error("1 of 2 samples reported conclusive against a 0.8 floor")
	}
	if summary.BaselineVariant != "incumbent" {
		t.Errorf("baseline = %q, want the first variant", summary.BaselineVariant)
	}
}

func TestEvalRuns_SummaryUnknownRunIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/runs/999/summary", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalRuns_SamplesUnknownRunIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/runs/999/samples", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalETA_ZeroWhenNothingLanded(t *testing.T) {
	if got := evalETA(nil, 8, 2); got != 0 {
		t.Errorf("eta = %d, want 0 before any sample lands", got)
	}
}

func TestEvalETA_ScalesByConcurrency(t *testing.T) {
	samples := []eval.Sample{{Status: eval.SampleOK, LatencyMs: 2000}}
	// 7 remaining × 2s ÷ 2 concurrent = 7s.
	if got := evalETA(samples, 8, 2); got != 7 {
		t.Errorf("eta = %d, want 7", got)
	}
}

// seedCategorisedTaskSet builds a set with one task per named category, so a
// stratified draw has something to stratify over.
func seedCategorisedTaskSet(t *testing.T, store *eval.Store, name string, perCategory int, categories ...string) *eval.TaskSet {
	t.Helper()
	set, err := store.CreateTaskSet(context.Background(), name, "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	for _, cat := range categories {
		for i := 0; i < perCategory; i++ {
			if _, err := store.AddTask(context.Background(), set.ID,
				eval.Task{Prompt: cat + " prompt", Category: cat}); err != nil {
				t.Fatalf("AddTask: %v", err)
			}
		}
	}
	return set
}

func TestEvalRuns_SampleTasksPinsAStratifiedSubset(t *testing.T) {
	srv, store := evalTestServer(t)
	seedCategorisedTaskSet(t, store, "set", 4,
		eval.CategoryChat, eval.CategorySkillCommand, eval.CategoryToolHeavy)

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","k":1,"sample_tasks":5,`+
			`"variants":[{"name":"incumbent"},{"name":"candidate"}]}`, "dk-test-key")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created evalRunCreated
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(created.TaskIDs) != 5 {
		t.Fatalf("task_ids = %v, want the 5 drawn tasks recorded on the run", created.TaskIDs)
	}
	if created.TaskCount != 5 {
		t.Errorf("task_count = %d, want the drawn 5 (of a 12-task set)", created.TaskCount)
	}

	rec = evalRequest(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/eval/runs/%d", created.ID), "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var detail evalRunDetail
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.SamplesTotal != 10 {
		t.Errorf("samples_total = %d, want 5 drawn tasks x 2 variants x k=1 = 10", detail.SamplesTotal)
	}
}

func TestEvalRuns_SampleTasksAtOrAboveSetSizeRunsEverything(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "a", "b")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","k":1,"sample_tasks":9,`+
			`"variants":[{"name":"incumbent"},{"name":"candidate"}]}`, "dk-test-key")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created evalRunCreated
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.TaskIDs != nil {
		t.Errorf("task_ids = %v, want no pin when the sample covers the whole set", created.TaskIDs)
	}
	if created.TaskCount != 2 {
		t.Errorf("task_count = %d, want the set's 2 tasks", created.TaskCount)
	}
}

func TestEvalRuns_CreateRejectsNegativeSampleTasks(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "a")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/runs",
		`{"task_set":"set","base_agent":"default","sample_tasks":-1,`+
			`"variants":[{"name":"incumbent"},{"name":"candidate"}]}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// A task added to the set after the run was created must not move the run's
// expected-sample count; before pinning it did, and could flip a finished run
// to inconclusive.
func TestEvalRuns_GetTotalsIgnoreTasksAddedAfterAPinnedRun(t *testing.T) {
	srv, store := evalTestServer(t)
	set := seedTaskSet(t, store, "set", "a", "b", "c")
	tasks, err := store.ListTasks(context.Background(), set.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	run, _, err := store.CreateRun(context.Background(), eval.Run{
		TaskSetID: set.ID, BaseAgent: "default", K: 1, CostCap: 1, AsOf: time.Now(),
		TaskIDs: eval.TaskIDList{tasks[0].ID},
	}, []eval.Variant{{Name: "a"}, {Name: "b"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AddTask(context.Background(), set.ID,
		eval.Task{Prompt: "added later", Category: eval.CategoryChat}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/eval/runs/%d", run.ID), "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var detail evalRunDetail
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.SamplesTotal != 2 {
		t.Errorf("samples_total = %d, want 1 pinned task x 2 variants x k=1 = 2", detail.SamplesTotal)
	}
}
