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

// The judge surface is exactly five tools, and none of them unblinds a pair.
// The unblinded view (GET /eval/runs/{id}/pairs) is REST-only on purpose: a
// judge that can look up which variant produced which response can unblind its
// own queue, and every position-bias control downstream stops meaning anything.
func TestEvalTools_JudgeSurfaceCannotUnblindAPair(t *testing.T) {
	want := []string{"eval_get_pair", "eval_pending", "eval_run_status", "eval_summary", "eval_verdict"}
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
