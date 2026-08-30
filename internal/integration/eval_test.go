//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/llm"
)

// evalHarness boots a harness with the eval subsystem wired. max_concurrent
// defaults to 1 for determinism: the harness shares one mockProvider across
// every agent with a single global response sequence, so concurrent samples
// would race for positions in it.
func evalHarness(t *testing.T, opts *HarnessOpts) *Harness {
	t.Helper()
	if opts == nil {
		opts = &HarnessOpts{}
	}
	opts.WithEval = true
	if opts.EvalConfig.MaxConcurrent == 0 {
		opts.EvalConfig.MaxConcurrent = 1
	}
	return NewHarness(t, opts)
}

// seedEvalSet creates a task set with the given prompts through the REST API,
// so the test exercises the same path the Chat UI does.
func seedEvalSet(t *testing.T, h *Harness, name string, prompts ...string) {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/eval/task-sets",
		map[string]any{"name": name}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating task set: status %d: %s", rec.Code, rec.Body.String())
	}
	for _, p := range prompts {
		rec := h.Do(h.AuthedRequest(http.MethodPost,
			"/api/v1/eval/task-sets/"+name+"/tasks", map[string]any{"prompt": p}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("adding task %q: status %d: %s", p, rec.Code, rec.Body.String())
		}
	}
}

// startEvalRun launches a run and returns its id.
func startEvalRun(t *testing.T, h *Harness, body map[string]any) int64 {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/eval/runs", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating run: status %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	DecodeJSON(t, rec, &created)
	if created.ID == 0 {
		t.Fatal("run created without an id")
	}
	return created.ID
}

// evalRunStatus polls GET /eval/runs/{id} until the run is terminal.
func evalRunStatus(t *testing.T, h *Harness, runID int64) eval.Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rec := h.Do(h.AuthedRequest(http.MethodGet, fmt.Sprintf("/api/v1/eval/runs/%d", runID), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("run status: %d: %s", rec.Code, rec.Body.String())
		}
		var detail struct {
			eval.Run
			SamplesTotal int `json:"samples_total"`
		}
		DecodeJSON(t, rec, &detail)
		if eval.IsTerminal(detail.Status) {
			return detail.Run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %d never reached a terminal status", runID)
	return eval.Run{}
}

func evalSamples(t *testing.T, h *Harness, runID int64) []eval.Sample {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodGet, fmt.Sprintf("/api/v1/eval/runs/%d/samples", runID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("samples: status %d: %s", rec.Code, rec.Body.String())
	}
	var samples []eval.Sample
	DecodeJSON(t, rec, &samples)
	return samples
}

func evalSummary(t *testing.T, h *Harness, runID int64) eval.Summary {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodGet, fmt.Sprintf("/api/v1/eval/runs/%d/summary", runID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: status %d: %s", rec.Code, rec.Body.String())
	}
	var summary eval.Summary
	DecodeJSON(t, rec, &summary)
	return summary
}

// memorySnapshot counts conversations and the messages across them, so a test
// can assert that a run left the real telemetry untouched.
func memorySnapshot(t *testing.T, h *Harness) (conversations, messages int) {
	t.Helper()
	convs, total, err := h.Memory.ListConversations(context.Background(), agent.SessionListOpts{Limit: 1000})
	if err != nil {
		t.Fatalf("listing conversations: %v", err)
	}
	for _, c := range convs {
		messages += c.MessageCount
	}
	return total, messages
}

// assertSampleLeftNoTrace checks the eval conversation ids the run used never
// became real conversations. This is the structural half of the isolation
// guarantee: ExecPolicy short-circuits resolveConversation and returns early
// from persistTurn, so there should be nothing under these ids at all.
func assertSampleLeftNoTrace(t *testing.T, h *Harness, samples []eval.Sample, runID int64) {
	t.Helper()
	var telemetry agent.TelemetryStore = h.Memory
	for _, smp := range samples {
		convID := eval.SampleConvID(runID, smp.TaskID, smp.KIndex, smp.VariantID)
		msgs, err := h.Memory.GetMessages(context.Background(), convID, 100)
		if err != nil {
			t.Fatalf("GetMessages(%s): %v", convID, err)
		}
		if len(msgs) != 0 {
			t.Errorf("conversation %s holds %d message(s); an eval sample must persist none", convID, len(msgs))
		}
		calls, err := telemetry.GetToolCalls(context.Background(), convID)
		if err != nil {
			t.Fatalf("GetToolCalls(%s): %v", convID, err)
		}
		if len(calls) != 0 {
			t.Errorf("conversation %s holds %d tool call(s)", convID, len(calls))
		}
		if stats, err := telemetry.GetConversationStats(context.Background(), convID); err == nil && stats != nil {
			t.Errorf("conversation %s has a stats row; eval turns must not reach conversation_stats", convID)
		}
	}
}

func TestEvalRun_TwoVariantsTwoTasksKTwo(t *testing.T) {
	h := evalHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{{
			Content:      "the answer",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
			FinishReason: "stop",
		}},
	})
	seedEvalSet(t, h, "regression", "first question", "second question")

	// Baseline for the isolation assertion: whatever the harness itself wrote.
	convsBefore, msgsBefore := memorySnapshot(t, h)

	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "regression",
		"base_agent": "default",
		"k":          2,
		"cost_cap":   10.0,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "candidate", "llm_model": "test-model"},
		},
	})

	run := evalRunStatus(t, h, runID)
	if run.Status != eval.StatusDone {
		t.Fatalf("status = %q, want %q (error %q)", run.Status, eval.StatusDone, run.Error)
	}

	samples := evalSamples(t, h, runID)
	if len(samples) != 8 {
		t.Fatalf("got %d samples, want 2 tasks × 2 variants × k=2 = 8", len(samples))
	}
	for _, smp := range samples {
		if smp.Status != eval.SampleOK {
			t.Errorf("sample %d is %q: %s", smp.ID, smp.Status, smp.Error)
		}
		if smp.Response != "the answer" {
			t.Errorf("sample %d response = %q, want the scripted answer", smp.ID, smp.Response)
		}
		if smp.TokensPrompt != 20 {
			t.Errorf("sample %d tokens_prompt = %d, want 20", smp.ID, smp.TokensPrompt)
		}
	}

	summary := evalSummary(t, h, runID)
	if len(summary.Variants) != 2 {
		t.Fatalf("summary has %d variants, want 2", len(summary.Variants))
	}
	for _, v := range summary.Variants {
		if v.SamplesOK != 4 {
			t.Errorf("variant %q has %d ok samples, want 4", v.Name, v.SamplesOK)
		}
		if v.RejectedRate != 0 || v.FailedRate != 0 {
			t.Errorf("variant %q rates = %v/%v, want 0/0 on a clean run", v.Name, v.RejectedRate, v.FailedRate)
		}
	}
	if summary.Completeness.SamplesOK != 8 || !summary.Completeness.Conclusive {
		t.Errorf("completeness = %+v, want 8/8 and conclusive", summary.Completeness)
	}
	if summary.BaselineVariant != "incumbent" {
		t.Errorf("baseline = %q, want the first variant", summary.BaselineVariant)
	}
	if len(summary.PerTask) != 2 {
		t.Errorf("per_task has %d entries, want one per task", len(summary.PerTask))
	}

	// Structural isolation: a policy turn never reaches the persistence layer,
	// so eight full turns must leave no trace in the real telemetry.
	convsAfter, msgsAfter := memorySnapshot(t, h)
	if convsAfter != convsBefore {
		t.Errorf("conversations grew from %d to %d — an eval sample must not create one", convsBefore, convsAfter)
	}
	if msgsAfter != msgsBefore {
		t.Errorf("messages grew from %d to %d — eval samples must not persist", msgsBefore, msgsAfter)
	}
	assertSampleLeftNoTrace(t, h, samples, runID)

	// Audit marking: every event the run produced carries source=eval and the
	// variant-scoped pseudo-agent, so ?agent=default excludes them for free.
	h.FlushAudit(t)
	events, _, err := h.AuditStore.List(context.Background(), audit.ListOpts{Source: "eval", Limit: 500})
	if err != nil {
		t.Fatalf("listing audit events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no audit events carried source=eval")
	}
	var sawStart, sawFinish bool
	for _, e := range events {
		if !strings.HasPrefix(e.Agent, "default#eval") {
			t.Errorf("event %q has agent %q, want the default#eval pseudo-identity", e.Action, e.Agent)
		}
		switch e.Action {
		case "eval_run_start":
			sawStart = true
		case "eval_run_finish":
			sawFinish = true
		}
	}
	if !sawStart || !sawFinish {
		t.Errorf("lifecycle events: start=%v finish=%v, want both", sawStart, sawFinish)
	}

	realAgent, _, err := h.AuditStore.List(context.Background(), audit.ListOpts{Agent: "default", Limit: 500})
	if err != nil {
		t.Fatalf("listing audit events: %v", err)
	}
	for _, e := range realAgent {
		if e.Source == "eval" {
			t.Errorf("?agent=default returned an eval event (%q); the pseudo-identity must exclude them", e.Action)
		}
	}
}

func TestEvalRun_CostCapStopsMidFlightWithCapped(t *testing.T) {
	// The harness router has no pricing registry, so TokenCost takes the
	// legacy $0.01/1k fallback. 50k tokens per completion is therefore $0.50 a
	// sample, and a $1.20 cap admits three of the eight before refusing to
	// dispatch the fourth.
	h := evalHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{{
			Content:      "expensive answer",
			TokensUsed:   llm.TokenUsage{Prompt: 40000, Completion: 10000, Total: 50000},
			Model:        "test-model",
			FinishReason: "stop",
		}},
	})
	seedEvalSet(t, h, "pricey", "one", "two", "three", "four")

	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "pricey",
		"base_agent": "default",
		"k":          1,
		"cost_cap":   1.20,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "candidate"},
		},
	})

	run := evalRunStatus(t, h, runID)
	if run.Status != eval.StatusCapped {
		t.Fatalf("status = %q, want %q (spent %v of %v)", run.Status, eval.StatusCapped, run.CostSpent, run.CostCap)
	}
	if run.CostSpent < run.CostCap {
		t.Errorf("cost_spent = %v, want it to have reached the %v cap", run.CostSpent, run.CostCap)
	}

	samples := evalSamples(t, h, runID)
	if len(samples) == 0 {
		t.Fatal("capped run kept no samples; partial results are the point of the status")
	}
	if len(samples) >= 8 {
		t.Fatalf("all %d samples ran despite the cap", len(samples))
	}
	for _, smp := range samples {
		if smp.Status != eval.SampleOK {
			t.Errorf("sample %d is %q — the cap must not kill a sample already in flight", smp.ID, smp.Status)
		}
	}

	summary := evalSummary(t, h, runID)
	if summary.Completeness.Conclusive {
		t.Error("a capped run below the floor reported conclusive; thin data must not read as a verdict")
	}
	if summary.Completeness.SamplesExpected != 8 {
		t.Errorf("samples_expected = %d, want the full 8 the run set out to do",
			summary.Completeness.SamplesExpected)
	}
}

func TestEvalRun_PanicCancelsActiveRun(t *testing.T) {
	h := evalHarness(t, nil)
	seedEvalSet(t, h, "slow", "one", "two", "three")
	// Long enough that the run is unambiguously mid-flight when panic lands.
	h.MockLLM.SetDelay(300 * time.Millisecond)

	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "slow",
		"base_agent": "default",
		"k":          2,
		"cost_cap":   10.0,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "candidate"},
		},
	})

	// Wait for the run to actually be executing before pulling the switch.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.MockLLM.CallCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if h.MockLLM.CallCount() == 0 {
		t.Fatal("run never reached the provider")
	}

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("panic: status = %d: %s", rec.Code, rec.Body.String())
	}

	run := evalRunStatus(t, h, runID)
	if run.Status != eval.StatusStopped {
		t.Fatalf("status = %q, want %q — panic must reach eval runs, which are never in the inFlight map",
			run.Status, eval.StatusStopped)
	}

	samples := evalSamples(t, h, runID)
	if len(samples) >= 12 {
		t.Errorf("all %d samples ran despite the panic", len(samples))
	}

	// Resume clears the panic but deliberately does not revive the run: a
	// panic is not a pause.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/resume", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume: status = %d", rec.Code)
	}
	time.Sleep(150 * time.Millisecond)

	rec = h.Do(h.AuthedRequest(http.MethodGet, fmt.Sprintf("/api/v1/eval/runs/%d", runID), nil))
	var detail struct {
		Status string `json:"status"`
		Active bool   `json:"active"`
	}
	DecodeJSON(t, rec, &detail)
	if detail.Status != eval.StatusStopped || detail.Active {
		t.Errorf("after resume the run is %q (active=%v), want it left stopped", detail.Status, detail.Active)
	}
}

// The full Stage C loop against a live server: run → pairs created at
// finalization → blinded items → verdicts → an unblinded upgrade.
func TestEvalRun_JudgedEndToEndProducesAnUpgradeVerdict(t *testing.T) {
	h := evalHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{{
			Content:      "the answer",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
			FinishReason: "stop",
		}},
	})
	seedEvalSet(t, h, "regression", "first question", "second question")

	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "regression",
		"base_agent": "default",
		"k":          1,
		"cost_cap":   10.0,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "candidate", "llm_model": "test-model"},
		},
	})
	if run := evalRunStatus(t, h, runID); run.Status != eval.StatusDone {
		t.Fatalf("status = %q, want %q (error %q)", run.Status, eval.StatusDone, run.Error)
	}

	ctx := context.Background()
	// Before any judging the objective half stands alone, and it can never
	// promote a candidate on its own.
	summary := evalSummary(t, h, runID)
	if len(summary.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(summary.Verdicts))
	}
	if got := summary.Verdicts[0].Verdict; got != eval.VerdictNoRegressions {
		t.Fatalf("unjudged verdict = %q (%s), want %q",
			got, summary.Verdicts[0].Reason, eval.VerdictNoRegressions)
	}
	if summary.Completeness.Pairs != 2 {
		t.Fatalf("got %d pairs, want 2 (2 tasks × k=1)", summary.Completeness.Pairs)
	}

	// Work the queue the way a judge would: pull the blinded item, decide, and
	// never consult the pair rows.
	pending, err := h.EvalStore.ListPending(ctx, runID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 4 {
		t.Fatalf("got %d pending items, want 4 (both presentation orders per pair)", len(pending))
	}
	candidateID := candidateVariantID(t, h, runID)
	for _, item := range pending {
		blinded, err := h.EvalStore.GetBlindedItem(ctx, item.ItemID)
		if err != nil {
			t.Fatalf("GetBlindedItem: %v", err)
		}
		if blinded.ResponseA.Response == "" || blinded.ResponseB.Response == "" {
			t.Fatalf("item %d is missing a response", item.ItemID)
		}
		if _, err := h.EvalStore.RecordVerdict(ctx, eval.Verdict{
			ItemID:     item.ItemID,
			Winner:     winnerFavouring(t, h, item.ItemID, candidateID),
			JudgeIdent: "integration-judge",
			Notes:      "candidate followed the persona more closely",
		}); err != nil {
			t.Fatalf("RecordVerdict: %v", err)
		}
	}

	summary = evalSummary(t, h, runID)
	vv := summary.Verdicts[0]
	if vv.Verdict != eval.VerdictUpgrade {
		t.Fatalf("judged verdict = %q (%s), want %q", vv.Verdict, vv.Reason, eval.VerdictUpgrade)
	}
	if vv.Judgment.JudgedPairs != 2 || vv.Judgment.Wins != 2 {
		t.Errorf("judgment = %+v, want 2 pairs judged and 2 wins", vv.Judgment)
	}
	if summary.Completeness.PairsJudged != 2 {
		t.Errorf("pairs judged = %d, want 2", summary.Completeness.PairsJudged)
	}
	if len(vv.Gates) != 3 || vv.Reason == "" {
		t.Errorf("verdict arrived without its work: %d gates, reason %q", len(vv.Gates), vv.Reason)
	}
}

// candidateVariantID resolves the non-baseline variant of a run.
func candidateVariantID(t *testing.T, h *Harness, runID int64) int64 {
	t.Helper()
	variants, err := h.EvalStore.ListVariants(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListVariants: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("got %d variants, want 2", len(variants))
	}
	return variants[1].ID
}

// winnerFavouring picks the presented letter that means `variant` on this item.
// A real judge decides on the responses; the test needs a deterministic outcome,
// so it works the unblinding backwards through the same helper the summary uses.
func winnerFavouring(t *testing.T, h *Harness, itemID, variant int64) string {
	t.Helper()
	ctx := context.Background()
	item, err := h.EvalStore.GetItem(ctx, itemID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	pair, err := h.EvalStore.GetPair(ctx, item.PairID)
	if err != nil {
		t.Fatalf("GetPair: %v", err)
	}
	assign, err := eval.DecodeAssignment(pair.Assignment)
	if err != nil {
		t.Fatalf("DecodeAssignment: %v", err)
	}
	for _, w := range []string{eval.WinnerA, eval.WinnerB} {
		if eval.VariantFor(assign, item.PresentationOrder, w) == variant {
			return w
		}
	}
	t.Fatalf("item %d resolves to neither side of variant %d", itemID, variant)
	return eval.WinnerTie
}

func TestEvalTaskSet_JSONLRoundTripThroughAPI(t *testing.T) {
	h := evalHarness(t, nil)
	seedEvalSet(t, h, "source", "alpha", "beta")
	seedEvalSet(t, h, "destination")

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/eval/task-sets/source/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status %d", rec.Code)
	}
	exported := rec.Body.String()
	if strings.Count(strings.TrimSpace(exported), "\n") != 1 {
		t.Fatalf("export produced %q, want two JSONL lines", exported)
	}

	req := h.AuthedRequest(http.MethodPost, "/api/v1/eval/task-sets/destination/import", nil)
	req.Body = io.NopCloser(strings.NewReader(exported))
	rec = h.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: status %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Imported int `json:"imported"`
	}
	DecodeJSON(t, rec, &result)
	if result.Imported != 2 {
		t.Fatalf("imported = %d, want 2", result.Imported)
	}

	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/eval/task-sets/destination", nil))
	var detail struct {
		Tasks []eval.Task `json:"tasks"`
	}
	DecodeJSON(t, rec, &detail)
	prompts := make([]string, 0, len(detail.Tasks))
	for _, task := range detail.Tasks {
		prompts = append(prompts, task.Prompt)
	}
	if strings.Join(prompts, ",") != "alpha,beta" {
		t.Errorf("imported prompts = %v, want [alpha beta] in order", prompts)
	}
}

// --- Internal judge ---

// The unattended half of the golden path: a finished run judged server-side by
// [eval] judge_model instead of from Claude Code over MCP. Same tables, same
// blinding, same aggregation — only the consumer differs.
func TestEvalRun_InternalJudgeProducesAnUpgradeVerdict(t *testing.T) {
	// The two variants answer differently so the judge has something to prefer;
	// without that a blinded pair is genuinely a tie and no run could ever be
	// an upgrade.
	h := evalHarness(t, &HarnessOpts{
		EvalJudgeModel: "judge-model",
		Responder: func(req llm.ChatRequest) (*llm.ChatResponse, error) {
			content := "the answer"
			if req.Model == "candidate-model" {
				content = "the thorough answer"
			}
			return &llm.ChatResponse{
				Content:      content,
				TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
				Model:        req.Model,
				FinishReason: "stop",
			}, nil
		},
	})
	seedEvalSet(t, h, "regression", "first question", "second question")

	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "regression",
		"base_agent": "default",
		"k":          1,
		"cost_cap":   10.0,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "candidate", "llm_model": "candidate-model"},
		},
	})
	if run := evalRunStatus(t, h, runID); run.Status != eval.StatusDone {
		t.Fatalf("status = %q, want %q", run.Status, eval.StatusDone)
	}

	// From here the mock answers as the judge, reading the blinded payload and
	// naming whichever side is the thorough one — which is the candidate in
	// both presentation orders, so the pair counts as a win.
	h.judgePrefers(t, "thorough")

	rec := h.Do(h.AuthedRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/eval/runs/%d/judge", runID), nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("judge: status %d: %s", rec.Code, rec.Body.String())
	}
	var pass eval.JudgePass
	if err := json.Unmarshal(rec.Body.Bytes(), &pass); err != nil {
		t.Fatalf("decoding pass: %v", err)
	}
	if pass.Items != 4 {
		t.Fatalf("pass took %d items, want 4 (2 pairs × both presentation orders)", pass.Items)
	}
	awaitJudging(t, h, runID)

	summary := evalSummary(t, h, runID)
	vv := summary.Verdicts[0]
	if vv.Judgment.JudgedPairs != 2 {
		t.Fatalf("judged pairs = %d, want 2 (judgment %+v)", vv.Judgment.JudgedPairs, vv.Judgment)
	}
	if vv.Verdict != eval.VerdictUpgrade {
		t.Fatalf("verdict = %q (%s), want %q", vv.Verdict, vv.Reason, eval.VerdictUpgrade)
	}
	// The internal judge's verdicts are ordinary verdicts: they carry the
	// rubric version and feed the same tally the MCP judge's do.
	if len(vv.Judgment.RubricVersions) != 1 || vv.Judgment.RubricVersions[0] != eval.RubricVersion {
		t.Errorf("rubric_versions = %v, want [%s]", vv.Judgment.RubricVersions, eval.RubricVersion)
	}
	verdicts, err := h.EvalStore.ListVerdicts(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	for _, v := range verdicts {
		if v.JudgeIdent != eval.JudgeInternal {
			t.Errorf("verdict %d judge_ident = %q, want %q", v.ID, v.JudgeIdent, eval.JudgeInternal)
		}
	}
	// Judging spend lands on judge_cost, not on the run's sample budget.
	run, err := h.EvalStore.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.JudgeCost <= 0 {
		t.Errorf("judge_cost = %v, want the pass's spend recorded", run.JudgeCost)
	}
}

// The Stage D constraint restated for the internal judge: it must be unable to
// unblind its own queue. The MCP judge is held to it by its tool set being
// pinned at five names; the internal judge is held to it by having no tools at
// all, and by never seeing an identity field in the first place.
func TestEvalRun_InternalJudgeIsOneShotAndBlind(t *testing.T) {
	h := evalHarness(t, &HarnessOpts{
		EvalJudgeModel: "judge-model",
		Responses: []*llm.ChatResponse{{
			Content:      "the answer",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
			FinishReason: "stop",
		}},
	})
	seedEvalSet(t, h, "regression", "first question")

	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "regression",
		"base_agent": "default",
		"k":          1,
		"cost_cap":   10.0,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "kimi-k3-candidate", "llm_model": "kimi-k3"},
		},
	})
	if run := evalRunStatus(t, h, runID); run.Status != eval.StatusDone {
		t.Fatalf("status = %q, want %q", run.Status, eval.StatusDone)
	}
	before := h.MockLLM.CallCount()
	h.judgeAlwaysPicks(t, "tie")

	rec := h.Do(h.AuthedRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/eval/runs/%d/judge", runID), nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("judge: status %d: %s", rec.Code, rec.Body.String())
	}
	awaitJudging(t, h, runID)

	reqs := h.MockLLM.requestsFrom(before)
	if len(reqs) == 0 {
		t.Fatal("the judge sent nothing")
	}
	for i, req := range reqs {
		if len(req.Tools) != 0 {
			t.Errorf("judge request %d carried %d tool definitions; it must be one-shot",
				i, len(req.Tools))
		}
		if req.Model != "judge-model" {
			t.Errorf("judge request %d ran on %q, want judge-model", i, req.Model)
		}
		var sb strings.Builder
		for _, m := range req.Messages {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
		for _, forbidden := range []string{"kimi-k3-candidate", "kimi-k3", "llm_model", "eval:"} {
			if strings.Contains(sb.String(), forbidden) {
				t.Errorf("judge request %d leaks %q", i, forbidden)
			}
		}
	}
}

func TestEvalRun_InternalJudgeIsOffWithoutAModel(t *testing.T) {
	h := evalHarness(t, nil)
	seedEvalSet(t, h, "regression", "first question")
	runID := startEvalRun(t, h, map[string]any{
		"task_set":   "regression",
		"base_agent": "default",
		"k":          1,
		"cost_cap":   10.0,
		"variants": []map[string]any{
			{"name": "incumbent"},
			{"name": "candidate", "llm_model": "test-model"},
		},
	})
	evalRunStatus(t, h, runID)

	rec := h.Do(h.AuthedRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/eval/runs/%d/judge", runID), nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 without [eval] judge_model", rec.Code)
	}
	// The MCP judge path is untouched: the queue is still waiting.
	pending, err := h.EvalStore.ListPending(context.Background(), runID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("got %d pending items, want the pair's two orders still queued", len(pending))
	}
}

// judgeAlwaysPicks switches the mock LLM over to answering as the judge with a
// fixed letter. Naming the same presented letter in both orders resolves to
// two different variants, so this is the position-bias control's input, not a
// way to make a candidate win.
func (h *Harness) judgeAlwaysPicks(t *testing.T, winner string) {
	t.Helper()
	h.MockLLM.SetResponder(func(llm.ChatRequest) (*llm.ChatResponse, error) {
		return judgeReply(winner), nil
	})
}

// judgePrefers answers as a judge that actually reads the blinded payload: it
// names whichever presented response carries marker. That is what makes both
// presentation orders agree on one variant, which is the only way a pair
// counts as a win.
func (h *Harness) judgePrefers(t *testing.T, marker string) {
	t.Helper()
	h.MockLLM.SetResponder(func(req llm.ChatRequest) (*llm.ChatResponse, error) {
		item, err := blindedItemOf(req)
		if err != nil {
			return nil, err
		}
		switch {
		case strings.Contains(item.ResponseA.Response, marker):
			return judgeReply("a"), nil
		case strings.Contains(item.ResponseB.Response, marker):
			return judgeReply("b"), nil
		default:
			return judgeReply("tie"), nil
		}
	})
}

func judgeReply(winner string) *llm.ChatResponse {
	return &llm.ChatResponse{
		Content: fmt.Sprintf(
			`{"winner":%q,"dimensions":{"task_success":%q,"tool_path":"tie"},"notes":"judged"}`,
			winner, winner),
		TokensUsed:   llm.TokenUsage{Prompt: 400, Completion: 40, Total: 440},
		Model:        "judge-model",
		CostUSD:      0.001,
		FinishReason: "stop",
	}
}

// blindedItemOf recovers the payload the judge was handed, which is the whole
// blinded item serialised into the user message.
func blindedItemOf(req llm.ChatRequest) (*eval.BlindedItem, error) {
	for _, m := range req.Messages {
		if m.Role != "user" {
			continue
		}
		start := strings.Index(m.Content, "{")
		end := strings.LastIndex(m.Content, "}")
		if start < 0 || end <= start {
			continue
		}
		var item eval.BlindedItem
		if err := json.Unmarshal([]byte(m.Content[start:end+1]), &item); err != nil {
			return nil, err
		}
		return &item, nil
	}
	return nil, fmt.Errorf("no judgment item in the request")
}

// awaitJudging waits for the background pass to finish, reading the same
// `judging` flag GET /eval/runs/{id} reports to the page.
func awaitJudging(t *testing.T, h *Harness, runID int64) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rec := h.Do(h.AuthedRequest(http.MethodGet,
			fmt.Sprintf("/api/v1/eval/runs/%d", runID), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("run detail: status %d: %s", rec.Code, rec.Body.String())
		}
		var detail struct {
			Judging bool `json:"judging"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
			t.Fatalf("decoding run detail: %v", err)
		}
		if !detail.Judging {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("judging pass did not finish within 20s")
}
