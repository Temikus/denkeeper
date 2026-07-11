package tool

import (
	"strings"
	"testing"
)

// envToMap parses a []string of "K=V" entries into a map for easy assertions.
func envToMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			t.Fatalf("malformed env entry %q", kv)
		}
		m[kv[:eq]] = kv[eq+1:]
	}
	return m
}

func TestBuildStdioEnv_IncludesAllowlistedAndCfgEnv(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/tester")

	cfgEnv := map[string]string{"MY_TOOL_FLAG": "on"}
	env := buildStdioEnv(cfgEnv, nil, testLogger())
	got := envToMap(t, env)

	if got["PATH"] != "/usr/bin:/bin" {
		t.Errorf("PATH not forwarded: got %q", got["PATH"])
	}
	if got["HOME"] != "/home/tester" {
		t.Errorf("HOME not forwarded: got %q", got["HOME"])
	}
	if got["MY_TOOL_FLAG"] != "on" {
		t.Errorf("cfg.Env value not present: got %q", got["MY_TOOL_FLAG"])
	}
}

func TestBuildStdioEnv_ExcludesDenkeeperSecrets(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("DENKEEPER_LLM_ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("DENKEEPER_API_AUTH_SESSION_SECRET", "hunter2")

	env := buildStdioEnv(nil, nil, testLogger())
	got := envToMap(t, env)

	if _, ok := got["DENKEEPER_LLM_ANTHROPIC_API_KEY"]; ok {
		t.Error("DENKEEPER_LLM_ANTHROPIC_API_KEY leaked into subprocess env")
	}
	if _, ok := got["DENKEEPER_API_AUTH_SESSION_SECRET"]; ok {
		t.Error("DENKEEPER_API_AUTH_SESSION_SECRET leaked into subprocess env")
	}
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH should still be forwarded: got %q", got["PATH"])
	}
}

func TestBuildStdioEnv_PassthroughCannotForwardDenkeeperSecret(t *testing.T) {
	t.Setenv("DENKEEPER_TELEGRAM_TOKEN", "bot-token")

	// Operator tries to explicitly pass a secret through — the hard exclusion
	// filter must still drop it.
	env := buildStdioEnv(nil, []string{"DENKEEPER_TELEGRAM_TOKEN"}, testLogger())
	got := envToMap(t, env)

	if _, ok := got["DENKEEPER_TELEGRAM_TOKEN"]; ok {
		t.Error("DENKEEPER_TELEGRAM_TOKEN was forwarded despite the exclusion filter")
	}
}

func TestBuildStdioEnv_PassthroughForwardsNonSecret(t *testing.T) {
	t.Setenv("MY_CUSTOM_VAR", "value1")

	// Not in the allowlist, so only forwarded because of passthrough.
	envWithout := buildStdioEnv(nil, nil, testLogger())
	if _, ok := envToMap(t, envWithout)["MY_CUSTOM_VAR"]; ok {
		t.Fatal("MY_CUSTOM_VAR should not be forwarded without passthrough")
	}

	env := buildStdioEnv(nil, []string{"MY_CUSTOM_VAR"}, testLogger())
	if got := envToMap(t, env)["MY_CUSTOM_VAR"]; got != "value1" {
		t.Errorf("MY_CUSTOM_VAR not forwarded via passthrough: got %q", got)
	}
}

func TestBuildStdioEnv_LCPrefixMatching(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LC_TIME", "de_DE.UTF-8")

	got := envToMap(t, buildStdioEnv(nil, nil, testLogger()))

	if got["LC_ALL"] != "en_US.UTF-8" {
		t.Errorf("LC_ALL not forwarded via prefix match: got %q", got["LC_ALL"])
	}
	if got["LC_TIME"] != "de_DE.UTF-8" {
		t.Errorf("LC_TIME not forwarded via prefix match: got %q", got["LC_TIME"])
	}
}

func TestBuildStdioEnv_WindowsMixedCaseNames(t *testing.T) {
	// Windows env var names are case-insensitive and conventionally mixed-case.
	// They must match the (uppercase) allowlist and be forwarded with their
	// original spelling intact.
	t.Setenv("Path", `C:\Windows\System32`)
	t.Setenv("SystemRoot", `C:\Windows`)
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)
	t.Setenv("windir", `C:\Windows`) // lowercase; not in allowlist, must be dropped

	got := envToMap(t, buildStdioEnv(nil, nil, testLogger()))

	if got["Path"] != `C:\Windows\System32` {
		t.Errorf("Path (mixed-case) not forwarded with original spelling: got %q", got["Path"])
	}
	if got["SystemRoot"] != `C:\Windows` {
		t.Errorf("SystemRoot (mixed-case) not forwarded with original spelling: got %q", got["SystemRoot"])
	}
	if got["ComSpec"] != `C:\Windows\System32\cmd.exe` {
		t.Errorf("ComSpec (mixed-case) not forwarded with original spelling: got %q", got["ComSpec"])
	}
	// Forwarded entries carry the parent's original spelling, never a
	// normalized/uppercased key. (Assert the exact key we set is present,
	// which the equality checks above already establish.)
	// windir is not in the allowlist, so it must not leak.
	if _, ok := got["windir"]; ok {
		t.Error("windir is not allowlisted and must not be forwarded")
	}
}

func TestBuildStdioEnv_MixedCaseSecretStillExcluded(t *testing.T) {
	// The exclusion filter must be case-insensitive too: a mixed-case
	// DENKEEPER_* name must never reach the child, whether inherited or passed.
	t.Setenv("Denkeeper_Api_Key", "sk-secret")

	got := envToMap(t, buildStdioEnv(nil, []string{"Denkeeper_Api_Key"}, testLogger()))

	if _, ok := got["Denkeeper_Api_Key"]; ok {
		t.Error("mixed-case Denkeeper_Api_Key leaked despite the exclusion filter")
	}
}

func TestBuildStdioEnv_PassthroughCaseInsensitive(t *testing.T) {
	// Operator lists the passthrough name in a different case than the parent
	// env spelling; it must still match and forward with the parent's spelling.
	t.Setenv("My_Custom_Var", "value1")

	env := buildStdioEnv(nil, []string{"MY_CUSTOM_VAR"}, testLogger())
	got := envToMap(t, env)

	if got["My_Custom_Var"] != "value1" {
		t.Errorf("case-insensitive passthrough failed: got %q", got["My_Custom_Var"])
	}
}

func TestBuildStdioEnv_CfgEnvOverridesAllowlistedParent(t *testing.T) {
	t.Setenv("PATH", "/parent/bin")

	env := buildStdioEnv(map[string]string{"PATH": "/tool/bin"}, nil, testLogger())

	// cfg.Env is appended last; with last-value-wins env semantics the tool's
	// value takes effect. Assert the final occurrence of PATH is the tool value.
	last := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			last = strings.TrimPrefix(kv, "PATH=")
		}
	}
	if last != "/tool/bin" {
		t.Errorf("cfg.Env should win for PATH: last value = %q", last)
	}
}
