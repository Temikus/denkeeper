package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The six judge tools. They read Deps.EvalStore, which is deliberately distinct
// from the write-path runner the REST handlers use: a judge can read pairs and
// record verdicts, and cannot touch run state. eval_verdict is the only writer
// here, and it writes verdicts only.
//
// Blinding is server-side. eval_get_pair builds its payload from scratch in the
// eval package. Anything that maps a response to its variant (eval_samples,
// and all of eval_summary but completeness and the calibration figures)
// passes identityRefusal, which opens only once
// the run has no pending item: exactly when RecordVerdict stops taking judge
// verdicts. REST /samples and /pairs stay ungated on purpose (operator view).

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
	JudgeIdent string `json:"judge_ident,omitempty" jsonschema:"Who is judging; defaults to the API key name. Pass \"operator\" to record a human calibration mark. It is accepted only while the item has no judge verdict, so mark before judging"`
	// RubricVersion is stored verbatim: the rubric is a skill file the operator
	// edits, so the server has no list of valid versions to check against.
	RubricVersion string `json:"rubric_version,omitempty" jsonschema:"The judging rubric revision this call was made under, e.g. \"v1\" — copy the 'Rubric version' line of the judging skill so the results view can name what the win-rate was judged against"`
}

type evalRunInput struct {
	RunID int64 `json:"run_id" jsonschema:"Eval run id"`
}

type evalSamplesInput struct {
	RunID  int64 `json:"run_id" jsonschema:"Eval run id"`
	TaskID int64 `json:"task_id,omitempty" jsonschema:"Only return samples for this task"`
	Limit  int   `json:"limit,omitempty" jsonschema:"Max samples to return (default 20, max 100)"`
	Offset int   `json:"offset,omitempty" jsonschema:"Skip this many samples, for paging"`
}

// evalSamplesResult serialises eval.Sample as is, so a field added to it
// reaches this tool and REST /samples alike.
type evalSamplesResult struct {
	RunID    int64          `json:"run_id"`
	Variants []eval.Variant `json:"variants"`
	Samples  []eval.Sample  `json:"samples"`
	Count    int            `json:"count"`
	Total    int            `json:"total"`
}

const (
	evalPendingMaxLimit        = 500
	evalSamplesDefaultLimit    = 20
	evalSamplesMaxLimit        = 100
	evalSamplesToolDescription = "List a fully judged run's samples with the variant that produced each: " +
		"response, tool trace, rounds, stop reason, cost, latency and token usage, the same rows as " +
		"GET /eval/runs/{id}/samples, plus the run's variants to resolve 'variant_id'. Refused until " +
		"the run is finished and every judgment item has a judge verdict; from then on the run takes " +
		"no more verdicts. Pass 'task_id' for one test case. Without it results are paged ('limit' " +
		"default 20, max 100, and 'offset'). Requires 'eval:read' scope."
)

func (s *Server) registerEvalTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_pending",
		Description: "List blinded judgment items still awaiting a verdict, optionally for one " +
			"run. Each pair of responses is queued twice with the presentation order " +
			"swapped, so both orders must be judged before the pair counts. Use " +
			"'sample_n' to draw a random calibration subset rather than the head of the " +
			"queue. The operator must mark those items (eval_verdict, judge_ident \"operator\") " +
			"before a judge records a verdict on them, because a mark on a judged item is refused. " +
			"Requires 'eval:read' scope.",
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
			"overwrites your own earlier verdict until the run has no pending items; after " +
			"that the run takes no more verdicts. Requires 'eval:write' scope.",
	}, s.handleEvalVerdict)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_summary",
		Description: "Aggregate one run (unblinded once judging is done): per-variant objective metrics, the gate " +
			"table with deltas and pass/fail, the judge win-rate over pairs both " +
			"presentation orders agreed on, the operator-judge agreement figure, a " +
			"per-category breakdown, and the verdict with its plain-language reason. " +
			"Until the run is finished and every judgment item has a judge verdict, it returns " +
			"only completeness and, per candidate, the pair counts and the operator-judge " +
			"agreement figure, with 'withheld_note' saying why the rest is held back. " +
			"Requires 'eval:read' scope.",
	}, s.handleEvalSummary)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "eval_run_status",
		Description: "Get an eval run's status, progress and cost so far, plus how many judgment " +
			"pairs it produced and how many are still pending. Use it to wait for a run to " +
			"finish before judging. Requires 'eval:read' scope.",
	}, s.handleEvalRunStatus)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "eval_samples",
		Description: evalSamplesToolDescription,
	}, s.handleEvalSamples)
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
	refusal, err := identityRefusal(ctx, store, input.RunID,
		"the per-variant breakdown and verdict can tie a response on eval_get_pair to its variant")
	if err != nil {
		return toolError("summarizing run: " + err.Error()), nil, nil
	}
	var out any = summary
	if refusal != "" {
		out = closedSummary(summary, refusal)
	}
	r, jsonErr := toolJSON(out)
	return r, nil, jsonErr
}

// evalSummaryClosed is eval_summary while identityRefusal is closed. It is
// built field by field, not filtered from eval.Summary, so a field added to
// the summary later stays hidden until the gate opens.
type evalSummaryClosed struct {
	RunID        int64             `json:"run_id"`
	Status       string            `json:"status"`
	BaseAgent    string            `json:"base_agent"`
	TaskSet      string            `json:"task_set"`
	K            int               `json:"k"`
	Completeness eval.Completeness `json:"completeness"`
	Judgments    []closedJudgment  `json:"judgments"`
	WithheldNote string            `json:"withheld_note"`
}

// closedJudgment is the part of a candidate's judgment calibration reads.
// Wins, losses, ties and win rate are left out: the tally moves when a pair
// is fully judged, which tells the judge which side it preferred while it can
// still re-judge that pair.
type closedJudgment struct {
	VariantID         int64           `json:"variant_id"`
	Variant           string          `json:"variant"`
	Baseline          string          `json:"baseline"`
	Pairs             int             `json:"pairs"`
	JudgedPairs       int             `json:"judged_pairs"`
	OperatorAgreement *eval.Agreement `json:"operator_agreement,omitempty"`
	RubricVersions    []string        `json:"rubric_versions,omitempty"`
	JudgeIdents       []string        `json:"judge_idents,omitempty"`
}

func closedSummary(s *eval.Summary, refusal string) evalSummaryClosed {
	out := evalSummaryClosed{
		RunID: s.RunID, Status: s.Status, BaseAgent: s.BaseAgent, TaskSet: s.TaskSet, K: s.K,
		Completeness: s.Completeness,
		Judgments:    make([]closedJudgment, 0, len(s.Verdicts)),
		WithheldNote: refusal,
	}
	for _, v := range s.Verdicts {
		j := v.Judgment
		out.Judgments = append(out.Judgments, closedJudgment{
			VariantID: v.VariantID, Variant: v.Variant, Baseline: v.Baseline,
			Pairs: j.Pairs, JudgedPairs: j.JudgedPairs, OperatorAgreement: j.OperatorAgreement,
			RubricVersions: j.RubricVersions, JudgeIdents: j.JudgeIdents,
		})
	}
	return out
}

// evalSummaryOpts mirrors the REST handler's: a nil config leaves the fields
// zero and eval.SummaryOpts fills in the shipped defaults.
func (s *Server) evalSummaryOpts() eval.SummaryOpts {
	cfg := s.deps.Config.Get()
	if cfg == nil {
		return eval.SummaryOpts{}
	}
	c := cfg.Eval
	return eval.SummaryOpts{
		CompletenessFloor: c.CompletenessFloor,
		WinThreshold:      c.WinThreshold,
		GateRejectedPP:    c.GateRejectedRatePP,
		GateRoundsPct:     c.GateRoundsPct,
		GateCostPct:       c.GateCostPct,
	}
}

// handleEvalSamples is the MCP mirror of REST /samples, gated where REST is
// not: it names the variant behind each response, so it opens only when the run
// can no longer take a judge verdict. The gate applies to every key, admin
// included; the operator reads samples over REST.
func (s *Server) handleEvalSamples(ctx context.Context, _ *mcp.CallToolRequest, input evalSamplesInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "eval:read"); err != nil {
		return err, nil, nil
	}
	store, errRes := s.evalStore()
	if errRes != nil {
		return errRes, nil, nil
	}
	if errRes := checkSamplesGate(ctx, store, input); errRes != nil {
		return errRes, nil, nil
	}
	variants, err := store.ListVariants(ctx, input.RunID)
	if err != nil {
		return toolError("listing samples: " + err.Error()), nil, nil
	}
	page, total, err := store.PageTaskSamples(ctx, input.RunID, input.TaskID, input.Offset, clampSamplesLimit(input.Limit))
	if err != nil {
		return toolError("listing samples: " + err.Error()), nil, nil
	}
	r, jsonErr := toolJSON(evalSamplesResult{
		RunID: input.RunID, Variants: variants, Samples: page, Count: len(page), Total: total,
	})
	return r, nil, jsonErr
}

// checkSamplesGate refuses until the run is terminal and has no pending item.
// The terminal check matters: a running run has no items yet.
func checkSamplesGate(ctx context.Context, store *eval.Store, input evalSamplesInput) *mcp.CallToolResult {
	if input.RunID <= 0 {
		return toolError("run_id is required")
	}
	if input.TaskID < 0 {
		return toolError("task_id must be a positive integer")
	}
	refusal, err := identityRefusal(ctx, store, input.RunID, "eval_samples names the variant behind each response")
	if err != nil {
		return toolError("listing samples: " + err.Error())
	}
	if refusal != "" {
		return toolError(refusal)
	}
	return nil
}

// identityRefusal is the one gate on anything that maps a response to its
// variant: it returns why that stays closed, or "" once the run is finished
// with no pending item, which is exactly when RecordVerdict stops taking
// judge verdicts. The finished check matters because a running run has no
// items yet. what says what the caller would reveal.
func identityRefusal(ctx context.Context, store *eval.Store, runID int64, what string) (string, error) {
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return "", err
	}
	if !eval.IsTerminal(run.Status) {
		return fmt.Sprintf("run %d is %s: %s, so it opens once the run has finished "+
			"and every judgment item has a judge verdict", run.ID, run.Status, what), nil
	}
	pending, err := store.PendingItems(ctx, run.ID)
	if err != nil {
		return "", err
	}
	if pending > 0 {
		items := "items"
		if pending == 1 {
			items = "item"
		}
		return fmt.Sprintf("run %d has %d judgment %s pending: %s, so it stays closed "+
			"until every item has a judge verdict", run.ID, pending, items, what), nil
	}
	return "", nil
}

func clampSamplesLimit(limit int) int {
	if limit <= 0 {
		return evalSamplesDefaultLimit
	}
	return min(limit, evalSamplesMaxLimit)
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
