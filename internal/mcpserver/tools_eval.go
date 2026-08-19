package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The judge tools. They read Deps.EvalStore, which is deliberately distinct
// from the write-path runner the REST handlers use: a judge can read pairs and
// record verdicts, and cannot touch run state. eval_verdict is the only writer
// here, and it writes verdicts only.
//
// Blinding is server-side and total. eval_get_pair builds its payload from
// scratch in the eval package, so nothing about which variant produced which
// response can reach a judge — a verdict tool cannot leak what it was never
// given.

type evalPendingInput struct {
	RunID   int64 `json:"run_id,omitempty" jsonschema:"Only list items for this run; omit for every run"`
	SampleN int   `json:"sample_n,omitempty" jsonschema:"Draw this many items at random instead of taking the head of the queue — use for the interactive calibration subset (~20)"`
	Limit   int   `json:"limit,omitempty" jsonschema:"Max items to return (default 50, max 500)"`
}

type evalGetPairInput struct {
	ItemID int64 `json:"item_id" jsonschema:"Judgment item id from eval_pending"`
}

type evalVerdictInput struct {
	ItemID     int64  `json:"item_id" jsonschema:"Judgment item id being judged"`
	Winner     string `json:"winner" jsonschema:"Which response won: a, b, or tie"`
	Notes      string `json:"notes,omitempty" jsonschema:"Short rationale; cite the persona or skill clause behind any persona_fit or task_success deduction"`
	Dimensions string `json:"dimensions,omitempty" jsonschema:"JSON object of dimension to winner, e.g. {\"task_success\":\"a\",\"tool_path\":\"tie\",\"persona_fit\":\"a\",\"length\":\"b\"}"`
	JudgeIdent string `json:"judge_ident,omitempty" jsonschema:"Who is judging; defaults to the API key name. Pass \"operator\" to record a human calibration mark alongside the judge's verdict rather than replacing it"`
	// RubricVersion is stored verbatim: the rubric is a skill file the operator
	// edits, so the server has no list of valid versions to check against.
	RubricVersion string `json:"rubric_version,omitempty" jsonschema:"The judging rubric revision this call was made under, e.g. \"v1\" — copy the 'Rubric version' line of the judging skill so the results view can name what the win-rate was judged against"`
}

type evalRunInput struct {
	RunID int64 `json:"run_id" jsonschema:"Eval run id"`
}

const evalPendingMaxLimit = 500

func (s *Server) registerEvalTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_pending",
		Description: "List blinded judgment items still awaiting a verdict, optionally for one " +
			"run. Each pair of responses is queued twice with the presentation order " +
			"swapped, so both orders must be judged before the pair counts. Use " +
			"'sample_n' to draw a random calibration subset rather than the head of the " +
			"queue. Requires 'eval:read' scope.",
	}, s.handleEvalPending)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_get_pair",
		Description: "Get one blinded judgment item: the task prompt, its notes and pinned " +
			"history, and the two responses with their tool traces. Which variant produced " +
			"which response is never included — model, provider, cost, latency and usage " +
			"are all withheld. Judge on the responses alone. Requires 'eval:read' scope.",
	}, s.handleEvalGetPair)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_verdict",
		Description: "Record a verdict on one judgment item: which response won (a, b, or tie), " +
			"optional per-dimension winners (task_success, tool_path, persona_fit, length), " +
			"notes, and the rubric version it was judged under. Re-judging the same item " +
			"overwrites your own earlier verdict. Requires 'eval:write' scope.",
	}, s.handleEvalVerdict)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_summary",
		Description: "Unblind and aggregate one run: per-variant objective metrics, the gate " +
			"table with deltas and pass/fail, the judge win-rate over pairs both " +
			"presentation orders agreed on, the operator-judge agreement figure, a " +
			"per-category breakdown, and the verdict with its plain-language reason. " +
			"Requires 'eval:read' scope.",
	}, s.handleEvalSummary)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_run_status",
		Description: "Get an eval run's status, progress and cost so far, plus how many judgment " +
			"pairs it produced and how many are still pending. Use it to wait for a run to " +
			"finish before judging. Requires 'eval:read' scope.",
	}, s.handleEvalRunStatus)
}

// evalStore returns the store, or the graceful error an unconfigured subsystem
// gets. The tools stay registered either way, following audit_events: an
// advertised tool that explains itself beats a tool that silently is not there.
func (s *Server) evalStore() (*eval.Store, *mcp.CallToolResult) {
	if s.deps.EvalStore == nil {
		return nil, toolError("eval not configured")
	}
	return s.deps.EvalStore, nil
}

func (s *Server) handleEvalPending(ctx context.Context, _ *mcp.CallToolRequest, input evalPendingInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "eval:read"); err != nil {
		return err, nil, nil
	}
	store, errRes := s.evalStore()
	if errRes != nil {
		return errRes, nil, nil
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > evalPendingMaxLimit {
		limit = evalPendingMaxLimit
	}
	items, err := store.ListPending(ctx, input.RunID, limit, input.SampleN)
	if err != nil {
		return toolError("listing pending judgment items: " + err.Error()), nil, nil
	}
	r, jsonErr := toolJSON(map[string]any{"items": items, "count": len(items)})
	return r, nil, jsonErr
}

func (s *Server) handleEvalGetPair(ctx context.Context, _ *mcp.CallToolRequest, input evalGetPairInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "eval:read"); err != nil {
		return err, nil, nil
	}
	store, errRes := s.evalStore()
	if errRes != nil {
		return errRes, nil, nil
	}
	if input.ItemID <= 0 {
		return toolError("item_id is required"), nil, nil
	}
	item, err := store.GetBlindedItem(ctx, input.ItemID)
	if err != nil {
		return toolError("getting judgment item: " + err.Error()), nil, nil
	}
	r, jsonErr := toolJSON(item)
	return r, nil, jsonErr
}

func (s *Server) handleEvalVerdict(ctx context.Context, _ *mcp.CallToolRequest, input evalVerdictInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "eval:write"); err != nil {
		return err, nil, nil
	}
	store, errRes := s.evalStore()
	if errRes != nil {
		return errRes, nil, nil
	}
	if input.ItemID <= 0 {
		return toolError("item_id is required"), nil, nil
	}
	winner := strings.ToLower(strings.TrimSpace(input.Winner))
	if !eval.ValidWinner(winner) {
		return toolError(fmt.Sprintf("invalid winner %q: want a, b, or tie", input.Winner)), nil, nil
	}
	dims, errRes := normalizeDimensions(input.Dimensions)
	if errRes != nil {
		return errRes, nil, nil
	}

	verdict, err := store.RecordVerdict(ctx, eval.Verdict{
		ItemID:        input.ItemID,
		Winner:        winner,
		Dimensions:    dims,
		Notes:         input.Notes,
		JudgeIdent:    judgeIdent(ctx, input.JudgeIdent),
		RubricVersion: strings.TrimSpace(input.RubricVersion),
	})
	if err != nil {
		return toolError("recording verdict: " + err.Error()), nil, nil
	}
	r, jsonErr := toolJSON(verdict)
	return r, nil, jsonErr
}

// judgeIdent names who judged. It defaults to the API key so verdicts are
// attributable without the caller having to remember, and the operator's
// calibration marks pass "operator" explicitly — that identity is what keeps
// them out of the win rate and in the agreement figure.
func judgeIdent(ctx context.Context, requested string) string {
	ident := strings.TrimSpace(requested)
	if ident == "" {
		ident = keyNameFromCtx(ctx)
	}
	if ident == "" {
		return "mcp"
	}
	return ident
}

// normalizeDimensions validates the optional per-dimension winners. Unknown
// dimension names are rejected rather than stored: a typo that silently
// vanishes from the results table is worse than a failed call.
func normalizeDimensions(raw string) (string, *mcp.CallToolResult) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}
	var dims map[string]string
	if err := json.Unmarshal([]byte(raw), &dims); err != nil {
		return "", toolError("invalid dimensions: must be a JSON object of dimension to winner: " + err.Error())
	}
	valid := make(map[string]bool, len(eval.Dimensions()))
	for _, d := range eval.Dimensions() {
		valid[d] = true
	}
	out := make(map[string]string, len(dims))
	for name, winner := range dims {
		if !valid[name] {
			return "", toolError(fmt.Sprintf("unknown dimension %q: want one of %s",
				name, strings.Join(eval.Dimensions(), ", ")))
		}
		w := strings.ToLower(strings.TrimSpace(winner))
		if !eval.ValidWinner(w) {
			return "", toolError(fmt.Sprintf("dimension %q: invalid winner %q, want a, b, or tie", name, winner))
		}
		out[name] = w
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", toolError("encoding dimensions: " + err.Error())
	}
	return string(b), nil
}

func (s *Server) handleEvalSummary(ctx context.Context, _ *mcp.CallToolRequest, input evalRunInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "eval:read"); err != nil {
		return err, nil, nil
	}
	store, errRes := s.evalStore()
	if errRes != nil {
		return errRes, nil, nil
	}
	if input.RunID <= 0 {
		return toolError("run_id is required"), nil, nil
	}
	summary, err := store.Summarize(ctx, input.RunID, s.evalSummaryOpts())
	if err != nil {
		return toolError("summarizing run: " + err.Error()), nil, nil
	}
	r, jsonErr := toolJSON(summary)
	return r, nil, jsonErr
}

// evalSummaryOpts mirrors the REST handler's: a nil config leaves the fields
// zero and eval.SummaryOpts fills in the shipped defaults.
func (s *Server) evalSummaryOpts() eval.SummaryOpts {
	if s.deps.Config == nil {
		return eval.SummaryOpts{}
	}
	c := s.deps.Config.Eval
	return eval.SummaryOpts{
		CompletenessFloor: c.CompletenessFloor,
		WinThreshold:      c.WinThreshold,
		GateRejectedPP:    c.GateRejectedRatePP,
		GateRoundsPct:     c.GateRoundsPct,
		GateCostPct:       c.GateCostPct,
	}
}

// evalRunStatus is what a headless judge polls while a run finishes.
type evalRunStatus struct {
	RunID        int64   `json:"run_id"`
	Status       string  `json:"status"`
	Terminal     bool    `json:"terminal"`
	BaseAgent    string  `json:"base_agent"`
	K            int     `json:"k"`
	CostSpent    float64 `json:"cost_spent"`
	CostCap      float64 `json:"cost_cap"`
	SamplesDone  int     `json:"samples_done"`
	SamplesTotal int     `json:"samples_total"`
	Pairs        int     `json:"pairs"`
	PendingItems int     `json:"pending_items"`
}

func (s *Server) handleEvalRunStatus(ctx context.Context, _ *mcp.CallToolRequest, input evalRunInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "eval:read"); err != nil {
		return err, nil, nil
	}
	store, errRes := s.evalStore()
	if errRes != nil {
		return errRes, nil, nil
	}
	if input.RunID <= 0 {
		return toolError("run_id is required"), nil, nil
	}
	status, err := buildEvalRunStatus(ctx, store, input.RunID)
	if err != nil {
		return toolError("getting run status: " + err.Error()), nil, nil
	}
	r, jsonErr := toolJSON(status)
	return r, nil, jsonErr
}

func buildEvalRunStatus(ctx context.Context, store *eval.Store, runID int64) (*evalRunStatus, error) {
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	variants, err := store.ListVariants(ctx, runID)
	if err != nil {
		return nil, err
	}
	tasks, err := store.RunTasks(ctx, run)
	if err != nil {
		return nil, err
	}
	done, err := store.CountSamples(ctx, runID)
	if err != nil {
		return nil, err
	}
	pairs, err := store.CountPairs(ctx, runID)
	if err != nil {
		return nil, err
	}
	pending, err := store.ListPending(ctx, runID, 0, 0)
	if err != nil {
		return nil, err
	}
	return &evalRunStatus{
		RunID:        run.ID,
		Status:       run.Status,
		Terminal:     eval.IsTerminal(run.Status),
		BaseAgent:    run.BaseAgent,
		K:            run.K,
		CostSpent:    run.CostSpent,
		CostCap:      run.CostCap,
		SamplesDone:  done,
		SamplesTotal: len(tasks) * len(variants) * run.K,
		Pairs:        pairs,
		PendingItems: len(pending),
	}, nil
}
