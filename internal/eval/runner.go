package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// maxTraceFieldLen caps tool arguments and results stored in a sample's trace.
// Results are unbounded in principle (a web_fetch page, a large KV value) and a
// run persists one row per sample, so a chatty tool must not be able to balloon
// the table. Matches the dry-run transcript cap.
const maxTraceFieldLen = 8 * 1024

// Engine is the slice of *agent.Engine the runner needs. It exists so the
// package is testable with a hand-written mock: agent.Engine is a concrete
// struct with a large constructor.
type Engine interface {
	// DryRun executes one turn under an execution policy and persists nothing.
	DryRun(ctx context.Context, msg adapter.IncomingMessage, policy agent.ExecPolicy) (*agent.TurnResult, error)
	// LLMRouter exposes the cost tracker (real per-sample spend) and provider
	// registry (overlay validation).
	LLMRouter() *llm.Router
	// Name is the base agent's name, used for the audit pseudo-identity.
	Name() string
}

// EngineSource resolves a base agent name to its live engine. main.go adapts
// Dispatcher.Agent; the nil check there must happen *before* the value is
// boxed into this interface, or a typed-nil pointer reads as non-nil here.
type EngineSource func(name string) (Engine, bool)

// Config is the runner's snapshot of [eval], resolved once at construction.
type Config struct {
	MaxConcurrent     int
	MaxCostPerRun     float64
	DefaultK          int
	CompletenessFloor float64
	AuditMode         string
}

// ProgressEvent is emitted after every sample and at both ends of a run. It is
// deliberately droppable: main.go forwards it to the WebSocket hub, and
// GET /eval/runs/{id} is the authoritative fallback.
type ProgressEvent struct {
	RunID        int64   `json:"run_id"`
	Status       string  `json:"status"`
	SamplesDone  int     `json:"samples_done"`
	SamplesTotal int     `json:"samples_total"`
	CostSpent    float64 `json:"cost_spent"`
	CostCap      float64 `json:"cost_cap"`
	ETASeconds   int     `json:"eta_seconds,omitempty"`
}

// activeRun is the cancellation handle for one in-flight run.
type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Runner executes eval runs in the background against live engines.
//
// Execution vehicle: samples run on the agent's *live* engine via
// Engine.DryRun with an ExecEval policy, not on a per-run rebuilt engine.
// The unit of evaluation is model-in-harness, so the sample must see the
// agent's real skills, tools, persona and auditor; a capability-reduced or
// duplicated engine would measure a different system. Isolation comes from
// ExecPolicy (structural, not filtered) and the variant's router is a per-turn
// clone, so nothing about the live engine is mutated.
type Runner struct {
	store   *Store
	engines EngineSource
	auditor audit.Emitter
	cfg     Config
	logger  *slog.Logger

	// sem bounds concurrent samples process-wide across every run, so evals
	// cannot starve live traffic.
	sem chan struct{}

	mu     sync.Mutex
	active map[int64]*activeRun
	wg     sync.WaitGroup

	// OnProgress is called after each sample and at run start/finish. Nil-safe.
	OnProgress func(ProgressEvent)
}

// NewRunner builds a runner. It starts no goroutine: a user who never launches
// a run pays nothing beyond the five empty tables.
func NewRunner(store *Store, engines EngineSource, auditor audit.Emitter, cfg Config, logger *slog.Logger) *Runner {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	if cfg.AuditMode == "" {
		cfg.AuditMode = agent.AuditFull
	}
	if auditor == nil {
		auditor = audit.NopEmitter{}
	}
	return &Runner{
		store:   store,
		engines: engines,
		auditor: auditor,
		cfg:     cfg,
		logger:  logger,
		sem:     make(chan struct{}, cfg.MaxConcurrent),
		active:  make(map[int64]*activeRun),
	}
}

// Config returns the runner's resolved settings, so handlers can report the
// defaults a run was created against.
func (r *Runner) Config() Config { return r.cfg }

// StartRun launches a pending run in the background. It returns as soon as the
// run is registered; progress is observable through the store and OnProgress.
func (r *Runner) StartRun(ctx context.Context, runID int64) error {
	run, err := r.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != StatusPending {
		return fmt.Errorf("run %d is %s, not pending", runID, run.Status)
	}

	// Detached from the request context: a run outlives the HTTP call that
	// created it. Cancellation comes from Stop/StopAll/Shutdown instead.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	handle := &activeRun{cancel: cancel, done: make(chan struct{})}

	r.mu.Lock()
	if _, dup := r.active[runID]; dup {
		r.mu.Unlock()
		cancel()
		return fmt.Errorf("run %d is already active", runID)
	}
	r.active[runID] = handle
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(handle.done)
		defer cancel()
		defer func() {
			r.mu.Lock()
			delete(r.active, runID)
			r.mu.Unlock()
		}()
		r.execute(runCtx, run)
	}()
	return nil
}

// Stop cancels an active run. Reports whether the run was active — a terminal
// run has nothing to cancel, and the handler turns that into a 409.
func (r *Runner) Stop(runID int64) bool {
	r.mu.Lock()
	handle, ok := r.active[runID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	handle.cancel()
	return true
}

// StopAll cancels every active run. Wired into the panic switch: an emergency
// stop halts eval spend along with everything else. Deliberately not paired
// with a resume — a panic is not a pause, and a stopped run stays stopped.
func (r *Runner) StopAll() {
	r.mu.Lock()
	handles := make([]*activeRun, 0, len(r.active))
	for _, h := range r.active {
		handles = append(handles, h)
	}
	r.mu.Unlock()
	for _, h := range handles {
		h.cancel()
	}
}

// Shutdown stops every run and waits for the goroutines to finish.
func (r *Runner) Shutdown() {
	r.StopAll()
	r.wg.Wait()
}

// IsActive reports whether a run is currently executing.
func (r *Runner) IsActive(runID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.active[runID]
	return ok
}

// runState is the mutable bookkeeping for one execution.
type runState struct {
	run        *Run
	tasks      []Task
	variants   []Variant
	engine     Engine
	total      int
	done       int
	spent      float64
	latencySum int64
	latencyN   int
	pairs      int
}

// execute drives one run to a terminal status.
//
// Bookkeeping — status writes, sample rows, cost, lifecycle audit — goes
// through a context detached from the run's, because the ordinary way a run
// ends is that its context was cancelled. Sharing one context would mean a
// stopped run could never record that it stopped: the terminal UPDATE would be
// refused by the same cancellation that ended it, leaving the row wedged at
// "running" forever.
func (r *Runner) execute(ctx context.Context, run *Run) {
	bookkeeping := context.WithoutCancel(ctx)

	st, err := r.prepare(ctx, run)
	if err != nil {
		r.logger.Error("eval run failed to start", "run", run.ID, "error", err)
		if ferr := r.store.FinishRun(bookkeeping, run.ID, StatusFailed, err.Error()); ferr != nil {
			r.logger.Error("eval run finish failed", "run", run.ID, "error", ferr)
		}
		r.emitLifecycle(bookkeeping, run, "eval_run_finish", StatusFailed, err, nil)
		r.progress(run.ID, StatusFailed, 0, 0, 0, run.CostCap, 0)
		return
	}

	if err := r.store.SetRunStatus(bookkeeping, run.ID, StatusRunning, ""); err != nil {
		r.logger.Error("eval run status update failed", "run", run.ID, "error", err)
	}
	run.Status = StatusRunning
	r.emitLifecycle(bookkeeping, run, "eval_run_start", StatusRunning, nil, st)
	r.progress(run.ID, StatusRunning, 0, st.total, 0, run.CostCap, 0)

	status, runErr := r.dispatch(ctx, st)
	st.pairs = r.finalizePairs(bookkeeping, run.ID, status)

	// The terminal row write comes first, then the finish event and frame.
	// That order is deliberate and must not be flipped for convenience: the
	// progress frame is droppable and GET /eval/runs/{id} is authoritative, so
	// a client that refetches on seeing "done" in a frame has to find the row
	// already terminal. The cost is that anything observing the row is not yet
	// guaranteed to have seen the event or the frame.
	if err := r.store.FinishRun(bookkeeping, run.ID, status, errText(runErr)); err != nil {
		r.logger.Error("eval run finish failed", "run", run.ID, "error", err)
	}
	run.Status = status
	run.CostSpent = st.spent
	r.emitLifecycle(bookkeeping, run, "eval_run_finish", status, runErr, st)
	r.progress(run.ID, status, st.done, st.total, st.spent, run.CostCap, 0)
	r.logger.Info("eval run finished", "run", run.ID, "status", status,
		"samples", st.done, "expected", st.total, "cost", st.spent, "pairs", st.pairs)
}

// finalizePairs turns the run's completed samples into blinded judgment work.
//
// It runs for capped and stopped runs as well as done ones: partial results are
// the point of those statuses, and a run that already spent the money should be
// judgeable on what it produced. Only a run that never got off the ground
// (failed before its first sample) is skipped, and even then CreatePairs would
// find nothing to pair — the skip is about not logging a confusing error.
//
// A pairing failure never changes the run's status: the samples are recorded
// and the objective scorecard stands on its own. Judging is a separate,
// re-runnable step.
func (r *Runner) finalizePairs(ctx context.Context, runID int64, status string) int {
	if status == StatusFailed {
		return 0
	}
	n, err := r.store.CreatePairs(ctx, runID)
	if err != nil {
		r.logger.Error("creating eval judgment pairs failed", "run", runID, "error", err)
		return 0
	}
	return n
}

// prepare resolves everything a run needs before the first sample: its tasks,
// variants and the live engine it will execute on.
func (r *Runner) prepare(ctx context.Context, run *Run) (*runState, error) {
	tasks, err := r.store.ListTasks(ctx, run.TaskSetID)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("task set %d has no tasks", run.TaskSetID)
	}
	variants, err := r.store.ListVariants(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if len(variants) < 2 {
		return nil, fmt.Errorf("run %d has %d variant(s), need at least 2", run.ID, len(variants))
	}
	e, ok := r.engines(run.BaseAgent)
	if !ok || e == nil {
		return nil, fmt.Errorf("agent %q not found", run.BaseAgent)
	}
	return &runState{
		run:      run,
		tasks:    tasks,
		variants: variants,
		engine:   e,
		total:    len(tasks) * len(variants) * run.K,
	}, nil
}

// dispatch runs the sample loop and returns the terminal status.
//
// Order is task → k → variant, so a task's variants execute adjacently: both
// sides then see maximally similar tool and world state, which is what makes
// their comparison mean anything.
func (r *Runner) dispatch(ctx context.Context, st *runState) (string, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, task := range st.tasks {
		for k := 0; k < st.run.K; k++ {
			for _, variant := range st.variants {
				if ctx.Err() != nil {
					wg.Wait()
					return StatusStopped, nil
				}
				mu.Lock()
				overCap := st.spent >= st.run.CostCap
				mu.Unlock()
				if overCap {
					// Stop dispatching, but never kill a sample already in
					// flight: it has been paid for, and its result is data.
					wg.Wait()
					return StatusCapped, nil
				}
				if !r.acquire(ctx) {
					wg.Wait()
					return StatusStopped, nil
				}
				wg.Add(1)
				go func(task Task, k int, variant Variant) {
					defer wg.Done()
					defer func() { <-r.sem }()
					r.recordSample(ctx, st, &mu, task, k, variant)
				}(task, k, variant)
			}
		}
	}
	wg.Wait()

	if ctx.Err() != nil {
		return StatusStopped, nil
	}
	return StatusDone, nil
}

// recordSample runs one sample and folds its result into the shared run state.
func (r *Runner) recordSample(ctx context.Context, st *runState, mu *sync.Mutex, task Task, k int, variant Variant) {
	smp := r.runSample(ctx, st, task, k, variant)
	mu.Lock()
	st.done++
	st.spent += smp.Cost
	if smp.Status == SampleOK {
		st.latencySum += smp.LatencyMs
		st.latencyN++
	}
	done, spent, eta := st.done, st.spent, st.eta()
	mu.Unlock()
	r.progress(st.run.ID, StatusRunning, done, st.total, spent, st.run.CostCap, eta)
}

// acquire takes a concurrency slot, aborting if the run is cancelled first.
func (r *Runner) acquire(ctx context.Context) bool {
	select {
	case r.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// eta estimates remaining seconds from mean ok-sample latency. Must be called
// with the caller's lock held.
func (st *runState) eta() int {
	if st.latencyN == 0 || st.done >= st.total {
		return 0
	}
	meanMs := float64(st.latencySum) / float64(st.latencyN)
	remaining := float64(st.total - st.done)
	return int(meanMs * remaining / 1000)
}

// SampleConvID mints the in-flight identity for one sample. The variant is
// part of it, not just run/task/k: the identity doubles as the cost tracker's
// session key, and two variants of the same (task, k) sharing one key would
// bill the second for the first's spend — enough to trip the cap early and to
// report a per-sample cost that is really a running total. It is also what
// makes the audit log's session grouping genuinely per-sample.
func SampleConvID(runID, taskID int64, k int, variantID int64) string {
	return fmt.Sprintf("eval:%d:%d:%d:%d", runID, taskID, k, variantID)
}

// runSample executes one (task, k, variant) sample and persists its row. A
// sample failure is recorded and never propagated: a provider hiccup fails
// that sample, not the run.
func (r *Runner) runSample(ctx context.Context, st *runState, task Task, k int, variant Variant) Sample {
	convID := SampleConvID(st.run.ID, task.ID, k, variant.ID)
	policy := agent.ExecPolicy{
		Kind:      agent.ExecEval,
		Variant:   variant.Name,
		ConvID:    convID,
		AsOf:      st.run.AsOf,
		History:   decodePinnedHistory(task.PinnedHistory, r.logger, task.ID),
		AuditMode: r.cfg.AuditMode,
	}
	if ov, err := DecodeOverlay(variant.Overlay); err == nil {
		policy.Model = ov.Model
		policy.Provider = ov.Provider
	} else {
		r.logger.Warn("eval variant overlay unreadable, running incumbent config",
			"run", st.run.ID, "variant", variant.Name, "error", err)
	}

	msg := adapter.IncomingMessage{
		ConversationID: convID,
		Text:           task.Prompt,
		UserName:       "eval",
		Timestamp:      st.run.AsOf,
	}

	start := time.Now()
	result, err := st.engine.DryRun(ctx, msg, policy)
	smp := Sample{
		RunID:     st.run.ID,
		VariantID: variant.ID,
		TaskID:    task.ID,
		KIndex:    k,
		LatencyMs: time.Since(start).Milliseconds(),
	}

	// Real spend is the cost tracker's, not TurnResult.CostUSD: the latter is
	// provider-reported and only OpenRouter fills it in, so the cap would
	// never trip on Anthropic/OpenAI/Ollama.
	smp.Cost = sessionCost(st.engine, convID)

	switch {
	case err != nil:
		smp.Status = SampleFailed
		if ctx.Err() != nil {
			smp.Error = "run stopped"
		} else {
			smp.Error = err.Error()
		}
	default:
		smp.Status = SampleOK
		applyResult(&smp, result, r.logger)
	}

	// Detached: a sample cancelled by Stop or panic must still leave its row
	// and its already-incurred cost behind, and the cancellation that ended it
	// would otherwise refuse both writes.
	bookkeeping := context.WithoutCancel(ctx)
	if err := r.store.AddRunCost(bookkeeping, st.run.ID, smp.Cost); err != nil {
		r.logger.Warn("recording eval sample cost failed", "run", st.run.ID, "error", err)
	}
	if _, err := r.store.AddSample(bookkeeping, smp); err != nil {
		r.logger.Error("persisting eval sample failed", "run", st.run.ID, "task", task.ID, "error", err)
	}
	return smp
}

// applyResult folds a turn result into the sample row.
func applyResult(smp *Sample, result *agent.TurnResult, logger *slog.Logger) {
	if result == nil {
		return
	}
	smp.Response = result.Response
	smp.Rounds = result.Rounds
	smp.StopReason = result.StopReason
	smp.TokensPrompt = result.Tokens.Prompt
	smp.TokensCompletion = result.Tokens.Completion
	for _, rec := range result.ToolCalls {
		switch rec.Outcome {
		case "ok":
			smp.OutcomeOK++
		case "rejected":
			smp.OutcomeRejected++
		case "failed":
			smp.OutcomeFailed++
		case "denied":
			smp.OutcomeDenied++
		case "cached":
			smp.OutcomeCached++
		case "suppressed":
			smp.OutcomeSuppressed++
		}
	}
	trace, err := encodeTrace(result.ToolCalls)
	if err != nil {
		logger.Warn("encoding eval trace failed", "error", err)
		return
	}
	smp.Trace = trace
}

// encodeTrace serialises the tool records, trimming arguments and results to
// the render cap first.
func encodeTrace(records []agent.ToolCallRecord) (string, error) {
	trimmed := make([]agent.ToolCallRecord, 0, len(records))
	for _, rec := range records {
		rec.Arguments = truncate(rec.Arguments)
		rec.Result = truncate(rec.Result)
		trimmed = append(trimmed, rec)
	}
	b, err := json.Marshal(trimmed)
	if err != nil {
		return "", fmt.Errorf("marshalling trace: %w", err)
	}
	return string(b), nil
}

func truncate(s string) string {
	if len(s) <= maxTraceFieldLen {
		return s
	}
	return s[:maxTraceFieldLen]
}

// sessionCost reads the sample's true spend from the router's cost tracker.
func sessionCost(e Engine, convID string) float64 {
	router := e.LLMRouter()
	if router == nil {
		return 0
	}
	tracker := router.CostTracker()
	if tracker == nil {
		return 0
	}
	return tracker.SessionCost(convID)
}

// DecodeOverlay parses a variant overlay. An empty or "{}" overlay is the
// incumbent: it runs the agent's live config unchanged.
func DecodeOverlay(raw string) (Overlay, error) {
	var ov Overlay
	if raw == "" || raw == "{}" {
		return ov, nil
	}
	if err := json.Unmarshal([]byte(raw), &ov); err != nil {
		return ov, fmt.Errorf("decoding overlay: %w", err)
	}
	return ov, nil
}

// pinnedMessage is one entry of eval_tasks.pinned_history.
type pinnedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// decodePinnedHistory converts the stored snippet into the engine's inline
// history. A malformed snippet degrades to a fresh turn with a warning rather
// than failing the sample — the prompt is still worth running.
func decodePinnedHistory(raw string, logger *slog.Logger, taskID int64) []agent.StoredMessage {
	if raw == "" || raw == "[]" {
		return nil
	}
	var msgs []pinnedMessage
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		logger.Warn("eval task has unreadable pinned history, running as a fresh turn",
			"task", taskID, "error", err)
		return nil
	}
	out := make([]agent.StoredMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, agent.StoredMessage{Role: m.Role, Content: m.Content})
	}
	return out
}

// progress fires OnProgress when one is wired.
func (r *Runner) progress(runID int64, status string, done, total int, spent, costCap float64, eta int) {
	if r.OnProgress == nil {
		return
	}
	r.OnProgress(ProgressEvent{
		RunID:        runID,
		Status:       status,
		SamplesDone:  done,
		SamplesTotal: total,
		CostSpent:    spent,
		CostCap:      costCap,
		ETASeconds:   eta,
	})
}

// emitLifecycle records the coarse anchor events for a run — the ones that
// survive the "summary" audit opt-down and say who ran what against whom.
func (r *Runner) emitLifecycle(ctx context.Context, run *Run, action, status string, runErr error, st *runState) {
	detail := map[string]any{
		"run_id":   run.ID,
		"task_set": run.TaskSetID,
		"k":        run.K,
		"cost_cap": run.CostCap,
		"status":   status,
	}
	if st != nil {
		detail["tasks"] = len(st.tasks)
		detail["variants"] = variantNames(st.variants)
		detail["samples_expected"] = st.total
		if action == "eval_run_finish" {
			detail["samples_done"] = st.done
			detail["cost_spent"] = st.spent
			detail["pairs"] = st.pairs
		}
	}
	evStatus := audit.StatusOK
	if runErr != nil {
		evStatus = audit.StatusError
		detail["error"] = runErr.Error()
	} else if status == StatusFailed {
		evStatus = audit.StatusError
	}
	body, _ := json.Marshal(detail)

	summary := fmt.Sprintf("Eval run %d %s on %s", run.ID, verbFor(action), run.BaseAgent)
	r.auditor.Emit(ctx, audit.Event{
		Category:       audit.CategoryEval,
		Action:         action,
		Agent:          run.BaseAgent + "#" + string(agent.ExecEval),
		Summary:        summary,
		Detail:         string(body),
		Status:         evStatus,
		Source:         string(agent.ExecEval),
		ConversationID: fmt.Sprintf("eval:%d", run.ID),
	})
}

func verbFor(action string) string {
	if action == "eval_run_start" {
		return "started"
	}
	return "finished"
}

func variantNames(vs []Variant) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ErrRunNotActive is returned by handlers that tried to stop a run that is
// already terminal.
var ErrRunNotActive = errors.New("eval: run is not active")
