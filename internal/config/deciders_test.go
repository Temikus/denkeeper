package config

import (
	"strings"
	"testing"
)

// deciderConfig is a valid config with an openrouter and an anthropic
// instance; extra is appended verbatim.
func deciderConfig(extra string) []byte {
	return []byte(`
[telegram]
token = "test"
allowed_users = [1]

[llm]
default_provider = "anthropic"

[[llm.providers]]
name = "anthropic"
type = "anthropic"
api_key = "sk-test"

[[llm.providers]]
name = "or"
type = "openrouter"
api_key = "sk-or-test"
` + extra)
}

func parseDeciderErr(t *testing.T, extra, wantErr string) {
	t.Helper()
	_, err := Parse(deciderConfig(extra))
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", wantErr)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("error = %q, want it to contain %q", err, wantErr)
	}
}

func TestDeciders_Defaults(t *testing.T) {
	cfg, err := Parse(deciderConfig(`
[[llm.deciders]]
name = "jev"
provider = "or"
model = "typesafe/jev-1.13"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.LLM.Deciders) != 1 {
		t.Fatalf("deciders = %d, want 1", len(cfg.LLM.Deciders))
	}
	d := cfg.LLM.Deciders[0]
	if d.Timeout != "5s" || d.MaxInputTokens != 30000 {
		t.Errorf("defaults = %q/%d, want 5s/30000", d.Timeout, d.MaxInputTokens)
	}
}

func TestDeciders_ExplicitValuesKept(t *testing.T) {
	cfg, err := Parse(deciderConfig(`
[[llm.deciders]]
name = "jev"
provider = "or"
model = "typesafe/jev-1.13"
timeout = "2s"
max_input_tokens = 8000
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cfg.LLM.Deciders[0]
	if d.Timeout != "2s" || d.MaxInputTokens != 8000 {
		t.Errorf("got %q/%d, want 2s/8000", d.Timeout, d.MaxInputTokens)
	}
}

func TestDeciders_InvalidName(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "Jev_1"
provider = "or"
model = "m"
`, "invalid name")
}

func TestDeciders_DuplicateName(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "jev"
provider = "or"
model = "m"

[[llm.deciders]]
name = "jev"
provider = "or"
model = "m2"
`, "duplicate decider name")
}

func TestDeciders_UnknownProvider(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "jev"
provider = "nope"
model = "m"
`, `provider "nope" does not match`)
}

func TestDeciders_NonDecisionProviderType(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "jev"
provider = "anthropic"
model = "m"
`, "does not serve decision models")
}

func TestDeciders_MissingModel(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "jev"
provider = "or"
`, "model is required")
}

func TestDeciders_BadTimeout(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "jev"
provider = "or"
model = "m"
timeout = "soon"
`, "positive duration")
}

func TestDeciders_NegativeTimeout(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "jev"
provider = "or"
model = "m"
timeout = "-1s"
`, "positive duration")
}

func TestDeciders_NegativeMaxInputTokens(t *testing.T) {
	parseDeciderErr(t, `
[[llm.deciders]]
name = "jev"
provider = "or"
model = "m"
max_input_tokens = -5
`, "max_input_tokens")
}

func TestDeciders_ProviderRequiresAPIKey(t *testing.T) {
	// "bare" is referenced only by the decider, so the API-key check must
	// still apply to it.
	parseDeciderErr(t, `
[[llm.providers]]
name = "bare"
type = "openrouter"

[[llm.deciders]]
name = "jev"
provider = "bare"
model = "m"
`, `provider "bare" (type openrouter) requires an api_key`)
}

func TestIsProviderReferenced_ByDecider(t *testing.T) {
	cfg, err := Parse(deciderConfig(`
[[llm.deciders]]
name = "jev"
provider = "or"
model = "m"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !IsProviderReferenced(cfg, "or") {
		t.Error("provider used only by a decider must count as referenced")
	}
}
