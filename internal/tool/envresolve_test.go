package tool

import (
	"testing"
)

func TestResolveEnvPlaceholders_FromToolEnv(t *testing.T) {
	result, err := resolveEnvPlaceholders("Bearer ${API_TOKEN}", map[string]string{"API_TOKEN": "tok123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Bearer tok123" {
		t.Fatalf("expected 'Bearer tok123', got: %s", result)
	}
}

func TestResolveEnvPlaceholders_FromProcessEnv(t *testing.T) {
	t.Setenv("TEST_MCP_VAR", "fromenv")
	result, err := resolveEnvPlaceholders("${TEST_MCP_VAR}", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fromenv" {
		t.Fatalf("expected 'fromenv', got: %s", result)
	}
}

func TestResolveEnvPlaceholders_ToolEnvTakesPrecedence(t *testing.T) {
	t.Setenv("SHARED_VAR", "process")
	result, err := resolveEnvPlaceholders("${SHARED_VAR}", map[string]string{"SHARED_VAR": "tool"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "tool" {
		t.Fatalf("expected 'tool', got: %s", result)
	}
}

func TestResolveEnvPlaceholders_MissingVarBecomesEmpty(t *testing.T) {
	// Use a variable name that is very unlikely to be set in the environment.
	result, err := resolveEnvPlaceholders("prefix-${DENKEEPER_TEST_NONEXISTENT_98765}-suffix", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "prefix--suffix" {
		t.Fatalf("expected 'prefix--suffix', got: %s", result)
	}
}

func TestResolveEnvPlaceholders_NoPlaceholders(t *testing.T) {
	result, err := resolveEnvPlaceholders("plain string", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "plain string" {
		t.Fatalf("expected 'plain string', got: %s", result)
	}
}

func TestResolveEnvPlaceholders_ForbiddenSessionSecret(t *testing.T) {
	_, err := resolveEnvPlaceholders("${DENKEEPER_API_AUTH_SESSION_SECRET}", nil)
	if err == nil {
		t.Fatal("expected error for forbidden env var pattern")
	}
}

func TestResolveEnvPlaceholders_ForbiddenOIDCSecret(t *testing.T) {
	_, err := resolveEnvPlaceholders("${DENKEEPER_OIDC_CLIENT_SECRET}", nil)
	if err == nil {
		t.Fatal("expected error for forbidden OIDC client secret")
	}
}

func TestResolveEnvPlaceholders_ForbiddenPasswordHash(t *testing.T) {
	_, err := resolveEnvPlaceholders("${DENKEEPER_API_AUTH_PASSWORD_HASH}", nil)
	if err == nil {
		t.Fatal("expected error for forbidden password hash pattern")
	}
}

func TestResolveEnvPlaceholders_ForbiddenDBKey(t *testing.T) {
	_, err := resolveEnvPlaceholders("${DENKEEPER_DB_KEY}", nil)
	if err == nil {
		t.Fatal("expected error for forbidden DB key")
	}
}

func TestResolveEnvPlaceholders_AllowedDenkeeperVar(t *testing.T) {
	t.Setenv("DENKEEPER_LLM_OPENROUTER_API_KEY", "ok")
	result, err := resolveEnvPlaceholders("${DENKEEPER_LLM_OPENROUTER_API_KEY}", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected 'ok', got: %s", result)
	}
}

func TestMatchGlob_ExactMatch(t *testing.T) {
	if !matchGlob("FOO", "FOO") {
		t.Fatal("expected exact match")
	}
}

func TestMatchGlob_WildcardMiddle(t *testing.T) {
	if !matchGlob("DENKEEPER_API_AUTH_SESSION_SECRET", "DENKEEPER_*_SECRET") {
		t.Fatal("expected wildcard match")
	}
}

func TestMatchGlob_WildcardSuffix(t *testing.T) {
	if !matchGlob("DENKEEPER_API_AUTH_PASSWORD_HASH", "DENKEEPER_*_PASSWORD*") {
		t.Fatal("expected wildcard suffix match")
	}
}

func TestMatchGlob_NoMatch(t *testing.T) {
	if matchGlob("DENKEEPER_LLM_KEY", "DENKEEPER_*_SECRET") {
		t.Fatal("expected no match")
	}
}
