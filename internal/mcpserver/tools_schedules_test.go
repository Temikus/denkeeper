package mcpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestValidateScheduleCreateInput_Valid(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "daily-check",
		Schedule: "0 9 * * *",
		Channel:  "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "" {
		t.Errorf("unexpected error: %s", msg)
	}
}

func TestValidateScheduleCreateInput_ChannelRef(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "daily-check",
		Schedule: "0 9 * * *",
		Channel:  "@general",
	}
	if msg := validateScheduleCreateInput(input); msg != "" {
		t.Errorf("unexpected error: %s", msg)
	}
}

func TestValidateScheduleCreateInput_MissingName(t *testing.T) {
	input := scheduleCreateInput{
		Schedule: "0 9 * * *",
		Channel:  "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "name is required" {
		t.Errorf("expected 'name is required', got %q", msg)
	}
}

func TestValidateScheduleCreateInput_MissingSchedule(t *testing.T) {
	input := scheduleCreateInput{
		Name:    "test",
		Channel: "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "schedule is required" {
		t.Errorf("expected 'schedule is required', got %q", msg)
	}
}

func TestValidateScheduleCreateInput_MissingChannel(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "test",
		Schedule: "0 9 * * *",
	}
	if msg := validateScheduleCreateInput(input); msg != "channel is required" {
		t.Errorf("expected 'channel is required', got %q", msg)
	}
}

func TestValidateScheduleCreateInput_InvalidCron(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "test",
		Schedule: "not a cron",
		Channel:  "telegram:12345",
	}
	msg := validateScheduleCreateInput(input)
	if msg == "" {
		t.Error("expected error for invalid cron")
	}
}

func TestValidateScheduleCreateInput_InvalidChannel(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "test",
		Schedule: "0 9 * * *",
		Channel:  "no-colon",
	}
	msg := validateScheduleCreateInput(input)
	if msg == "" {
		t.Error("expected error for invalid channel format")
	}
}

func TestValidateScheduleCreateInput_WhitespaceOnly(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "  ",
		Schedule: "0 9 * * *",
		Channel:  "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "name is required" {
		t.Errorf("expected 'name is required' for whitespace, got %q", msg)
	}
}

type stubLLM struct{}

func (stubLLM) Name() string { return "stub" }
func (stubLLM) ChatCompletion(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "stub"}, nil
}
func (stubLLM) HealthCheck(context.Context) error { return nil }

func testEngine(t *testing.T) *agent.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mem, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating memory: %v", err)
	}
	perms, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	router := llm.NewRouter("stub", "test-model", nil)
	router.RegisterProvider(stubLLM{})
	return agent.NewEngine("test-agent", router, mem, nil, perms, nil,
		"test agent", nil, nil, nil, logger)
}

func TestRollbackSchedule_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(logger, time.UTC)
	e := testEngine(t)

	s := &Server{
		deps: Deps{
			Scheduler: sched,
			Logger:    logger,
		},
	}

	entry := scheduler.Entry{
		Name:        "test-sched",
		Type:        scheduler.ScheduleTypeAgent,
		Expr:        "0 9 * * *",
		Agent:       "test-agent",
		Channel:     "telegram:12345",
		SessionMode: "isolated",
		Enabled:     true,
	}

	err := s.rollbackSchedule(entry, e)
	if err != nil {
		t.Fatalf("rollback should succeed: %v", err)
	}

	// Verify the schedule was re-registered
	_, ok := sched.GetEntry("test-sched")
	if !ok {
		t.Error("expected schedule to exist after rollback")
	}
}

func TestRollbackSchedule_FailsDuplicate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(logger, time.UTC)
	e := testEngine(t)

	s := &Server{
		deps: Deps{
			Scheduler: sched,
			Logger:    logger,
		},
	}

	entry := scheduler.Entry{
		Name:        "existing",
		Type:        scheduler.ScheduleTypeAgent,
		Expr:        "0 9 * * *",
		Agent:       "test-agent",
		Channel:     "telegram:12345",
		SessionMode: "isolated",
		Enabled:     true,
	}

	// Pre-register the schedule so rollback hits duplicate name error
	cfg := scheduler.Config{
		Name:     "existing",
		Type:     string(scheduler.ScheduleTypeAgent),
		Schedule: "0 9 * * *",
		Enabled:  false,
	}
	if err := sched.RegisterAndStart(cfg, func(scheduler.Entry) {}); err != nil {
		t.Fatalf("pre-register: %v", err)
	}

	err := s.rollbackSchedule(entry, e)
	if err == nil {
		t.Fatal("expected error for duplicate schedule")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got: %v", err)
	}
}

// failingScheduler wraps a real scheduler but returns an injected error
// from RegisterAndStart once failAfter reaches 0.
type failingScheduler struct {
	*scheduler.Scheduler
	registerErr error
	failAfter   int // number of RegisterAndStart calls to pass through before failing
	calls       int
}

func (f *failingScheduler) RegisterAndStart(cfg scheduler.Config, job scheduler.JobFunc) error {
	f.calls++
	if f.calls > f.failAfter && f.registerErr != nil {
		return f.registerErr
	}
	return f.Scheduler.RegisterAndStart(cfg, job)
}

func TestHandleScheduleUpdate_RollbackDoubleFail(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	realSched := scheduler.New(logger, time.UTC)
	e := testEngine(t)

	engines := map[string]*agent.Engine{"test-agent": e}
	dispatcher := agent.NewDispatcher(engines, nil, nil, logger)

	// Register the initial schedule on the real scheduler.
	cfg := scheduler.Config{
		Name:     "target",
		Type:     string(scheduler.ScheduleTypeAgent),
		Schedule: "0 9 * * *",
		Agent:    "test-agent",
		Channel:  "telegram:12345",
		Enabled:  true,
	}
	if err := realSched.RegisterAndStart(cfg, func(scheduler.Entry) {}); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	// Wrap in failingScheduler: the initial RegisterAndStart (seed above)
	// already passed through the real scheduler, so set failAfter=0 to make
	// ALL subsequent RegisterAndStart calls fail — both the update attempt
	// and the rollback attempt.
	fs := &failingScheduler{
		Scheduler:   realSched,
		registerErr: errors.New("injected: storage full"),
		failAfter:   0,
	}

	s := &Server{
		deps: Deps{
			Scheduler:  fs,
			Dispatcher: dispatcher,
			Logger:     logger,
		},
	}

	ctx := withScopes(context.Background(), []string{"admin"})
	disabled := false
	result, _, err := s.handleScheduleUpdate(ctx, nil, scheduleUpdateInput{
		Name:    "target",
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected MCP error result")
	}

	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if !strings.Contains(tc.Text, "update failed") {
		t.Errorf("expected 'update failed' in error, got: %s", tc.Text)
	}
	if !strings.Contains(tc.Text, "rollback also failed") {
		t.Errorf("expected 'rollback also failed' in error, got: %s", tc.Text)
	}
	if !strings.Contains(tc.Text, "target") {
		t.Errorf("expected schedule name in error, got: %s", tc.Text)
	}
}

func TestHandleScheduleList_AgentFilter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(logger, time.UTC)

	for _, sc := range []scheduler.Config{
		{Name: "alice-1", Type: string(scheduler.ScheduleTypeAgent), Schedule: "0 9 * * *", Agent: "alice", Enabled: true},
		{Name: "alice-2", Type: string(scheduler.ScheduleTypeAgent), Schedule: "0 10 * * *", Agent: "alice", Enabled: true},
		{Name: "bob-1", Type: string(scheduler.ScheduleTypeAgent), Schedule: "0 11 * * *", Agent: "bob", Enabled: true},
	} {
		if err := sched.RegisterAndStart(sc, func(scheduler.Entry) {}); err != nil {
			t.Fatalf("seed %s: %v", sc.Name, err)
		}
	}

	s := &Server{deps: Deps{Scheduler: sched, Logger: logger}}
	ctx := withScopes(context.Background(), []string{"admin"})

	textOf := func(r *mcp.CallToolResult) string {
		t.Helper()
		tc, ok := r.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", r.Content[0])
		}
		return tc.Text
	}

	// No filter: all agents' schedules.
	all, _, err := s.handleScheduleList(ctx, nil, scheduleListInput{})
	if err != nil || all.IsError {
		t.Fatalf("list all: err=%v result=%v", err, all)
	}
	allText := textOf(all)
	for _, want := range []string{"alice-1", "alice-2", "bob-1"} {
		if !strings.Contains(allText, want) {
			t.Errorf("unfiltered list missing %q: %s", want, allText)
		}
	}

	// Filtered to alice.
	filtered, _, err := s.handleScheduleList(ctx, nil, scheduleListInput{Agent: "alice"})
	if err != nil || filtered.IsError {
		t.Fatalf("list alice: err=%v result=%v", err, filtered)
	}
	aliceText := textOf(filtered)
	if !strings.Contains(aliceText, "alice-1") || !strings.Contains(aliceText, "alice-2") {
		t.Errorf("alice list missing own schedules: %s", aliceText)
	}
	if strings.Contains(aliceText, "bob-1") {
		t.Errorf("alice list leaked bob's schedule: %s", aliceText)
	}
}
