package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/netguard"
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
	if names := clients.providers.Names(); len(names) != 0 {
		t.Errorf("expected 0 providers when none configured, got %v", names)
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
	if _, ok := clients.providers.Get("openrouter"); !ok {
		t.Error("expected non-nil openrouter provider with API key")
	}
}

func TestInitLLMClients_BuildsDeciders(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: []config.ProviderInstanceConfig{
				{Name: "or", Type: "openrouter", APIKey: "test-key"},
				{Name: "anthropic", Type: "anthropic", APIKey: "test-key"},
			},
			Deciders: []config.DeciderConfig{
				{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", Timeout: "5s", MaxInputTokens: 30000},
				// Unreachable after config validation; registered, but every call fails closed.
				{Name: "bad", Provider: "anthropic", Model: "m", Timeout: "5s"},
			},
		},
	}
	clients := initLLMClients(cfg)
	d := clients.deciders.Get("jev")
	if d == nil {
		t.Fatal("expected decider jev to be built")
	}
	if d.Name() != "jev" || d.Model() != "typesafe/jev-1.13" {
		t.Errorf("decider = %s/%s", d.Name(), d.Model())
	}
	_, err := clients.deciders.Get("bad").Decide(context.Background(), "s", "state",
		map[string]llm.Question{"q": {Type: llm.QuestionNoul, Instructions: "x"}})
	if !errors.Is(err, llm.ErrNoDecisionProvider) {
		t.Errorf("decider on a non-decision provider: err = %v, want ErrNoDecisionProvider", err)
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
	if _, ok := clients.providers.Get("anthropic"); !ok {
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

func TestKVTargetsFor_NilStoreGivesNoTargets(t *testing.T) {
	if got := kvTargetsFor(nil); got != nil {
		t.Errorf("kvTargetsFor(nil) = %v, want nil", got)
	}
}

func TestKVTargetAdapter_ReadsAndStampsTheWriter(t *testing.T) {
	store, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatalf("kv store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	targets := kvTargetsFor(store)

	if exists, _, _, err := targets.KVTarget(ctx, "pamela", "missing"); err != nil || exists {
		t.Fatalf("missing key: exists=%v err=%v", exists, err)
	}
	if err := store.Set(ctx, "pamela", "digest", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := targets.StampKVWriter(ctx, "pamela", "digest", "v", "daily-digest"); err != nil {
		t.Fatalf("StampKVWriter: %v", err)
	}
	exists, writer, updatedAt, err := targets.KVTarget(ctx, "pamela", "digest")
	if err != nil || !exists || writer != "daily-digest" || updatedAt.IsZero() {
		t.Errorf("KVTarget = (%v, %q, %v, %v), want an existing key written by daily-digest",
			exists, writer, updatedAt, err)
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

func TestRegisterSchedules_HeadlessRegistersWithoutChannel(t *testing.T) {
	disp := testDispatcher(t, "default", []skill.Skill{{Name: "check-tasks"}})
	sched := scheduler.New(slog.Default(), nil)
	cfg := &config.Config{Schedules: []config.ScheduleConfig{{
		Name: "check-tasks-default", Type: "agent", Schedule: "@every 30m",
		Skill: "check-tasks", Agent: "default", Headless: true, Enabled: testBoolPtr(true),
	}}}

	if err := registerSchedules(context.Background(), cfg, sched, disp, nil, slog.Default()); err != nil {
		t.Fatal(err)
	}
	entry, ok := sched.GetEntry("check-tasks-default")
	if !ok || !entry.Headless {
		t.Fatalf("entry = %+v, %v; want a registered headless schedule", entry, ok)
	}
	msg := buildScheduledMessage(cfg.Schedules[0], entry, agent.AdapterBinding{}, "sched:check-tasks-default", time.UTC, time.Now())
	if !msg.Headless {
		t.Error("scheduled message not marked headless, so the engine would try to send a reply")
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

// A reload that clears supervisor_max_args_bytes must restore the default,
// not keep the previous override.
func TestApplySupervisorKnobs_MaxArgsBytesReloadClears(t *testing.T) {
	e := testDispatcher(t, "default", nil).Agent("default")
	def := e.SupervisorMaxArgsBytes()

	applySupervisorKnobs(e, config.AgentInstanceConfig{Name: "default", SupervisorMaxArgsBytes: 32768})
	if got := e.SupervisorMaxArgsBytes(); got != 32768 {
		t.Fatalf("after override: SupervisorMaxArgsBytes = %d, want 32768", got)
	}

	applySupervisorKnobs(e, config.AgentInstanceConfig{Name: "default"})
	if got := e.SupervisorMaxArgsBytes(); got != def {
		t.Errorf("after clearing: SupervisorMaxArgsBytes = %d, want default %d", got, def)
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

	entry.PrevRun = now.Add(-3 * time.Hour)
	withPrev := buildScheduledMessage(sc, entry, target, "chan:main", sydney, now)
	wantPrev := "[Scheduled: heartbeat | 2026-07-07T10:45:00+10:00 Australia/Sydney | 2026-W28 | last run 2026-07-07T07:45:00+10:00 (3h ago)]"
	if withPrev.Text != wantPrev {
		t.Errorf("Text with PrevRun = %q, want %q", withPrev.Text, wantPrev)
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

// The two packages state the same cap independently — config so an operator
// reads the number out of TOML documentation, agent so the engine can truncate
// without importing config. main is the one place that sees both, so this is
// where the drift is caught.
func TestTraceCapDefaultsAgree(t *testing.T) {
	if config.DefaultMaxTraceBytes != agent.DefaultMaxTraceBytes {
		t.Errorf("config default %d != agent default %d",
			config.DefaultMaxTraceBytes, agent.DefaultMaxTraceBytes)
	}
}

// webFetchConfig returns the [web.fetch] section a config file produces,
// with all defaults applied.
func webFetchConfig(t *testing.T, toml string) config.WebFetchConfig {
	t.Helper()
	cfg, err := config.Parse([]byte(toml))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg.Web.Fetch
}

// The default deployment must not reach loopback, where Denkeeper's own API
// listens.
func TestBuildWebFetcher_DefaultConfigBlocksLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	f := buildWebFetcher(webFetchConfig(t, ""), slog.Default())
	_, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("err = %v, want netguard.ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", hits.Load())
	}
}

func TestBuildWebFetcher_AllowLoopbackReaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	f := buildWebFetcher(webFetchConfig(t, "[web.fetch]\nallow_loopback = true\n"), slog.Default())
	res, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "hello" {
		t.Errorf("content = %q, want hello", res.Content)
	}
}

func TestBuildWebFetcher_DomainListsWired(t *testing.T) {
	fc := webFetchConfig(t, "[web.fetch]\nblocked_domains = [\"evil.example.com\"]\n")
	_, err := buildWebFetcher(fc, slog.Default()).Fetch(context.Background(), "https://evil.example.com/")
	if !errors.Is(err, netguard.ErrBlocked) || !strings.Contains(err.Error(), "blocked_domains") {
		t.Errorf("err = %v, want a blocked_domains refusal", err)
	}
}

// [web.fetch] is global, so the opt-in warning is logged once at startup by
// warnWebFetchOptIns, never by the per-agent buildWebFetcher.
func TestWarnWebFetchOptIns_WarnsOnceWhenOptInOn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cfg, err := config.Parse([]byte("[web.fetch]\nallow_private = true\n"))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	warnWebFetchOptIns(cfg.Web, logger)
	if strings.Count(buf.String(), "level=WARN") != 1 || !strings.Contains(buf.String(), "allow_private=true") {
		t.Errorf("want one Warn naming allow_private, got: %s", buf.String())
	}

	buf.Reset()
	buildWebFetcher(cfg.Web.Fetch, logger)
	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("buildWebFetcher runs per agent and must not warn, got: %s", buf.String())
	}
}

// reloadWebFetchLog loads before, reloads after, and returns what the reload
// logged.
func reloadWebFetchLog(t *testing.T, before, after string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denkeeper.toml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(before)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	disp := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	reload := buildReloadFunc(path, config.NewHolder(cfg), disp, approval.NewManager(store, logger), nil, reloadRuntime{}, logger)

	write(after)
	if err := reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	return buf.String()
}

const reloadPolicyWarn = "[web.fetch] network policy changed by reload"

// assertOneReloadPolicyWarn checks for exactly one reload Warn carrying all
// four opt-in fields, and no startup-style Warn.
func assertOneReloadPolicyWarn(t *testing.T, out string, fields ...string) {
	t.Helper()
	if strings.Count(out, "level=WARN") != 1 || strings.Count(out, reloadPolicyWarn) != 1 {
		t.Fatalf("want exactly one reload policy Warn, got: %s", out)
	}
	if strings.Contains(out, "network policy opened for every agent") {
		t.Errorf("reload must not reuse the startup Warn, got: %s", out)
	}
	for _, f := range fields {
		if !strings.Contains(out, f) {
			t.Errorf("reload Warn missing %q, got: %s", f, out)
		}
	}
}

// Agents built after a reload read the reloaded [web.fetch]; running agents
// keep theirs. Opening by reload must say so.
func TestReload_WebFetchOptInTurnedOn_Warns(t *testing.T) {
	out := reloadWebFetchLog(t, "", "[web.fetch]\nallow_private = true\n")
	assertOneReloadPolicyWarn(t, out,
		"allow_private=true", "allow_loopback=false",
		"was_allow_private=false", "was_allow_loopback=false")
}

// Closing by reload leaves running agents open until restart, so it must
// warn too.
func TestReload_WebFetchOptInTurnedOff_Warns(t *testing.T) {
	out := reloadWebFetchLog(t, "[web.fetch]\nallow_loopback = true\n", "")
	assertOneReloadPolicyWarn(t, out,
		"allow_private=false", "allow_loopback=false",
		"was_allow_private=false", "was_allow_loopback=true")
}

// Startup already warned; an unchanged reload must not repeat it.
func TestReload_WebFetchOptInUnchanged_Silent(t *testing.T) {
	cfg := "[web.fetch]\nallow_loopback = true\n"
	out := reloadWebFetchLog(t, cfg, cfg)
	if strings.Contains(out, "level=WARN") {
		t.Errorf("unchanged opt-in must not warn, got: %s", out)
	}
}

// With web tools off the opt-ins have no effect, so toggling them is silent.
func TestReload_WebFetchOptInWhileWebOff_Silent(t *testing.T) {
	out := reloadWebFetchLog(t,
		"[web]\nenabled = false\n",
		"[web]\nenabled = false\n[web.fetch]\nallow_private = true\n")
	if strings.Contains(out, "level=WARN") {
		t.Errorf("opt-in change with web off must not warn, got: %s", out)
	}
}

func TestWarnWebFetchOptIns_SilentByDefault(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cfg, err := config.Parse([]byte(""))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	warnWebFetchOptIns(cfg.Web, logger)
	if buf.Len() != 0 {
		t.Errorf("default config must not log, got: %s", buf.String())
	}
}

func TestWarnWebFetchOptIns_SilentWhenWebDisabled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cfg, err := config.Parse([]byte("[web]\nenabled = false\n\n[web.fetch]\nallow_loopback = true\n"))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	warnWebFetchOptIns(cfg.Web, logger)
	if buf.Len() != 0 {
		t.Errorf("disabled web tools must not warn, got: %s", buf.String())
	}
}
