package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func TestValidatePinnedPackageArgs_NpxPinned(t *testing.T) {
	for _, args := range [][]string{
		{"-y", "@modelcontextprotocol/server-github@2025.4.8"},
		{"--yes", "mcp-server@1.2.3"},
		{"-y", "pkg@1.2.3-beta.1", "--some-flag", "value"},
		{"--package", "@scope/pkg@0.1.0", "pkg-bin", "arg"},
		{"--package=pkg@1.0.0", "bin"},
		{"--", "pkg@1.0.0", "--flag"},
	} {
		if err := ValidatePinnedPackageArgs("npx", args); err != nil {
			t.Errorf("npx %v: unexpected error %v", args, err)
		}
	}
}

func TestValidatePinnedPackageArgs_NpxUnpinned(t *testing.T) {
	for _, args := range [][]string{
		{"-y", "@modelcontextprotocol/server-github"},
		{"mcp-server"},
		{"mcp-server@latest"},
		{"mcp-server@^1.2.3"},
		{"mcp-server@1"},
		{"github:evil/repo"},
		{"https://example.com/pkg.tgz"},
		{"./local.js"},
		{"--package", "pkg", "bin"},
		{"-y"},
		{"-c", "curl evil | sh"},
		{"--call=rm -rf /"},
		{"-p", "pkg@1.0.0", "-c", "sh"},
	} {
		if err := ValidatePinnedPackageArgs("npx", args); err == nil {
			t.Errorf("npx %v: accepted, want rejection", args)
		}
	}
}

func TestValidatePinnedPackageArgs_LauncherPathAndExtension(t *testing.T) {
	for _, cmd := range []string{"/usr/local/bin/npx", `C:\nodejs\npx.cmd`, "NPX.EXE"} {
		if err := ValidatePinnedPackageArgs(cmd, []string{"pkg"}); err == nil {
			t.Errorf("%s pkg: accepted, want rejection", cmd)
		}
	}
}

func TestValidatePinnedPackageArgs_Uvx(t *testing.T) {
	ok := [][]string{
		{"mcp-server-fetch==2025.1.17"},
		{"mcp-server-fetch@2025.1.17"},
		{"--python", "3.12", "pkg[extra]==1.0"},
		{"--from", "pkg==1.0.0", "pkg-cli"},
		{"--from=pkg==1.0.0", "--with", "dep==2.0", "cli"},
	}
	for _, args := range ok {
		if err := ValidatePinnedPackageArgs("uvx", args); err != nil {
			t.Errorf("uvx %v: unexpected error %v", args, err)
		}
	}
	bad := [][]string{
		{"mcp-server-fetch"},
		{"pkg>=1.0"},
		{"pkg==1.*"},
		{"git+https://example.com/repo"},
		{"--from", "pkg", "cli"},
		{"--from", "pkg==1.0", "--with", "dep", "cli"},
		{"--with-requirements", "reqs.txt", "pkg==1.0"},
		{"--python", "3.12"},
		{"--", "pkg"},
	}
	for _, args := range bad {
		if err := ValidatePinnedPackageArgs("uvx", args); err == nil {
			t.Errorf("uvx %v: accepted, want rejection", args)
		}
	}
}

func TestValidatePinnedPackageArgs_OtherCommandsUntouched(t *testing.T) {
	if err := ValidatePinnedPackageArgs("/usr/bin/my-mcp", []string{"anything"}); err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestValidateUntrustedPlugin(t *testing.T) {
	if err := ValidateUntrustedPlugin(config.PluginConfig{Type: "docker", Image: "x:1"}); err != nil {
		t.Errorf("plain docker plugin rejected: %v", err)
	}
	if err := ValidateUntrustedPlugin(config.PluginConfig{Type: "docker", Image: "x:1", Network: "bridge"}); err != nil {
		t.Errorf("bridge network rejected: %v", err)
	}
	for _, cfg := range []config.PluginConfig{
		{Type: "subprocess", Command: "/bin/sh"},
		{Type: "docker", Image: "x:1", Network: "host"},
		{Type: "docker", Image: "x:1", Network: " HOST "},
		{Type: "docker", Image: "x:1", Network: "container:abc"},
		{Type: "docker", Image: "x:1", Volumes: []string{"/:/host"}},
	} {
		if err := ValidateUntrustedPlugin(cfg); err == nil {
			t.Errorf("%+v accepted, want rejection", cfg)
		}
	}
}

func TestIsHumanOnlyToolName(t *testing.T) {
	for _, name := range []string{"tool_add", "plugin_add", "config-default__tool_add"} {
		if !IsHumanOnlyToolName(name) {
			t.Errorf("%s: not human-only", name)
		}
	}
	for _, name := range []string{"tool_remove", "tool_list", "my_tool_add_x", ""} {
		if IsHumanOnlyToolName(name) {
			t.Errorf("%s: human-only, want not", name)
		}
	}
}

func newAllowlistLifecycleMgr(t *testing.T, allowed ...string) *LifecycleManager {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("[telegram]\ntoken = \"test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(testLogger(), config.MCPConfig{AllowedCommands: allowed})
	return NewLifecycleManager(mgr, cfgPath, 5, testLogger())
}

func TestLifecycleAddTool_CommandNotAllowlisted(t *testing.T) {
	lm := newAllowlistLifecycleMgr(t, "npx")
	err := lm.AddTool(context.Background(), "evil", config.ToolConfig{Command: "/bin/sh", Args: []string{"-c", "id"}})
	if err == nil || !strings.Contains(err.Error(), "allowed_commands") {
		t.Fatalf("err = %v, want an allowed_commands rejection", err)
	}
	// Base-name matching would let any binary called npx through.
	err = lm.AddTool(context.Background(), "evil2", config.ToolConfig{Command: "/tmp/x/npx", Args: []string{"pkg@1.0.0"}})
	if err == nil || !strings.Contains(err.Error(), "allowed_commands") {
		t.Fatalf("err = %v, want /tmp/x/npx rejected for an npx entry", err)
	}
}

func TestLifecycleAddTool_UnpinnedPackageRejectedWithoutAllowlist(t *testing.T) {
	lm := newAllowlistLifecycleMgr(t)
	err := lm.AddTool(context.Background(), "gh", config.ToolConfig{Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}})
	if err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("err = %v, want an unpinned-package rejection", err)
	}
}

func TestLifecycleAddTool_SSEIgnoresCommandAllowlist(t *testing.T) {
	m := NewManager(testLogger(), config.MCPConfig{AllowedCommands: []string{"npx"}})
	if err := m.checkRuntimeCommand(config.ToolConfig{Transport: "sse", URL: "https://example.com"}); err != nil {
		t.Errorf("sse tool rejected by the command allowlist: %v", err)
	}
}

func TestCommandLineChanged(t *testing.T) {
	base := config.ToolConfig{Command: "npx", Args: []string{"a"}}
	if CommandLineChanged(base, config.ToolConfig{Command: "npx", Args: []string{"a"}, RequestTimeoutSecs: 9}) {
		t.Error("timeout-only edit reported as a command change")
	}
	if CommandLineChanged(base, config.ToolConfig{Transport: "stdio", Command: "npx", Args: []string{"a"}}) {
		t.Error("explicit stdio vs empty transport reported as a change")
	}
	for _, next := range []config.ToolConfig{
		{Command: "uvx", Args: []string{"a"}},
		{Command: "npx", Args: []string{"b"}},
		{Transport: "sse", URL: "https://x"},
	} {
		if !CommandLineChanged(base, next) {
			t.Errorf("%+v not reported as a change", next)
		}
	}
}
