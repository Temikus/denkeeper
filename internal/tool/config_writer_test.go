package tool

import (
	"os"
	"path/filepath"
	"strings"
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
