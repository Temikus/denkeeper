package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

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
	// ErrJudgeModelSwapped means the completion came back from a model other
	// than the configured one — a router cost_limit fallback, most likely.
	ErrJudgeModelSwapped = errors.New("eval: judge completion served by a different model")
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

// judgeSession is one pass's whole view of the router: the completion call and
// the cost tracker behind it, both resolved once at Start.
//
// Resolved once rather than per item because the engine can be deleted or
// rebuilt mid-pass: re-resolving would silently start reading zero, which
// disables the cost cap (spend never grows) and loses the judge_cost deltas
// (a negative delta is never written) for the rest of the pass.
type judgeSession struct {
	llm     judgeLLM
	tracker *llm.CostTracker
}

// cost reads the pass's true spend, for the same reason a sample does:
// provider-reported cost is only filled in by OpenRouter, so a cap keyed on it
// would never trip elsewhere.
func (s judgeSession) cost(convID string) float64 {
	if s.tracker == nil {
		return 0
	}
	return s.tracker.SessionCost(convID)
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
	// MaxConcurrent bounds items in flight across every pass. Fixed at
	// construction — SetConfig does not resize the semaphore — because the
	// point of the bound is the provider's rate limit, and a live resize would
	// hand an in-flight pass more slots than the operator asked for.
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
	logger  *slog.Logger

	// sem bounds in-flight completions process-wide, not per pass: judging
	// three finished runs at once must not multiply max_concurrent by three
	// against a rate-limited provider.
	sem chan struct{}
	// passSeq numbers passes so each gets its own cost-tracker session key.
	passSeq atomic.Int64

	cfgMu sync.RWMutex
	cfg   JudgeConfig

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
		sem:     make(chan struct{}, cfg.MaxConcurrent),
		active:  make(map[int64]*activeRun),
	}
}

// Available reports whether an internal judge is configured.
func (j *Judge) Available() bool { return j != nil && j.Config().Model != "" }

// Config returns the resolved settings, so a handler can report the model and
// cap a pass will run under.
func (j *Judge) Config() JudgeConfig {
	if j == nil {
		return JudgeConfig{}
	}
	j.cfgMu.RLock()
	defer j.cfgMu.RUnlock()
	return j.cfg
}

// SetConfig applies a reloaded [eval] judge block, so turning the judge on,
// pointing it at another model, or moving its cap takes effect on the next
// pass instead of at the next restart. MaxConcurrent is deliberately not
// re-read: the semaphore is process-wide and sized once.
//
// A pass in flight keeps the config it started under — it has already told the
// caller which model and cap it is running against.
func (j *Judge) SetConfig(cfg JudgeConfig) {
	if j == nil {
		return
	}
	j.cfgMu.Lock()
	defer j.cfgMu.Unlock()
	cfg.MaxConcurrent = j.cfg.MaxConcurrent
	j.cfg = cfg
}

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
	cfg := j.Config()
	if cfg.Model == "" {
		return nil, ErrJudgeNotConfigured
	}
	run, err := j.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !IsTerminal(run.Status) {
		return nil, fmt.Errorf("run %d is %s: %w", runID, run.Status, ErrRunNotTerminal)
	}
	session, err := j.sessionFor(run.BaseAgent, cfg)
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
		Model:         cfg.Model,
		Provider:      cfg.Provider,
		JudgeIdent:    JudgeInternal,
		RubricVersion: RubricVersion,
		CostCap:       cfg.MaxCost,
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

	st := &passState{
		cfg:     cfg,
		session: session,
		convID:  JudgeConvID(runID, j.passSeq.Add(1)),
	}
	// Register the session under the eval pseudo-identity before the first
	// call. Without it the tracker prefix-parses "eval:judge:..." to an agent
	// literally named "eval" — a valid resource name someone may actually
	// have — merging judge spend into that agent's totals and applying its
	// [costs] overrides to judging.
	if session.tracker != nil {
		session.tracker.RegisterSessionAgent(st.convID, JudgeAgentIdent(run.BaseAgent))
	}

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
		j.run(passCtx, run, st, items)
	}()
	return pass, nil
}

// sessionFor resolves the base agent's router and applies the judge overlay.
//
// The overlay is the same WithModel/WithProvider clone an eval variant uses, so
// the judge bills to the agent's own cost tracker and honours its pricing and
// fallback rules — only the target differs. An unknown provider is rejected
// here rather than at request time, where it would fail every item instead of
// the pass.
func (j *Judge) sessionFor(baseAgent string, cfg JudgeConfig) (judgeSession, error) {
	e, ok := j.engines(baseAgent)
	if !ok || e == nil {
		return judgeSession{}, fmt.Errorf("agent %q not found", baseAgent)
	}
	router := e.LLMRouter()
	if router == nil {
		return judgeSession{}, fmt.Errorf("agent %q has no LLM router", baseAgent)
	}
	if cfg.Provider != "" && !router.HasProvider(cfg.Provider) {
		return judgeSession{}, fmt.Errorf("judge provider %q is not registered", cfg.Provider)
	}
	return judgeSession{
		llm:     router.WithModel(cfg.Model).WithProvider(cfg.Provider),
		tracker: router.CostTracker(),
	}, nil
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

// JudgeConvID is the cost tracker's session key for one judging pass, distinct
// from every eval:{run}:{task}:{k}:{variant} sample key so judge cost can never
// be mistaken for a sample's.
//
// The pass number is part of it because the key is also what the router's own
// session guards read: a key shared across passes accumulates forever, so a
// [llm.fallbacks] cost_limit rule would eventually swap the judge model out
// from under the rubric, and a [costs] hard limit would refuse every later
// pass. One key per pass keeps both guards measuring the pass in front of them.
func JudgeConvID(runID, pass int64) string {
	return fmt.Sprintf("eval:judge:%d:%d", runID, pass)
}

// JudgeAgentIdent is the pseudo-identity judging spend and audit events are
// attributed to. "#" is rejected by the resource-name validator, so it can
// never collide with a real agent and never lands in one's totals.
func JudgeAgentIdent(baseAgent string) string {
	return baseAgent + "#" + string(agent.ExecEval) + ":judge"
}

// passState is one pass's bookkeeping, including the config and router session
// it started under — a reload mid-pass must not move the cap a caller was
// already told about.
type passState struct {
	cfg     JudgeConfig
	session judgeSession
	convID  string

	mu sync.Mutex
	// recorded is what has been written to eval_runs.judge_cost so far, so each
	// item persists only its own delta.
	recorded float64
	judged   int
	failed   int
	capped   bool
}

// run works the queue.
func (j *Judge) run(ctx context.Context, run *Run, st *passState, items []PendingItem) {
	bookkeeping := context.WithoutCancel(ctx)
	j.emitLifecycle(bookkeeping, run, "eval_judge_start", len(items), st)

	var wg sync.WaitGroup
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		// Checked before dispatch, never mid-flight: an item already asking the
		// model has been paid for, and its verdict is data. Same rule as the
		// runner's cap.
		if overCap(st) {
			break
		}
		select {
		case j.sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(item PendingItem) {
			defer wg.Done()
			defer func() { <-j.sem }()
			j.judgeItem(ctx, run, item, st)
		}(item)
	}
	wg.Wait()

	j.emitLifecycle(bookkeeping, run, "eval_judge_finish", len(items), st)
	j.logger.Info("eval judging pass finished", "run", run.ID, "model", st.cfg.Model,
		"items", len(items), "judged", st.judged, "failed", st.failed, "capped", st.capped)
}

// overCap reports whether the pass has spent its budget, recording the fact so
// the finish event can say why it stopped short.
func overCap(st *passState) bool {
	if st.session.cost(st.convID) < st.cfg.MaxCost {
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
func (j *Judge) judgeItem(ctx context.Context, run *Run, item PendingItem, st *passState) {
	bookkeeping := context.WithoutCancel(ctx)
	defer j.recordCost(bookkeeping, run, st)

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

	resp, err := st.session.llm.CompleteFinal(ctx, st.convID, wire)
	if err != nil {
		j.itemFailed(st, item, "judging item", err)
		return
	}
	// A cost_limit fallback rule reroutes silently, and a verdict stamped
	// judge_model + rubric v1 that a different model actually produced is a
	// lie the results table cannot detect later. Fail the item instead.
	if !servedByJudgeModel(st.cfg.Model, resp.Model) {
		j.itemFailed(st, item, "checking the serving model",
			fmt.Errorf("%w: wanted %q, got %q", ErrJudgeModelSwapped, st.cfg.Model, resp.Model))
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

// servedByJudgeModel reports whether got is the model the pass asked for.
//
// A provider that reports no model at all is trusted — several do not — and a
// dated or aliased variant of the same name ("claude-x" vs "claude-x-20260101")
// counts as a match. What must not pass is a genuinely different model, which
// is what a fallback swap looks like.
func servedByJudgeModel(want, got string) bool {
	if got == "" || got == want {
		return true
	}
	return strings.HasPrefix(got, want) || strings.HasPrefix(want, got)
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
func (j *Judge) recordCost(ctx context.Context, run *Run, st *passState) {
	total := st.session.cost(st.convID)
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

// emitLifecycle records the pass's anchor events under the same eval
// pseudo-identity judging spend is attributed to, so it is excluded from the
// real agent's totals by the existing marking.
func (j *Judge) emitLifecycle(ctx context.Context, run *Run, action string, items int, st *passState) {
	st.mu.Lock()
	detail := map[string]any{
		"run_id":         run.ID,
		"items":          items,
		"model":          st.cfg.Model,
		"provider":       st.cfg.Provider,
		"judge_ident":    JudgeInternal,
		"rubric_version": RubricVersion,
		"cost_cap":       st.cfg.MaxCost,
	}
	if action == "eval_judge_finish" {
		detail["judged"] = st.judged
		detail["failed"] = st.failed
		detail["capped"] = st.capped
		detail["cost_spent"] = st.recorded
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
		Agent:          JudgeAgentIdent(run.BaseAgent),
		Summary:        fmt.Sprintf("Internal judging of eval run %d %s", run.ID, verb),
		Detail:         string(body),
		Status:         audit.StatusOK,
		Source:         string(agent.ExecEval),
		ConversationID: st.convID,
	})
}
