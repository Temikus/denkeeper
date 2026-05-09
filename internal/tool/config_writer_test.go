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

func TestToolConfigToMap_IncludesDisabledTools(t *testing.T) {
	cfg := config.ToolConfig{
		Command:       "/usr/bin/tool",
		DisabledTools: []string{"tool-a", "tool-b"},
	}
	m := toolConfigToMap(cfg)
	dt, ok := m["disabled_tools"]
	if !ok {
		t.Fatal("disabled_tools not present in map")
	}
	dtSlice, ok := dt.([]string)
	if !ok {
		t.Fatalf("disabled_tools has type %T, want []string", dt)
	}
	if len(dtSlice) != 2 {
		t.Errorf("disabled_tools len = %d, want 2", len(dtSlice))
	}
}

func TestToolConfigToMap_OmitsEmptyDisabledTools(t *testing.T) {
	cfg := config.ToolConfig{Command: "/usr/bin/tool"}
	m := toolConfigToMap(cfg)
	if _, ok := m["disabled_tools"]; ok {
		t.Error("disabled_tools should be omitted when empty")
	}
}

func TestUpdateDisabledToolsInConfig_AddAndRemove(t *testing.T) {
	path := writeTestConfig(t, `[tools]
[tools.my-server]
command = "/usr/bin/tool"
`)

	if err := updateDisabledToolsInConfig(path, "my-server", []string{"tool-a", "tool-b"}); err != nil {
		t.Fatal(err)
	}
	content := readConfig(t, path)
	if !strings.Contains(content, "disabled_tools") {
		t.Error("config should contain disabled_tools after update")
	}
	if !strings.Contains(content, "tool-a") {
		t.Error("config should contain tool-a")
	}

	// Clear disabled tools.
	if err := updateDisabledToolsInConfig(path, "my-server", nil); err != nil {
		t.Fatal(err)
	}
	content = readConfig(t, path)
	if strings.Contains(content, "disabled_tools") {
		t.Error("disabled_tools should be removed when empty")
	}
	if !strings.Contains(content, "command") {
		t.Error("other fields should be preserved")
	}
}

func TestUpdateDisabledToolsInConfig_ToolNotFound(t *testing.T) {
	path := writeTestConfig(t, `[tools]
[tools.other]
command = "/usr/bin/other"
`)

	err := updateDisabledToolsInConfig(path, "nonexistent", []string{"x"})
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
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
