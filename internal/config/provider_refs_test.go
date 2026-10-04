package config

import (
	"slices"
	"testing"
)

// refConfig is a minimal valid config with one unreferenced provider "spare"
// and a default provider "main"; extra is appended verbatim.
func refConfig(t *testing.T, extra string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(`
[api]
enabled = true

[llm]
default_provider = "main"

[[llm.providers]]
name = "main"
type = "openai"
api_key = "sk-main"

[[llm.providers]]
name = "spare"
type = "openai"
api_key = "sk-spare"
` + extra))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cfg
}

func assertReferrers(t *testing.T, cfg *Config, name string, want ...string) {
	t.Helper()
	if got := ProviderReferrers(cfg, name); !slices.Equal(got, want) {
		t.Errorf("ProviderReferrers(%q) = %v, want %v", name, got, want)
	}
	if IsProviderReferenced(cfg, name) != (len(want) > 0) {
		t.Errorf("IsProviderReferenced(%q) disagrees with ProviderReferrers", name)
	}
}

func TestProviderReferrers_Unreferenced(t *testing.T) {
	assertReferrers(t, refConfig(t, ""), "spare")
}

func TestProviderReferrers_DefaultProvider(t *testing.T) {
	assertReferrers(t, refConfig(t, ""), "main", "llm.default_provider")
}

func TestProviderReferrers_AgentReviewerProvider(t *testing.T) {
	cfg := refConfig(t, `
[[agents]]
name = "pam"
reviewer_model = "gpt-x"
reviewer_provider = "spare"
`)
	assertReferrers(t, cfg, "spare", "agent:pam.reviewer_provider")
}

func TestProviderReferrers_AgentFallbackRule(t *testing.T) {
	cfg := refConfig(t, `
[[agents]]
name = "pam"

[[agents.fallback]]
trigger = "error"
action = "switch_provider"
provider = "spare"
`)
	assertReferrers(t, cfg, "spare", "agent:pam.fallback")
}

func TestProviderReferrers_EvalJudgeProvider(t *testing.T) {
	cfg := refConfig(t, `
[eval]
judge_model = "gpt-x"
judge_provider = "spare"
`)
	assertReferrers(t, cfg, "spare", "eval.judge_provider")
}

func TestProviderReferrers_ListsEveryReferrer(t *testing.T) {
	cfg := refConfig(t, `
[eval]
judge_model = "gpt-x"
judge_provider = "spare"

[[agents]]
name = "pam"
llm_provider = "spare"
reviewer_model = "gpt-x"
reviewer_provider = "spare"
`)
	assertReferrers(t, cfg, "spare", "eval.judge_provider", "agent:pam.llm_provider", "agent:pam.reviewer_provider")
}
