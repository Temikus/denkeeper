package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	if err := addToolToConfig(path, "web-search", "/usr/bin/ws", []string{"--flag"}, map[string]string{"API_KEY": "secret"}); err != nil {
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

	if err := addToolToConfig(path, "new-tool", "/usr/bin/new", nil, nil); err != nil {
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

func TestRoundTrip_AddThenRemove(t *testing.T) {
	path := writeTestConfig(t, `[telegram]
token = "keep-me"
`)

	if err := addToolToConfig(path, "my-tool", "/bin/tool", nil, nil); err != nil {
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
