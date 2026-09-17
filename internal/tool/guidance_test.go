package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// registerGuidedServer connects m to ts under name with the given guidance,
// optionally disabling some of its tools.
func registerGuidedServer(t *testing.T, m *Manager, name string, url, guidance string, disabled ...string) {
	t.Helper()
	if err := m.RegisterServer(context.Background(), name, config.ToolConfig{
		Transport:     "sse",
		URL:           url,
		AllowLoopback: true,
		Guidance:      guidance,
		DisabledTools: disabled,
	}); err != nil {
		t.Fatalf("RegisterServer(%s): %v", name, err)
	}
}

func TestServerGuidance_IncludesGuidedServerWithAdvertisedTools(t *testing.T) {
	ts := startCollisionServer(t, "todoist", "find-tasks", "add-tasks")
	m := NewManager(testLogger())
	t.Cleanup(func() { _ = m.Close() })
	registerGuidedServer(t, m, "todoist", ts.URL, "  There is no workspace concept.  ")

	got := m.ServerGuidance()
	if len(got) != 1 {
		t.Fatalf("ServerGuidance() = %d entries, want 1", len(got))
	}
	if got[0].Server != "todoist" {
		t.Errorf("server = %q, want %q", got[0].Server, "todoist")
	}
	if got[0].Guidance != "There is no workspace concept." {
		t.Errorf("guidance = %q, want it trimmed", got[0].Guidance)
	}
	if strings.Join(got[0].Tools, ",") != "add-tasks,find-tasks" {
		t.Errorf("tools = %v, want them sorted lexicographically", got[0].Tools)
	}
}

func TestServerGuidance_ExcludesServerWithoutGuidance(t *testing.T) {
	ts := startCollisionServer(t, "plain", "search")
	m := NewManager(testLogger())
	t.Cleanup(func() { _ = m.Close() })
	registerGuidedServer(t, m, "plain", ts.URL, "   ")

	if got := m.ServerGuidance(); len(got) != 0 {
		t.Fatalf("ServerGuidance() = %v, want none (guidance is blank)", got)
	}
}

func TestServerGuidance_ExcludesServerWithEveryToolDisabled(t *testing.T) {
	ts := startCollisionServer(t, "muted", "only-tool")
	m := NewManager(testLogger())
	t.Cleanup(func() { _ = m.Close() })
	registerGuidedServer(t, m, "muted", ts.URL, "never shown", "only-tool")

	if got := m.ServerGuidance(); len(got) != 0 {
		t.Fatalf("ServerGuidance() = %v, want none — the model can call nothing on this server", got)
	}
}

func TestServerGuidance_ExcludesDisabledServer(t *testing.T) {
	m := NewManager(testLogger())
	t.Cleanup(func() { _ = m.Close() })
	m.RegisterDisabled("offline", config.ToolConfig{
		Command:  "/usr/bin/nope",
		Guidance: "never shown",
	}, "disabled by operator", false)

	if got := m.ServerGuidance(); len(got) != 0 {
		t.Fatalf("ServerGuidance() = %v, want none for a disabled server", got)
	}
}

func TestServerGuidance_SortedAndParentMerged(t *testing.T) {
	zebra := startCollisionServer(t, "zebra", "z-tool")
	alpha := startCollisionServer(t, "alpha", "a-tool")
	shared := startCollisionServer(t, "shared", "s-tool")

	parent := NewManager(testLogger())
	t.Cleanup(func() { _ = parent.Close() })
	registerGuidedServer(t, parent, "zebra", zebra.URL, "parent zebra")
	registerGuidedServer(t, parent, "shared", shared.URL, "parent shared")

	child := NewManager(testLogger())
	t.Cleanup(func() { _ = child.Close() })
	child.AdoptFrom(parent)
	registerGuidedServer(t, child, "alpha", alpha.URL, "child alpha")
	registerGuidedServer(t, child, "shared", shared.URL, "child shared")

	got := child.ServerGuidance()
	if len(got) != 3 {
		t.Fatalf("ServerGuidance() = %d entries, want 3: %v", len(got), got)
	}
	wantOrder := []string{"alpha", "shared", "zebra"}
	for i, want := range wantOrder {
		if got[i].Server != want {
			t.Fatalf("entry %d = %q, want %q (sorted by server name)", i, got[i].Server, want)
		}
	}
	if got[1].Guidance != "child shared" {
		t.Errorf("shared guidance = %q, want the local entry to win over the parent's", got[1].Guidance)
	}
}

func TestGuidanceForTool_ReturnsOwningServerGuidance(t *testing.T) {
	ts := startCollisionServer(t, "todoist", "find-tasks")
	m := NewManager(testLogger())
	t.Cleanup(func() { _ = m.Close() })
	registerGuidedServer(t, m, "todoist", ts.URL, "  no workspaces  ")

	if got := m.GuidanceForTool("find-tasks"); got != "no workspaces" {
		t.Errorf("GuidanceForTool(find-tasks) = %q, want trimmed guidance", got)
	}
	if got := m.GuidanceForTool("no-such-tool"); got != "" {
		t.Errorf("GuidanceForTool(no-such-tool) = %q, want empty", got)
	}
}

func TestGuidanceForTool_ParentDelegation(t *testing.T) {
	ts := startCollisionServer(t, "upstream", "u-tool")
	parent := NewManager(testLogger())
	t.Cleanup(func() { _ = parent.Close() })
	registerGuidedServer(t, parent, "upstream", ts.URL, "parent rules")

	child := NewManager(testLogger())
	t.Cleanup(func() { _ = child.Close() })
	child.AdoptFrom(parent)

	if got := child.GuidanceForTool("u-tool"); got != "parent rules" {
		t.Errorf("GuidanceForTool(u-tool) via parent = %q, want %q", got, "parent rules")
	}
}

func TestGuidanceForTool_CollidingBareNameResolvesToNeither(t *testing.T) {
	a := startCollisionServer(t, "alpha", "dup")
	b := startCollisionServer(t, "beta", "dup")
	m := NewManager(testLogger())
	t.Cleanup(func() { _ = m.Close() })
	registerGuidedServer(t, m, "alpha", a.URL, "alpha rules")
	registerGuidedServer(t, m, "beta", b.URL, "beta rules")

	if got := m.GuidanceForTool("dup"); got != "" {
		t.Errorf("GuidanceForTool(dup) = %q, want empty — a colliding bare name must never guess an owner", got)
	}
	if got := m.GuidanceForTool("alpha__dup"); got != "alpha rules" {
		t.Errorf("GuidanceForTool(alpha__dup) = %q, want %q", got, "alpha rules")
	}
}

func TestToolConfigToMap_IncludesGuidance(t *testing.T) {
	m := toolConfigToMap(config.ToolConfig{Command: "/usr/bin/tool", Guidance: "IDs come from the value, not the key."})
	if m["guidance"] != "IDs come from the value, not the key." {
		t.Errorf("guidance = %v, want the verbatim text", m["guidance"])
	}
}

func TestToolConfigToMap_OmitsEmptyGuidance(t *testing.T) {
	m := toolConfigToMap(config.ToolConfig{Command: "/usr/bin/tool"})
	if _, ok := m["guidance"]; ok {
		t.Error("guidance should be omitted when empty")
	}
}

// TestGuidanceRoundTrip_ParseThenWriteBack pins the full config cycle: a
// hand-written guidance survives Load and a writer pass, and clearing it
// removes the key from the TOML rather than leaving an empty string behind.
func TestGuidanceRoundTrip_ParseThenWriteBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "denkeeper.toml")
	const guidance = "Never send workspaceId. Project IDs come from the cached value, not the key."
	err := os.WriteFile(path, []byte(`[llm]
default_provider = "mock-existing"
default_model = "mock-model"

[[llm.providers]]
name = "mock-existing"
type = "openai"
api_key = "sk-test"

[tools.todoist]
command = "/usr/bin/todoist-mcp"
guidance = """`+guidance+`"""
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := cfg.Tools["todoist"]
	if loaded.Guidance != guidance {
		t.Fatalf("loaded guidance = %q, want %q", loaded.Guidance, guidance)
	}

	// Write the loaded config back and re-read the raw TOML: the field must
	// survive a writer pass unchanged.
	if err := addToolToConfig(path, "todoist", loaded); err != nil {
		t.Fatalf("addToolToConfig: %v", err)
	}
	if raw := readConfig(t, path); !strings.Contains(raw, guidance) {
		t.Errorf("guidance missing from written TOML:\n%s", raw)
	}

	// Clearing it must drop the key, not persist an empty string. Asserted
	// against the raw TOML — a post-Load struct cannot tell "absent" from "".
	loaded.Guidance = ""
	if err := addToolToConfig(path, "todoist", loaded); err != nil {
		t.Fatalf("addToolToConfig (cleared): %v", err)
	}
	if raw := readConfig(t, path); strings.Contains(raw, "guidance") {
		t.Errorf("guidance key survived clearing:\n%s", raw)
	}
}

func TestLoad_TruncatesOversizeGuidanceWithoutDisablingServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "denkeeper.toml")
	// Multi-byte runes so a naive byte cut would land mid-rune.
	oversize := strings.Repeat("é", config.MaxToolGuidanceBytes)
	err := os.WriteFile(path, []byte(`[llm]
default_provider = "mock-existing"
default_model = "mock-model"

[[llm.providers]]
name = "mock-existing"
type = "openai"
api_key = "sk-test"

[tools.todoist]
command = "/usr/bin/todoist-mcp"
guidance = """`+oversize+`"""
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tc := cfg.Tools["todoist"]
	if len(tc.Guidance) > config.MaxToolGuidanceBytes {
		t.Errorf("guidance is %d bytes, want it truncated to at most %d", len(tc.Guidance), config.MaxToolGuidanceBytes)
	}
	if !tc.IsEnabled() {
		t.Error("oversize guidance disabled the server — truncation must never take a working MCP server offline")
	}
	if _, ok := cfg.ToolWarnings["todoist"]; ok {
		t.Error("truncation wrote to ToolWarnings, which registers the server as disabled")
	}
	if !strings.HasSuffix(tc.Guidance, "é") {
		t.Errorf("guidance was cut mid-rune: %q", tc.Guidance[len(tc.Guidance)-4:])
	}
}
