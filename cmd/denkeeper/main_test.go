package main

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
)

func TestParseChannel_ValidInput(t *testing.T) {
	adapter, id, ok := parseChannel("telegram:123456789")
	if !ok {
		t.Fatal("expected ok=true for valid channel")
	}
	if adapter != "telegram" {
		t.Errorf("adapter = %q, want telegram", adapter)
	}
	if id != "123456789" {
		t.Errorf("externalID = %q, want 123456789", id)
	}
}

func TestParseChannel_EmptyString(t *testing.T) {
	_, _, ok := parseChannel("")
	if ok {
		t.Error("expected ok=false for empty string")
	}
}

func TestParseChannel_NoColon(t *testing.T) {
	_, _, ok := parseChannel("telegram")
	if ok {
		t.Error("expected ok=false for string without colon")
	}
}

func TestParseChannel_ColonAtStart(t *testing.T) {
	_, _, ok := parseChannel(":12345")
	if ok {
		t.Error("expected ok=false when colon is at start (empty adapter)")
	}
}

func TestParseChannel_ColonAtEnd(t *testing.T) {
	_, _, ok := parseChannel("telegram:")
	if ok {
		t.Error("expected ok=false when colon is at end (empty ID)")
	}
}

func TestParseChannel_OnlyColon(t *testing.T) {
	_, _, ok := parseChannel(":")
	if ok {
		t.Error("expected ok=false for bare colon")
	}
}

// --- initLogger tests ---

func TestInitLogger_DefaultLevel(t *testing.T) {
	cfg := &config.Config{}
	logger := initLogger(cfg)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	if !logger.Handler().Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected info level to be enabled by default")
	}
}

func TestInitLogger_DebugLevel(t *testing.T) {
	cfg := &config.Config{Log: config.LogConfig{Level: "debug"}}
	logger := initLogger(cfg)
	if !logger.Handler().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("expected debug level to be enabled")
	}
}

func TestInitLogger_WarnLevel(t *testing.T) {
	cfg := &config.Config{Log: config.LogConfig{Level: "warn"}}
	logger := initLogger(cfg)
	if logger.Handler().Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected info level to be disabled at warn")
	}
	if !logger.Handler().Enabled(context.Background(), slog.LevelWarn) {
		t.Error("expected warn level to be enabled")
	}
}

func TestInitLogger_JSONFormat(t *testing.T) {
	cfg := &config.Config{Log: config.LogConfig{Format: "json"}}
	logger := initLogger(cfg)
	if logger == nil {
		t.Fatal("expected non-nil logger for json format")
	}
}

// --- initLLMClients tests ---

func TestInitLLMClients_NoProviders(t *testing.T) {
	cfg := &config.Config{}
	clients := initLLMClients(cfg)
	if len(clients.providers) != 0 {
		t.Errorf("expected 0 providers when none configured, got %d", len(clients.providers))
	}
	if clients.cost == nil {
		t.Fatal("expected non-nil cost tracker")
	}
	if len(clients.fallbacks) != 0 {
		t.Error("expected no fallback rules")
	}
}

func TestInitLLMClients_WithOpenRouterKey(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: []config.ProviderInstanceConfig{
				{Name: "openrouter", Type: "openrouter", APIKey: "test-key"},
			},
		},
	}
	clients := initLLMClients(cfg)
	if clients.providers["openrouter"] == nil {
		t.Error("expected non-nil openrouter provider with API key")
	}
}

func TestInitLLMClients_WithAnthropicKey(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: []config.ProviderInstanceConfig{
				{Name: "anthropic", Type: "anthropic", APIKey: "test-key"},
			},
		},
	}
	clients := initLLMClients(cfg)
	if clients.providers["anthropic"] == nil {
		t.Error("expected non-nil anthropic provider with API key")
	}
}

func TestInitLLMClients_WithFallbacks(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Fallbacks: []config.FallbackConfig{
				{Trigger: "error", Action: "switch_provider", Provider: "ollama"},
				{Trigger: "rate_limit", Action: "wait_and_retry", MaxRetries: 3},
			},
		},
	}
	clients := initLLMClients(cfg)
	if len(clients.fallbacks) != 2 {
		t.Fatalf("expected 2 fallback rules, got %d", len(clients.fallbacks))
	}
	if clients.fallbacks[0].Trigger != "error" {
		t.Errorf("fallback[0].Trigger = %q, want error", clients.fallbacks[0].Trigger)
	}
	if clients.fallbacks[1].MaxRetries != 3 {
		t.Errorf("fallback[1].MaxRetries = %d, want 3", clients.fallbacks[1].MaxRetries)
	}
}

// --- initVoiceOpts tests ---

func TestInitVoiceOpts_Disabled(t *testing.T) {
	cfg := &config.Config{}
	logger := slog.Default()
	opts := initVoiceOpts(cfg, logger)
	if opts != nil {
		t.Error("expected nil voice opts when no providers configured")
	}
}

func TestInitVoiceOpts_STTOnly(t *testing.T) {
	cfg := &config.Config{
		Voice: config.VoiceConfig{
			STTProvider: "openai",
			OpenAI:      config.VoiceOpenAIConfig{APIKey: "test"},
		},
	}
	logger := slog.Default()
	opts := initVoiceOpts(cfg, logger)
	if opts == nil {
		t.Fatal("expected non-nil voice opts")
	}
	if opts.STT == nil {
		t.Error("expected non-nil STT provider")
	}
	if opts.TTS != nil {
		t.Error("expected nil TTS when not configured")
	}
}

func TestInitVoiceOpts_TTSOnly(t *testing.T) {
	cfg := &config.Config{
		Voice: config.VoiceConfig{
			TTSProvider:    "openai",
			TTSVoice:       "alloy",
			AutoVoiceReply: true,
			OpenAI:         config.VoiceOpenAIConfig{APIKey: "test"},
		},
	}
	logger := slog.Default()
	opts := initVoiceOpts(cfg, logger)
	if opts == nil {
		t.Fatal("expected non-nil voice opts")
	}
	if opts.TTS == nil {
		t.Error("expected non-nil TTS provider")
	}
	if opts.TTSVoice != "alloy" {
		t.Errorf("TTSVoice = %q, want alloy", opts.TTSVoice)
	}
	if !opts.AutoVoiceReply {
		t.Error("expected AutoVoiceReply = true")
	}
}

// --- initAdapters tests ---

func TestInitAdapters_NoAdapters(t *testing.T) {
	cfg := &config.Config{}
	logger := slog.Default()
	adapters, tg, err := initAdapters(cfg, logger, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(adapters) != 0 {
		t.Errorf("expected 0 adapters, got %d", len(adapters))
	}
	if tg != nil {
		t.Error("expected nil telegram adapter")
	}
}

// --- resolveConfigPath tests ---

func TestResolveConfigPath_FromFlag(t *testing.T) {
	orig := cfgFile
	defer func() { cfgFile = orig }()

	cfgFile = "/custom/path.toml"
	if got := resolveConfigPath(); got != "/custom/path.toml" {
		t.Errorf("resolveConfigPath() = %q, want /custom/path.toml", got)
	}
}

func TestResolveConfigPath_FromEnv(t *testing.T) {
	orig := cfgFile
	defer func() { cfgFile = orig }()
	cfgFile = ""

	t.Setenv("DENKEEPER_CONFIG", "/env/path.toml")
	if got := resolveConfigPath(); got != "/env/path.toml" {
		t.Errorf("resolveConfigPath() = %q, want /env/path.toml", got)
	}
}

func TestResolveConfigPath_Default(t *testing.T) {
	orig := cfgFile
	defer func() { cfgFile = orig }()
	cfgFile = ""

	t.Setenv("DENKEEPER_CONFIG", "")
	got := resolveConfigPath()
	if got == "" {
		t.Error("resolveConfigPath() should not return empty string")
	}
}

// --- kvCleanupDuration tests ---

func TestKVCleanupDuration_Valid(t *testing.T) {
	if got := kvCleanupDuration("30m"); got.String() != "30m0s" {
		t.Errorf("kvCleanupDuration(30m) = %v, want 30m", got)
	}
}

func TestKVCleanupDuration_Empty(t *testing.T) {
	if got := kvCleanupDuration(""); got.String() != "1h0m0s" {
		t.Errorf("kvCleanupDuration('') = %v, want 1h", got)
	}
}

func TestKVCleanupDuration_Invalid(t *testing.T) {
	if got := kvCleanupDuration("not-a-duration"); got.String() != "1h0m0s" {
		t.Errorf("kvCleanupDuration(invalid) = %v, want 1h (fallback)", got)
	}
}

func TestKVCleanupDuration_Zero(t *testing.T) {
	if got := kvCleanupDuration("0s"); got.String() != "1h0m0s" {
		t.Errorf("kvCleanupDuration(0s) = %v, want 1h (fallback for non-positive)", got)
	}
}

// --- registerSchedules tests ---

func testBoolPtr(v bool) *bool { return &v }

func testDispatcher(t *testing.T, agentName string, skills []skill.Skill) *agent.Dispatcher {
	t.Helper()
	eng := agent.NewEngine(
		agentName,
		nil, // router
		nil, // memory
		nil, // sendFunc
		nil, // permissions
		nil, // persona
		"",  // fallbackPrompt
		skills,
		nil, // tools
		nil, // approvals
		slog.Default(),
	)
	agents := map[string]*agent.Engine{agentName: eng}
	return agent.NewDispatcher(agents, nil, nil, slog.Default())
}

func TestRegisterSchedules_MissingSkillSkips(t *testing.T) {
	disp := testDispatcher(t, "default", []skill.Skill{
		{Name: "existing-skill"},
	})
	sched := scheduler.New(slog.Default(), nil)

	cfg := &config.Config{
		Schedules: []config.ScheduleConfig{
			{
				Name:     "bad-schedule",
				Type:     "agent",
				Schedule: "@daily",
				Skill:    "nonexistent-skill",
				Agent:    "default",
				Channel:  "telegram:123",
				Enabled:  testBoolPtr(true),
			},
		},
	}

	err := registerSchedules(context.Background(), cfg, sched, disp, nil, slog.Default())
	if err != nil {
		t.Fatalf("expected no error for missing skill, got: %v", err)
	}

	// The schedule should NOT have been registered.
	if _, ok := sched.GetEntry("bad-schedule"); ok {
		t.Error("schedule with missing skill should not be registered")
	}
}

func TestRegisterSchedules_UnknownAgentErrors(t *testing.T) {
	disp := testDispatcher(t, "default", nil)
	sched := scheduler.New(slog.Default(), nil)

	cfg := &config.Config{
		Schedules: []config.ScheduleConfig{
			{
				Name:     "bad-agent-schedule",
				Type:     "agent",
				Schedule: "@daily",
				Skill:    "some-skill",
				Agent:    "nonexistent-agent",
				Channel:  "telegram:123",
				Enabled:  testBoolPtr(true),
			},
		},
	}

	err := registerSchedules(context.Background(), cfg, sched, disp, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
}

func TestRegisterSchedules_ValidSkillRegisters(t *testing.T) {
	disp := testDispatcher(t, "default", []skill.Skill{
		{Name: "daily-report"},
	})
	sched := scheduler.New(slog.Default(), nil)

	cfg := &config.Config{
		Schedules: []config.ScheduleConfig{
			{
				Name:     "good-schedule",
				Type:     "agent",
				Schedule: "@daily",
				Skill:    "daily-report",
				Agent:    "default",
				Channel:  "telegram:123",
				Enabled:  testBoolPtr(true),
			},
		},
	}

	err := registerSchedules(context.Background(), cfg, sched, disp, nil, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := sched.GetEntry("good-schedule"); !ok {
		t.Error("valid schedule should be registered")
	}
}

func TestRegisterSchedules_MixedValidAndInvalid(t *testing.T) {
	disp := testDispatcher(t, "default", []skill.Skill{
		{Name: "real-skill"},
	})
	sched := scheduler.New(slog.Default(), nil)

	cfg := &config.Config{
		Schedules: []config.ScheduleConfig{
			{
				Name:     "bad-one",
				Type:     "agent",
				Schedule: "@daily",
				Skill:    "missing-skill",
				Agent:    "default",
				Channel:  "telegram:123",
				Enabled:  testBoolPtr(true),
			},
			{
				Name:     "good-one",
				Type:     "agent",
				Schedule: "@hourly",
				Skill:    "real-skill",
				Agent:    "default",
				Channel:  "telegram:456",
				Enabled:  testBoolPtr(true),
			},
		},
	}

	err := registerSchedules(context.Background(), cfg, sched, disp, nil, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := sched.GetEntry("bad-one"); ok {
		t.Error("bad schedule should be skipped")
	}
	if _, ok := sched.GetEntry("good-one"); !ok {
		t.Error("valid schedule should still be registered")
	}
}

func TestAgentLocation_AgentOverride(t *testing.T) {
	cfg := &config.Config{}
	cfg.API.Timezone = "UTC"
	ac := config.AgentInstanceConfig{Name: "default", Timezone: "Australia/Sydney"}

	loc := agentLocation(cfg, ac)
	if loc.String() != "Australia/Sydney" {
		t.Errorf("loc = %v, want Australia/Sydney", loc)
	}
}

func TestAgentLocation_GlobalFallback(t *testing.T) {
	cfg := &config.Config{}
	cfg.API.Timezone = "Europe/London"
	ac := config.AgentInstanceConfig{Name: "default"}

	loc := agentLocation(cfg, ac)
	if loc.String() != "Europe/London" {
		t.Errorf("loc = %v, want Europe/London", loc)
	}
}

func TestAgentLocation_DefaultUTC(t *testing.T) {
	loc := agentLocation(&config.Config{}, config.AgentInstanceConfig{Name: "default"})
	if loc != time.UTC {
		t.Errorf("loc = %v, want UTC", loc)
	}
}

func TestBuildScheduledMessage_TextIncludesDateHeader(t *testing.T) {
	sydney, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatalf("loading Australia/Sydney: %v", err)
	}
	sc := config.ScheduleConfig{Name: "heartbeat", Schedule: "30 8 * * *", Skill: "heartbeat"}
	entry := scheduler.Entry{Name: "heartbeat", Skill: "heartbeat"}
	target := agent.AdapterBinding{Adapter: "telegram", ExternalID: "123"}
	now := time.Date(2026, 7, 7, 10, 45, 0, 0, sydney)

	msg := buildScheduledMessage(sc, entry, target, "chan:main", sydney, now)

	wantText := "[Scheduled: heartbeat | 2026-07-07T10:45:00+10:00 Australia/Sydney | 2026-W28]"
	if msg.Text != wantText {
		t.Errorf("Text = %q, want %q", msg.Text, wantText)
	}
	if !msg.Timestamp.Equal(now) {
		t.Errorf("Timestamp = %v, want %v (must match the rendered header)", msg.Timestamp, now)
	}
	if !msg.IsScheduled {
		t.Error("IsScheduled should be true")
	}
	if msg.ConversationID != "chan:main" {
		t.Errorf("ConversationID = %q, want chan:main", msg.ConversationID)
	}
	if msg.ScheduleName != "heartbeat" || msg.ScheduleCron != "30 8 * * *" {
		t.Errorf("schedule metadata not propagated: %q %q", msg.ScheduleName, msg.ScheduleCron)
	}
}

func TestBuildScheduledMessage_IsolatedConversationID(t *testing.T) {
	sc := config.ScheduleConfig{Name: "oneshot", Skill: "oneshot"}
	entry := scheduler.Entry{Name: "oneshot", Skill: "oneshot", SessionMode: "isolated", SessionTier: "restricted"}
	target := agent.AdapterBinding{Adapter: "telegram", ExternalID: "123"}
	now := time.Date(2026, 7, 7, 0, 45, 0, 0, time.UTC)

	msg := buildScheduledMessage(sc, entry, target, "chan:main", time.UTC, now)

	wantConv := fmt.Sprintf("sched:oneshot:%d", now.UnixNano())
	if msg.ConversationID != wantConv {
		t.Errorf("ConversationID = %q, want %q", msg.ConversationID, wantConv)
	}
	if msg.SessionTier != "restricted" {
		t.Errorf("SessionTier = %q, want restricted (entry override)", msg.SessionTier)
	}
}
