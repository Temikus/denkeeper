package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
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
	if clients.openRouter != nil {
		t.Error("expected nil openRouter when no API key set")
	}
	if clients.ollama == nil {
		t.Error("expected non-nil ollama client (always created)")
	}
	if clients.anthropic != nil {
		t.Error("expected nil anthropic when no API key set")
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
			OpenRouter: config.OpenRouterConfig{APIKey: "test-key"},
		},
	}
	clients := initLLMClients(cfg)
	if clients.openRouter == nil {
		t.Error("expected non-nil openRouter with API key")
	}
}

func TestInitLLMClients_WithAnthropicKey(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Anthropic: config.AnthropicConfig{APIKey: "test-key"},
		},
	}
	clients := initLLMClients(cfg)
	if clients.anthropic == nil {
		t.Error("expected non-nil anthropic with API key")
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
