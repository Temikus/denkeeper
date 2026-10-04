package config

import (
	"errors"
	"strings"
	"testing"
)

func deciderFile(t *testing.T, extra string) string {
	t.Helper()
	return writeTestConfig(t, string(deciderConfig(extra)))
}

func loadDeciders(t *testing.T, path string) []DeciderConfig {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	return cfg.LLM.Deciders
}

func TestAddDeciderToConfig_OmittedKeysTakeDefaults(t *testing.T) {
	path := deciderFile(t, "")

	if err := AddDeciderToConfig(path, DeciderConfig{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13"}); err != nil {
		t.Fatalf("AddDeciderToConfig: %v", err)
	}

	if raw := readTestConfig(t, path); strings.Contains(raw, "timeout") || strings.Contains(raw, "max_input_tokens") {
		t.Errorf("omitted keys were written:\n%s", raw)
	}
	got := loadDeciders(t, path)
	want := DeciderConfig{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", Timeout: "5s", MaxInputTokens: 30000}
	if len(got) != 1 || got[0] != want {
		t.Errorf("deciders = %+v, want [%+v]", got, want)
	}
}

func TestAddDeciderToConfig_KeepsExplicitValues(t *testing.T) {
	path := deciderFile(t, "")

	d := DeciderConfig{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", Timeout: "2s", MaxInputTokens: 8000}
	if err := AddDeciderToConfig(path, d); err != nil {
		t.Fatalf("AddDeciderToConfig: %v", err)
	}

	if got := loadDeciders(t, path); len(got) != 1 || got[0] != d {
		t.Errorf("deciders = %+v, want [%+v]", got, d)
	}
}

func TestUpdateDeciderConfig_ChangesAndNilRestoresDefault(t *testing.T) {
	path := deciderFile(t, `
[[llm.deciders]]
name = "jev"
provider = "or"
model = "typesafe/jev-1.13"
timeout = "2s"
`)

	if err := UpdateDeciderConfig(path, "jev", map[string]any{"model": "typesafe/jev-2", "timeout": nil}); err != nil {
		t.Fatalf("UpdateDeciderConfig: %v", err)
	}

	got := loadDeciders(t, path)
	if len(got) != 1 || got[0].Model != "typesafe/jev-2" || got[0].Timeout != DefaultDeciderTimeout {
		t.Errorf("deciders = %+v, want model typesafe/jev-2 and the default timeout", got)
	}
}

func TestUpdateDeciderConfig_UnknownNameIsNotFound(t *testing.T) {
	path := deciderFile(t, "")

	err := UpdateDeciderConfig(path, "nope", map[string]any{"model": "m"})
	if !errors.Is(err, ErrDeciderNotFound) {
		t.Errorf("err = %v, want ErrDeciderNotFound", err)
	}
}

func TestRemoveDeciderFromConfig_LastEntryDropsTheKey(t *testing.T) {
	path := deciderFile(t, `
[[llm.deciders]]
name = "jev"
provider = "or"
model = "typesafe/jev-1.13"
`)

	if err := RemoveDeciderFromConfig(path, "jev"); err != nil {
		t.Fatalf("RemoveDeciderFromConfig: %v", err)
	}

	if raw := readTestConfig(t, path); strings.Contains(raw, "deciders") {
		t.Errorf("empty deciders key left behind:\n%s", raw)
	}
	if got := loadDeciders(t, path); len(got) != 0 {
		t.Errorf("deciders = %+v, want none", got)
	}
}

func TestValidateDecider_AppliesDefaultsAndChecksProvider(t *testing.T) {
	cfg, err := Parse(deciderConfig(""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := ValidateDecider(cfg, DeciderConfig{Name: "jev", Provider: "or", Model: "m"}); err != nil {
		t.Errorf("valid decider with omitted defaults rejected: %v", err)
	}
	if err := ValidateDecider(cfg, DeciderConfig{Name: "jev", Provider: "anthropic", Model: "m"}); err == nil || !strings.Contains(err.Error(), "does not serve decision models") {
		t.Errorf("err = %v, want a non-decision provider rejection", err)
	}
}

func TestValidateDecider_ProviderWithoutAPIKeyRejected(t *testing.T) {
	cfg, err := Parse(deciderConfig(""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cfg.LLM.Providers = append(cfg.LLM.Providers, ProviderInstanceConfig{Name: "or-nokey", Type: "openrouter"})

	err = ValidateDecider(cfg, DeciderConfig{Name: "jev", Provider: "or-nokey", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "no api_key") {
		t.Errorf("err = %v, want a missing api_key rejection", err)
	}
}

func TestDeciderReferrers_ListsEveryUse(t *testing.T) {
	cfg := &Config{
		Agents: []AgentInstanceConfig{{Name: "pamela", SupervisorDecider: "jev"}, {Name: "scout"}},
		Eval:   EvalConfig{JudgeDecider: "jev"},
		Decide: DecideConfig{Decider: "jev"},
	}

	got := DeciderReferrers(cfg, "jev")
	want := []string{"agent:pamela", "eval.judge_decider", "decide.decider"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("referrers = %v, want %v", got, want)
	}
	if refs := DeciderReferrers(cfg, "other"); len(refs) != 0 {
		t.Errorf("unused decider has referrers %v", refs)
	}
}
