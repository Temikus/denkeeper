package pricing

import (
	"math"
	"testing"
)

func TestRegistry_ExactMatch(t *testing.T) {
	r := NewEmpty()
	r.Register("my-model-v1", ModelPrice{InputPerMTok: 10.0, OutputPerMTok: 30.0})

	p, ok := r.Lookup("my-model-v1")
	if !ok {
		t.Fatal("expected exact match")
	}
	if p.InputPerMTok != 10.0 || p.OutputPerMTok != 30.0 {
		t.Errorf("got %+v", p)
	}
}

func TestRegistry_PrefixMatch(t *testing.T) {
	r := NewEmpty()
	r.RegisterPrefix("claude-3-opus", ModelPrice{InputPerMTok: 15.0, OutputPerMTok: 75.0})

	p, ok := r.Lookup("claude-3-opus-20240229")
	if !ok {
		t.Fatal("expected prefix match")
	}
	if p.InputPerMTok != 15.0 {
		t.Errorf("InputPerMTok = %f, want 15.0", p.InputPerMTok)
	}
}

func TestRegistry_LongestPrefixWins(t *testing.T) {
	r := NewEmpty()
	r.RegisterPrefix("claude-3", ModelPrice{InputPerMTok: 3.0, OutputPerMTok: 15.0})
	r.RegisterPrefix("claude-3-opus", ModelPrice{InputPerMTok: 15.0, OutputPerMTok: 75.0})

	p, ok := r.Lookup("claude-3-opus-20240229")
	if !ok {
		t.Fatal("expected prefix match")
	}
	if p.InputPerMTok != 15.0 {
		t.Errorf("InputPerMTok = %f, want 15.0 (longest prefix)", p.InputPerMTok)
	}
}

func TestRegistry_ExactBeatsPrefix(t *testing.T) {
	r := NewEmpty()
	r.RegisterPrefix("claude-3-opus", ModelPrice{InputPerMTok: 15.0, OutputPerMTok: 75.0})
	r.Register("claude-3-opus-20240229", ModelPrice{InputPerMTok: 12.0, OutputPerMTok: 60.0})

	p, ok := r.Lookup("claude-3-opus-20240229")
	if !ok {
		t.Fatal("expected exact match")
	}
	if p.InputPerMTok != 12.0 {
		t.Errorf("InputPerMTok = %f, want 12.0 (exact match)", p.InputPerMTok)
	}
}

func TestRegistry_Miss(t *testing.T) {
	r := NewEmpty()
	r.RegisterPrefix("claude-3", ModelPrice{InputPerMTok: 3.0, OutputPerMTok: 15.0})

	_, ok := r.Lookup("gpt-4o")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestRegistry_Cost_ProviderReported(t *testing.T) {
	r := NewEmpty()
	cost, source := r.Cost("", "any-model", 1000, 500, 0, 0.42)
	if cost != 0.42 {
		t.Errorf("cost = %f, want 0.42", cost)
	}
	if source != "provider" {
		t.Errorf("source = %q, want %q", source, "provider")
	}
}

func TestRegistry_Cost_RegistryLookup(t *testing.T) {
	r := NewEmpty()
	// $10/M input, $30/M output
	r.RegisterPrefix("test-model", ModelPrice{InputPerMTok: 10.0, OutputPerMTok: 30.0})

	// 1000 input + 500 output = $0.01 + $0.015 = $0.025
	cost, source := r.Cost("", "test-model-v1", 1000, 500, 0, 0)
	want := 1000.0/1_000_000*10.0 + 500.0/1_000_000*30.0
	if !approxEqual(cost, want) {
		t.Errorf("cost = %f, want %f", cost, want)
	}
	if source != "registry" {
		t.Errorf("source = %q, want %q", source, "registry")
	}
}

func TestRegistry_Cost_WithCachedTokens(t *testing.T) {
	r := NewEmpty()
	// $10/M input, $30/M output, $1/M cached
	r.RegisterPrefix("test-model", ModelPrice{InputPerMTok: 10.0, OutputPerMTok: 30.0, CachedInputPerMTok: 1.0})

	// 800 input + 200 cached + 500 output
	cost, source := r.Cost("", "test-model-v1", 800, 500, 200, 0)
	want := 800.0/1_000_000*10.0 + 500.0/1_000_000*30.0 + 200.0/1_000_000*1.0
	if !approxEqual(cost, want) {
		t.Errorf("cost = %f, want %f", cost, want)
	}
	if source != "registry" {
		t.Errorf("source = %q, want %q", source, "registry")
	}
}

func TestRegistry_Cost_CachedFallsBackToInputRate(t *testing.T) {
	r := NewEmpty()
	// CachedInputPerMTok = 0 → uses InputPerMTok
	r.RegisterPrefix("test-model", ModelPrice{InputPerMTok: 10.0, OutputPerMTok: 30.0})

	cost, _ := r.Cost("", "test-model-v1", 800, 500, 200, 0)
	want := (800.0+200.0)/1_000_000*10.0 + 500.0/1_000_000*30.0
	if !approxEqual(cost, want) {
		t.Errorf("cost = %f, want %f (cached at input rate)", cost, want)
	}
}

func TestRegistry_Cost_Fallback(t *testing.T) {
	r := NewEmpty()
	r.SetFallbackRate(0.01) // $0.01 per 1K tokens

	cost, source := r.Cost("", "unknown-model", 1000, 500, 0, 0)
	want := 1500.0 / 1000.0 * 0.01
	if !approxEqual(cost, want) {
		t.Errorf("cost = %f, want %f", cost, want)
	}
	if source != "fallback" {
		t.Errorf("source = %q, want %q", source, "fallback")
	}
}

func TestRegistry_Cost_Unknown(t *testing.T) {
	r := NewEmpty()
	// No fallback rate set
	cost, source := r.Cost("", "unknown-model", 1000, 500, 0, 0)
	if cost != 0 {
		t.Errorf("cost = %f, want 0", cost)
	}
	if source != "unknown" {
		t.Errorf("source = %q, want %q", source, "unknown")
	}
}

func TestRegistry_RegisterPrefixOverwrite(t *testing.T) {
	r := NewEmpty()
	r.RegisterPrefix("claude-3", ModelPrice{InputPerMTok: 3.0, OutputPerMTok: 15.0})
	r.RegisterPrefix("claude-3", ModelPrice{InputPerMTok: 5.0, OutputPerMTok: 25.0})

	p, ok := r.Lookup("claude-3-sonnet")
	if !ok {
		t.Fatal("expected prefix match")
	}
	if p.InputPerMTok != 5.0 {
		t.Errorf("InputPerMTok = %f, want 5.0 (overwritten)", p.InputPerMTok)
	}
}

func TestNew_HasDefaults(t *testing.T) {
	r := New()

	tests := []struct {
		model string
		input float64
	}{
		{"claude-opus-4-20250514", 15.0},
		{"claude-sonnet-4-20250514", 3.0},
		{"gpt-4o-2024-05-13", 2.50},
		{"o3-mini-2025-01-31", 1.10},
		{"anthropic/claude-sonnet-4-20250514", 3.0},
		{"openai/gpt-4o-mini-2024-07-18", 0.15},
	}

	for _, tt := range tests {
		p, ok := r.Lookup(tt.model)
		if !ok {
			t.Errorf("Lookup(%q): expected match", tt.model)
			continue
		}
		if p.InputPerMTok != tt.input {
			t.Errorf("Lookup(%q).InputPerMTok = %f, want %f", tt.model, p.InputPerMTok, tt.input)
		}
	}
}

// --- Per-provider override tests ---

func TestRegistry_ProviderOverride_ExactMatch(t *testing.T) {
	r := NewEmpty()
	r.Register("my-model", ModelPrice{InputPerMTok: 10.0, OutputPerMTok: 30.0})
	r.RegisterProviderModel("cheap-host", "my-model", ModelPrice{InputPerMTok: 2.0, OutputPerMTok: 6.0})

	// With provider name: override wins.
	cost, source := r.Cost("cheap-host", "my-model", 1_000_000, 0, 0, 0)
	if !approxEqual(cost, 2.0) {
		t.Errorf("provider override cost = %f, want 2.0", cost)
	}
	if source != "registry" {
		t.Errorf("source = %q, want %q", source, "registry")
	}

	// Without provider name: global wins.
	cost, _ = r.Cost("", "my-model", 1_000_000, 0, 0, 0)
	if !approxEqual(cost, 10.0) {
		t.Errorf("global cost = %f, want 10.0", cost)
	}
}

func TestRegistry_ProviderOverride_FallsBackToGlobal(t *testing.T) {
	r := NewEmpty()
	r.Register("known-model", ModelPrice{InputPerMTok: 5.0, OutputPerMTok: 15.0})
	r.RegisterProviderModel("my-provider", "other-model", ModelPrice{InputPerMTok: 1.0, OutputPerMTok: 3.0})

	// Model not in provider sub-registry → falls through to global.
	cost, source := r.Cost("my-provider", "known-model", 1_000_000, 0, 0, 0)
	if !approxEqual(cost, 5.0) {
		t.Errorf("fallback-to-global cost = %f, want 5.0", cost)
	}
	if source != "registry" {
		t.Errorf("source = %q, want %q", source, "registry")
	}
}

func TestRegistry_ProviderOverride_CrossProviderIsolation(t *testing.T) {
	r := NewEmpty()
	r.RegisterProviderModel("provider-a", "shared-model", ModelPrice{InputPerMTok: 1.0, OutputPerMTok: 3.0})

	// Provider B has no override for shared-model → miss (no global either).
	cost, source := r.Cost("provider-b", "shared-model", 1_000_000, 0, 0, 0)
	if cost != 0 {
		t.Errorf("isolated cost = %f, want 0 (unknown)", cost)
	}
	if source != "unknown" {
		t.Errorf("source = %q, want %q", source, "unknown")
	}
}

func TestRegistry_ProviderOverride_FallbackRate(t *testing.T) {
	r := NewEmpty()
	r.SetFallbackRate(0.01)
	r.SetProviderFallbackRate("cheap", 0.001)

	// Provider fallback rate used before global.
	cost, source := r.Cost("cheap", "unknown-model", 1000, 0, 0, 0)
	want := 1.0 * 0.001
	if !approxEqual(cost, want) {
		t.Errorf("provider fallback cost = %f, want %f", cost, want)
	}
	if source != "fallback" {
		t.Errorf("source = %q, want %q", source, "fallback")
	}

	// Different provider uses global fallback.
	cost, _ = r.Cost("other", "unknown-model", 1000, 0, 0, 0)
	want = 1.0 * 0.01
	if !approxEqual(cost, want) {
		t.Errorf("global fallback cost = %f, want %f", cost, want)
	}
}

func TestRegistry_ProviderOverride_PrefixMatch(t *testing.T) {
	r := NewEmpty()
	r.RegisterPrefix("claude-3", ModelPrice{InputPerMTok: 3.0, OutputPerMTok: 15.0})
	r.RegisterProviderPrefix("my-host", "claude-3", ModelPrice{InputPerMTok: 1.0, OutputPerMTok: 5.0})

	cost, _ := r.Cost("my-host", "claude-3-opus-20240229", 1_000_000, 0, 0, 0)
	if !approxEqual(cost, 1.0) {
		t.Errorf("provider prefix cost = %f, want 1.0", cost)
	}
}

func TestRegistry_Cost_EmptyProviderName(t *testing.T) {
	r := NewEmpty()
	r.Register("model-a", ModelPrice{InputPerMTok: 5.0, OutputPerMTok: 15.0})
	r.RegisterProviderModel("prov", "model-a", ModelPrice{InputPerMTok: 1.0, OutputPerMTok: 3.0})

	// Empty provider name skips override layer entirely.
	cost, _ := r.Cost("", "model-a", 1_000_000, 0, 0, 0)
	if !approxEqual(cost, 5.0) {
		t.Errorf("empty-provider cost = %f, want 5.0 (global)", cost)
	}
}

func TestRegistry_LookupForProvider(t *testing.T) {
	r := NewEmpty()
	r.Register("model-a", ModelPrice{InputPerMTok: 10.0, OutputPerMTok: 30.0})
	r.RegisterProviderModel("prov", "model-a", ModelPrice{InputPerMTok: 2.0, OutputPerMTok: 6.0})

	p, ok := r.LookupForProvider("prov", "model-a")
	if !ok {
		t.Fatal("expected match")
	}
	if p.InputPerMTok != 2.0 {
		t.Errorf("provider lookup input = %f, want 2.0", p.InputPerMTok)
	}

	// Unknown provider falls back to global.
	p, ok = r.LookupForProvider("other", "model-a")
	if !ok {
		t.Fatal("expected global fallback match")
	}
	if p.InputPerMTok != 10.0 {
		t.Errorf("global fallback input = %f, want 10.0", p.InputPerMTok)
	}
}

func TestRegistry_ProviderCost_StillUsesProviderReported(t *testing.T) {
	r := NewEmpty()
	r.RegisterProviderModel("prov", "model", ModelPrice{InputPerMTok: 10.0, OutputPerMTok: 30.0})

	// Provider-reported cost always wins, even with overrides.
	cost, source := r.Cost("prov", "model", 1000, 500, 0, 0.42)
	if cost != 0.42 {
		t.Errorf("cost = %f, want 0.42", cost)
	}
	if source != "provider" {
		t.Errorf("source = %q, want %q", source, "provider")
	}
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}
