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
	// ErrJudgeNotConfigured means neither [eval] judge_model nor judge_decider
	// is set: the internal judge is opt-in, and its absence is not a failure —
	// the MCP judge path is unaffected.
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
	// llm is nil when only a decider is configured: there is then no model
	// stage to fall through to.
	llm     judgeLLM
	decider *llm.Decider
	tracker *llm.CostTracker
}

// cost reads the pass's true spend across both backends, for the same reason
// a sample does: provider-reported cost is only filled in by OpenRouter, so a
// cap keyed on it would never trip elsewhere.
func (s judgeSession) cost(convID string) float64 {
	if s.tracker == nil {
		return 0
	}
	return s.tracker.SessionCost(convID) + s.tracker.SessionCost(deciderConvID(convID))
}

// JudgeConfig is the judge's snapshot of the [eval] judge keys.
type JudgeConfig struct {
	// Model is the judging model. Empty with no Decider disables the internal
	// judge entirely; empty with one means the decider has nothing to fall
	// through to.
	Model string
	// Provider names a registered provider instance, or is empty to use the
	// base agent's own.
	Provider string
	// MaxCost caps one judging pass in USD, across both backends.
	MaxCost float64
	// Decider, when set, grades each item first; see tryDecider for what falls
	// through to the model. It arrives already bound to its timeout.
	Decider *llm.Decider
	// DeciderRecordAt is the winning option's probability a decider verdict
	// needs before it is recorded.
	DeciderRecordAt float64
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
func (j *Judge) Available() bool { return j != nil && j.Config().enabled() }

// enabled reports whether either backend is configured.
func (c JudgeConfig) enabled() bool { return c.Model != "" || c.Decider != nil }

// idents lists the judge identities a pass under this config can record,
// decider first since it is asked first.
func (c JudgeConfig) idents() []string {
	var out []string
	if c.Decider != nil {
		out = append(out, JudgeDecider)
	}
	if c.Model != "" {
		out = append(out, JudgeInternal)
	}
	return out
}

// deciderName is the decider's name, or "" without one.
func (c JudgeConfig) deciderName() string {
	if c.Decider == nil {
		return ""
	}
	return c.Decider.Name()
}

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
	RunID    int64  `json:"run_id"`
	Items    int    `json:"items"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	// Decider names the decision model asked first, when one is configured.
	Decider         string  `json:"decider,omitempty"`
	DeciderRecordAt float64 `json:"decider_record_at,omitempty"`
	// JudgeIdents are the identities this pass may record under, decider first.
	JudgeIdents   []string `json:"judge_idents"`
	RubricVersion string   `json:"rubric_version"`
	CostCap       float64  `json:"cost_cap"`
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
	if !cfg.enabled() {
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
		RunID:           runID,
		Items:           len(items),
		Model:           cfg.Model,
		Provider:        cfg.Provider,
		Decider:         cfg.deciderName(),
		DeciderRecordAt: cfg.DeciderRecordAt,
		JudgeIdents:     cfg.idents(),
		RubricVersion:   RubricVersion,
		CostCap:         cfg.MaxCost,
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
	// The decider bills a sibling key: the tracker remembers one provider per
	// session for limit resolution, and the two backends can be on different
	// providers. Both keys carry the same pseudo-identity.
	if session.tracker != nil {
		session.tracker.RegisterSessionAgent(st.convID, JudgeAgentIdent(run.BaseAgent))
		session.tracker.RegisterSessionAgent(deciderConvID(st.convID), JudgeAgentIdent(run.BaseAgent))
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
// the pass. With a decider only, the model stage is left nil; the tracker is
// still the agent's (the decider shares the process-wide one in production),
// falling back to the decider's own when the router has none.
func (j *Judge) sessionFor(baseAgent string, cfg JudgeConfig) (judgeSession, error) {
	e, ok := j.engines(baseAgent)
	if !ok || e == nil {
		return judgeSession{}, fmt.Errorf("agent %q not found", baseAgent)
	}
	router := e.LLMRouter()
	if router == nil {
		return judgeSession{}, fmt.Errorf("agent %q has no LLM router", baseAgent)
	}
	session := judgeSession{decider: cfg.Decider, tracker: router.CostTracker()}
	if cfg.Model != "" {
		if cfg.Provider != "" && !router.HasProvider(cfg.Provider) {
			return judgeSession{}, fmt.Errorf("judge provider %q is not registered", cfg.Provider)
		}
		session.llm = router.WithModel(cfg.Model).WithProvider(cfg.Provider)
	}
	if session.tracker == nil && cfg.Decider != nil {
		session.tracker = cfg.Decider.CostTracker()
	}
	return session, nil
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

// deciderConvID is the sibling session key the decider stage of a pass bills
// to. Its spend is summed with the pass's own by judgeSession.cost.
func deciderConvID(convID string) string { return convID + ":decider" }

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
	// Decider stage counters: decided is recorded by it, abstained is a
	// low-confidence answer, tooLarge an input over its token cap, escalated
	// everything it handed to the model stage for any reason.
	decided   int
	abstained int
	tooLarge  int
	escalated int
}

// setCapped marks the pass as out of budget.
func (st *passState) setCapped() {
	st.mu.Lock()
	st.capped = true
	st.mu.Unlock()
}

// isCapped reports whether a backend tripped the tracker's hard limit, which
// overCap's spend comparison alone would not see.
func (st *passState) isCapped() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.capped
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
		if st.isCapped() || overCap(st) {
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
		"decider", st.cfg.deciderName(), "items", len(items), "judged", st.judged,
		"decided", st.decided, "escalated", st.escalated, "failed", st.failed, "capped", st.capped)
}

// overCap reports whether the pass has spent its budget, recording the fact so
// the finish event can say why it stopped short.
func overCap(st *passState) bool {
	if st.session.cost(st.convID) < st.cfg.MaxCost {
		return false
	}
	st.setCapped()
	return true
}

// judgeItem grades one blinded item and records the verdict.
//
// A failure is per item: an unreadable reply or a provider hiccup costs that
// item's verdict, never the pass. The item stays pending, so a later pass —
// internal or from Claude Code — picks it up again. With both backends
// configured the decider is asked first and the model only sees what it
// could not settle.
func (j *Judge) judgeItem(ctx context.Context, run *Run, item PendingItem, st *passState) {
	bookkeeping := context.WithoutCancel(ctx)
	defer j.recordCost(bookkeeping, run, st)

	blinded, err := j.store.GetBlindedItem(ctx, item.ItemID)
	if err != nil {
		j.itemFailed(st, item, "reading blinded item", err)
		return
	}
	if st.session.decider != nil {
		if handled := j.tryDecider(ctx, item, blinded, st); handled || st.session.llm == nil {
			return
		}
		st.count(&st.escalated)
	}
	j.judgeWithModel(ctx, item, blinded, st)
}

// tryDecider runs the decider stage. It reports true when the item is settled
// (recorded, or failed outright) and false when the model stage should take it.
//
// Everything short of a recorded verdict falls through: a low-confidence
// answer, an input over the decider's token cap, a timeout, a provider error,
// or a response served by a different model. Nothing was recorded in any of
// those cases, so none is a failure of the item. The one exception is the
// tracker's hard limit, which caps the pass: the model stage shares the
// budget and would only fail the same way.
func (j *Judge) tryDecider(ctx context.Context, item PendingItem, blinded *BlindedItem, st *passState) bool {
	d := st.session.decider
	resp, err := d.Decide(ctx, deciderConvID(st.convID), blinded, judgeDeciderQuestions())
	if err != nil {
		switch cause := llm.DecisionErrorCause(err); cause {
		case "cost_limit":
			st.setCapped()
			j.itemFailed(st, item, "decider over budget", err)
			return true
		case "too_large":
			st.count(&st.tooLarge)
		default:
			j.logger.Debug("eval judge decider fell through", "item", item.ItemID, "cause", cause, "error", err)
		}
		return false
	}
	if !servedByJudgeModel(d.Model(), resp.Model) {
		j.logger.Warn("eval judge decider answered from a different model", "item", item.ItemID,
			"want", d.Model(), "got", resp.Model)
		return false
	}
	call, ok, err := deciderCall(resp.Answers, st.cfg.DeciderRecordAt)
	if err != nil {
		j.itemFailed(st, item, "reading the decider's answers", err)
		return true
	}
	if !ok {
		st.count(&st.abstained)
		return false
	}
	if !j.record(ctx, item, call, JudgeDecider, st) {
		return true
	}
	st.count(&st.decided)
	return true
}

// judgeWithModel is the completion stage: one no-tools call, parsed and
// recorded under JudgeInternal.
func (j *Judge) judgeWithModel(ctx context.Context, item PendingItem, blinded *BlindedItem, st *passState) {
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
		if errors.Is(err, llm.ErrHardLimitExceeded) {
			st.setCapped()
		}
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
	j.record(ctx, item, call, JudgeInternal, st)
}

// record writes one verdict under ident and counts it. Reports success.
func (j *Judge) record(ctx context.Context, item PendingItem, call judgeCall, ident string, st *passState) bool {
	if _, err := j.store.RecordVerdict(context.WithoutCancel(ctx), Verdict{
		ItemID:        item.ItemID,
		Winner:        call.Winner,
		Dimensions:    encodeDimensions(call.Dimensions),
		Notes:         call.Notes,
		JudgeIdent:    ident,
		RubricVersion: RubricVersion,
	}); err != nil {
		j.itemFailed(st, item, "recording verdict", err)
		return false
	}
	st.count(&st.judged)
	return true
}

// count bumps one pass counter under the lock.
func (st *passState) count(n *int) {
	st.mu.Lock()
	*n++
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
		"judge_idents":   st.cfg.idents(),
		"rubric_version": RubricVersion,
		"cost_cap":       st.cfg.MaxCost,
	}
	if d := st.cfg.Decider; d != nil {
		detail["decider"] = d.Name()
		detail["decider_model"] = d.Model()
		detail["record_at"] = st.cfg.DeciderRecordAt
	}
	if action == "eval_judge_finish" {
		detail["judged"] = st.judged
		detail["failed"] = st.failed
		detail["capped"] = st.capped
		detail["cost_spent"] = st.recorded
		if st.cfg.Decider != nil {
			detail["decided"] = st.decided
			detail["abstained"] = st.abstained
			detail["too_large"] = st.tooLarge
			detail["escalated"] = st.escalated
		}
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
