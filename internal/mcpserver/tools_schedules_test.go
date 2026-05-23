package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
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
