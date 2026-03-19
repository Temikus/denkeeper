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
	if !strings.HasSuffix(cfg.Agent.PersonaDir, filepath.Join(".foxbox", "agents", "default")) {
		t.Errorf("Agent.PersonaDir = %q, want suffix .foxbox/agents/default", cfg.Agent.PersonaDir)
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
