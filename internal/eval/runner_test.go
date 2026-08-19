package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockEngine stands in for *agent.Engine. It drives a real llm.CostTracker so
// the runner's cost read exercises the same path production takes rather than
// a programmed return value.
type mockEngine struct {
	mu sync.Mutex

	name    string
	router  *llm.Router
	tracker *llm.CostTracker

	// costPerSample is charged to the sample's conversation ID through the
	// cost tracker, the way the router bills a completion.
	costPerSample float64
	// respond builds the turn result for a call; nil uses a plain response.
	respond func(call int, policy agent.ExecPolicy) (*agent.TurnResult, error)
	// block, when non-nil, is waited on inside DryRun so a test can hold a
	// sample in flight.
	block chan struct{}
	// delay pauses each call, for concurrency high-water measurement.
	delay time.Duration

	calls    int
	inFlight int
	maxSeen  int
	policies []agent.ExecPolicy
}

func newMockEngine() *mockEngine {
	tracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", tracker)
	return &mockEngine{name: "pamela", router: router, tracker: tracker}
}

func (m *mockEngine) Name() string           { return m.name }
func (m *mockEngine) LLMRouter() *llm.Router { return m.router }

func (m *mockEngine) DryRun(ctx context.Context, msg adapter.IncomingMessage, policy agent.ExecPolicy) (*agent.TurnResult, error) {
	m.mu.Lock()
	call := m.calls
	m.calls++
	m.inFlight++
	if m.inFlight > m.maxSeen {
		m.maxSeen = m.inFlight
	}
	m.policies = append(m.policies, policy)
	block, delay, cost, respond := m.block, m.delay, m.costPerSample, m.respond
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.inFlight--
		m.mu.Unlock()
	}()

	if cost != 0 {
		m.tracker.Record(policy.ConvID, cost)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if respond != nil {
		return respond(call, policy)
	}
	return &agent.TurnResult{
		ConversationID: policy.ConvID,
		Prompt:         msg.Text,
		Response:       "answer for " + msg.Text,
		Rounds:         1,
	}, nil
}

func (m *mockEngine) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockEngine) highWater() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxSeen
}

func (m *mockEngine) seenPolicies() []agent.ExecPolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]agent.ExecPolicy, len(m.policies))
	copy(out, m.policies)
	return out
}

// collectingAuditor records events. The engine package's equivalent is not
// concurrency-safe and the runner is concurrent, so this one takes a mutex.
type collectingAuditor struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *collectingAuditor) Emit(_ context.Context, e audit.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *collectingAuditor) all() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, len(c.events))
	copy(out, c.events)
	return out
}

// runnerFixture wires a store, a mock engine and a runner over them.
type runnerFixture struct {
	store  *Store
	engine *mockEngine
	runner *Runner
	setID  int64
}

func newRunnerFixture(t *testing.T, cfg Config, auditor audit.Emitter) *runnerFixture {
	t.Helper()
	store := newTestStore(t)
	set := mustTaskSet(t, store, "set")
	engine := newMockEngine()
	source := func(name string) (Engine, bool) {
		if name != engine.name {
			return nil, false
		}
		return engine, true
	}
	if cfg.CompletenessFloor == 0 {
		cfg.CompletenessFloor = 0.8
	}
	runner := NewRunner(store, source, auditor, cfg, testLogger())
	t.Cleanup(runner.Shutdown)
	return &runnerFixture{store: store, engine: engine, runner: runner, setID: set.ID}
}

func (f *runnerFixture) addTasks(t *testing.T, prompts ...string) []Task {
	t.Helper()
	out := make([]Task, 0, len(prompts))
	for _, p := range prompts {
		out = append(out, *mustTask(t, f.store, f.setID, p))
	}
	return out
}

func (f *runnerFixture) createRun(t *testing.T, k int, costCap float64, variants ...Variant) *Run {
	t.Helper()
	run, _, err := f.store.CreateRun(context.Background(), Run{
		TaskSetID: f.setID, BaseAgent: "pamela", K: k, CostCap: costCap, AsOf: time.Now().UTC(),
	}, variants)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return run
}

// waitFor polls cond until it holds, and fails with msg if it never does.
//
// It exists because the run row reaches its terminal status *before* the finish
// audit event and the final progress frame are emitted, and that ordering is
// deliberate: the authoritative GET must never lag the droppable frame, or a
// client that refetches on seeing "done" would read "running". So waiting on
// the row (waitForTerminal) is not the same as waiting on the side effects that
// follow it, and a test asserting on those has to wait for them specifically.
func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// waitForTerminal polls until the run reaches a terminal status. Note that the
// run's finish audit event and final progress frame are emitted *after* this
// returns — use waitFor to observe those.
func waitForTerminal(t *testing.T, store *Store, runID int64) *Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if IsTerminal(run.Status) {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %d never reached a terminal status", runID)
	return nil
}

func twoVariants() []Variant {
	return []Variant{{Name: "incumbent"}, {Name: "candidate", Overlay: `{"llm_model":"kimi-k3"}`}}
}

func TestRunner_ProducesTasksTimesVariantsTimesKSamples(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 2}, nil)
	f.addTasks(t, "first", "second")
	run := f.createRun(t, 2, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusDone {
		t.Fatalf("status = %q, want %q (error %q)", got.Status, StatusDone, got.Error)
	}

	samples, err := f.store.ListSamples(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 8 {
		t.Fatalf("got %d samples, want 2 tasks × 2 variants × k=2 = 8", len(samples))
	}
	for _, smp := range samples {
		if smp.Status != SampleOK {
			t.Errorf("sample %d is %q: %s", smp.ID, smp.Status, smp.Error)
		}
	}
}

func TestRunner_SampleConvIDIsUniquePerVariant(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "only")
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)

	seen := make(map[string]struct{})
	for _, p := range f.engine.seenPolicies() {
		if _, dup := seen[p.ConvID]; dup {
			t.Fatalf("two samples shared conversation id %q — cost would be double-counted", p.ConvID)
		}
		seen[p.ConvID] = struct{}{}
		if !strings.HasPrefix(p.ConvID, "eval:") {
			t.Errorf("conversation id %q is outside the eval namespace", p.ConvID)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("got %d distinct conversation ids, want 2", len(seen))
	}
}

func TestRunner_PolicyCarriesVariantOverlayAndPinnedHistory(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1, AuditMode: agent.AuditSummary}, nil)
	if _, err := f.store.AddTask(context.Background(), f.setID, Task{
		Prompt: "continue", Category: CategoryChat,
		PinnedHistory: `[{"role":"user","content":"earlier"},{"role":"assistant","content":"noted"}]`,
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	run := f.createRun(t, 1, 10.0,
		Variant{Name: "incumbent"},
		Variant{Name: "candidate", Overlay: `{"llm_model":"kimi-k3","llm_provider":"openrouter"}`})

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)

	policies := f.engine.seenPolicies()
	if len(policies) != 2 {
		t.Fatalf("got %d policies, want 2", len(policies))
	}
	byVariant := make(map[string]agent.ExecPolicy, 2)
	for _, p := range policies {
		byVariant[p.Variant] = p
		if p.Kind != agent.ExecEval {
			t.Errorf("policy kind = %q, want %q", p.Kind, agent.ExecEval)
		}
		if p.AuditMode != agent.AuditSummary {
			t.Errorf("audit mode = %q, want the configured %q", p.AuditMode, agent.AuditSummary)
		}
		if len(p.History) != 2 || p.History[0].Content != "earlier" {
			t.Errorf("pinned history = %+v, want the task's two messages replayed verbatim", p.History)
		}
	}
	if got := byVariant["incumbent"]; got.Model != "" || got.Provider != "" {
		t.Errorf("incumbent overlay = %q/%q, want the live config (empty)", got.Model, got.Provider)
	}
	if got := byVariant["candidate"]; got.Model != "kimi-k3" || got.Provider != "openrouter" {
		t.Errorf("candidate overlay = %q/%q, want kimi-k3/openrouter", got.Model, got.Provider)
	}
}

func TestRunner_SampleFailureDoesNotFailRun(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a", "b")
	f.engine.respond = func(call int, policy agent.ExecPolicy) (*agent.TurnResult, error) {
		if call == 1 {
			return nil, fmt.Errorf("provider hiccup")
		}
		return &agent.TurnResult{ConversationID: policy.ConvID, Response: "fine", Rounds: 1}, nil
	}
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusDone {
		t.Fatalf("status = %q, want %q — one bad sample must not fail the run", got.Status, StatusDone)
	}

	samples, err := f.store.ListSamples(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 4", len(samples))
	}
	failed := 0
	for _, smp := range samples {
		if smp.Status == SampleFailed {
			failed++
			if !strings.Contains(smp.Error, "provider hiccup") {
				t.Errorf("failed sample error = %q, want the provider message preserved", smp.Error)
			}
		}
	}
	if failed != 1 {
		t.Errorf("%d failed samples, want exactly 1", failed)
	}
}

func TestRunner_CostCapStopsDispatchWithCappedStatus(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a", "b", "c", "d")
	f.engine.costPerSample = 0.30
	// Four tasks × two variants = eight samples at $0.30; a $1 cap admits
	// three and refuses to dispatch the fourth.
	run := f.createRun(t, 1, 1.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusCapped {
		t.Fatalf("status = %q, want %q", got.Status, StatusCapped)
	}

	samples, err := f.store.ListSamples(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("capped run kept no samples; partial results are the point of the status")
	}
	if len(samples) >= 8 {
		t.Fatalf("all %d samples ran despite the cap", len(samples))
	}
	if got.CostSpent < 1.0 {
		t.Errorf("cost_spent = %v, want it to have reached the $1 cap", got.CostSpent)
	}
	for _, smp := range samples {
		if smp.Status != SampleOK {
			t.Errorf("sample %d is %q — an in-flight sample must be allowed to finish", smp.ID, smp.Status)
		}
	}
}

func TestRunner_StopCancelsInFlightSamples(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a", "b")
	f.engine.block = make(chan struct{})
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForCalls(t, f.engine, 1)

	if !f.runner.Stop(run.ID) {
		t.Fatal("Stop reported the run was not active")
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusStopped {
		t.Fatalf("status = %q, want %q", got.Status, StatusStopped)
	}
	if f.engine.callCount() >= 4 {
		t.Errorf("%d samples dispatched after the stop; queued samples must never start", f.engine.callCount())
	}

	samples, err := f.store.ListSamples(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	for _, smp := range samples {
		if smp.Status == SampleFailed && smp.Error != "run stopped" {
			t.Errorf("cancelled sample error = %q, want %q", smp.Error, "run stopped")
		}
	}
}

func TestRunner_StopReportsFalseForUnknownRun(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	if f.runner.Stop(4242) {
		t.Fatal("Stop reported success for a run that was never active")
	}
}

func TestRunner_StopAllOnPanicStopsEveryActiveRun(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 4}, nil)
	f.addTasks(t, "a", "b")
	f.engine.block = make(chan struct{})

	runA := f.createRun(t, 1, 10.0, twoVariants()...)
	runB := f.createRun(t, 1, 10.0, twoVariants()...)
	for _, run := range []*Run{runA, runB} {
		if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
			t.Fatalf("StartRun: %v", err)
		}
	}
	waitForCalls(t, f.engine, 2)

	f.runner.StopAll()

	for _, run := range []*Run{runA, runB} {
		got := waitForTerminal(t, f.store, run.ID)
		if got.Status != StatusStopped {
			t.Errorf("run %d status = %q, want %q", run.ID, got.Status, StatusStopped)
		}
	}
}

// A run cancelled in the window before its first sample is stopped, not
// failed. prepare's store reads run on the cancellable context, so a stop
// landing there returns context.Canceled — which is a decision, not a store
// error, and "failed" has to keep meaning something was actually wrong.
func TestRunner_StopDuringPrepareReportsStoppedNotFailed(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a")
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	// Cancel immediately, racing prepare rather than waiting for a sample.
	f.runner.StopAll()

	got := waitForTerminal(t, f.store, run.ID)
	if got.Status == StatusFailed {
		t.Fatalf("a cancelled run reported %q with error %q; a stop is not a failure",
			got.Status, got.Error)
	}
	if got.Status != StatusStopped {
		t.Fatalf("status = %q, want %q", got.Status, StatusStopped)
	}
	if got.Error != "" {
		t.Errorf("stopped run carries error %q, want none", got.Error)
	}
}

func TestRunner_ConcurrencyBoundedByMaxConcurrent(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 2}, nil)
	f.addTasks(t, "a", "b", "c")
	f.engine.delay = 20 * time.Millisecond
	run := f.createRun(t, 2, 100.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)

	if hw := f.engine.highWater(); hw > 2 {
		t.Fatalf("%d samples ran concurrently, want at most max_concurrent = 2", hw)
	}
}

func TestRunner_EmitsLifecycleAuditEvents(t *testing.T) {
	auditor := &collectingAuditor{}
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, auditor)
	f.addTasks(t, "a")
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)
	// The finish event trails the row write, so wait for the event itself.
	waitFor(t, "the eval_run_finish audit event", func() bool {
		for _, e := range auditor.all() {
			if e.Action == "eval_run_finish" {
				return true
			}
		}
		return false
	})

	events := auditor.all()
	var start, finish *audit.Event
	for i := range events {
		switch events[i].Action {
		case "eval_run_start":
			start = &events[i]
		case "eval_run_finish":
			finish = &events[i]
		}
	}
	if start == nil || finish == nil {
		t.Fatalf("missing lifecycle events: start=%v finish=%v", start != nil, finish != nil)
	}
	for _, e := range []*audit.Event{start, finish} {
		if e.Category != audit.CategoryEval {
			t.Errorf("category = %q, want %q", e.Category, audit.CategoryEval)
		}
		if e.Source != string(agent.ExecEval) {
			t.Errorf("source = %q, want %q so exclude_source can filter it", e.Source, agent.ExecEval)
		}
		if e.Agent != "pamela#eval" {
			t.Errorf("agent = %q, want the pseudo-identity %q", e.Agent, "pamela#eval")
		}
	}

	var detail map[string]any
	if err := json.Unmarshal([]byte(finish.Detail), &detail); err != nil {
		t.Fatalf("finish detail is not JSON: %v", err)
	}
	if detail["samples_done"] != float64(2) {
		t.Errorf("samples_done = %v, want 2", detail["samples_done"])
	}
}

func TestRunner_UnknownAgentFailsTheRun(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a")
	run, _, err := f.store.CreateRun(context.Background(), Run{
		TaskSetID: f.setID, BaseAgent: "ghost", K: 1, CostCap: 1, AsOf: time.Now().UTC(),
	}, twoVariants())
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, StatusFailed)
	}
	if !strings.Contains(got.Error, "ghost") {
		t.Errorf("error = %q, want it to name the missing agent", got.Error)
	}
}

func TestRunner_EmptyTaskSetFailsTheRun(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, StatusFailed)
	}
}

func TestRunner_StartRunRejectsNonPendingRun(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a")
	run := f.createRun(t, 1, 10.0, twoVariants()...)
	if err := f.store.FinishRun(context.Background(), run.ID, StatusDone, ""); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	if err := f.runner.StartRun(context.Background(), run.ID); err == nil {
		t.Fatal("StartRun accepted a terminal run")
	}
}

func TestRunner_TraceCarriesArgumentsAndTruncatesLongFields(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a")
	huge := strings.Repeat("x", maxTraceFieldLen+500)
	f.engine.respond = func(_ int, policy agent.ExecPolicy) (*agent.TurnResult, error) {
		return &agent.TurnResult{
			ConversationID: policy.ConvID,
			Response:       "done",
			Rounds:         2,
			StopReason:     "max_rounds",
			ToolCalls: []agent.ToolCallRecord{
				{ToolName: "read_thing", Round: 1, Outcome: "ok", Arguments: `{"v":1}`, Result: huge},
				{ToolName: "write_thing", Round: 2, Outcome: "suppressed"},
			},
		}, nil
	}
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)

	samples, err := f.store.ListSamples(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	smp := samples[0]
	if smp.OutcomeOK != 1 || smp.OutcomeSuppressed != 1 {
		t.Errorf("outcome counts ok/suppressed = %d/%d, want 1/1", smp.OutcomeOK, smp.OutcomeSuppressed)
	}
	if smp.StopReason != "max_rounds" {
		t.Errorf("stop_reason = %q, want the slug %q", smp.StopReason, "max_rounds")
	}

	var trace []agent.ToolCallRecord
	if err := json.Unmarshal([]byte(smp.Trace), &trace); err != nil {
		t.Fatalf("trace is not JSON: %v", err)
	}
	if len(trace) != 2 {
		t.Fatalf("trace has %d calls, want 2", len(trace))
	}
	if trace[0].Arguments != `{"v":1}` {
		t.Errorf("arguments = %q, want them preserved", trace[0].Arguments)
	}
	if len(trace[0].Result) != maxTraceFieldLen {
		t.Errorf("result length = %d, want it trimmed to %d", len(trace[0].Result), maxTraceFieldLen)
	}
}

func TestRunner_ShutdownStopsActiveRuns(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a", "b")
	f.engine.block = make(chan struct{})
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForCalls(t, f.engine, 1)

	f.runner.Shutdown()

	if f.runner.IsActive(run.ID) {
		t.Error("run still active after Shutdown returned")
	}
	got, err := f.store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != StatusStopped {
		t.Errorf("status = %q, want %q", got.Status, StatusStopped)
	}
}

func TestRunner_ProgressReportsSampleCounts(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.addTasks(t, "a")
	run := f.createRun(t, 1, 10.0, twoVariants()...)

	var mu sync.Mutex
	var collected []ProgressEvent
	f.runner.OnProgress = func(e ProgressEvent) {
		mu.Lock()
		collected = append(collected, e)
		mu.Unlock()
	}

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)
	// The terminal frame trails the row write, so wait for the frame itself.
	waitFor(t, "the terminal progress frame", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(collected) > 0 && IsTerminal(collected[len(collected)-1].Status)
	})

	mu.Lock()
	defer mu.Unlock()
	events := collected
	if len(events) < 3 {
		t.Fatalf("got %d progress events, want start + per-sample + finish", len(events))
	}
	last := events[len(events)-1]
	if last.Status != StatusDone || last.SamplesDone != 2 || last.SamplesTotal != 2 {
		t.Errorf("final event = %+v, want done 2/2", last)
	}
	if last.CostCap != 10.0 {
		t.Errorf("cost cap = %v, want 10", last.CostCap)
	}
}

// Pairing is a run-finalization step, so a finished run arrives at the judge
// queue already populated — no separate "prepare for judging" call to forget.
func TestRunner_FinalizationCreatesJudgmentPairs(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 2}, nil)
	f.addTasks(t, "first", "second")
	run := f.createRun(t, 2, 10.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)

	pairs, err := f.store.CountPairs(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("CountPairs: %v", err)
	}
	if pairs != 4 {
		t.Fatalf("got %d pairs, want 4 (2 tasks × k=2)", pairs)
	}
	pending, err := f.store.ListPending(context.Background(), run.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 8 {
		t.Fatalf("got %d pending items, want 8 (both presentation orders per pair)", len(pending))
	}
}

// A capped run keeps its partial results, so it must still be judgeable on the
// samples it paid for.
func TestRunner_CappedRunStillPairsWhatItProduced(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	f.engine.costPerSample = 0.6
	f.addTasks(t, "first", "second", "third")
	run := f.createRun(t, 1, 1.0, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusCapped {
		t.Fatalf("status = %q, want %q", got.Status, StatusCapped)
	}
	pairs, err := f.store.CountPairs(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("CountPairs: %v", err)
	}
	if pairs == 0 {
		t.Fatal("a capped run produced no pairs — its partial results are unjudgeable")
	}
}

func TestDecodeOverlay_EmptyIsIncumbent(t *testing.T) {
	for _, raw := range []string{"", "{}"} {
		ov, err := DecodeOverlay(raw)
		if err != nil {
			t.Fatalf("DecodeOverlay(%q): %v", raw, err)
		}
		if ov.Model != "" || ov.Provider != "" {
			t.Errorf("DecodeOverlay(%q) = %+v, want the zero overlay", raw, ov)
		}
	}
}

func TestDecodeOverlay_RejectsGarbage(t *testing.T) {
	if _, err := DecodeOverlay("not json"); err == nil {
		t.Fatal("DecodeOverlay accepted invalid JSON")
	}
}

// waitForCalls blocks until the mock engine has seen at least n calls.
func waitForCalls(t *testing.T, m *mockEngine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.callCount() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("engine saw %d calls, waited for %d", m.callCount(), n)
}

// createPinnedRun mirrors createRun but pins the run to a subset of the set's
// tasks, the shape POST /eval/runs produces when sample_tasks is set.
func (f *runnerFixture) createPinnedRun(t *testing.T, k int, costCap float64, pin TaskIDList, variants ...Variant) *Run {
	t.Helper()
	run, _, err := f.store.CreateRun(context.Background(), Run{
		TaskSetID: f.setID, BaseAgent: "pamela", K: k, CostCap: costCap,
		AsOf: time.Now().UTC(), TaskIDs: pin,
	}, variants)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return run
}

func TestRunner_DispatchesOnlyThePinnedTasks(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 2}, nil)
	tasks := f.addTasks(t, "first", "second", "third", "fourth")
	run := f.createPinnedRun(t, 1, 10.0,
		TaskIDList{tasks[0].ID, tasks[2].ID}, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if got := waitForTerminal(t, f.store, run.ID); got.Status != StatusDone {
		t.Fatalf("status = %q, want %q (error %q)", got.Status, StatusDone, got.Error)
	}

	samples, err := f.store.ListSamples(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 2 pinned tasks x 2 variants x k=1 = 4", len(samples))
	}
	pinned := map[int64]bool{tasks[0].ID: true, tasks[2].ID: true}
	for _, smp := range samples {
		if !pinned[smp.TaskID] {
			t.Errorf("sample ran task %d, which the run does not pin", smp.TaskID)
		}
	}
}

// The runner's dispatch count and the summary's samples_expected are read from
// the same pinned list; a disagreement would make every finished sampled run
// look incomplete.
func TestRunner_PinnedRunIsSampleComplete(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 2}, nil)
	tasks := f.addTasks(t, "first", "second", "third")
	run := f.createPinnedRun(t, 2, 10.0, TaskIDList{tasks[1].ID}, twoVariants()...)

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminal(t, f.store, run.ID)

	sum, err := f.store.Summarize(context.Background(), run.ID, SummaryOpts{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.Completeness.SamplesExpected != 4 {
		t.Errorf("samples_expected = %d, want 1 pinned task x 2 variants x k=2 = 4",
			sum.Completeness.SamplesExpected)
	}
	if sum.Completeness.SamplesOK != 4 || !sum.Completeness.Conclusive {
		t.Errorf("completeness = %+v, want a complete, conclusive run", sum.Completeness)
	}
}

func TestRunner_PinnedRunWhoseTasksAllVanishedFails(t *testing.T) {
	f := newRunnerFixture(t, Config{MaxConcurrent: 1}, nil)
	tasks := f.addTasks(t, "only", "other")
	run := f.createPinnedRun(t, 1, 10.0, TaskIDList{tasks[0].ID}, twoVariants()...)
	if err := f.store.DeleteTask(context.Background(), f.setID, tasks[0].ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if err := f.runner.StartRun(context.Background(), run.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitForTerminal(t, f.store, run.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want %q - a run with nothing left to dispatch is a failure, not a silent no-op",
			got.Status, StatusFailed)
	}
	if !strings.Contains(got.Error, "pins") {
		t.Errorf("error = %q, want it to name the vanished pin", got.Error)
	}
}
