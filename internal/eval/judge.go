package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// Errors the judge endpoints map onto status codes.
var (
	// ErrJudgeNotConfigured means [eval] judge_model is unset: the internal
	// judge is opt-in, and its absence is not a failure — the MCP judge path
	// is unaffected.
	ErrJudgeNotConfigured = errors.New("eval: internal judge not configured")
	// ErrRunNotTerminal means the run is still producing samples. Judging a
	// moving queue wastes money on pairs that do not exist yet.
	ErrRunNotTerminal = errors.New("eval: run is not terminal")
	// ErrJudgeActive means a judging pass is already working this run's queue.
	ErrJudgeActive = errors.New("eval: run is already being judged")
)

// judgeLLM is the entire capability the internal judge is given: one
// completion whose request carries no tool definitions.
//
// The narrowness is the guarantee, not a convenience. The judge surface must
// never be able to unblind its own queue — the same rule that keeps
// GET /eval/runs/{id}/pairs off the MCP judge's tool set — and a one-method
// interface with no tool channel makes that structural rather than a
// convention: there is nothing for the model to call, so there is nothing for
// it to call the unblinded views with. llm.Router.CompleteFinal satisfies it
// and omits tools by request shape, not by prompt instruction.
type judgeLLM interface {
	CompleteFinal(ctx context.Context, sessionID string, messages []llm.Message) (*llm.ChatResponse, error)
}

// JudgeConfig is the judge's snapshot of the [eval] judge keys.
type JudgeConfig struct {
	// Model is the judging model. Empty disables the internal judge entirely.
	Model string
	// Provider names a registered provider instance, or is empty to use the
	// base agent's own.
	Provider string
	// MaxCost caps one judging pass in USD.
	MaxCost float64
	// MaxConcurrent bounds items in flight, shared with nothing: the runner's
	// semaphore guards sample dispatch, and a judging pass runs after a run has
	// finished.
	MaxConcurrent int
}

// Judge grades a finished run's blinded pairs through denkeeper's own router,
// so a run can be judged unattended instead of only from Claude Code over MCP.
//
// It is capability-reduced on purpose: one completion per item, no tools, no
// engine turn, and no reader beyond Store.GetBlindedItem. Same tables and same
// blinding as the MCP path — the queue is ListPending, the payload is
// GetBlindedItem, the write is RecordVerdict — so the two judges are
// interchangeable and the win rate has exactly one derivation.
type Judge struct {
	store   *Store
	engines EngineSource
	auditor audit.Emitter
	cfg     JudgeConfig
	logger  *slog.Logger

	mu     sync.Mutex
	active map[int64]*activeRun
	wg     sync.WaitGroup
}

// NewJudge builds a judge. A zero-value Model leaves it unavailable; callers
// ask Available before offering it.
func NewJudge(store *Store, engines EngineSource, auditor audit.Emitter, cfg JudgeConfig, logger *slog.Logger) *Judge {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	if auditor == nil {
		auditor = audit.NopEmitter{}
	}
	return &Judge{
		store:   store,
		engines: engines,
		auditor: auditor,
		cfg:     cfg,
		logger:  logger,
		active:  make(map[int64]*activeRun),
	}
}

// Available reports whether an internal judge is configured.
func (j *Judge) Available() bool { return j != nil && j.cfg.Model != "" }

// Config returns the resolved settings, so a handler can report the model and
// cap a pass will run under.
func (j *Judge) Config() JudgeConfig { return j.cfg }

// JudgeOpts scopes one pass over a run's queue.
type JudgeOpts struct {
	// SampleN draws that many pending items at random instead of taking the
	// head of the queue — the calibration subset, same knob eval_pending has.
	SampleN int
	// Limit caps how many items the pass takes. 0 is the whole queue.
	Limit int
}

// JudgePass describes a launched pass, so the caller can report what it will
// cost and under which policy it is being judged.
type JudgePass struct {
	RunID         int64   `json:"run_id"`
	Items         int     `json:"items"`
	Model         string  `json:"model"`
	Provider      string  `json:"provider,omitempty"`
	JudgeIdent    string  `json:"judge_ident"`
	RubricVersion string  `json:"rubric_version"`
	CostCap       float64 `json:"cost_cap"`
}

// Start launches a judging pass in the background and returns as soon as the
// queue is known.
//
// Background rather than synchronous because a full run's queue is hundreds of
// items — 50 tasks x k=3 x two presentation orders — and no HTTP client waits
// that long. Progress is observable through the same figures the MCP judge's
// work shows up in: completeness.pairs_judged on the summary, and the pair
// view's per-item verdicts.
func (j *Judge) Start(ctx context.Context, runID int64, opts JudgeOpts) (*JudgePass, error) {
	if !j.Available() {
		return nil, ErrJudgeNotConfigured
	}
	run, err := j.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !IsTerminal(run.Status) {
		return nil, fmt.Errorf("run %d is %s: %w", runID, run.Status, ErrRunNotTerminal)
	}
	client, err := j.clientFor(run.BaseAgent)
	if err != nil {
		return nil, err
	}
	items, err := j.store.ListPending(ctx, runID, opts.Limit, opts.SampleN)
	if err != nil {
		return nil, err
	}

	pass := &JudgePass{
		RunID:         runID,
		Items:         len(items),
		Model:         j.cfg.Model,
		Provider:      j.cfg.Provider,
		JudgeIdent:    JudgeInternal,
		RubricVersion: RubricVersion,
		CostCap:       j.cfg.MaxCost,
	}
	if len(items) == 0 {
		// Nothing to do is not an error and must not register an active pass:
		// the caller sees items = 0 and says so.
		return pass, nil
	}

	// Detached from the request context for the same reason a run is: a pass
	// outlives the HTTP call that asked for it. Cancellation comes from Stop,
	// StopAll and the panic switch.
	passCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	handle := &activeRun{cancel: cancel, done: make(chan struct{})}

	j.mu.Lock()
	if _, dup := j.active[runID]; dup {
		j.mu.Unlock()
		cancel()
		return nil, ErrJudgeActive
	}
	j.active[runID] = handle
	j.mu.Unlock()

	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		defer close(handle.done)
		defer cancel()
		defer func() {
			j.mu.Lock()
			delete(j.active, runID)
			j.mu.Unlock()
		}()
		j.run(passCtx, run, client, items)
	}()
	return pass, nil
}

// clientFor resolves the base agent's router and applies the judge overlay.
//
// The overlay is the same WithModel/WithProvider clone an eval variant uses, so
// the judge bills to the agent's own cost tracker and honours its pricing and
// fallback rules — only the target differs. An unknown provider is rejected
// here rather than at request time, where it would fail every item instead of
// the pass.
func (j *Judge) clientFor(baseAgent string) (judgeLLM, error) {
	e, ok := j.engines(baseAgent)
	if !ok || e == nil {
		return nil, fmt.Errorf("agent %q not found", baseAgent)
	}
	router := e.LLMRouter()
	if router == nil {
		return nil, fmt.Errorf("agent %q has no LLM router", baseAgent)
	}
	if j.cfg.Provider != "" && !router.HasProvider(j.cfg.Provider) {
		return nil, fmt.Errorf("judge provider %q is not registered", j.cfg.Provider)
	}
	return router.WithModel(j.cfg.Model).WithProvider(j.cfg.Provider), nil
}

// Stop cancels an active pass. Reports whether one was running.
func (j *Judge) Stop(runID int64) bool {
	j.mu.Lock()
	handle, ok := j.active[runID]
	j.mu.Unlock()
	if !ok {
		return false
	}
	handle.cancel()
	return true
}

// StopAll cancels every active pass. Wired into the panic switch beside
// Runner.StopAll: judging spends real money, so an emergency stop has to reach
// it too.
func (j *Judge) StopAll() {
	j.mu.Lock()
	handles := make([]*activeRun, 0, len(j.active))
	for _, h := range j.active {
		handles = append(handles, h)
	}
	j.mu.Unlock()
	for _, h := range handles {
		h.cancel()
	}
}

// Shutdown stops every pass and waits for the goroutines to finish.
func (j *Judge) Shutdown() {
	j.StopAll()
	j.wg.Wait()
}

// IsActive reports whether a pass is currently judging this run.
func (j *Judge) IsActive(runID int64) bool {
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_, ok := j.active[runID]
	return ok
}

// JudgeConvID is the cost tracker's session key for a run's judging spend,
// distinct from every eval:{run}:{task}:{k}:{variant} sample key so judge cost
// can never be mistaken for a sample's.
func JudgeConvID(runID int64) string { return fmt.Sprintf("eval:judge:%d", runID) }

// passState is one pass's bookkeeping.
type passState struct {
	mu sync.Mutex
	// baseline is the tracker's total for this key when the pass started. The
	// key is stable across passes, so spend is measured from here rather than
	// from zero — otherwise a second pass would open already over its cap.
	baseline float64
	// recorded is what has been written to eval_runs.judge_cost so far, so each
	// item persists only its own delta.
	recorded float64
	judged   int
	failed   int
	capped   bool
}

// run works the queue.
func (j *Judge) run(ctx context.Context, run *Run, client judgeLLM, items []PendingItem) {
	bookkeeping := context.WithoutCancel(ctx)
	convID := JudgeConvID(run.ID)
	st := &passState{baseline: j.sessionCost(run.BaseAgent, convID)}
	st.recorded = st.baseline

	j.emitLifecycle(bookkeeping, run, "eval_judge_start", len(items), st)

	sem := make(chan struct{}, j.cfg.MaxConcurrent)
	var wg sync.WaitGroup
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		// Checked before dispatch, never mid-flight: an item already asking the
		// model has been paid for, and its verdict is data. Same rule as the
		// runner's cap.
		if j.overCap(run.BaseAgent, convID, st) {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(item PendingItem) {
			defer wg.Done()
			defer func() { <-sem }()
			j.judgeItem(ctx, run, client, convID, item, st)
		}(item)
	}
	wg.Wait()

	j.emitLifecycle(bookkeeping, run, "eval_judge_finish", len(items), st)
	j.logger.Info("eval judging pass finished", "run", run.ID, "model", j.cfg.Model,
		"items", len(items), "judged", st.judged, "failed", st.failed, "capped", st.capped)
}

// overCap reports whether the pass has spent its budget, recording the fact so
// the finish event can say why it stopped short.
func (j *Judge) overCap(baseAgent, convID string, st *passState) bool {
	spent := j.sessionCost(baseAgent, convID) - st.baseline
	if spent < j.cfg.MaxCost {
		return false
	}
	st.mu.Lock()
	st.capped = true
	st.mu.Unlock()
	return true
}

// judgeItem grades one blinded item and records the verdict.
//
// A failure is per item: an unreadable reply or a provider hiccup costs that
// item's verdict, never the pass. The item stays pending, so a later pass —
// internal or from Claude Code — picks it up again.
func (j *Judge) judgeItem(ctx context.Context, run *Run, client judgeLLM, convID string, item PendingItem, st *passState) {
	bookkeeping := context.WithoutCancel(ctx)
	defer j.recordCost(bookkeeping, run, convID, st)

	blinded, err := j.store.GetBlindedItem(ctx, item.ItemID)
	if err != nil {
		j.itemFailed(st, item, "reading blinded item", err)
		return
	}
	msgs, err := buildJudgeMessages(blinded)
	if err != nil {
		j.itemFailed(st, item, "building judge prompt", err)
		return
	}
	wire := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		wire = append(wire, llm.Message{Role: m.Role, Content: m.Content})
	}

	resp, err := client.CompleteFinal(ctx, convID, wire)
	if err != nil {
		j.itemFailed(st, item, "judging item", err)
		return
	}
	call, err := parseJudgeCall(resp.Content)
	if err != nil {
		j.itemFailed(st, item, "reading the judge's reply", err)
		return
	}

	if _, err := j.store.RecordVerdict(bookkeeping, Verdict{
		ItemID:        item.ItemID,
		Winner:        call.Winner,
		Dimensions:    encodeDimensions(call.Dimensions),
		Notes:         call.Notes,
		JudgeIdent:    JudgeInternal,
		RubricVersion: RubricVersion,
	}); err != nil {
		j.itemFailed(st, item, "recording verdict", err)
		return
	}
	st.mu.Lock()
	st.judged++
	st.mu.Unlock()
}

func (j *Judge) itemFailed(st *passState, item PendingItem, what string, err error) {
	st.mu.Lock()
	st.failed++
	st.mu.Unlock()
	j.logger.Warn("eval internal judge item failed", "item", item.ItemID,
		"run", item.RunID, "stage", what, "error", err)
}

// recordCost persists what the pass has spent since it last wrote.
//
// The delta is taken under the pass lock so concurrent items cannot both claim
// the same spend; individual attribution does not matter, the run-level total
// does. Written per item rather than once at the end so a process that dies
// mid-pass still leaves an honest figure behind — the same reason
// AddRunCost fires after every sample.
func (j *Judge) recordCost(ctx context.Context, run *Run, convID string, st *passState) {
	total := j.sessionCost(run.BaseAgent, convID)
	st.mu.Lock()
	delta := total - st.recorded
	st.recorded = total
	st.mu.Unlock()
	if delta <= 0 {
		return
	}
	if err := j.store.AddJudgeCost(ctx, run.ID, delta); err != nil {
		j.logger.Warn("recording eval judge cost failed", "run", run.ID, "error", err)
	}
}

// sessionCost reads the pass's true spend from the agent router's cost
// tracker, for the same reason a sample does: provider-reported cost is only
// filled in by OpenRouter, so a cap keyed on it would never trip elsewhere.
func (j *Judge) sessionCost(baseAgent, convID string) float64 {
	e, ok := j.engines(baseAgent)
	if !ok || e == nil {
		return 0
	}
	return sessionCost(e, convID)
}

// emitLifecycle records the pass's anchor events under the same eval
// pseudo-identity a run uses, so judge spend is attributable and excluded from
// the real agent's totals by the existing marking.
func (j *Judge) emitLifecycle(ctx context.Context, run *Run, action string, items int, st *passState) {
	st.mu.Lock()
	detail := map[string]any{
		"run_id":         run.ID,
		"items":          items,
		"model":          j.cfg.Model,
		"provider":       j.cfg.Provider,
		"judge_ident":    JudgeInternal,
		"rubric_version": RubricVersion,
		"cost_cap":       j.cfg.MaxCost,
	}
	if action == "eval_judge_finish" {
		detail["judged"] = st.judged
		detail["failed"] = st.failed
		detail["capped"] = st.capped
		detail["cost_spent"] = st.recorded - st.baseline
	}
	st.mu.Unlock()
	body, _ := json.Marshal(detail)

	verb := "started"
	if action == "eval_judge_finish" {
		verb = "finished"
	}
	j.auditor.Emit(ctx, audit.Event{
		Category:       audit.CategoryEval,
		Action:         action,
		Agent:          run.BaseAgent + "#" + string(agent.ExecEval),
		Summary:        fmt.Sprintf("Internal judging of eval run %d %s", run.ID, verb),
		Detail:         string(body),
		Status:         audit.StatusOK,
		Source:         string(agent.ExecEval),
		ConversationID: JudgeConvID(run.ID),
	})
}
