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
	cost, source := r.Cost("any-model", 1000, 500, 0, 0.42)
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
	cost, source := r.Cost("test-model-v1", 1000, 500, 0, 0)
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
	cost, source := r.Cost("test-model-v1", 800, 500, 200, 0)
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

	cost, _ := r.Cost("test-model-v1", 800, 500, 200, 0)
	want := (800.0+200.0)/1_000_000*10.0 + 500.0/1_000_000*30.0
	if !approxEqual(cost, want) {
		t.Errorf("cost = %f, want %f (cached at input rate)", cost, want)
	}
}

func TestRegistry_Cost_Fallback(t *testing.T) {
	r := NewEmpty()
	r.SetFallbackRate(0.01) // $0.01 per 1K tokens

	cost, source := r.Cost("unknown-model", 1000, 500, 0, 0)
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
	cost, source := r.Cost("unknown-model", 1000, 500, 0, 0)
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

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}
