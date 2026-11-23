package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func evalReadCtx() context.Context {
	return withScopes(context.Background(), []string{"eval:read"})
}

func evalWriteCtx() context.Context {
	return withKeyName(withScopes(context.Background(), []string{"eval:write"}), "judge-key")
}

// evalServer wires a Server over an in-memory eval store holding one finished
// two-variant run: one task, k=1, both samples ok, pairs created.
func evalServer(t *testing.T) (*Server, *eval.Store, int64) {
	t.Helper()
	store, err := eval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating eval store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	set, err := store.CreateTaskSet(ctx, "regression", "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	task, err := store.AddTask(ctx, set.ID, eval.Task{
		Prompt: "what is on today?", Category: eval.CategoryChat, Notes: "should check the calendar",
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	run, variants, err := store.CreateRun(ctx, eval.Run{
		TaskSetID: set.ID, BaseAgent: "pamela", K: 1, CostCap: 2, AsOf: time.Now().UTC(),
	}, []eval.Variant{{Name: "incumbent"}, {Name: "candidate", Overlay: `{"llm_model":"kimi-k3"}`}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	for _, v := range variants {
		if _, err := store.AddSample(ctx, eval.Sample{
			RunID: run.ID, VariantID: v.ID, TaskID: task.ID, KIndex: 0,
			Status: eval.SampleOK, Response: "an answer", Rounds: 2, Cost: 0.1, OutcomeOK: 5,
		}); err != nil {
			t.Fatalf("AddSample: %v", err)
		}
	}
	if err := store.FinishRun(ctx, run.ID, eval.StatusDone, ""); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	if _, err := store.CreatePairs(ctx, run.ID); err != nil {
		t.Fatalf("CreatePairs: %v", err)
	}
	return &Server{deps: Deps{EvalStore: store, Logger: testLogger()}}, store, run.ID
}

// An unconfigured eval subsystem degrades to a readable tool error rather than
// an unregistered tool — the audit_events convention.
func TestEvalTools_UnconfiguredReportGracefully(t *testing.T) {
	s := &Server{deps: Deps{Logger: testLogger()}}

	res, _, _ := s.handleEvalPending(evalReadCtx(), nil, evalPendingInput{})
	if !res.IsError || !strings.Contains(toolResultText(res), "eval not configured") {
		t.Errorf("eval_pending on an unconfigured server: %s", toolResultText(res))
	}
	res, _, _ = s.handleEvalVerdict(evalWriteCtx(), nil, evalVerdictInput{ItemID: 1, Winner: "a"})
	if !res.IsError || !strings.Contains(toolResultText(res), "eval not configured") {
		t.Errorf("eval_verdict on an unconfigured server: %s", toolResultText(res))
	}
}

func TestEvalTools_RequireTheirScopes(t *testing.T) {
	s, _, runID := evalServer(t)
	none := context.Background()

	if res, _, _ := s.handleEvalPending(none, nil, evalPendingInput{}); !res.IsError {
		t.Error("eval_pending ran without eval:read")
	}
	if res, _, _ := s.handleEvalSummary(none, nil, evalRunInput{RunID: runID}); !res.IsError {
		t.Error("eval_summary ran without eval:read")
	}
	// A read-scoped key must not be able to write a verdict.
	res, _, _ := s.handleEvalVerdict(evalReadCtx(), nil, evalVerdictInput{ItemID: 1, Winner: "a"})
	if !res.IsError || !strings.Contains(toolResultText(res), "eval:write") {
		t.Errorf("eval_verdict accepted a read-only key: %s", toolResultText(res))
	}
}

func TestEvalPending_ListsQueueAndGetPairStaysBlind(t *testing.T) {
	s, _, runID := evalServer(t)

	res, _, err := s.handleEvalPending(evalReadCtx(), nil, evalPendingInput{RunID: runID})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var queue struct {
		Items []eval.PendingItem `json:"items"`
		Count int                `json:"count"`
	}
	if err := json.Unmarshal([]byte(toolResultText(res)), &queue); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if queue.Count != 2 || len(queue.Items) != 2 {
		t.Fatalf("queue = %+v, want 2 items (both presentation orders)", queue)
	}

	res, _, _ = s.handleEvalGetPair(evalReadCtx(), nil, evalGetPairInput{ItemID: queue.Items[0].ItemID})
	if res.IsError {
		t.Fatalf("eval_get_pair: %s", toolResultText(res))
	}
	payload := toolResultText(res)
	for _, forbidden := range []string{"incumbent", "candidate", "kimi-k3", "variant", "cost"} {
		if strings.Contains(payload, forbidden) {
			t.Errorf("eval_get_pair leaked %q:\n%s", forbidden, payload)
		}
	}
	var item eval.BlindedItem
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item.Prompt == "" || item.Notes == "" {
		t.Errorf("blinded item lost its task context: %+v", item)
	}
}

func TestEvalVerdict_RecordsAndDefaultsJudgeToTheKeyName(t *testing.T) {
	s, store, runID := evalServer(t)
	ctx := context.Background()
	items, err := store.ListItems(ctx, runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	res, _, _ := s.handleEvalVerdict(evalWriteCtx(), nil, evalVerdictInput{
		ItemID: items[0].ID, Winner: "A",
		Dimensions: `{"task_success":"a","length":"tie"}`,
		Notes:      "A answered the whole question",
	})
	if res.IsError {
		t.Fatalf("eval_verdict: %s", toolResultText(res))
	}
	var got eval.Verdict
	if err := json.Unmarshal([]byte(toolResultText(res)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Winner != eval.WinnerA {
		t.Errorf("winner = %q, want it normalized to %q", got.Winner, eval.WinnerA)
	}
	if got.JudgeIdent != "judge-key" {
		t.Errorf("judge_ident = %q, want it defaulted to the API key name", got.JudgeIdent)
	}
	item, err := store.GetItem(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Status != eval.ItemJudged {
		t.Errorf("item status = %q, want %q", item.Status, eval.ItemJudged)
	}
}

// The rubric version has to survive the whole path — MCP input, store row,
// aggregation — or the results view cannot name the policy a win-rate was
// produced under.
func TestEvalVerdict_RubricVersionReachesTheSummary(t *testing.T) {
	s, store, runID := evalServer(t)
	ctx := context.Background()
	items, err := store.ListItems(ctx, runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	for _, it := range items {
		res, _, _ := s.handleEvalVerdict(evalWriteCtx(), nil, evalVerdictInput{
			ItemID: it.ID, Winner: "a", RubricVersion: "  v1  ",
		})
		if res.IsError {
			t.Fatalf("eval_verdict: %s", toolResultText(res))
		}
	}

	res, _, _ := s.handleEvalSummary(evalReadCtx(), nil, evalRunInput{RunID: runID})
	if res.IsError {
		t.Fatalf("eval_summary: %s", toolResultText(res))
	}
	var sum eval.Summary
	if err := json.Unmarshal([]byte(toolResultText(res)), &sum); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sum.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(sum.Verdicts))
	}
	got := sum.Verdicts[0].Judgment.RubricVersions
	if len(got) != 1 || got[0] != "v1" {
		t.Errorf("rubric_versions = %v, want [v1] with the padding trimmed", got)
	}
}

// listEvalToolNames connects an in-process client and returns what the judge
// surface advertises.
func listEvalToolNames(t *testing.T) []string {
	t.Helper()
	s := &Server{deps: Deps{Logger: testLogger()}}
	s.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "denkeeper", Version: "test"}, nil)
	s.registerEvalTools()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.mcpServer.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "tool-list-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// The judge surface is exactly six tools, and none of them unblinds a pair
// while the run can still take verdicts. eval_samples names the variant behind
// each response, so it opens only once no item is pending, and from then on
// RecordVerdict refuses judge verdicts. The unblinded pair view
// (GET /eval/runs/{id}/pairs) stays REST-only.
func TestEvalTools_JudgeSurfaceCannotUnblindAPair(t *testing.T) {
	want := []string{"eval_get_pair", "eval_pending", "eval_run_status", "eval_samples", "eval_summary", "eval_verdict"}
	got := listEvalToolNames(t)

	if len(got) != len(want) {
		t.Fatalf("judge tools = %v, want exactly %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("judge tools = %v, want exactly %v", got, want)
		}
	}
}

func TestEvalVerdict_RejectsBadWinnersAndDimensions(t *testing.T) {
	s, store, runID := evalServer(t)
	items, err := store.ListItems(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	id := items[0].ID

	cases := []struct {
		name  string
		input evalVerdictInput
		want  string
	}{
		{"unknown winner", evalVerdictInput{ItemID: id, Winner: "maybe"}, "invalid winner"},
		{"missing item", evalVerdictInput{Winner: "a"}, "item_id is required"},
		{"malformed dimensions", evalVerdictInput{ItemID: id, Winner: "a", Dimensions: "not json"}, "invalid dimensions"},
		{"unknown dimension", evalVerdictInput{ItemID: id, Winner: "a", Dimensions: `{"vibes":"a"}`}, "unknown dimension"},
		{"bad dimension winner", evalVerdictInput{ItemID: id, Winner: "a", Dimensions: `{"length":"neither"}`}, "invalid winner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, _, _ := s.handleEvalVerdict(evalWriteCtx(), nil, c.input)
			if !res.IsError || !strings.Contains(toolResultText(res), c.want) {
				t.Errorf("got %q, want an error mentioning %q", toolResultText(res), c.want)
			}
		})
	}
}

func TestEvalSummaryAndRunStatus(t *testing.T) {
	s, _, runID := evalServer(t)

	res, _, _ := s.handleEvalRunStatus(evalReadCtx(), nil, evalRunInput{RunID: runID})
	if res.IsError {
		t.Fatalf("eval_run_status: %s", toolResultText(res))
	}
	var status evalRunStatus
	if err := json.Unmarshal([]byte(toolResultText(res)), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !status.Terminal || status.Pairs != 1 || status.PendingItems != 2 {
		t.Errorf("status = %+v, want terminal with 1 pair and 2 pending items", status)
	}

	res, _, _ = s.handleEvalSummary(evalReadCtx(), nil, evalRunInput{RunID: runID})
	if res.IsError {
		t.Fatalf("eval_summary: %s", toolResultText(res))
	}
	var sum eval.Summary
	if err := json.Unmarshal([]byte(toolResultText(res)), &sum); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sum.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(sum.Verdicts))
	}
	// eval_summary is the unblinding surface: here the variant names are the
	// point, and the verdict must carry its work rather than a bare label.
	if sum.Verdicts[0].Variant != "candidate" || sum.Verdicts[0].Reason == "" {
		t.Errorf("verdict = %+v, want the named candidate with a reason", sum.Verdicts[0])
	}
	if len(sum.Verdicts[0].Gates) != 3 {
		t.Errorf("got %d gates, want 3", len(sum.Verdicts[0].Gates))
	}
}

// judgeEvalRun records a judge verdict on every item of the run.
func judgeEvalRun(t *testing.T, store *eval.Store, runID int64) {
	t.Helper()
	items, err := store.ListItems(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range items {
		if _, err := store.RecordVerdict(context.Background(), eval.Verdict{
			ItemID: it.ID, Winner: eval.WinnerA, JudgeIdent: "judge-key",
		}); err != nil {
			t.Fatalf("RecordVerdict: %v", err)
		}
	}
}

type samplesPayload struct {
	RunID    int64             `json:"run_id"`
	Variants []eval.Variant    `json:"variants"`
	Samples  []json.RawMessage `json:"samples"`
	Count    int               `json:"count"`
	Total    int               `json:"total"`
}

func callEvalSamples(t *testing.T, s *Server, input evalSamplesInput) samplesPayload {
	t.Helper()
	res, _, err := s.handleEvalSamples(evalReadCtx(), nil, input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("eval_samples: %s", toolResultText(res))
	}
	var out samplesPayload
	if err := json.Unmarshal([]byte(toolResultText(res)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestEvalSamples_RefusedWhileItemsPending(t *testing.T) {
	s, _, runID := evalServer(t)

	res, _, _ := s.handleEvalSamples(evalReadCtx(), nil, evalSamplesInput{RunID: runID})
	text := toolResultText(res)
	if !res.IsError || !strings.Contains(text, "has 2 judgment items pending") {
		t.Fatalf("eval_samples with pending items = %q, want a refusal naming the count", text)
	}
	for _, forbidden := range []string{"incumbent", "candidate", "kimi-k3"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("refusal leaked %q: %s", forbidden, text)
		}
	}
}

func TestEvalSamples_RefusedUntilRunFinishes(t *testing.T) {
	s, store, _ := evalServer(t)
	ctx := context.Background()
	set, err := store.CreateTaskSet(ctx, "unfinished", "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	if _, err := store.AddTask(ctx, set.ID, eval.Task{Prompt: "p", Category: eval.CategoryChat}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	run, _, err := store.CreateRun(ctx, eval.Run{
		TaskSetID: set.ID, BaseAgent: "pamela", K: 1, CostCap: 2, AsOf: time.Now().UTC(),
	}, []eval.Variant{{Name: "incumbent"}, {Name: "candidate"}})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	res, _, _ := s.handleEvalSamples(evalReadCtx(), nil, evalSamplesInput{RunID: run.ID})
	if !res.IsError || !strings.Contains(toolResultText(res), "opens once the run has finished") {
		t.Errorf("eval_samples on an unfinished run = %q, want the not-finished refusal", toolResultText(res))
	}
}

func TestEvalSamples_OperatorMarksDoNotOpenIt(t *testing.T) {
	s, store, runID := evalServer(t)
	items, err := store.ListItems(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range items {
		if _, err := store.RecordVerdict(context.Background(), eval.Verdict{
			ItemID: it.ID, Winner: eval.WinnerA, JudgeIdent: eval.JudgeOperator,
		}); err != nil {
			t.Fatalf("RecordVerdict(operator): %v", err)
		}
	}

	res, _, _ := s.handleEvalSamples(evalReadCtx(), nil, evalSamplesInput{RunID: runID})
	if !res.IsError || !strings.Contains(toolResultText(res), "pending") {
		t.Errorf("eval_samples after operator marks only = %q, want still refused", toolResultText(res))
	}
}

func TestEvalSamples_FullyJudgedRunReturnsSamplesAndVariants(t *testing.T) {
	s, store, runID := evalServer(t)
	judgeEvalRun(t, store, runID)

	got := callEvalSamples(t, s, evalSamplesInput{RunID: runID})
	if got.RunID != runID || got.Count != 2 || got.Total != 2 || len(got.Samples) != 2 {
		t.Fatalf("payload = run %d count %d total %d samples %d, want run %d with 2/2/2",
			got.RunID, got.Count, got.Total, len(got.Samples), runID)
	}
	names := map[string]bool{}
	for _, v := range got.Variants {
		names[v.Name] = true
	}
	if !names["incumbent"] || !names["candidate"] {
		t.Errorf("variants = %+v, want incumbent and candidate", got.Variants)
	}

	rows, err := store.ListTaskSamples(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("ListTaskSamples: %v", err)
	}
	for i, row := range rows {
		want, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(got.Samples[i]) != string(want) {
			t.Errorf("sample %d = %s\nwant the store row as REST serialises it: %s", i, got.Samples[i], want)
		}
	}
}

func TestEvalSamples_TaskIDNarrows(t *testing.T) {
	s, store, runID := evalServer(t)
	judgeEvalRun(t, store, runID)
	rows, err := store.ListTaskSamples(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("ListTaskSamples: %v", err)
	}

	got := callEvalSamples(t, s, evalSamplesInput{RunID: runID, TaskID: rows[0].TaskID})
	if got.Total != 2 || got.Count != 2 {
		t.Errorf("task_id %d: count %d total %d, want 2/2", rows[0].TaskID, got.Count, got.Total)
	}
	got = callEvalSamples(t, s, evalSamplesInput{RunID: runID, TaskID: 9999})
	if got.Samples == nil || len(got.Samples) != 0 || got.Total != 0 {
		t.Errorf("unknown task_id: samples %v total %d, want an empty array and total 0", got.Samples, got.Total)
	}
}

func TestEvalSamples_Paging(t *testing.T) {
	s, store, runID := evalServer(t)
	judgeEvalRun(t, store, runID)
	rows, err := store.ListTaskSamples(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("ListTaskSamples: %v", err)
	}

	got := callEvalSamples(t, s, evalSamplesInput{RunID: runID, Limit: 1, Offset: 1})
	if got.Count != 1 || got.Total != 2 {
		t.Fatalf("limit 1 offset 1: count %d total %d, want 1/2", got.Count, got.Total)
	}
	var smp eval.Sample
	if err := json.Unmarshal(got.Samples[0], &smp); err != nil {
		t.Fatalf("unmarshal sample: %v", err)
	}
	if smp.ID != rows[1].ID {
		t.Errorf("limit 1 offset 1 returned sample %d, want the second, %d", smp.ID, rows[1].ID)
	}

	got = callEvalSamples(t, s, evalSamplesInput{RunID: runID, Offset: 5})
	if got.Count != 0 || got.Total != 2 || got.Samples == nil {
		t.Errorf("offset past the end: count %d total %d samples %v, want an empty array and total 2",
			got.Count, got.Total, got.Samples)
	}
	if n := clampSamplesLimit(500); n != evalSamplesMaxLimit {
		t.Errorf("clampSamplesLimit(500) = %d, want %d", n, evalSamplesMaxLimit)
	}
}

func TestEvalSamples_ScopeAndConfiguration(t *testing.T) {
	s, store, runID := evalServer(t)
	judgeEvalRun(t, store, runID)

	if res, _, _ := s.handleEvalSamples(context.Background(), nil, evalSamplesInput{RunID: runID}); !res.IsError ||
		!strings.Contains(toolResultText(res), "eval:read") {
		t.Errorf("eval_samples without eval:read = %q, want refused", toolResultText(res))
	}
	bare := &Server{deps: Deps{Logger: testLogger()}}
	if res, _, _ := bare.handleEvalSamples(evalReadCtx(), nil, evalSamplesInput{RunID: runID}); !res.IsError ||
		!strings.Contains(toolResultText(res), "eval not configured") {
		t.Errorf("eval_samples unconfigured = %q, want eval not configured", toolResultText(res))
	}
	if res, _, _ := s.handleEvalSamples(evalReadCtx(), nil, evalSamplesInput{}); !res.IsError ||
		!strings.Contains(toolResultText(res), "run_id is required") {
		t.Errorf("eval_samples without run_id = %q", toolResultText(res))
	}
	if res, _, _ := s.handleEvalSamples(evalReadCtx(), nil, evalSamplesInput{RunID: runID, TaskID: -1}); !res.IsError ||
		!strings.Contains(toolResultText(res), "task_id must be a positive integer") {
		t.Errorf("eval_samples with task_id -1 = %q", toolResultText(res))
	}
}

func TestEvalVerdict_FullyJudgedRunTakesNoMoreVerdicts(t *testing.T) {
	s, store, runID := evalServer(t)
	judgeEvalRun(t, store, runID)
	items, err := store.ListItems(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	res, _, _ := s.handleEvalVerdict(evalWriteCtx(), nil, evalVerdictInput{ItemID: items[0].ID, Winner: "b"})
	if !res.IsError || !strings.Contains(toolResultText(res), "takes no more verdicts") {
		t.Errorf("eval_verdict on a fully judged run = %q, want refused", toolResultText(res))
	}
}

func TestEvalVerdict_OperatorMarkOnJudgedItemIsRefused(t *testing.T) {
	s, store, runID := evalServer(t)
	items, err := store.ListItems(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if res, _, _ := s.handleEvalVerdict(evalWriteCtx(), nil, evalVerdictInput{ItemID: items[0].ID, Winner: "a"}); res.IsError {
		t.Fatalf("judge verdict: %s", toolResultText(res))
	}

	res, _, _ := s.handleEvalVerdict(evalWriteCtx(), nil, evalVerdictInput{
		ItemID: items[0].ID, Winner: "b", JudgeIdent: eval.JudgeOperator,
	})
	if !res.IsError || !strings.Contains(toolResultText(res), "already has a judge verdict") {
		t.Errorf("operator mark on a judged item = %q, want refused", toolResultText(res))
	}
}

// While a run can still take verdicts, nothing a judge can call names a
// variant or the candidate model. eval_summary is left out: it names
// variants by design.
func TestEvalTools_NoIdentityWhileItemsPending(t *testing.T) {
	s, store, runID := evalServer(t)
	items, err := store.ListItems(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	ctx := evalReadCtx()
	var outputs []string
	res, _, _ := s.handleEvalPending(ctx, nil, evalPendingInput{RunID: runID})
	outputs = append(outputs, toolResultText(res))
	res, _, _ = s.handleEvalGetPair(ctx, nil, evalGetPairInput{ItemID: items[0].ID})
	outputs = append(outputs, toolResultText(res))
	res, _, _ = s.handleEvalRunStatus(ctx, nil, evalRunInput{RunID: runID})
	outputs = append(outputs, toolResultText(res))
	res, _, _ = s.handleEvalSamples(ctx, nil, evalSamplesInput{RunID: runID})
	outputs = append(outputs, toolResultText(res))

	for i, out := range outputs {
		for _, forbidden := range []string{"incumbent", "candidate", "kimi-k3"} {
			if strings.Contains(out, forbidden) {
				t.Errorf("output %d leaked %q: %s", i, forbidden, out)
			}
		}
	}
}
