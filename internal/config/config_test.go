package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_ValidConfig(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm]
default_provider = "openrouter"
default_model = "anthropic/claude-sonnet-4-20250514"
max_cost_per_session = 2.0

[llm.openrouter]
api_key = "sk-or-test-key"

[memory]
db_path = "/tmp/test.db"

[log]
level = "debug"
format = "json"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Telegram.Token != "123456:ABC-DEF" {
		t.Errorf("telegram token = %q, want %q", cfg.Telegram.Token, "123456:ABC-DEF")
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 111222333 {
		t.Errorf("allowed_users = %v, want [111222333]", cfg.Telegram.AllowedUsers)
	}
	if cfg.LLM.MaxCostPerSession != 2.0 {
		t.Errorf("max_cost_per_session = %f, want 2.0", cfg.LLM.MaxCostPerSession)
	}
	if cfg.Memory.DBPath != "/tmp/test.db" {
		t.Errorf("db_path = %q, want /tmp/test.db", cfg.Memory.DBPath)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("log level = %q, want debug", cfg.Log.Level)
	}
}

func TestParse_Defaults(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LLM.DefaultProvider != "openrouter" {
		t.Errorf("default_provider = %q, want openrouter", cfg.LLM.DefaultProvider)
	}
	if cfg.LLM.DefaultModel != "anthropic/claude-sonnet-4-20250514" {
		t.Errorf("default_model = %q, want anthropic/claude-sonnet-4-20250514", cfg.LLM.DefaultModel)
	}
	if cfg.LLM.MaxCostPerSession != 1.0 {
		t.Errorf("max_cost_per_session = %f, want 1.0", cfg.LLM.MaxCostPerSession)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("log level = %q, want info", cfg.Log.Level)
	}
}

func TestParse_MissingToken(t *testing.T) {
	tomlData := []byte(`
[api]
enabled = false

[telegram]
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestParse_NoAllowedUsers(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"

[llm.openrouter]
api_key = "sk-or-test-key"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for empty allowed_users")
	}
}

func TestParse_MissingAPIKey(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for missing api_key")
	}
}

func TestParse_AgentDefaults(t *testing.T) {
	tomlData := []byte(baseConfig)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agent.PersonaDir == "" {
		t.Fatal("Agent.PersonaDir should not be empty after defaults")
	}
	if !strings.HasSuffix(cfg.Agent.PersonaDir, filepath.Join(".denkeeper", "agents", "default")) {
		t.Errorf("Agent.PersonaDir = %q, want suffix .denkeeper/agents/default", cfg.Agent.PersonaDir)
	}

	if cfg.Agent.SkillsDir == "" {
		t.Fatal("Agent.SkillsDir should not be empty after defaults")
	}
	if !strings.HasSuffix(cfg.Agent.SkillsDir, filepath.Join(".denkeeper", "skills")) {
		t.Errorf("Agent.SkillsDir = %q, want suffix .denkeeper/skills", cfg.Agent.SkillsDir)
	}
}

func TestParse_AgentCustomPersonaDir(t *testing.T) {
	tomlData := []byte(baseConfig + `
[agent]
persona_dir = "/custom/persona/path"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agent.PersonaDir != "/custom/persona/path" {
		t.Errorf("Agent.PersonaDir = %q, want /custom/persona/path", cfg.Agent.PersonaDir)
	}
}

// ---------------------------------------------------------------------------
// Schedule config tests
// ---------------------------------------------------------------------------

const baseConfig = `
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"
`

func TestParse_Schedules_Valid(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "daily-briefing"
type = "agent"
schedule = "@daily"
skill = "briefing"
session_tier = "supervised"
channel = "telegram:123456"
tags = ["morning", "briefing"]
enabled = true

[[schedules]]
name = "agent-heartbeat"
type = "system"
schedule = "@every 5m"
tags = ["system"]
# enabled omitted — should default to true
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Schedules) != 2 {
		t.Fatalf("Schedules len = %d, want 2", len(cfg.Schedules))
	}

	s0 := cfg.Schedules[0]
	if s0.Name != "daily-briefing" {
		t.Errorf("Schedules[0].Name = %q, want daily-briefing", s0.Name)
	}
	if s0.Type != "agent" {
		t.Errorf("Schedules[0].Type = %q, want agent", s0.Type)
	}
	if s0.Skill != "briefing" {
		t.Errorf("Schedules[0].Skill = %q, want briefing", s0.Skill)
	}
	if len(s0.Tags) != 2 || s0.Tags[0] != "morning" {
		t.Errorf("Schedules[0].Tags = %v, want [morning briefing]", s0.Tags)
	}
	if s0.Enabled == nil || !*s0.Enabled {
		t.Error("Schedules[0].Enabled should be true")
	}

	s1 := cfg.Schedules[1]
	if s1.Type != "system" {
		t.Errorf("Schedules[1].Type = %q, want system", s1.Type)
	}
	// Omitted enabled should default to true.
	if s1.Enabled == nil || !*s1.Enabled {
		t.Error("Schedules[1].Enabled should default to true when omitted")
	}
	// Omitted session_tier should default to "supervised".
	if s1.SessionTier != "supervised" {
		t.Errorf("Schedules[1].SessionTier = %q, want supervised (default)", s1.SessionTier)
	}
	// Omitted session_mode should default to "shared".
	if s1.SessionMode != "shared" {
		t.Errorf("Schedules[1].SessionMode = %q, want shared (default)", s1.SessionMode)
	}
}

func TestParse_Schedules_CronExpr(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "weekday-standup"
type = "agent"
schedule = "0 9 * * 1-5"
enabled = true
`)

	if _, err := Parse(tomlData); err != nil {
		t.Fatalf("unexpected error for valid cron expression: %v", err)
	}
}

func TestParse_Schedules_ExplicitlyDisabled(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "paused-job"
type = "agent"
schedule = "@weekly"
enabled = false
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Schedules[0].Enabled == nil || *cfg.Schedules[0].Enabled {
		t.Error("explicitly disabled schedule should have Enabled = false")
	}
}

func TestParse_Schedules_MissingName(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
type = "agent"
schedule = "@daily"
enabled = true
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for missing schedule name")
	}
}

func TestParse_Schedules_DuplicateName(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "dup"
type = "agent"
schedule = "@daily"
enabled = true

[[schedules]]
name = "dup"
type = "system"
schedule = "@hourly"
enabled = true
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for duplicate schedule name")
	}
}

func TestParse_Schedules_InvalidType(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "bad-type"
type = "worker"
schedule = "@daily"
enabled = true
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid schedule type")
	}
}

func TestParse_Schedules_MissingScheduleExpr(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "no-expr"
type = "agent"
enabled = true
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for missing schedule expression")
	}
}

func TestParse_Schedules_InvalidSessionTier(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "bad-tier"
type = "agent"
schedule = "@daily"
session_tier = "superuser"
enabled = true
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid session_tier")
	}
}

func TestParse_Schedules_SessionMode(t *testing.T) {
	for _, mode := range []string{"shared", "isolated"} {
		tomlData := []byte(baseConfig + `
[[schedules]]
name = "mode-test"
type = "agent"
schedule = "@daily"
session_mode = "` + mode + `"
enabled = true
`)
		if _, err := Parse(tomlData); err != nil {
			t.Errorf("session_mode=%q: unexpected error: %v", mode, err)
		}
	}
}

func TestParse_Schedules_InvalidSessionMode(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "bad-mode"
type = "agent"
schedule = "@daily"
session_mode = "shared-ish"
enabled = true
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid session_mode")
	}
}

func TestParse_Schedules_MissingType(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "no-type"
schedule = "@daily"
enabled = true
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for missing schedule type")
	}
}

// ---------------------------------------------------------------------------
// Fallback config tests
// ---------------------------------------------------------------------------

func TestParse_Fallbacks_Valid(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "low_funds"
action = "switch_model"
model = "meta-llama/llama-3.1-8b-instruct:free"
threshold = 5.00

[[llm.fallback]]
trigger = "rate_limit"
action = "wait_and_retry"
max_retries = 3
backoff = "exponential"

[[llm.fallback]]
trigger = "error"
action = "switch_provider"
provider = "backup"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.LLM.Fallbacks) != 3 {
		t.Fatalf("Fallbacks len = %d, want 3", len(cfg.LLM.Fallbacks))
	}

	f0 := cfg.LLM.Fallbacks[0]
	if f0.Trigger != "low_funds" || f0.Action != "switch_model" || f0.Threshold != 5.00 {
		t.Errorf("Fallbacks[0] = %+v, unexpected values", f0)
	}

	f1 := cfg.LLM.Fallbacks[1]
	if f1.Trigger != "rate_limit" || f1.Action != "wait_and_retry" || f1.MaxRetries != 3 || f1.Backoff != "exponential" {
		t.Errorf("Fallbacks[1] = %+v, unexpected values", f1)
	}

	f2 := cfg.LLM.Fallbacks[2]
	if f2.Trigger != "error" || f2.Action != "switch_provider" || f2.Provider != "backup" {
		t.Errorf("Fallbacks[2] = %+v, unexpected values", f2)
	}
}

func TestParse_Fallbacks_Empty(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.LLM.Fallbacks) != 0 {
		t.Errorf("expected no fallbacks, got %d", len(cfg.LLM.Fallbacks))
	}
}

func TestParse_Fallbacks_DefaultBackoff(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "rate_limit"
action = "wait_and_retry"
max_retries = 2
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.Fallbacks[0].Backoff != "exponential" {
		t.Errorf("backoff = %q, want exponential (default)", cfg.LLM.Fallbacks[0].Backoff)
	}
}

func TestParse_Fallbacks_InvalidTrigger(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "foo"
action = "switch_model"
model = "some-model"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid trigger")
	}
}

func TestParse_Fallbacks_InvalidAction(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "error"
action = "restart"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestParse_Fallbacks_InvalidBackoff(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "rate_limit"
action = "wait_and_retry"
max_retries = 3
backoff = "linear"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid backoff")
	}
}

func TestParse_Fallbacks_SwitchProviderMissingProvider(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "error"
action = "switch_provider"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for switch_provider without provider field")
	}
}

func TestParse_Fallbacks_SwitchModelMissingModel(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "error"
action = "switch_model"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for switch_model without model field")
	}
}

func TestParse_Fallbacks_WaitAndRetryMissingMaxRetries(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "rate_limit"
action = "wait_and_retry"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for wait_and_retry without max_retries")
	}
}

func TestParse_Fallbacks_LowFundsMissingThreshold(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "low_funds"
action = "switch_model"
model = "some-model"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for low_funds without threshold")
	}
}

func TestParse_Fallbacks_LowFundsNegativeThreshold(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[llm.fallback]]
trigger = "low_funds"
action = "switch_model"
model = "some-model"
threshold = -1.0
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for low_funds with negative threshold")
	}
}

// ---------------------------------------------------------------------------
// Session config tests
// ---------------------------------------------------------------------------

func TestParse_SessionTierDefault(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Session.Tier != "supervised" {
		t.Errorf("Session.Tier = %q, want supervised (default)", cfg.Session.Tier)
	}
}

func TestParse_SessionTierExplicit(t *testing.T) {
	for _, tier := range []string{"supervised", "autonomous", "restricted"} {
		tomlData := []byte(baseConfig + `
[session]
tier = "` + tier + `"
`)
		cfg, err := Parse(tomlData)
		if err != nil {
			t.Fatalf("tier=%q: unexpected error: %v", tier, err)
		}
		if cfg.Session.Tier != tier {
			t.Errorf("Session.Tier = %q, want %q", cfg.Session.Tier, tier)
		}
	}
}

func TestParse_SessionTierInvalid(t *testing.T) {
	tomlData := []byte(baseConfig + `
[session]
tier = "root"
`)

	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid session tier")
	}
}

func TestParse_Tools(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.web-search]
command = "denkeeper-tool-websearch"
args = ["--provider", "tavily"]

[tools.calendar]
command = "denkeeper-tool-gcal"
env = { GCAL_TOKEN = "test-token" }
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(cfg.Tools))
	}

	ws, ok := cfg.Tools["web-search"]
	if !ok {
		t.Fatal("missing web-search tool config")
	}
	if ws.Command != "denkeeper-tool-websearch" {
		t.Errorf("command = %q, want denkeeper-tool-websearch", ws.Command)
	}
	if len(ws.Args) != 2 || ws.Args[0] != "--provider" || ws.Args[1] != "tavily" {
		t.Errorf("args = %v, want [--provider tavily]", ws.Args)
	}

	cal, ok := cfg.Tools["calendar"]
	if !ok {
		t.Fatal("missing calendar tool config")
	}
	if cal.Command != "denkeeper-tool-gcal" {
		t.Errorf("command = %q, want denkeeper-tool-gcal", cal.Command)
	}
	if cal.Env["GCAL_TOKEN"] != "test-token" {
		t.Errorf("env GCAL_TOKEN = %q, want test-token", cal.Env["GCAL_TOKEN"])
	}
}

func TestParse_ToolMissingCommand(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-tool]
args = ["--flag"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for tool missing command")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolEnvExpansion(t *testing.T) {
	t.Setenv("DENKEEPER_TEST_VAR", "expanded-value")

	tomlData := []byte(baseConfig + `
[tools.test-tool]
command = "test-cmd"
env = { MY_VAR = "${DENKEEPER_TEST_VAR}" }
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tc := cfg.Tools["test-tool"]
	if tc.Env["MY_VAR"] != "expanded-value" {
		t.Errorf("MY_VAR = %q, want expanded-value", tc.Env["MY_VAR"])
	}
}

func TestParse_VoiceConfig(t *testing.T) {
	tomlData := []byte(baseConfig + `
[voice]
stt_provider = "openai"
tts_provider = "openai"
tts_voice = "nova"
auto_voice_reply = true

[voice.openai]
api_key = "sk-voice-test"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Voice.STTProvider != "openai" {
		t.Errorf("STTProvider = %q, want openai", cfg.Voice.STTProvider)
	}
	if cfg.Voice.TTSProvider != "openai" {
		t.Errorf("TTSProvider = %q, want openai", cfg.Voice.TTSProvider)
	}
	if cfg.Voice.TTSVoice != "nova" {
		t.Errorf("TTSVoice = %q, want nova", cfg.Voice.TTSVoice)
	}
	if !cfg.Voice.AutoVoiceReply {
		t.Error("AutoVoiceReply should be true")
	}
	if cfg.Voice.OpenAI.APIKey != "sk-voice-test" {
		t.Errorf("OpenAI.APIKey = %q, want sk-voice-test", cfg.Voice.OpenAI.APIKey)
	}
}

func TestParse_VoiceDefaults(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Voice.STTProvider != "" {
		t.Errorf("STTProvider should be empty, got %q", cfg.Voice.STTProvider)
	}
	if cfg.Voice.TTSProvider != "" {
		t.Errorf("TTSProvider should be empty, got %q", cfg.Voice.TTSProvider)
	}
	if cfg.Voice.AutoVoiceReply {
		t.Error("AutoVoiceReply should default to false")
	}
}

func TestParse_VoiceTTSVoiceDefault(t *testing.T) {
	tomlData := []byte(baseConfig + `
[voice]
tts_provider = "openai"

[voice.openai]
api_key = "sk-test"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Voice.TTSVoice != "alloy" {
		t.Errorf("TTSVoice should default to alloy, got %q", cfg.Voice.TTSVoice)
	}
}

func TestParse_VoiceInvalidSTTProvider(t *testing.T) {
	tomlData := []byte(baseConfig + `
[voice]
stt_provider = "google"

[voice.openai]
api_key = "sk-test"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for unsupported STT provider")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_VoiceMissingAPIKey(t *testing.T) {
	tomlData := []byte(baseConfig + `
[voice]
stt_provider = "openai"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for missing OpenAI API key")
	}
	if !strings.Contains(err.Error(), "api_key is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_VoiceInvalidTTSVoice(t *testing.T) {
	tomlData := []byte(baseConfig + `
[voice]
tts_provider = "openai"
tts_voice = "invalid-voice"

[voice.openai]
api_key = "sk-test"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for invalid TTS voice")
	}
	if !strings.Contains(err.Error(), "invalid voice") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Multi-agent config tests
// ---------------------------------------------------------------------------

func TestParse_Agents_BackwardCompat(t *testing.T) {
	// No [[agents]] defined — should synthesize a "default" agent from [agent]/[session].
	tomlData := []byte(baseConfig + `
[agent]
persona_dir = "/custom/persona"
skills_dir = "/custom/skills"

[session]
tier = "autonomous"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1 (synthesized)", len(cfg.Agents))
	}
	a := cfg.Agents[0]
	if a.Name != "default" {
		t.Errorf("Name = %q, want default", a.Name)
	}
	if a.PersonaDir != "/custom/persona" {
		t.Errorf("PersonaDir = %q, want /custom/persona", a.PersonaDir)
	}
	if a.SkillsDir != "/custom/skills" {
		t.Errorf("SkillsDir = %q, want /custom/skills", a.SkillsDir)
	}
	if a.SessionTier != "autonomous" {
		t.Errorf("SessionTier = %q, want autonomous", a.SessionTier)
	}
	if len(a.Adapters) != 1 || a.Adapters[0] != "telegram" {
		t.Errorf("Adapters = %v, want [telegram]", a.Adapters)
	}
}

func TestParse_Agents_Explicit(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
description = "General assistant"
persona_dir = "/agents/default"
adapters = ["telegram"]

[[agents]]
name = "work"
description = "Work assistant"
persona_dir = "/agents/work"
adapters = ["telegram:99999"]
llm_model = "openai/gpt-4o"
session_tier = "restricted"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Agents) != 2 {
		t.Fatalf("Agents len = %d, want 2", len(cfg.Agents))
	}

	a0 := cfg.Agents[0]
	if a0.Name != "default" || a0.PersonaDir != "/agents/default" {
		t.Errorf("Agents[0] = %+v, unexpected", a0)
	}

	a1 := cfg.Agents[1]
	if a1.Name != "work" {
		t.Errorf("Agents[1].Name = %q, want work", a1.Name)
	}
	if a1.LLMModel != "openai/gpt-4o" {
		t.Errorf("Agents[1].LLMModel = %q, want openai/gpt-4o", a1.LLMModel)
	}
	if a1.SessionTier != "restricted" {
		t.Errorf("Agents[1].SessionTier = %q, want restricted", a1.SessionTier)
	}
}

func TestParse_Agents_MissingDefault(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "work"
persona_dir = "/agents/work"
adapters = ["telegram"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when no default agent defined")
	}
	if !strings.Contains(err.Error(), "named \"default\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_Agents_DuplicateName(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/a"
adapters = ["telegram"]

[[agents]]
name = "default"
persona_dir = "/agents/b"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for duplicate agent name")
	}
	if !strings.Contains(err.Error(), "duplicate agent name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_Agents_InvalidSessionTier(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
session_tier = "root"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for invalid agent session_tier")
	}
}

func TestParse_Agents_NegativeMaxToolRounds(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
max_tool_rounds = -1
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for negative max_tool_rounds")
	}
	if !strings.Contains(err.Error(), "max_tool_rounds must be >= 0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_Agents_ConflictingWildcard(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]

[[agents]]
name = "other"
persona_dir = "/agents/other"
adapters = ["telegram"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for conflicting wildcard bindings")
	}
	if !strings.Contains(err.Error(), "conflicts with") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_Agents_PersonaDirDefault(t *testing.T) {
	// When persona_dir is omitted from [[agents]], it should default to ~/.denkeeper/agents/<name>.
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
adapters = ["telegram"]
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(cfg.Agents[0].PersonaDir, filepath.Join(".denkeeper", "agents", "default")) {
		t.Errorf("PersonaDir = %q, want suffix .denkeeper/agents/default", cfg.Agents[0].PersonaDir)
	}
}

func TestParse_Schedules_AgentField(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]

[[agents]]
name = "work"
persona_dir = "/agents/work"
adapters = ["telegram:99999"]

[[schedules]]
name = "work-report"
type = "agent"
schedule = "@daily"
agent = "work"
channel = "telegram:99999"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Schedules[0].Agent != "work" {
		t.Errorf("Schedules[0].Agent = %q, want work", cfg.Schedules[0].Agent)
	}
}

func TestParse_Schedules_AgentDefault(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig + `
[[schedules]]
name = "test"
type = "agent"
schedule = "@daily"
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Schedules[0].Agent != "default" {
		t.Errorf("Schedules[0].Agent = %q, want default", cfg.Schedules[0].Agent)
	}
}

// ---------------------------------------------------------------------------
// API config tests
// ---------------------------------------------------------------------------

func TestParse_APIEnabledByDefault(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.Enabled == nil || !*cfg.API.Enabled {
		t.Error("API should be enabled by default")
	}
}

func TestParse_APIExplicitlyDisabled(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = false
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.Enabled == nil || *cfg.API.Enabled {
		t.Error("API should be disabled when explicitly set to false")
	}
}

func TestParse_APIEnabled(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true
listen = ":9090"

[[api.keys]]
name = "test-key"
key = "dk-secret"
scopes = ["health", "chat"]
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.Enabled == nil || !*cfg.API.Enabled {
		t.Error("API should be enabled")
	}
	if cfg.API.Listen != ":9090" {
		t.Errorf("Listen = %q, want :9090", cfg.API.Listen)
	}
	if len(cfg.API.Keys) != 1 {
		t.Fatalf("Keys len = %d, want 1", len(cfg.API.Keys))
	}
	if cfg.API.Keys[0].Name != "test-key" {
		t.Errorf("Keys[0].Name = %q, want test-key", cfg.API.Keys[0].Name)
	}
}

func TestParse_APIDefaultListen(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.Listen != ":8080" {
		t.Errorf("Listen = %q, want :8080 (default)", cfg.API.Listen)
	}
}

func TestParse_APITLSMissingCertFile(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true
tls = true
key_file = "certs/api.key"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for TLS without cert_file")
	}
	if !strings.Contains(err.Error(), "cert_file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APITLSMissingKeyFile(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true
tls = true
cert_file = "certs/api.crt"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for TLS without key_file")
	}
	if !strings.Contains(err.Error(), "key_file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APIKeyMissingName(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[[api.keys]]
key = "dk-secret"
scopes = ["health"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for key missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APIKeyMissingSecret(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[[api.keys]]
name = "test"
scopes = ["health"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for key missing secret")
	}
	if !strings.Contains(err.Error(), "key is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APIKeyDuplicateName(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[[api.keys]]
name = "dup"
key = "dk-one"
scopes = ["health"]

[[api.keys]]
name = "dup"
key = "dk-two"
scopes = ["chat"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for duplicate key name")
	}
	if !strings.Contains(err.Error(), "duplicate key name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APIKeyNoScopes(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[[api.keys]]
name = "test"
key = "dk-secret"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for key with no scopes")
	}
	if !strings.Contains(err.Error(), "at least one scope") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APIKeyInvalidScope(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[[api.keys]]
name = "test"
key = "dk-secret"
scopes = ["health", "superadmin"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
	if !strings.Contains(err.Error(), "invalid scope") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APIKeyToolScopes_Valid(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[[api.keys]]
name = "tool-admin"
key = "dk-tool-key"
scopes = ["tools:read", "tools:write"]
`)

	_, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error for tools:read/write scopes: %v", err)
	}
}

func TestParse_MaxTools(t *testing.T) {
	tomlData := []byte(`
max_tools = 25

[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxTools != 25 {
		t.Errorf("MaxTools = %d, want 25", cfg.MaxTools)
	}
}

func TestParse_OllamaProvider_NoAPIKeyRequired(t *testing.T) {
	// When default_provider is "ollama", no OpenRouter API key should be required.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm]
default_provider = "ollama"
default_model = "llama3"

[llm.ollama]
base_url = "http://localhost:11434"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error for ollama-only config: %v", err)
	}
	if cfg.LLM.DefaultProvider != "ollama" {
		t.Errorf("default_provider = %q, want ollama", cfg.LLM.DefaultProvider)
	}
	if cfg.LLM.Ollama.BaseURL != "http://localhost:11434" {
		t.Errorf("ollama.base_url = %q, want http://localhost:11434", cfg.LLM.Ollama.BaseURL)
	}
}

func TestParse_OllamaProvider_FallbackRequiresOpenRouterKey(t *testing.T) {
	// When a fallback references openrouter, the API key must be present.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm]
default_provider = "ollama"
default_model = "llama3"

[[llm.fallback]]
trigger = "error"
action = "switch_provider"
provider = "openrouter"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when openrouter fallback is configured without an API key")
	}
}

func TestParse_Schedules_InvalidAgent(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "bad-agent-ref"
type = "agent"
schedule = "@daily"
agent = "nonexistent"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for schedule referencing nonexistent agent")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_Schedules_InvalidChannelFormat(t *testing.T) {
	for _, ch := range []string{"telegram", ":123456", "telegram:", ""} {
		label := ch
		if label == "" {
			continue // empty channel is allowed (schedule fires but skips delivery)
		}
		tomlData := []byte(baseConfig + `
[[schedules]]
name = "bad-chan"
type = "agent"
schedule = "@daily"
channel = "` + ch + `"
`)
		_, err := Parse(tomlData)
		if err == nil {
			t.Errorf("channel=%q: expected error for invalid channel format", label)
		}
	}
}

func TestParse_Schedules_ValidChannel(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[schedules]]
name = "good-chan"
type = "agent"
schedule = "@daily"
channel = "telegram:387956986"
`)
	if _, err := Parse(tomlData); err != nil {
		t.Fatalf("unexpected error for valid channel: %v", err)
	}
}

func TestParse_Session_ApprovalTimeout_Valid(t *testing.T) {
	tomlData := []byte(baseConfig + `
[session]
approval_timeout = "10m"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Session.ApprovalTimeout != "10m" {
		t.Errorf("ApprovalTimeout = %q, want 10m", cfg.Session.ApprovalTimeout)
	}
}

func TestParse_Session_ApprovalTimeout_Invalid(t *testing.T) {
	tomlData := []byte(baseConfig + `
[session]
approval_timeout = "banana"
`)
	if _, err := Parse(tomlData); err == nil {
		t.Fatal("expected error for invalid approval_timeout")
	}
}

func TestParse_Session_ApprovalTimeout_Default(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Session.ApprovalTimeout != "5m" {
		t.Errorf("ApprovalTimeout = %q, want 5m", cfg.Session.ApprovalTimeout)
	}
}

func TestParse_Plugins(t *testing.T) {
	tomlData := []byte(baseConfig + `
[plugins.web-scraper]
type         = "subprocess"
command      = "denkeeper-plugin-scraper"
args         = ["--headless"]
capabilities = ["tools"]
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(cfg.Plugins))
	}
	p, ok := cfg.Plugins["web-scraper"]
	if !ok {
		t.Fatal("missing web-scraper plugin config")
	}
	if p.Type != "subprocess" {
		t.Errorf("Type = %q, want subprocess", p.Type)
	}
	if p.Command != "denkeeper-plugin-scraper" {
		t.Errorf("Command = %q, want denkeeper-plugin-scraper", p.Command)
	}
	if len(p.Args) != 1 || p.Args[0] != "--headless" {
		t.Errorf("Args = %v, want [--headless]", p.Args)
	}
	if len(p.Capabilities) != 1 || p.Capabilities[0] != "tools" {
		t.Errorf("Capabilities = %v, want [tools]", p.Capabilities)
	}
}

func TestParse_PluginDockerTypeAcceptedInConfig(t *testing.T) {
	tomlData := []byte(baseConfig + `
[plugins.sandboxed]
type         = "docker"
image        = "ghcr.io/example/plugin:latest"
memory_limit = "256m"
cpu_limit    = "0.5"
network      = "none"
volumes      = ["/data:/mnt/data:ro"]
capabilities = ["tools"]
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("expected docker plugin to be accepted, got: %v", err)
	}
	p := cfg.Plugins["sandboxed"]
	if p.Image != "ghcr.io/example/plugin:latest" {
		t.Errorf("Image = %q, want ghcr.io/example/plugin:latest", p.Image)
	}
	if p.MemoryLimit != "256m" {
		t.Errorf("MemoryLimit = %q, want 256m", p.MemoryLimit)
	}
	if p.CPULimit != "0.5" {
		t.Errorf("CPULimit = %q, want 0.5", p.CPULimit)
	}
}

func TestParse_PluginDockerMissingImage_ReturnsError(t *testing.T) {
	tomlData := []byte(baseConfig + `
[plugins.bad-docker]
type         = "docker"
command      = "something"
capabilities = ["tools"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for docker plugin without image, got nil")
	}
}

func TestParse_PluginMissingType(t *testing.T) {
	tomlData := []byte(baseConfig + `
[plugins.bad-plugin]
command      = "some-command"
capabilities = ["tools"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for plugin missing type")
	}
	if !strings.Contains(err.Error(), "type is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_PluginInvalidType(t *testing.T) {
	tomlData := []byte(baseConfig + `
[plugins.bad-plugin]
type         = "kubernetes"
command      = "some-command"
capabilities = ["tools"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for invalid plugin type")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_PluginMissingCommand(t *testing.T) {
	tomlData := []byte(baseConfig + `
[plugins.bad-plugin]
type         = "subprocess"
capabilities = ["tools"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for plugin missing command")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_PluginNameCollisionWithTool(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.my-server]
command = "denkeeper-tool-server"

[plugins.my-server]
type         = "subprocess"
command      = "denkeeper-plugin-server"
capabilities = ["tools"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for plugin name conflicting with tool")
	}
	if !strings.Contains(err.Error(), "conflicts with tools") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_PluginEnvExpansion(t *testing.T) {
	t.Setenv("DENKEEPER_PLUGIN_TEST_VAR", "plugin-expanded-value")

	tomlData := []byte(baseConfig + `
[plugins.test-plugin]
type         = "subprocess"
command      = "test-plugin-cmd"
env          = { PLUGIN_VAR = "${DENKEEPER_PLUGIN_TEST_VAR}" }
capabilities = ["tools"]
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := cfg.Plugins["test-plugin"]
	if p.Env["PLUGIN_VAR"] != "plugin-expanded-value" {
		t.Errorf("PLUGIN_VAR = %q, want plugin-expanded-value", p.Env["PLUGIN_VAR"])
	}
}

// ---------------------------------------------------------------------------
// Discord + Anthropic config tests
// ---------------------------------------------------------------------------

const discordBaseConfig = `
[discord]
token = "Bot.discord.token"
allowed_users = ["123456789012345678"]

[llm.openrouter]
api_key = "sk-or-test-key"
`

func TestParse_DiscordOnly_Valid(t *testing.T) {
	_, err := Parse([]byte(discordBaseConfig))
	if err != nil {
		t.Fatalf("unexpected error for discord-only config: %v", err)
	}
}

func TestParse_DiscordOnly_NoAllowedUsers(t *testing.T) {
	tomlData := []byte(`
[discord]
token = "Bot.discord.token"

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for discord with no allowed_users")
	}
	if !strings.Contains(err.Error(), "discord.allowed_users") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_NoAdaptersConfigured(t *testing.T) {
	// Neither telegram nor discord token set and API disabled.
	tomlData := []byte(`
[api]
enabled = false

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when no adapters are configured")
	}
	if !strings.Contains(err.Error(), "at least one adapter") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_APIOnlyMode_Valid(t *testing.T) {
	// No adapter tokens but API is enabled (default) — valid for web-only use.
	tomlData := []byte(`
[llm]
default_provider = "ollama"

[llm.ollama]
base_url = "http://localhost:11434"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error for API-only config: %v", err)
	}
	if !cfg.API.IsEnabled() {
		t.Error("API should be enabled by default")
	}
}

func TestParse_BothAdapters_Valid(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[discord]
token = "Bot.discord.token"
allowed_users = ["123456789012345678"]

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	_, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error for both-adapter config: %v", err)
	}
}

func TestParse_AnthropicProvider_Valid(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "anthropic"

[llm.anthropic]
api_key = "sk-ant-test"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error for anthropic provider config: %v", err)
	}
	if cfg.LLM.DefaultProvider != "anthropic" {
		t.Errorf("default_provider = %q, want anthropic", cfg.LLM.DefaultProvider)
	}
	if cfg.LLM.Anthropic.APIKey != "sk-ant-test" {
		t.Errorf("anthropic api_key = %q, want sk-ant-test", cfg.LLM.Anthropic.APIKey)
	}
}

func TestParse_AnthropicProvider_MissingAPIKey(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "anthropic"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for anthropic provider without api_key")
	}
	if !strings.Contains(err.Error(), "requires an api_key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_AnthropicProvider_NoOpenRouterKeyRequired(t *testing.T) {
	// When using anthropic provider, openrouter.api_key should NOT be required.
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "anthropic"

[llm.anthropic]
api_key = "sk-ant-test"
`)
	_, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("expected no error when openrouter key absent with anthropic provider: %v", err)
	}
}

func TestParse_DiscordOnly_DefaultAgentAdapters(t *testing.T) {
	// When only discord is configured, the synthesized default agent should
	// have "discord" in its adapters list (not "telegram").
	cfg, err := Parse([]byte(discordBaseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents) == 0 {
		t.Fatal("expected synthesized agents, got none")
	}
	found := false
	for _, a := range cfg.Agents[0].Adapters {
		if a == "discord" {
			found = true
		}
	}
	if !found {
		t.Errorf("default agent adapters = %v, want to contain discord", cfg.Agents[0].Adapters)
	}
}

func TestParse_DiscordConfig_BaseURL(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "anthropic"

[llm.anthropic]
api_key    = "sk-ant-test"
base_url   = "https://custom.anthropic.proxy"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.Anthropic.BaseURL != "https://custom.anthropic.proxy" {
		t.Errorf("anthropic base_url = %q, want https://custom.anthropic.proxy", cfg.LLM.Anthropic.BaseURL)
	}
}

// ---------------------------------------------------------------------------
// Default path helpers
// ---------------------------------------------------------------------------

func TestDefaultDBPath(t *testing.T) {
	got := DefaultDBPath()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".denkeeper", "data", "memory.db")
	if got != want {
		t.Errorf("DefaultDBPath() = %q, want %q", got, want)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	got := DefaultConfigPath()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".denkeeper", "denkeeper.toml")
	if got != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Security config tests
// ---------------------------------------------------------------------------

func TestParse_SecurityConfig_Defaults(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Security.AllowUnsigned == nil || !*cfg.Security.AllowUnsigned {
		t.Error("expected AllowUnsigned to default to true")
	}
	if len(cfg.Security.TrustedKeys) != 0 {
		t.Errorf("expected no trusted keys by default, got %d", len(cfg.Security.TrustedKeys))
	}
}

func TestParse_SecurityConfig_ExplicitValues(t *testing.T) {
	tomlData := []byte(baseConfig + `
[security]
trusted_keys = ["~/.denkeeper/keys/pub1.pub", "~/.denkeeper/keys/pub2.pub"]
allow_unsigned = false
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Security.TrustedKeys) != 2 {
		t.Fatalf("expected 2 trusted keys, got %d", len(cfg.Security.TrustedKeys))
	}
	if cfg.Security.TrustedKeys[0] != "~/.denkeeper/keys/pub1.pub" {
		t.Errorf("trusted_keys[0] = %q, want ~/.denkeeper/keys/pub1.pub", cfg.Security.TrustedKeys[0])
	}
	if cfg.Security.AllowUnsigned == nil || *cfg.Security.AllowUnsigned {
		t.Error("expected AllowUnsigned to be false")
	}
}

func TestParse_SecurityConfig_AllowUnsignedTrue(t *testing.T) {
	tomlData := []byte(baseConfig + `
[security]
allow_unsigned = true
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Security.AllowUnsigned == nil || !*cfg.Security.AllowUnsigned {
		t.Error("expected AllowUnsigned to be true")
	}
}

// --------------------------------------------------------------------------
// Tests: KV config
// --------------------------------------------------------------------------

func TestParse_KVDefaults(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.KV.MaxKeysPerAgent != 1000 {
		t.Errorf("KV.MaxKeysPerAgent = %d, want 1000", cfg.KV.MaxKeysPerAgent)
	}
	if cfg.KV.MaxValueBytes != 65536 {
		t.Errorf("KV.MaxValueBytes = %d, want 65536", cfg.KV.MaxValueBytes)
	}
	if cfg.KV.CleanupInterval != "1h" {
		t.Errorf("KV.CleanupInterval = %q, want %q", cfg.KV.CleanupInterval, "1h")
	}
}

func TestParse_KVExplicitValues(t *testing.T) {
	tomlData := []byte(baseConfig + `
[kv]
max_keys_per_agent = 500
max_value_bytes = 32768
cleanup_interval = "30m"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.KV.MaxKeysPerAgent != 500 {
		t.Errorf("KV.MaxKeysPerAgent = %d, want 500", cfg.KV.MaxKeysPerAgent)
	}
	if cfg.KV.MaxValueBytes != 32768 {
		t.Errorf("KV.MaxValueBytes = %d, want 32768", cfg.KV.MaxValueBytes)
	}
	if cfg.KV.CleanupInterval != "30m" {
		t.Errorf("KV.CleanupInterval = %q, want %q", cfg.KV.CleanupInterval, "30m")
	}
}

// --------------------------------------------------------------------------
// Tests: float-to-int normalisation (Helm toToml compatibility)
// --------------------------------------------------------------------------

func TestParse_FloatToIntCoercion_AllowedUsers(t *testing.T) {
	// Helm's toToml renders YAML integers as TOML floats because YAML
	// values flow through Go as float64. Verify Parse handles this.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [3.87956986e+08]

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 387956986 {
		t.Errorf("allowed_users = %v, want [387956986]", cfg.Telegram.AllowedUsers)
	}
}

func TestParse_FloatToIntCoercion_DecimalFloat(t *testing.T) {
	// Same as above but with decimal notation instead of scientific.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [387956986.0]

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 387956986 {
		t.Errorf("allowed_users = %v, want [387956986]", cfg.Telegram.AllowedUsers)
	}
}

func TestParse_FloatToIntCoercion_PreservesRealFloats(t *testing.T) {
	// Ensure float64 fields like max_cost_per_session are not mangled.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [3.87956986e+08]

[llm]
max_cost_per_session = 2.5

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.MaxCostPerSession != 2.5 {
		t.Errorf("max_cost_per_session = %f, want 2.5", cfg.LLM.MaxCostPerSession)
	}
}

func TestParse_FloatToIntCoercion_MultipleUsers(t *testing.T) {
	// Multiple float-encoded user IDs should all convert.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [1.0, 3.87956986e+08, 42.0]

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int64{1, 387956986, 42}
	if len(cfg.Telegram.AllowedUsers) != len(want) {
		t.Fatalf("allowed_users length = %d, want %d", len(cfg.Telegram.AllowedUsers), len(want))
	}
	for i, w := range want {
		if cfg.Telegram.AllowedUsers[i] != w {
			t.Errorf("allowed_users[%d] = %d, want %d", i, cfg.Telegram.AllowedUsers[i], w)
		}
	}
}

func TestParse_FloatToIntCoercion_NestedTables(t *testing.T) {
	// Floats in nested tables (e.g. max_keys_per_agent) should also be fixed.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [1.0]

[llm.openrouter]
api_key = "sk-or-test-key"

[kv]
max_keys_per_agent = 500.0
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.KV.MaxKeysPerAgent != 500 {
		t.Errorf("kv.max_keys_per_agent = %d, want 500", cfg.KV.MaxKeysPerAgent)
	}
}

func TestParse_NormalIntegers_NoNormalisationNeeded(t *testing.T) {
	// Plain integers should work without triggering normalisation.
	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [387956986]

[llm.openrouter]
api_key = "sk-or-test-key"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 387956986 {
		t.Errorf("allowed_users = %v, want [387956986]", cfg.Telegram.AllowedUsers)
	}
}

// --------------------------------------------------------------------------
// Tests: synthesizeDefaultAgent backward compatibility
// --------------------------------------------------------------------------

func TestSynthesizeDefaultAgent_TelegramOnly(t *testing.T) {
	cfg := &Config{
		Telegram: TelegramConfig{Token: "tok"},
		Agent:    AgentConfig{PersonaDir: "/p", SkillsDir: "/s"},
		Session:  SessionConfig{Tier: "autonomous"},
	}
	synthesizeDefaultAgent(cfg)

	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
	a := cfg.Agents[0]
	if a.Name != "default" {
		t.Errorf("Name = %q, want %q", a.Name, "default")
	}
	if len(a.Adapters) != 1 || a.Adapters[0] != "telegram" {
		t.Errorf("Adapters = %v, want [telegram]", a.Adapters)
	}
	if a.SessionTier != "autonomous" {
		t.Errorf("SessionTier = %q, want %q", a.SessionTier, "autonomous")
	}
}

func TestSynthesizeDefaultAgent_BothAdapters(t *testing.T) {
	cfg := &Config{
		Telegram: TelegramConfig{Token: "tok"},
		Discord:  DiscordConfig{Token: "dtok"},
		Session:  SessionConfig{Tier: "supervised"},
	}
	synthesizeDefaultAgent(cfg)

	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
	if len(cfg.Agents[0].Adapters) != 2 {
		t.Errorf("expected 2 adapters, got %v", cfg.Agents[0].Adapters)
	}
}

func TestSynthesizeDefaultAgent_ExplicitAgentsNotOverridden(t *testing.T) {
	cfg := &Config{
		Agents: []AgentInstanceConfig{{Name: "custom"}},
	}
	synthesizeDefaultAgent(cfg)

	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "custom" {
		t.Errorf("explicit agents should not be overridden, got %v", cfg.Agents)
	}
}

// --------------------------------------------------------------------------
// Tests: expandEnvVars
// --------------------------------------------------------------------------

func TestApplyEnvOverrides_TelegramToken(t *testing.T) {
	t.Setenv("DENKEEPER_TELEGRAM_TOKEN", "env-token-123")

	cfg := &Config{}
	applyEnvOverrides(cfg)

	if cfg.Telegram.Token != "env-token-123" {
		t.Errorf("telegram token = %q, want %q", cfg.Telegram.Token, "env-token-123")
	}
}

func TestApplyEnvOverrides_DoesNotOverrideWhenUnset(t *testing.T) {
	cfg := &Config{
		Telegram: TelegramConfig{Token: "toml-token"},
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			OpenRouter:      OpenRouterConfig{APIKey: "toml-key"},
		},
	}
	applyEnvOverrides(cfg)

	if cfg.Telegram.Token != "toml-token" {
		t.Errorf("telegram token = %q, want %q", cfg.Telegram.Token, "toml-token")
	}
	if cfg.LLM.OpenRouter.APIKey != "toml-key" {
		t.Errorf("openrouter api_key = %q, want %q", cfg.LLM.OpenRouter.APIKey, "toml-key")
	}
}

func TestApplyEnvOverrides_APIEnabledBoolParsing(t *testing.T) {
	// "true" should enable
	t.Setenv("DENKEEPER_API_ENABLED", "true")
	cfg := &Config{}
	applyEnvOverrides(cfg)
	if cfg.API.Enabled == nil || !*cfg.API.Enabled {
		t.Error("API.Enabled should be true when env is \"true\"")
	}

	// "1" should enable
	t.Setenv("DENKEEPER_API_ENABLED", "1")
	cfg = &Config{}
	applyEnvOverrides(cfg)
	if cfg.API.Enabled == nil || !*cfg.API.Enabled {
		t.Error("API.Enabled should be true when env is \"1\"")
	}

	// "false" should disable
	t.Setenv("DENKEEPER_API_ENABLED", "false")
	cfg = &Config{}
	applyEnvOverrides(cfg)
	if cfg.API.Enabled == nil || *cfg.API.Enabled {
		t.Error("API.Enabled should be false when env is \"false\"")
	}

	// "0" should disable
	t.Setenv("DENKEEPER_API_ENABLED", "0")
	cfg = &Config{}
	applyEnvOverrides(cfg)
	if cfg.API.Enabled == nil || *cfg.API.Enabled {
		t.Error("API.Enabled should be false when env is \"0\"")
	}

	// "yes" should NOT change the value (strict parsing)
	t.Setenv("DENKEEPER_API_ENABLED", "yes")
	cfg = &Config{}
	applyEnvOverrides(cfg)
	if cfg.API.Enabled != nil {
		t.Error("API.Enabled should be nil when env is \"yes\" (strict parsing)")
	}
}

func TestApplyEnvOverrides_OTelEnabledBoolParsing(t *testing.T) {
	t.Setenv("DENKEEPER_OTEL_ENABLED", "true")
	cfg := &Config{}
	applyEnvOverrides(cfg)
	if !cfg.OTel.Enabled {
		t.Error("OTel.Enabled should be true when env is \"true\"")
	}

	t.Setenv("DENKEEPER_OTEL_ENABLED", "1")
	cfg = &Config{}
	applyEnvOverrides(cfg)
	if !cfg.OTel.Enabled {
		t.Error("OTel.Enabled should be true when env is \"1\"")
	}

	t.Setenv("DENKEEPER_OTEL_ENABLED", "false")
	cfg = &Config{OTel: OTelConfig{Enabled: true}}
	applyEnvOverrides(cfg)
	if cfg.OTel.Enabled {
		t.Error("OTel.Enabled should be false when env is \"false\"")
	}

	t.Setenv("DENKEEPER_OTEL_ENABLED", "0")
	cfg = &Config{OTel: OTelConfig{Enabled: true}}
	applyEnvOverrides(cfg)
	if cfg.OTel.Enabled {
		t.Error("OTel.Enabled should be false when env is \"0\"")
	}
}

func TestApplyEnvOverrides_AllSecrets(t *testing.T) {
	t.Setenv("DENKEEPER_TELEGRAM_TOKEN", "tg-token")
	t.Setenv("DENKEEPER_DISCORD_TOKEN", "dc-token")
	t.Setenv("DENKEEPER_LLM_OPENROUTER_API_KEY", "or-key")
	t.Setenv("DENKEEPER_LLM_ANTHROPIC_API_KEY", "ant-key")
	t.Setenv("DENKEEPER_VOICE_OPENAI_API_KEY", "oai-key")
	t.Setenv("DENKEEPER_LLM_PROVIDER", "anthropic")
	t.Setenv("DENKEEPER_LLM_MODEL", "claude-opus")
	t.Setenv("DENKEEPER_LLM_ANTHROPIC_BASE_URL", "https://custom.api")
	t.Setenv("DENKEEPER_LLM_OLLAMA_BASE_URL", "http://ollama:11434")
	t.Setenv("DENKEEPER_LLM_OPENAI_API_KEY", "oai-llm-key")
	t.Setenv("DENKEEPER_LLM_OPENAI_BASE_URL", "https://custom.openai.example.com/v1")
	t.Setenv("DENKEEPER_LOG_LEVEL", "debug")
	t.Setenv("DENKEEPER_LOG_FORMAT", "json")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "/custom/db.sqlite")
	t.Setenv("DENKEEPER_API_ENABLED", "true")
	t.Setenv("DENKEEPER_API_LISTEN", ":9090")
	t.Setenv("DENKEEPER_SESSION_TIER", "autonomous")

	cfg := &Config{}
	applyEnvOverrides(cfg)

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Telegram.Token", cfg.Telegram.Token, "tg-token"},
		{"Discord.Token", cfg.Discord.Token, "dc-token"},
		{"LLM.OpenRouter.APIKey", cfg.LLM.OpenRouter.APIKey, "or-key"},
		{"LLM.Anthropic.APIKey", cfg.LLM.Anthropic.APIKey, "ant-key"},
		{"Voice.OpenAI.APIKey", cfg.Voice.OpenAI.APIKey, "oai-key"},
		{"LLM.DefaultProvider", cfg.LLM.DefaultProvider, "anthropic"},
		{"LLM.DefaultModel", cfg.LLM.DefaultModel, "claude-opus"},
		{"LLM.Anthropic.BaseURL", cfg.LLM.Anthropic.BaseURL, "https://custom.api"},
		{"LLM.Ollama.BaseURL", cfg.LLM.Ollama.BaseURL, "http://ollama:11434"},
		{"LLM.OpenAI.APIKey", cfg.LLM.OpenAI.APIKey, "oai-llm-key"},
		{"LLM.OpenAI.BaseURL", cfg.LLM.OpenAI.BaseURL, "https://custom.openai.example.com/v1"},
		{"Log.Level", cfg.Log.Level, "debug"},
		{"Log.Format", cfg.Log.Format, "json"},
		{"Memory.DBPath", cfg.Memory.DBPath, "/custom/db.sqlite"},
		{"API.Listen", cfg.API.Listen, ":9090"},
		{"Session.Tier", cfg.Session.Tier, "autonomous"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if cfg.API.Enabled == nil || !*cfg.API.Enabled {
		t.Error("API.Enabled should be true")
	}
}

func TestApplyEnvOverrides_FullPipeline(t *testing.T) {
	t.Setenv("DENKEEPER_TELEGRAM_TOKEN", "env-tg-token")
	t.Setenv("DENKEEPER_LLM_ANTHROPIC_API_KEY", "env-ant-key")

	tomlData := []byte(`
[telegram]
allowed_users = [111222333]

[llm]
default_provider = "anthropic"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "env-tg-token" {
		t.Errorf("telegram token = %q, want %q", cfg.Telegram.Token, "env-tg-token")
	}
	if cfg.LLM.Anthropic.APIKey != "env-ant-key" {
		t.Errorf("anthropic api_key = %q, want %q", cfg.LLM.Anthropic.APIKey, "env-ant-key")
	}
}

func TestExpandEnvVars_Tools(t *testing.T) {
	t.Setenv("TEST_TOOL_KEY", "secret-value")

	cfg := &Config{
		Tools: map[string]ToolConfig{
			"test": {
				Command: "test-cmd",
				Env:     map[string]string{"API_KEY": "$TEST_TOOL_KEY"},
			},
		},
	}
	expandEnvVars(cfg)

	if cfg.Tools["test"].Env["API_KEY"] != "secret-value" {
		t.Errorf("env var not expanded: got %q", cfg.Tools["test"].Env["API_KEY"])
	}
}

func TestExpandEnvVars_Plugins(t *testing.T) {
	t.Setenv("TEST_PLUGIN_KEY", "plugin-secret")

	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"test": {
				Type:    "subprocess",
				Command: "test-cmd",
				Env:     map[string]string{"TOKEN": "$TEST_PLUGIN_KEY"},
			},
		},
	}
	expandEnvVars(cfg)

	if cfg.Plugins["test"].Env["TOKEN"] != "plugin-secret" {
		t.Errorf("env var not expanded: got %q", cfg.Plugins["test"].Env["TOKEN"])
	}
}

func TestParse_WebEnabledByDefault(t *testing.T) {
	// Web should be enabled even without an explicit [web] section.
	tomlData := []byte(baseConfig)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Web.Enabled == nil || !*cfg.Web.Enabled {
		t.Error("web.enabled should default to true")
	}
	if cfg.Web.Search.Provider != "duckduckgo" {
		t.Errorf("search provider = %q, want %q", cfg.Web.Search.Provider, "duckduckgo")
	}
	if cfg.Web.Search.MaxResults != 5 {
		t.Errorf("max_results = %d, want 5", cfg.Web.Search.MaxResults)
	}
	if cfg.Web.Fetch.Timeout != "30s" {
		t.Errorf("timeout = %q, want %q", cfg.Web.Fetch.Timeout, "30s")
	}
	if cfg.Web.Fetch.MaxSizeBytes != 5242880 {
		t.Errorf("max_size_bytes = %d, want 5242880", cfg.Web.Fetch.MaxSizeBytes)
	}
	if cfg.Web.Fetch.UserAgent != "Denkeeper/1.0 (+https://denkeeper.io)" {
		t.Errorf("user_agent = %q, want default", cfg.Web.Fetch.UserAgent)
	}
}

func TestParse_WebTavilyRequiresAPIKey(t *testing.T) {
	tomlData := []byte(baseConfig + `
[web]
enabled = true

[web.search]
provider = "tavily"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for tavily without api_key")
	}
	if !strings.Contains(err.Error(), "api_key is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_WebTavilyWithAPIKey(t *testing.T) {
	tomlData := []byte(baseConfig + `
[web]
enabled = true

[web.search]
provider = "tavily"
api_key = "tvly-test-key"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Web.Search.Provider != "tavily" {
		t.Errorf("provider = %q, want %q", cfg.Web.Search.Provider, "tavily")
	}
	if cfg.Web.Search.APIKey != "tvly-test-key" {
		t.Errorf("api_key = %q, want %q", cfg.Web.Search.APIKey, "tvly-test-key")
	}
}

func TestParse_WebInvalidProvider(t *testing.T) {
	tomlData := []byte(baseConfig + `
[web]
enabled = true

[web.search]
provider = "google"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for unsupported search provider")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_WebDisabledSkipsValidation(t *testing.T) {
	tomlData := []byte(baseConfig + `
[web]
enabled = false

[web.search]
provider = "invalid-provider"
`)
	_, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("expected no error when web disabled, got: %v", err)
	}
}

func TestParse_WebSearchAPIKeyEnvOverride(t *testing.T) {
	t.Setenv("DENKEEPER_SEARCH_API_KEY", "env-search-key")

	tomlData := []byte(baseConfig + `
[web]
enabled = true

[web.search]
provider = "tavily"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Web.Search.APIKey != "env-search-key" {
		t.Errorf("api_key = %q, want %q", cfg.Web.Search.APIKey, "env-search-key")
	}
}

func TestParse_WebFetchConfig(t *testing.T) {
	tomlData := []byte(baseConfig + `
[web]
enabled = true

[web.fetch]
timeout = "15s"
max_size_bytes = 1048576
user_agent = "CustomBot/2.0"
respect_robots_txt = true
respect_agents_txt = true

[web.fetch.jina]
enabled = true
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Web.Fetch.Timeout != "15s" {
		t.Errorf("timeout = %q, want %q", cfg.Web.Fetch.Timeout, "15s")
	}
	if cfg.Web.Fetch.MaxSizeBytes != 1048576 {
		t.Errorf("max_size_bytes = %d, want 1048576", cfg.Web.Fetch.MaxSizeBytes)
	}
	if cfg.Web.Fetch.UserAgent != "CustomBot/2.0" {
		t.Errorf("user_agent = %q, want %q", cfg.Web.Fetch.UserAgent, "CustomBot/2.0")
	}
	if !cfg.Web.Fetch.RespectRobotsTxt {
		t.Error("respect_robots_txt should be true")
	}
	if !cfg.Web.Fetch.RespectAgentsTxt {
		t.Error("respect_agents_txt should be true")
	}
	if !cfg.Web.Fetch.Jina.Enabled {
		t.Error("jina.enabled should be true")
	}
}

func TestParse_BrowserDefaults(t *testing.T) {
	tomlData := []byte(baseConfig)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Browser.Enabled {
		t.Error("browser should be disabled by default")
	}
	if cfg.Browser.Image != "ghcr.io/temikus/denkeeper-browser:latest" {
		t.Errorf("browser image = %q, want default", cfg.Browser.Image)
	}
	if cfg.Browser.MemoryLimit != "512m" {
		t.Errorf("memory_limit = %q, want %q", cfg.Browser.MemoryLimit, "512m")
	}
	if cfg.Browser.CPULimit != "1" {
		t.Errorf("cpu_limit = %q, want %q", cfg.Browser.CPULimit, "1")
	}
	if cfg.Browser.ProfileDir != "data/browser-profiles" {
		t.Errorf("profile_dir = %q, want %q", cfg.Browser.ProfileDir, "data/browser-profiles")
	}
	if cfg.Browser.SessionTTL != "10m" {
		t.Errorf("session_ttl = %q, want %q", cfg.Browser.SessionTTL, "10m")
	}
	if cfg.Browser.MaxPages != 5 {
		t.Errorf("max_pages = %d, want 5", cfg.Browser.MaxPages)
	}
	if len(cfg.Browser.URLAllowlist.Domains) != 0 {
		t.Errorf("url_allowlist.domains should be empty by default, got %v", cfg.Browser.URLAllowlist.Domains)
	}
}

func TestParse_BrowserCustomConfig(t *testing.T) {
	tomlData := []byte(baseConfig + `
[browser]
enabled = true
image = "custom-registry/browser:v2"
memory_limit = "1g"
cpu_limit = "2"
profile_dir = "/custom/profiles"
session_ttl = "30m"
max_pages = 10

[browser.url_allowlist]
domains = ["github.com", "*.example.com"]
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Browser.Enabled {
		t.Error("browser should be enabled")
	}
	if cfg.Browser.Image != "custom-registry/browser:v2" {
		t.Errorf("image = %q, want custom", cfg.Browser.Image)
	}
	if cfg.Browser.MemoryLimit != "1g" {
		t.Errorf("memory_limit = %q, want %q", cfg.Browser.MemoryLimit, "1g")
	}
	if cfg.Browser.CPULimit != "2" {
		t.Errorf("cpu_limit = %q, want %q", cfg.Browser.CPULimit, "2")
	}
	if cfg.Browser.ProfileDir != "/custom/profiles" {
		t.Errorf("profile_dir = %q, want /custom/profiles", cfg.Browser.ProfileDir)
	}
	if cfg.Browser.SessionTTL != "30m" {
		t.Errorf("session_ttl = %q, want 30m", cfg.Browser.SessionTTL)
	}
	if cfg.Browser.MaxPages != 10 {
		t.Errorf("max_pages = %d, want 10", cfg.Browser.MaxPages)
	}
	if len(cfg.Browser.URLAllowlist.Domains) != 2 {
		t.Fatalf("url_allowlist.domains length = %d, want 2", len(cfg.Browser.URLAllowlist.Domains))
	}
	if cfg.Browser.URLAllowlist.Domains[0] != "github.com" {
		t.Errorf("domains[0] = %q, want github.com", cfg.Browser.URLAllowlist.Domains[0])
	}
}

func TestParse_AgentBrowserURLAllowlist(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/tmp/default"
adapters = ["telegram:111"]

[[agents]]
name = "work"
persona_dir = "/tmp/work"
adapters = ["telegram:222"]
browser_url_allowlist = ["github.com", "*.atlassian.net"]
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var workAgent *AgentInstanceConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "work" {
			workAgent = &cfg.Agents[i]
			break
		}
	}
	if workAgent == nil {
		t.Fatal("work agent not found")
	}
	if len(workAgent.BrowserURLAllowlist) != 2 {
		t.Fatalf("browser_url_allowlist length = %d, want 2", len(workAgent.BrowserURLAllowlist))
	}
	if workAgent.BrowserURLAllowlist[0] != "github.com" {
		t.Errorf("browser_url_allowlist[0] = %q, want github.com", workAgent.BrowserURLAllowlist[0])
	}
}

func TestParse_OpenAIProvider_Valid(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "openai"

[llm.openai]
api_key = "sk-test-key"
base_url = "https://custom.openai.example.com/v1"
organization = "org-test123"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error for openai provider config: %v", err)
	}
	if cfg.LLM.DefaultProvider != "openai" {
		t.Errorf("default_provider = %q, want openai", cfg.LLM.DefaultProvider)
	}
	if cfg.LLM.OpenAI.APIKey != "sk-test-key" {
		t.Errorf("openai api_key = %q, want sk-test-key", cfg.LLM.OpenAI.APIKey)
	}
	if cfg.LLM.OpenAI.BaseURL != "https://custom.openai.example.com/v1" {
		t.Errorf("openai base_url = %q, want https://custom.openai.example.com/v1", cfg.LLM.OpenAI.BaseURL)
	}
	if cfg.LLM.OpenAI.Organization != "org-test123" {
		t.Errorf("openai organization = %q, want org-test123", cfg.LLM.OpenAI.Organization)
	}
}

func TestParse_OpenAIProvider_MissingAPIKey(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "openai"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for openai provider without api_key")
	}
	if !strings.Contains(err.Error(), "requires an api_key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_OpenAIProvider_NoOtherKeysRequired(t *testing.T) {
	// When using openai provider, openrouter.api_key and anthropic.api_key should NOT be required.
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "openai"

[llm.openai]
api_key = "sk-test-key"
`)
	_, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("expected no error when other provider keys absent with openai provider: %v", err)
	}
}

func TestParse_OpenAIProvider_FallbackRequiresKey(t *testing.T) {
	// When openai appears as a fallback provider, its API key is required.
	tomlData := []byte(`
[telegram]
token = "tg-token"
allowed_users = [111222333]

[llm]
default_provider = "ollama"

[[llm.fallback]]
trigger = "error"
action = "switch_provider"
provider = "openai"
model = "gpt-4o"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when openai is fallback provider without api_key")
	}
	if !strings.Contains(err.Error(), "requires an api_key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyEnvOverrides_OpenAI(t *testing.T) {
	t.Setenv("DENKEEPER_LLM_OPENAI_API_KEY", "env-oai-key")
	t.Setenv("DENKEEPER_LLM_OPENAI_BASE_URL", "https://env.openai.example.com/v1")

	cfg := &Config{}
	applyEnvOverrides(cfg)

	if cfg.LLM.OpenAI.APIKey != "env-oai-key" {
		t.Errorf("LLM.OpenAI.APIKey = %q, want env-oai-key", cfg.LLM.OpenAI.APIKey)
	}
	if cfg.LLM.OpenAI.BaseURL != "https://env.openai.example.com/v1" {
		t.Errorf("LLM.OpenAI.BaseURL = %q, want https://env.openai.example.com/v1", cfg.LLM.OpenAI.BaseURL)
	}
}

// --------------------------------------------------------------------------
// Tests: Auth config validation
// --------------------------------------------------------------------------

func TestValidateAuth_PasswordRequiresSessionSecret(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[api.auth]
password_hash = "$2a$13$somehashvaluehere"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when password_hash set without session_secret")
	}
	if !strings.Contains(err.Error(), "session_secret is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAuth_OIDCRequiresSessionSecret(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[api.auth.oidc]
enabled = true
issuer = "https://accounts.google.com"
client_id = "my-client-id"
client_secret = "my-client-secret"
redirect_url = "https://example.com/auth/callback"
allowed_emails = ["user@example.com"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when OIDC enabled without session_secret")
	}
	if !strings.Contains(err.Error(), "session_secret is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAuth_InvalidPasswordHashPrefix(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[api.auth]
password_hash = "plaintext-not-bcrypt"
session_secret = "test-fake-session-secret-not-real-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for password_hash without bcrypt prefix")
	}
	if !strings.Contains(err.Error(), "must be a bcrypt hash") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAuth_OIDCMissingIssuer(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[api.auth]
session_secret = "test-fake-session-secret-not-real-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

[api.auth.oidc]
enabled = true
client_id = "my-client-id"
client_secret = "my-client-secret"
redirect_url = "https://example.com/auth/callback"
allowed_emails = ["user@example.com"]
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when OIDC enabled without issuer")
	}
	if !strings.Contains(err.Error(), "issuer is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAuth_OIDCEmptyAllowedEmails(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[api.auth]
session_secret = "test-fake-session-secret-not-real-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

[api.auth.oidc]
enabled = true
issuer = "https://accounts.google.com"
client_id = "my-client-id"
client_secret = "my-client-secret"
redirect_url = "https://example.com/auth/callback"
allowed_emails = []
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error when OIDC enabled with empty allowed_emails")
	}
	if !strings.Contains(err.Error(), "allowed_emails must not be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAuth_ValidPasswordOnly(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[api.auth]
password_hash = "$2a$13$somehashvalueherethatis.validlookingbcrypthashstring"
session_secret = "test-fake-session-secret-not-real-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
`)

	_, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error for valid password-only auth config: %v", err)
	}
}

func TestValidateAuth_InvalidSessionMaxAge(t *testing.T) {
	tomlData := []byte(baseConfig + `
[api]
enabled = true

[api.auth]
password_hash = "$2b$13$somehashvalueherethatis.validlookingbcrypthashstring"
session_secret = "test-fake-session-secret-not-real-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
session_max_age = "not-a-duration"
`)

	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for invalid session_max_age duration")
	}
	if !strings.Contains(err.Error(), "session_max_age") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// Tests: OTel config
// --------------------------------------------------------------------------

func TestValidateOTel_DefaultServiceName(t *testing.T) {
	tomlData := []byte(baseConfig + `
[otel]
enabled = true
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OTel.ServiceName != "denkeeper" {
		t.Errorf("OTel.ServiceName = %q, want %q", cfg.OTel.ServiceName, "denkeeper")
	}
}

// ---------------------------------------------------------------------------
// MCP Config & Transport Tests
// ---------------------------------------------------------------------------

func TestParse_MCPDefaults(t *testing.T) {
	tomlData := []byte(baseConfig)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MCP.RequestTimeoutSecs != 30 {
		t.Errorf("MCP.RequestTimeoutSecs = %d, want 30", cfg.MCP.RequestTimeoutSecs)
	}
	if cfg.MCP.SSEKeepAliveSecs != 15 {
		t.Errorf("MCP.SSEKeepAliveSecs = %d, want 15", cfg.MCP.SSEKeepAliveSecs)
	}
	if cfg.MCP.AutoRestart == nil || !*cfg.MCP.AutoRestart {
		t.Error("MCP.AutoRestart should default to true")
	}
	if cfg.MCP.MaxRestartAttempts != 3 {
		t.Errorf("MCP.MaxRestartAttempts = %d, want 3", cfg.MCP.MaxRestartAttempts)
	}
	if cfg.MCP.RestartCooldown != "5m" {
		t.Errorf("MCP.RestartCooldown = %q, want 5m", cfg.MCP.RestartCooldown)
	}
}

func TestParse_MCPConfigExplicit(t *testing.T) {
	tomlData := []byte(baseConfig + `
[mcp]
request_timeout_secs = 60
auto_restart = false
max_restart_attempts = 5
restart_cooldown = "10m"
url_allowlist = ["api.example.com", "*.internal.corp"]
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MCP.RequestTimeoutSecs != 60 {
		t.Errorf("MCP.RequestTimeoutSecs = %d, want 60", cfg.MCP.RequestTimeoutSecs)
	}
	if cfg.MCP.AutoRestart == nil || *cfg.MCP.AutoRestart {
		t.Error("MCP.AutoRestart should be false")
	}
	if cfg.MCP.MaxRestartAttempts != 5 {
		t.Errorf("MCP.MaxRestartAttempts = %d, want 5", cfg.MCP.MaxRestartAttempts)
	}
	if len(cfg.MCP.URLAllowlist) != 2 {
		t.Errorf("MCP.URLAllowlist len = %d, want 2", len(cfg.MCP.URLAllowlist))
	}
}

func TestParse_MCPSSEKeepAliveExplicit(t *testing.T) {
	tomlData := []byte(baseConfig + `
[mcp]
sse_keep_alive_secs = 30
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MCP.SSEKeepAliveSecs != 30 {
		t.Errorf("MCP.SSEKeepAliveSecs = %d, want 30", cfg.MCP.SSEKeepAliveSecs)
	}
}

func TestParse_MCPSSEKeepAliveNegativeRejected(t *testing.T) {
	tomlData := []byte(baseConfig + `
[mcp]
sse_keep_alive_secs = -1
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for negative sse_keep_alive_secs")
	}
	if !strings.Contains(err.Error(), "sse_keep_alive_secs") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_MCPInvalidCooldown(t *testing.T) {
	tomlData := []byte(baseConfig + `
[mcp]
restart_cooldown = "not-a-duration"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for invalid restart_cooldown")
	}
	if !strings.Contains(err.Error(), "restart_cooldown") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolSSETransport(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.remote-mcp]
transport = "sse"
url = "https://mcp.example.com/events"
env = { MCP_TOKEN = "test" }
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := cfg.Tools["remote-mcp"]
	if tc.Transport != "sse" {
		t.Errorf("Transport = %q, want sse", tc.Transport)
	}
	if tc.URL != "https://mcp.example.com/events" {
		t.Errorf("URL = %q, want https://mcp.example.com/events", tc.URL)
	}
}

func TestParse_ToolSSEHeaders(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.remote]
transport = "sse"
url = "https://mcp.example.com"
request_timeout_secs = 45
sse_keep_alive_secs = 20

[tools.remote.headers]
Authorization = "Bearer test-token"
X-Custom = "value"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := cfg.Tools["remote"]
	if tc.Headers["Authorization"] != "Bearer test-token" {
		t.Errorf("Authorization header = %q", tc.Headers["Authorization"])
	}
	if tc.RequestTimeoutSecs != 45 {
		t.Errorf("RequestTimeoutSecs = %d, want 45", tc.RequestTimeoutSecs)
	}
	if tc.SSEKeepAliveSecs != 20 {
		t.Errorf("SSEKeepAliveSecs = %d, want 20", tc.SSEKeepAliveSecs)
	}
}

func TestParse_ToolSSEMissingURL(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-sse]
transport = "sse"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for SSE tool missing URL")
	}
	if !strings.Contains(err.Error(), "url is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolSSEWithCommand(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-sse]
transport = "sse"
url = "https://mcp.example.com"
command = "/usr/bin/should-not-be-here"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for SSE tool with command")
	}
	if !strings.Contains(err.Error(), "command must be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolStdioWithURL(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-stdio]
command = "/usr/bin/tool"
url = "https://should-not-be-here.com"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for stdio tool with URL")
	}
	if !strings.Contains(err.Error(), "url must be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolStdioWithHeaders(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-stdio]
command = "/usr/bin/tool"

[tools.bad-stdio.headers]
Authorization = "Bearer should-not-be-here"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for stdio tool with headers")
	}
	if !strings.Contains(err.Error(), "headers are not supported") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolUnknownTransport(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-transport]
transport = "grpc"
command = "/usr/bin/tool"
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
	if !strings.Contains(err.Error(), "unsupported transport") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolMissingCommand_StdioExplicit(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-tool]
transport = "stdio"
args = ["--flag"]
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for stdio tool missing command")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ToolSSEWithArgs(t *testing.T) {
	tomlData := []byte(baseConfig + `
[tools.bad-sse]
transport = "sse"
url = "https://mcp.example.com"
args = ["--flag"]
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for SSE tool with args")
	}
	if !strings.Contains(err.Error(), "args must be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_CostsConfig(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "test"
allowed_users = [1]

[llm.openrouter]
api_key = "sk-or-key"

[costs]
default_rate_per_1k_tokens = 0.02

[costs.model_prices.claude-opus-4]
input = 15.0
output = 75.0
cached_input = 1.5

[costs.model_prices.gpt-4o]
input = 2.50
output = 10.0
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Costs.DefaultRatePerKTokens != 0.02 {
		t.Errorf("default_rate = %f, want 0.02", cfg.Costs.DefaultRatePerKTokens)
	}
	if len(cfg.Costs.ModelPrices) != 2 {
		t.Errorf("model_prices count = %d, want 2", len(cfg.Costs.ModelPrices))
	}
	opus := cfg.Costs.ModelPrices["claude-opus-4"]
	if opus.InputPerMTok != 15.0 {
		t.Errorf("claude-opus-4 input = %f, want 15.0", opus.InputPerMTok)
	}
	if opus.CachedInputPerMTok != 1.5 {
		t.Errorf("claude-opus-4 cached_input = %f, want 1.5", opus.CachedInputPerMTok)
	}
}

func TestParse_CostsDefaults(t *testing.T) {
	tomlData := []byte(`
[telegram]
token = "test"
allowed_users = [1]

[llm.openrouter]
api_key = "sk-or-key"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No [costs] section — should default to zero (use registry only).
	if cfg.Costs.DefaultRatePerKTokens != 0 {
		t.Errorf("default_rate = %f, want 0", cfg.Costs.DefaultRatePerKTokens)
	}
	if cfg.Costs.ModelPrices != nil {
		t.Errorf("model_prices = %v, want nil", cfg.Costs.ModelPrices)
	}
}

func TestParse_DataDirEnvVar(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "/data")

	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q, want /data", cfg.DataDir)
	}
	if cfg.Memory.DBPath != "/data/data/memory.db" {
		t.Errorf("DBPath = %q, want /data/data/memory.db", cfg.Memory.DBPath)
	}
	if cfg.Agent.PersonaDir != "/data/agents/default" {
		t.Errorf("PersonaDir = %q, want /data/agents/default", cfg.Agent.PersonaDir)
	}
	if cfg.Agent.SkillsDir != "/data/skills" {
		t.Errorf("SkillsDir = %q, want /data/skills", cfg.Agent.SkillsDir)
	}
}

func TestParse_DataDirToml(t *testing.T) {
	tomlData := []byte(`
data_dir = "/custom/path"

[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DataDir != "/custom/path" {
		t.Errorf("DataDir = %q, want /custom/path", cfg.DataDir)
	}
	if cfg.Memory.DBPath != "/custom/path/data/memory.db" {
		t.Errorf("DBPath = %q, want /custom/path/data/memory.db", cfg.Memory.DBPath)
	}
}

func TestParse_DataDirEnvOverridesToml(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "/env/path")

	tomlData := []byte(`
data_dir = "/toml/path"

[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DataDir != "/env/path" {
		t.Errorf("DataDir = %q, want /env/path (env should override TOML)", cfg.DataDir)
	}
}

func TestParse_DataDirMultiAgent(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "/data")

	tomlData := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"

[[agents]]
name = "default"
adapters = ["telegram"]

[[agents]]
name = "helper"
adapters = ["telegram:99999"]
`)

	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agents[1].PersonaDir != "/data/agents/helper" {
		t.Errorf("agent persona_dir = %q, want /data/agents/helper", cfg.Agents[1].PersonaDir)
	}
}

func TestMemoryConfig_Defaults(t *testing.T) {
	toml := []byte(`
[llm]
default_provider = "openrouter"
[llm.openrouter]
api_key = "sk-test"
`)
	cfg, err := Parse(toml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Memory.RetentionDays != 90 {
		t.Errorf("retention_days = %d, want 90", cfg.Memory.RetentionDays)
	}
	if cfg.Memory.MaxConversations != 10000 {
		t.Errorf("max_conversations = %d, want 10000", cfg.Memory.MaxConversations)
	}
	if cfg.Memory.CleanupInterval != "1h" {
		t.Errorf("cleanup_interval = %q, want 1h", cfg.Memory.CleanupInterval)
	}
}

func TestMemoryConfig_Validation(t *testing.T) {
	toml := []byte(`
[llm]
default_provider = "openrouter"
[llm.openrouter]
api_key = "sk-test"
[memory]
cleanup_interval = "not-a-duration"
`)
	_, err := Parse(toml)
	if err == nil {
		t.Fatal("expected validation error for bad cleanup_interval")
	}
	if !strings.Contains(err.Error(), "cleanup_interval") {
		t.Errorf("error should mention cleanup_interval: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Channel config tests
// ---------------------------------------------------------------------------

func TestParse_Channels_Explicit(t *testing.T) {
	toml := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111]
[llm.openrouter]
api_key = "sk-test"
[[agents]]
name = "default"
adapters = ["telegram"]
[[agents]]
name = "work"
adapters = []
[[channels]]
name = "personal"
agent = "default"
adapters = ["telegram"]
[[channels]]
name = "work"
agent = "work"
adapters = ["telegram:999"]
`)
	cfg, err := Parse(toml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(cfg.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(cfg.Channels))
	}
	if cfg.Channels[0].Name != "personal" {
		t.Errorf("channel[0].Name = %q", cfg.Channels[0].Name)
	}
	if cfg.Channels[1].Agent != "work" {
		t.Errorf("channel[1].Agent = %q", cfg.Channels[1].Agent)
	}
	// Explicit channels must have Implicit=false.
	if cfg.Channels[0].Implicit {
		t.Error("channel[0].Implicit = true, want false for explicit channel")
	}
	if cfg.Channels[1].Implicit {
		t.Error("channel[1].Implicit = true, want false for explicit channel")
	}
}

func TestParse_Channels_SynthesizedFromAgents(t *testing.T) {
	toml := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111]
[llm.openrouter]
api_key = "sk-test"
[[agents]]
name = "default"
adapters = ["telegram"]
[[agents]]
name = "work"
adapters = ["telegram:999"]
`)
	cfg, err := Parse(toml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// No explicit [[channels]], so they should be auto-synthesized.
	if len(cfg.Channels) != 2 {
		t.Fatalf("expected 2 synthesized channels, got %d", len(cfg.Channels))
	}
	// Synthesized names follow "agent:pattern" format.
	if cfg.Channels[0].Name != "default:telegram" {
		t.Errorf("channel[0].Name = %q, want default:telegram", cfg.Channels[0].Name)
	}
	if cfg.Channels[1].Name != "work:telegram:999" {
		t.Errorf("channel[1].Name = %q, want work:telegram:999", cfg.Channels[1].Name)
	}
	// Synthesized channels must have Implicit=true.
	if !cfg.Channels[0].Implicit {
		t.Error("channel[0].Implicit = false, want true for synthesized channel")
	}
	if !cfg.Channels[1].Implicit {
		t.Error("channel[1].Implicit = false, want true for synthesized channel")
	}
}

func TestParse_Channels_DuplicateName(t *testing.T) {
	toml := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111]
[llm.openrouter]
api_key = "sk-test"
[[agents]]
name = "default"
adapters = []
[[channels]]
name = "work"
agent = "default"
[[channels]]
name = "work"
agent = "default"
`)
	_, err := Parse(toml)
	if err == nil {
		t.Fatal("expected error for duplicate channel name")
	}
	if !strings.Contains(err.Error(), "duplicate channel name") {
		t.Errorf("error = %v", err)
	}
}

func TestParse_Channels_UnknownAgent(t *testing.T) {
	toml := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111]
[llm.openrouter]
api_key = "sk-test"
[[agents]]
name = "default"
adapters = []
[[channels]]
name = "broken"
agent = "nonexistent"
`)
	_, err := Parse(toml)
	if err == nil {
		t.Fatal("expected error for channel referencing unknown agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v", err)
	}
}

func TestParse_Channels_ConflictingWildcard(t *testing.T) {
	toml := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111]
[llm.openrouter]
api_key = "sk-test"
[[agents]]
name = "default"
adapters = []
[[channels]]
name = "a"
agent = "default"
adapters = ["telegram"]
[[channels]]
name = "b"
agent = "default"
adapters = ["telegram"]
`)
	_, err := Parse(toml)
	if err == nil {
		t.Fatal("expected error for conflicting wildcard binding")
	}
	if !strings.Contains(err.Error(), "conflicts with channel") {
		t.Errorf("error = %v", err)
	}
}

func TestParse_Channels_EmptyAdapters(t *testing.T) {
	// Channels with empty adapters are valid — reachable only via /session.
	toml := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111]
[llm.openrouter]
api_key = "sk-test"
[[agents]]
name = "default"
adapters = ["telegram"]
[[channels]]
name = "research"
agent = "default"
adapters = []
`)
	cfg, err := Parse(toml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// 2 channels: 1 explicit (research, empty adapters) + 1 synthesized
	// for the uncovered agent telegram binding.
	if len(cfg.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d: %+v", len(cfg.Channels), cfg.Channels)
	}
	if cfg.Channels[0].Name != "research" {
		t.Errorf("channel[0].Name = %q, want research", cfg.Channels[0].Name)
	}
	if cfg.Channels[1].Name != "default:telegram" || !cfg.Channels[1].Implicit {
		t.Errorf("channel[1] = %q implicit=%v, want default:telegram/true", cfg.Channels[1].Name, cfg.Channels[1].Implicit)
	}
}

func TestParse_Channels_PartialExplicit_SynthesizesRemainder(t *testing.T) {
	// When explicit [[channels]] exist but don't cover all agent adapter
	// bindings, the uncovered bindings should be auto-synthesized as
	// implicit channels so they aren't silently dropped.
	toml := []byte(`
[telegram]
token = "123456:ABC-DEF"
allowed_users = [111]
[discord]
token = "discord-test"
allowed_users = ["user-1"]
[llm.openrouter]
api_key = "sk-test"
[[agents]]
name = "default"
adapters = ["telegram", "discord"]
[[channels]]
name = "personal"
agent = "default"
adapters = ["telegram"]
`)
	cfg, err := Parse(toml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Should have 2 channels: 1 explicit + 1 synthesized for uncovered discord.
	if len(cfg.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d: %+v", len(cfg.Channels), cfg.Channels)
	}

	// First should be the explicit channel.
	if cfg.Channels[0].Name != "personal" || cfg.Channels[0].Implicit {
		t.Errorf("channel[0] = %q implicit=%v, want personal/false", cfg.Channels[0].Name, cfg.Channels[0].Implicit)
	}

	// Second should be the synthesized channel for the uncovered discord binding.
	if cfg.Channels[1].Name != "default:discord" || !cfg.Channels[1].Implicit {
		t.Errorf("channel[1] = %q implicit=%v, want default:discord/true", cfg.Channels[1].Name, cfg.Channels[1].Implicit)
	}
}
