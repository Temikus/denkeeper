package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAddToolToConfig_NewSection(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test-token"
`)

	if err := addToolToConfig(path, "web-search", config.ToolConfig{Command: "/usr/bin/ws", Args: []string{"--flag"}, Env: map[string]string{"API_KEY": "secret"}}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "web-search") {
		t.Error("config should contain tool name 'web-search'")
	}
	if !strings.Contains(content, "/usr/bin/ws") {
		t.Error("config should contain command")
	}
}

func TestAddToolToConfig_ExistingSection(t *testing.T) {
	path := writeTestConfig(t, `[tools]
[tools.existing]
command = "/usr/bin/existing"
`)

	if err := addToolToConfig(path, "new-tool", config.ToolConfig{Command: "/usr/bin/new"}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "existing") {
		t.Error("existing tool should be preserved")
	}
	if !strings.Contains(content, "new-tool") {
		t.Error("new tool should be added")
	}
}

func TestRemoveToolFromConfig(t *testing.T) {
	path := writeTestConfig(t, `[tools]
[tools.keep-me]
command = "/usr/bin/keep"
[tools.remove-me]
command = "/usr/bin/remove"
`)

	if err := removeToolFromConfig(path, "remove-me"); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "remove-me") {
		t.Error("removed tool should not be in config")
	}
	if !strings.Contains(content, "keep-me") {
		t.Error("other tool should still be in config")
	}
}

func TestRemoveToolFromConfig_NoSection(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test"
`)
	// Should not error when there's no tools section.
	if err := removeToolFromConfig(path, "nope"); err != nil {
		t.Fatal(err)
	}
}

func TestAddPluginToConfig(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test"
`)

	pe := pluginEntry{
		Type:    "docker",
		Image:   "ghcr.io/org/plugin:v1",
		Network: "none",
	}
	if err := addPluginToConfig(path, "code-runner", pe); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "code-runner") {
		t.Error("plugin should be added")
	}
	if !strings.Contains(content, "docker") {
		t.Error("plugin type should be present")
	}
}

func TestRemovePluginFromConfig(t *testing.T) {
	path := writeTestConfig(t, `[plugins]
[plugins.runner]
type = "docker"
image = "test"
`)

	if err := removePluginFromConfig(path, "runner"); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "runner") {
		t.Error("plugin should be removed")
	}
}

// ---------------------------------------------------------------------------
// Schedule config persistence
// ---------------------------------------------------------------------------

func TestAddScheduleToConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := AddScheduleToConfig(path, "daily-report", "@daily", "greet", "telegram:123", "isolated", "", "default", nil, true)
	if err != nil {
		t.Fatalf("AddScheduleToConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "daily-report") {
		t.Errorf("config missing schedule name; content:\n%s", content)
	}
	if !strings.Contains(content, "@daily") {
		t.Errorf("config missing schedule expression; content:\n%s", content)
	}
}

func TestAddScheduleToConfig_WithTags(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := AddScheduleToConfig(path, "tagged", "@hourly", "", "telegram:1", "", "", "", []string{"tag1", "tag2"}, true)
	if err != nil {
		t.Fatalf("AddScheduleToConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "tag1") || !strings.Contains(content, "tag2") {
		t.Errorf("config missing tags; content:\n%s", content)
	}
}

func TestUpdateScheduleInConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddScheduleToConfig(path, "update-me", "@daily", "", "telegram:1", "", "", "", nil, true); err != nil {
		t.Fatalf("AddScheduleToConfig: %v", err)
	}

	err := UpdateScheduleInConfig(path, "update-me", "@hourly", "skill1", "telegram:1", "shared", "", "", nil, false)
	if err != nil {
		t.Fatalf("UpdateScheduleInConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "@hourly") {
		t.Errorf("config not updated to @hourly; content:\n%s", content)
	}
	if strings.Contains(content, "@daily") {
		t.Errorf("config still contains old @daily; content:\n%s", content)
	}
}

func TestUpdateScheduleInConfig_NotFound(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := UpdateScheduleInConfig(path, "nonexistent", "@daily", "", "telegram:1", "", "", "", nil, true)
	if err == nil {
		t.Fatal("expected error for nonexistent schedule, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestRemoveScheduleFromConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddScheduleToConfig(path, "keep", "@daily", "", "telegram:1", "", "", "", nil, true); err != nil {
		t.Fatalf("AddScheduleToConfig: %v", err)
	}
	if err := AddScheduleToConfig(path, "remove-me", "@hourly", "", "telegram:2", "", "", "", nil, true); err != nil {
		t.Fatalf("AddScheduleToConfig: %v", err)
	}

	if err := RemoveScheduleFromConfig(path, "remove-me"); err != nil {
		t.Fatalf("RemoveScheduleFromConfig: %v", err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "remove-me") {
		t.Errorf("config still contains removed schedule; content:\n%s", content)
	}
	if !strings.Contains(content, "keep") {
		t.Errorf("config missing kept schedule; content:\n%s", content)
	}
}

func TestRemoveScheduleFromConfig_LastEntry(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddScheduleToConfig(path, "only-one", "@daily", "", "telegram:1", "", "", "", nil, true); err != nil {
		t.Fatalf("AddScheduleToConfig: %v", err)
	}

	if err := RemoveScheduleFromConfig(path, "only-one"); err != nil {
		t.Fatalf("RemoveScheduleFromConfig: %v", err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "schedules") {
		t.Errorf("config should not contain schedules key after removing last entry; content:\n%s", content)
	}
}

func TestUpdateAgentInConfig(t *testing.T) {
	path := writeTestConfig(t, `[api]
enabled = true

[[agents]]
name = "default"
session_tier = "supervised"
llm_model = "old-model"
description = "Original"
`)

	changes := map[string]any{
		"session_tier": "autonomous",
		"llm_model":    "new-model-v2",
		"description":  "Updated description",
	}
	if err := UpdateAgentInConfig(path, "default", changes); err != nil {
		t.Fatalf("UpdateAgentInConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "autonomous") {
		t.Errorf("config missing updated session_tier; content:\n%s", content)
	}
	if !strings.Contains(content, "new-model-v2") {
		t.Errorf("config missing updated llm_model; content:\n%s", content)
	}
	if !strings.Contains(content, "Updated description") {
		t.Errorf("config missing updated description; content:\n%s", content)
	}
	// Existing keys should be preserved.
	if !strings.Contains(content, "enabled = true") {
		t.Errorf("existing [api] config lost; content:\n%s", content)
	}
}

func TestUpdateAgentInConfig_NotFound(t *testing.T) {
	path := writeTestConfig(t, `[[agents]]
name = "default"
session_tier = "supervised"
`)

	err := UpdateAgentInConfig(path, "nonexistent", map[string]any{"session_tier": "autonomous"})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestUpdateAgentInConfig_SynthesizedAgent(t *testing.T) {
	// No [[agents]] section — the "default" agent is synthesized at runtime.
	// UpdateAgentInConfig should create the entry with legacy fields so a
	// reload doesn't lose adapters or session tier.
	path := writeTestConfig(t, `[api]
enabled = true

[telegram]
token = "keep-me"

[session]
tier = "supervised"

[agent]
persona_dir = "/data/agents/default"
`)

	changes := map[string]any{
		"llm_model": "claude-4-sonnet",
	}
	if err := UpdateAgentInConfig(path, "default", changes); err != nil {
		t.Fatalf("UpdateAgentInConfig on synthesized agent: %v", err)
	}

	content := readConfig(t, path)
	// Changed field must be present.
	if !strings.Contains(content, "claude-4-sonnet") {
		t.Errorf("config missing llm_model; content:\n%s", content)
	}
	// Legacy fields must be carried into the new [[agents]] entry.
	if !strings.Contains(content, `name = "default"`) && !strings.Contains(content, "name = 'default'") {
		t.Errorf("config missing agent name; content:\n%s", content)
	}
	if !strings.Contains(content, "supervised") {
		t.Errorf("config missing session_tier from [session]; content:\n%s", content)
	}
	if !strings.Contains(content, "telegram") {
		t.Errorf("config missing telegram adapter; content:\n%s", content)
	}
	if !strings.Contains(content, "/data/agents/default") {
		t.Errorf("config missing persona_dir from [agent]; content:\n%s", content)
	}
	// Existing config should be preserved.
	if !strings.Contains(content, "keep-me") {
		t.Errorf("telegram token was lost; content:\n%s", content)
	}
}

func TestUpdateAgentInConfig_SynthesizedAgentReload(t *testing.T) {
	// Verify the created [[agents]] entry round-trips through config.Parse
	// and produces the same agent that synthesizeDefaultAgent would have.
	path := writeTestConfig(t, `[telegram]
token = "tok"
allowed_users = [12345]

[discord]
token = "disc-tok"
allowed_users = ["67890"]

[session]
tier = "autonomous"

[agent]
persona_dir = "/custom/persona"
skills_dir = "/custom/skills"

[llm]
default_provider = "anthropic"
default_model = "claude-3-opus"

[llm.anthropic]
api_key = "sk-test"
`)

	if err := UpdateAgentInConfig(path, "default", map[string]any{"description": "updated"}); err != nil {
		t.Fatalf("UpdateAgentInConfig: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
	a := cfg.Agents[0]
	if a.Name != "default" {
		t.Errorf("name = %q, want default", a.Name)
	}
	if a.Description != "updated" {
		t.Errorf("description = %q, want updated", a.Description)
	}
	if a.SessionTier != "autonomous" {
		t.Errorf("session_tier = %q, want autonomous", a.SessionTier)
	}
	if a.PersonaDir != "/custom/persona" {
		t.Errorf("persona_dir = %q, want /custom/persona", a.PersonaDir)
	}
	// Both telegram and discord should be inferred.
	if len(a.Adapters) != 2 {
		t.Errorf("adapters = %v, want [telegram discord]", a.Adapters)
	}
}

func TestUpdateAgentInConfig_PartialUpdate(t *testing.T) {
	path := writeTestConfig(t, `[[agents]]
name = "myagent"
session_tier = "supervised"
llm_model = "keep-this"
description = "keep-this-too"
`)

	// Only update description, other fields should be preserved.
	if err := UpdateAgentInConfig(path, "myagent", map[string]any{"description": "changed"}); err != nil {
		t.Fatalf("UpdateAgentInConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "changed") {
		t.Errorf("config missing updated description; content:\n%s", content)
	}
	if !strings.Contains(content, "keep-this") {
		t.Errorf("llm_model was lost during partial update; content:\n%s", content)
	}
	if !strings.Contains(content, "supervised") {
		t.Errorf("session_tier was lost during partial update; content:\n%s", content)
	}
}

func TestRoundTrip_AddThenRemove(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "keep-me"
`)

	if err := addToolToConfig(path, "my-tool", config.ToolConfig{Command: "/bin/tool"}); err != nil {
		t.Fatal(err)
	}
	content := readConfig(t, path)
	if !strings.Contains(content, "my-tool") {
		t.Fatal("tool should exist after add")
	}

	if err := removeToolFromConfig(path, "my-tool"); err != nil {
		t.Fatal(err)
	}
	content = readConfig(t, path)
	if strings.Contains(content, "my-tool") {
		t.Error("tool should not exist after remove")
	}
	if !strings.Contains(content, "keep-me") {
		t.Error("existing config should be preserved")
	}
}

// ---------------------------------------------------------------------------
// SetAuthConfig
// ---------------------------------------------------------------------------

func TestAddToolToConfig_SSETransport(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test-token"
`)

	if err := addToolToConfig(path, "remote-mcp", config.ToolConfig{
		Transport:          "sse",
		URL:                "https://mcp.example.com/events",
		Headers:            map[string]string{"Authorization": "Bearer tok"},
		RequestTimeoutSecs: 45,
	}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "remote-mcp") {
		t.Error("config should contain tool name")
	}
	if !strings.Contains(content, "sse") {
		t.Error("config should contain transport")
	}
	if !strings.Contains(content, "https://mcp.example.com/events") {
		t.Error("config should contain URL")
	}
	if !strings.Contains(content, "Authorization") {
		t.Error("config should contain headers")
	}
	if !strings.Contains(content, "45") {
		t.Error("config should contain request_timeout_secs")
	}
}

func TestAddToolToConfig_SSEKeepAlivePersistedWhenSet(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test-token"
`)

	if err := addToolToConfig(path, "local-mcp", config.ToolConfig{
		Transport:        "sse",
		URL:              "http://localhost:8080/events",
		AllowLoopback:    true,
		SSEKeepAliveSecs: 30,
	}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "sse_keep_alive_secs") {
		t.Error("config should contain sse_keep_alive_secs when set")
	}
}

func TestAddToolToConfig_SSEKeepAliveOmittedWhenZero(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test-token"
`)

	if err := addToolToConfig(path, "remote-mcp", config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/events",
	}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "sse_keep_alive_secs") {
		t.Error("config should not contain sse_keep_alive_secs when zero")
	}
}

func TestAddToolToConfig_AllowLoopbackPersisted(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test-token"
`)

	if err := addToolToConfig(path, "local-mcp", config.ToolConfig{
		Transport:     "sse",
		URL:           "http://localhost:8080/events",
		AllowLoopback: true,
	}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "allow_loopback") {
		t.Error("config should contain allow_loopback when set to true")
	}
}

func TestAddToolToConfig_AllowLoopbackOmittedWhenFalse(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test-token"
`)

	if err := addToolToConfig(path, "remote-mcp", config.ToolConfig{
		Transport: "sse",
		URL:       "https://mcp.example.com/events",
	}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "allow_loopback") {
		t.Error("config should not contain allow_loopback when false")
	}
}

func TestAddToolToConfig_StdioOmitsTransport(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "test"
`)

	if err := addToolToConfig(path, "local-tool", config.ToolConfig{Command: "/usr/bin/tool"}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	// stdio is the default, so transport field should not appear
	if strings.Contains(content, "transport") {
		t.Error("stdio tool should not have transport field in config")
	}
	if !strings.Contains(content, "/usr/bin/tool") {
		t.Error("config should contain command")
	}
}

func TestSetSessionSecret_CreatesAuthSection(t *testing.T) {
	path := writeTestConfig(t, `[api]
enabled = true
`)

	secret := "aabbccdd00112233aabbccdd00112233aabbccdd00112233aabbccdd00112233"
	if err := SetSessionSecret(path, secret); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, secret) {
		t.Error("config should contain session_secret")
	}
}

func TestSetSessionSecret_PreservesExistingAuth(t *testing.T) {
	path := writeTestConfig(t, `[api.auth]
password_hash = "$2a$13$existinghash"
`)

	secret := "00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff"
	if err := SetSessionSecret(path, secret); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "$2a$13$existinghash") {
		t.Error("existing password_hash should be preserved")
	}
	if !strings.Contains(content, secret) {
		t.Error("config should contain session_secret")
	}
}

func TestSetAuthConfig_CreatesAuthSection(t *testing.T) {
	path := writeTestConfig(t, `[api]
enabled = true
`)

	if err := SetAuthConfig(path, "$2a$13$testhashvalue", "aabbccdd00112233aabbccdd00112233aabbccdd00112233aabbccdd00112233"); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "$2a$13$testhashvalue") {
		t.Error("config should contain password_hash")
	}
	if !strings.Contains(content, "aabbccdd00112233") {
		t.Error("config should contain session_secret")
	}
}

func TestSetAuthConfig_PreservesExistingConfig(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "my-bot-token"

[api]
enabled = true
listen = ":8080"
`)

	if err := SetAuthConfig(path, "$2b$13$newhash", "00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff"); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "my-bot-token") {
		t.Error("telegram config should be preserved")
	}
	if !strings.Contains(content, ":8080") {
		t.Error("api listen should be preserved")
	}
	if !strings.Contains(content, "$2b$13$newhash") {
		t.Error("config should contain new password_hash")
	}
}

func TestUpdateAgentInConfig_FallbackRoundTrip(t *testing.T) {
	path := writeTestConfig(t, `[api]
enabled = true

[llm]
default_provider = "ollama"
default_model = "llama3"

[[agents]]
name = "default"
session_tier = "supervised"
llm_model = "claude-3-opus"
`)

	// Write fallback rules via UpdateAgentInConfig.
	fallbacks := []any{
		map[string]any{
			"trigger":     "rate_limit",
			"action":      "wait_and_retry",
			"max_retries": 3,
			"backoff":     "exponential",
		},
		map[string]any{
			"trigger":  "error",
			"action":   "switch_provider",
			"provider": "ollama",
			"model":    "llama3",
		},
		map[string]any{
			"trigger":   "low_funds",
			"action":    "switch_model",
			"model":     "claude-haiku",
			"threshold": 2.5,
		},
	}
	if err := UpdateAgentInConfig(path, "default", map[string]any{"fallback": fallbacks}); err != nil {
		t.Fatalf("UpdateAgentInConfig: %v", err)
	}

	// Verify TOML contains the fallback data.
	content := readConfig(t, path)
	if !strings.Contains(content, "rate_limit") {
		t.Errorf("TOML missing rate_limit trigger; content:\n%s", content)
	}
	if !strings.Contains(content, "wait_and_retry") {
		t.Errorf("TOML missing wait_and_retry action; content:\n%s", content)
	}
	if !strings.Contains(content, "ollama") {
		t.Errorf("TOML missing ollama provider; content:\n%s", content)
	}

	// Round-trip: load the config and verify fallbacks are parsed correctly.
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(cfg.Agents))
	}
	fb := cfg.Agents[0].Fallbacks
	if len(fb) != 3 {
		t.Fatalf("fallbacks len = %d, want 3", len(fb))
	}

	// Rule 1: wait_and_retry
	if fb[0].Trigger != "rate_limit" || fb[0].Action != "wait_and_retry" || fb[0].MaxRetries != 3 || fb[0].Backoff != "exponential" {
		t.Errorf("fallback[0] = %+v, unexpected", fb[0])
	}
	// Rule 2: switch_provider
	if fb[1].Trigger != "error" || fb[1].Action != "switch_provider" || fb[1].Provider != "ollama" || fb[1].Model != "llama3" {
		t.Errorf("fallback[1] = %+v, unexpected", fb[1])
	}
	// Rule 3: switch_model with threshold
	if fb[2].Trigger != "low_funds" || fb[2].Action != "switch_model" || fb[2].Model != "claude-haiku" || fb[2].Threshold != 2.5 {
		t.Errorf("fallback[2] = %+v, unexpected", fb[2])
	}

	// Verify existing fields survived.
	if cfg.Agents[0].SessionTier != "supervised" {
		t.Errorf("session_tier lost during fallback update")
	}
	if cfg.Agents[0].LLMModel != "claude-3-opus" {
		t.Errorf("llm_model lost during fallback update")
	}
}

func TestUpdateAgentInConfig_FallbackEmptyClears(t *testing.T) {
	path := writeTestConfig(t, `[api]
enabled = true

[llm]
default_provider = "ollama"
default_model = "llama3"

[[agents]]
name = "default"

[[agents.fallback]]
trigger = "error"
action = "wait_and_retry"
max_retries = 1
`)

	// Write empty fallbacks to clear.
	if err := UpdateAgentInConfig(path, "default", map[string]any{"fallback": []any{}}); err != nil {
		t.Fatalf("UpdateAgentInConfig: %v", err)
	}

	// Round-trip: fallbacks should be empty.
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Agents[0].Fallbacks) != 0 {
		t.Errorf("fallbacks should be empty after clearing, got %d", len(cfg.Agents[0].Fallbacks))
	}
}

func TestUpdateAuthConfig_PreferredLogin(t *testing.T) {
	path := writeTestConfig(t, `[api.auth]
password_hash = "$2a$13$existing"
session_secret = "aabb"
`)

	if err := UpdateAuthConfig(path, map[string]any{"preferred_login_method": "password"}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "preferred_login_method") {
		t.Error("should contain preferred_login_method")
	}
	if !strings.Contains(content, "$2a$13$existing") {
		t.Error("should preserve password_hash")
	}
	if !strings.Contains(content, "aabb") {
		t.Error("should preserve session_secret")
	}
}

func TestUpdateAuthConfig_PasswordOnly(t *testing.T) {
	path := writeTestConfig(t, `[api.auth]
password_hash = "$2a$13$old"
session_secret = "ccdd"
preferred_login_method = "apikey"
`)

	if err := UpdateAuthConfig(path, map[string]any{"password_hash": "$2b$13$new"}); err != nil {
		t.Fatal(err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "$2b$13$new") {
		t.Error("should contain new password_hash")
	}
	if !strings.Contains(content, "apikey") {
		t.Error("should preserve preferred_login_method")
	}
	if !strings.Contains(content, "ccdd") {
		t.Error("should preserve session_secret")
	}
}

// ---------------------------------------------------------------------------
// Channel config persistence
// ---------------------------------------------------------------------------

func TestAddChannelToConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := AddChannelToConfig(path, "work", "default", "", "", nil)
	if err != nil {
		t.Fatalf("AddChannelToConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, `name = "work"`) && !strings.Contains(content, "name = 'work'") {
		t.Errorf("config missing channel name; content:\n%s", content)
	}
	if !strings.Contains(content, `agent = "default"`) && !strings.Contains(content, "agent = 'default'") {
		t.Errorf("config missing channel agent; content:\n%s", content)
	}
}

func TestAddChannelToConfig_WithAdapters(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := AddChannelToConfig(path, "personal", "my-agent", "broadcast", "ephemeral", []string{"telegram:123", "discord"})
	if err != nil {
		t.Fatalf("AddChannelToConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "telegram:123") {
		t.Errorf("config missing adapter telegram:123; content:\n%s", content)
	}
	if !strings.Contains(content, "discord") {
		t.Errorf("config missing adapter discord; content:\n%s", content)
	}
	if !strings.Contains(content, "broadcast") {
		t.Errorf("config missing delivery; content:\n%s", content)
	}
	if !strings.Contains(content, "ephemeral") {
		t.Errorf("config missing session_mode; content:\n%s", content)
	}
}

func TestUpdateChannelInConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddChannelToConfig(path, "work", "old-agent", "", "", nil); err != nil {
		t.Fatalf("AddChannelToConfig: %v", err)
	}

	err := UpdateChannelInConfig(path, "work", "new-agent", "single", "persistent", []string{"telegram"})
	if err != nil {
		t.Fatalf("UpdateChannelInConfig: %v", err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "old-agent") {
		t.Errorf("config still contains old agent; content:\n%s", content)
	}
	if !strings.Contains(content, "new-agent") {
		t.Errorf("config missing updated agent; content:\n%s", content)
	}
	if !strings.Contains(content, "single") {
		t.Errorf("config missing delivery; content:\n%s", content)
	}
}

func TestUpdateChannelInConfig_NotFound(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := UpdateChannelInConfig(path, "nonexistent", "agent", "", "", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent channel, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestRemoveChannelFromConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddChannelToConfig(path, "keep", "agent-a", "", "", nil); err != nil {
		t.Fatalf("AddChannelToConfig: %v", err)
	}
	if err := AddChannelToConfig(path, "remove-me", "agent-b", "", "", nil); err != nil {
		t.Fatalf("AddChannelToConfig: %v", err)
	}

	if err := RemoveChannelFromConfig(path, "remove-me"); err != nil {
		t.Fatalf("RemoveChannelFromConfig: %v", err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "remove-me") {
		t.Errorf("config still contains removed channel; content:\n%s", content)
	}
	if !strings.Contains(content, "keep") {
		t.Errorf("config missing kept channel; content:\n%s", content)
	}
}

func TestRemoveChannelFromConfig_LastEntry(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddChannelToConfig(path, "only-one", "default", "", "", nil); err != nil {
		t.Fatalf("AddChannelToConfig: %v", err)
	}

	if err := RemoveChannelFromConfig(path, "only-one"); err != nil {
		t.Fatalf("RemoveChannelFromConfig: %v", err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "channels") {
		t.Errorf("config should not contain channels key after removing last entry; content:\n%s", content)
	}
}

// ---------------------------------------------------------------------------
// Agent create / delete persistence
// ---------------------------------------------------------------------------

func TestAddAgentToConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := AddAgentToConfig(path, "helper", "openrouter", "claude-3", "supervised", "A helper agent", "/data/agents/helper")
	if err != nil {
		t.Fatalf("AddAgentToConfig: %v", err)
	}

	content := readConfig(t, path)
	for _, want := range []string{"helper", "openrouter", "claude-3", "supervised", "A helper agent", "/data/agents/helper"} {
		if !strings.Contains(content, want) {
			t.Errorf("config missing %q; content:\n%s", want, content)
		}
	}
}

func TestAddAgentToConfig_MinimalFields(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	err := AddAgentToConfig(path, "minimal", "", "", "", "", "")
	if err != nil {
		t.Fatalf("AddAgentToConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "minimal") {
		t.Errorf("config missing agent name; content:\n%s", content)
	}
	// Optional fields should not appear.
	if strings.Contains(content, "llm_provider") {
		t.Errorf("config should not contain llm_provider when empty; content:\n%s", content)
	}
}

func TestAddAgentToConfig_AppendsToExisting(t *testing.T) {
	path := writeTestConfig(t, "[[agents]]\nname = \"default\"\n")

	err := AddAgentToConfig(path, "second", "ollama", "llama3", "autonomous", "", "")
	if err != nil {
		t.Fatalf("AddAgentToConfig: %v", err)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "default") {
		t.Errorf("config missing original agent; content:\n%s", content)
	}
	if !strings.Contains(content, "second") {
		t.Errorf("config missing new agent; content:\n%s", content)
	}
}

func TestRemoveAgentFromConfig(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddAgentToConfig(path, "keep", "openai", "", "", "", ""); err != nil {
		t.Fatalf("AddAgentToConfig: %v", err)
	}
	if err := AddAgentToConfig(path, "remove-me", "ollama", "", "", "", ""); err != nil {
		t.Fatalf("AddAgentToConfig: %v", err)
	}

	if err := RemoveAgentFromConfig(path, "remove-me"); err != nil {
		t.Fatalf("RemoveAgentFromConfig: %v", err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "remove-me") {
		t.Errorf("config still contains removed agent; content:\n%s", content)
	}
	if !strings.Contains(content, "keep") {
		t.Errorf("config missing kept agent; content:\n%s", content)
	}
}

func TestRemoveAgentFromConfig_LastEntry(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	if err := AddAgentToConfig(path, "only-one", "", "", "", "", ""); err != nil {
		t.Fatalf("AddAgentToConfig: %v", err)
	}

	if err := RemoveAgentFromConfig(path, "only-one"); err != nil {
		t.Fatalf("RemoveAgentFromConfig: %v", err)
	}

	content := readConfig(t, path)
	if strings.Contains(content, "agents") {
		t.Errorf("config should not contain agents key after removing last entry; content:\n%s", content)
	}
}

// ---------------------------------------------------------------------------
// Config writer hardening (backup + concurrency)
// ---------------------------------------------------------------------------

func TestWriteRawConfig_CreatesBackup(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	// Trigger a write that creates a backup.
	if err := AddScheduleToConfig(path, "test", "@daily", "", "telegram:1", "", "", "", nil, true); err != nil {
		t.Fatalf("AddScheduleToConfig: %v", err)
	}

	bakPath := path + ".bak"
	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	if !strings.Contains(string(bakData), "enabled = true") {
		t.Errorf("backup should contain original config; got:\n%s", bakData)
	}
	// The current config should have the new schedule (not the backup content).
	current := readConfig(t, path)
	if !strings.Contains(current, "test") {
		t.Errorf("current config should contain new schedule; got:\n%s", current)
	}
}

func TestConcurrentConfigWrites(t *testing.T) {
	path := writeTestConfig(t, "[api]\nenabled = true\n")

	const N = 10
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = AddScheduleToConfig(path, fmt.Sprintf("sched-%d", i),
				"@daily", "", "telegram:1", "", "", "", nil, true)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d failed: %v", i, err)
		}
	}

	content := readConfig(t, path)
	for i := range N {
		name := fmt.Sprintf("sched-%d", i)
		if !strings.Contains(content, name) {
			t.Errorf("config missing schedule %q", name)
		}
	}
}
