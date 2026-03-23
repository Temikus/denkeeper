package config

import (
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

func TestParse_APIDisabledByDefault(t *testing.T) {
	cfg, err := Parse([]byte(baseConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.Enabled {
		t.Error("API should be disabled by default")
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
	if !cfg.API.Enabled {
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
