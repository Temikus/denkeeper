package pricing

import (
	"sort"
	"strings"
)

// ModelPrice holds per-million-token pricing for a model.
type ModelPrice struct {
	InputPerMTok       float64 // cost per million input tokens
	OutputPerMTok      float64 // cost per million output tokens
	CachedInputPerMTok float64 // cost per million cached input tokens; 0 means same as InputPerMTok
}

// Registry maps model names/prefixes to their pricing.
type Registry struct {
	exact             map[string]ModelPrice
	prefixes          []prefixEntry        // sorted longest-first for greedy matching
	fallbackRate      float64              // per-1K-token rate; 0 = no fallback
	providerOverrides map[string]*Registry // per-provider sub-registries
}

type prefixEntry struct {
	prefix string
	price  ModelPrice
}

// New creates a Registry pre-loaded with bundled defaults.
func New() *Registry {
	r := &Registry{
		exact:             make(map[string]ModelPrice),
		providerOverrides: make(map[string]*Registry),
	}
	r.loadDefaults()
	return r
}

// NewEmpty creates a Registry with no bundled defaults.
func NewEmpty() *Registry {
	return &Registry{
		exact:             make(map[string]ModelPrice),
		providerOverrides: make(map[string]*Registry),
	}
}

// Register adds an exact-match model price.
func (r *Registry) Register(model string, price ModelPrice) {
	r.exact[model] = price
}

// RegisterPrefix adds a prefix-match model price. Longer prefixes take
// priority. Duplicate prefixes are overwritten.
func (r *Registry) RegisterPrefix(prefix string, price ModelPrice) {
	for i, e := range r.prefixes {
		if e.prefix == prefix {
			r.prefixes[i].price = price
			return
		}
	}
	r.prefixes = append(r.prefixes, prefixEntry{prefix: prefix, price: price})
	sort.Slice(r.prefixes, func(i, j int) bool {
		return len(r.prefixes[i].prefix) > len(r.prefixes[j].prefix)
	})
}

// SetFallbackRate sets the default rate per 1K tokens used when no model
// match is found. Set to 0 to disable the fallback (cost will be 0).
func (r *Registry) SetFallbackRate(ratePerKTokens float64) {
	r.fallbackRate = ratePerKTokens
}

// RegisterProviderModel adds an exact-match model price to a per-provider
// sub-registry. The sub-registry is created on first use.
func (r *Registry) RegisterProviderModel(provider, model string, price ModelPrice) {
	r.providerRegistry(provider).Register(model, price)
}

// RegisterProviderPrefix adds a prefix-match model price to a per-provider
// sub-registry.
func (r *Registry) RegisterProviderPrefix(provider, prefix string, price ModelPrice) {
	r.providerRegistry(provider).RegisterPrefix(prefix, price)
}

// SetProviderFallbackRate sets the per-provider fallback rate ($/1K tokens).
func (r *Registry) SetProviderFallbackRate(provider string, rate float64) {
	r.providerRegistry(provider).SetFallbackRate(rate)
}

func (r *Registry) providerRegistry(provider string) *Registry {
	sub, ok := r.providerOverrides[provider]
	if !ok {
		sub = NewEmpty()
		r.providerOverrides[provider] = sub
	}
	return sub
}

// LookupForProvider returns the pricing for a model, checking the provider
// sub-registry first, then falling back to the global registry.
func (r *Registry) LookupForProvider(provider, model string) (ModelPrice, bool) {
	if provider != "" {
		if sub, ok := r.providerOverrides[provider]; ok {
			if p, found := sub.Lookup(model); found {
				return p, true
			}
		}
	}
	return r.Lookup(model)
}

// Lookup returns the pricing for a model. Exact match is tried first,
// then longest prefix match.
func (r *Registry) Lookup(model string) (ModelPrice, bool) {
	if p, ok := r.exact[model]; ok {
		return p, true
	}
	for _, e := range r.prefixes {
		if strings.HasPrefix(model, e.prefix) {
			return e.price, true
		}
	}
	return ModelPrice{}, false
}

// Cost calculates the USD cost for a given model and token usage.
// providerName selects the per-provider override layer (empty string skips it).
// Resolution order:
//  1. providerCost > 0 → "provider"
//  2. provider sub-registry (exact → prefix)
//  3. global registry (exact → prefix)
//  4. provider sub-registry fallback rate
//  5. global fallback rate
//  6. 0 / "unknown"
func (r *Registry) Cost(providerName, model string, input, output, cachedInput int, providerCost float64) (float64, string) {
	if providerCost > 0 {
		return providerCost, "provider"
	}

	// Provider sub-registry lookup.
	if providerName != "" {
		if sub, ok := r.providerOverrides[providerName]; ok {
			if p, found := sub.Lookup(model); found {
				return computeCost(p, input, output, cachedInput), "registry"
			}
		}
	}

	// Global registry lookup.
	if p, ok := r.Lookup(model); ok {
		return computeCost(p, input, output, cachedInput), "registry"
	}

	// Provider sub-registry fallback rate.
	if providerName != "" {
		if sub, ok := r.providerOverrides[providerName]; ok && sub.fallbackRate > 0 {
			total := input + output + cachedInput
			return float64(total) / 1000.0 * sub.fallbackRate, "fallback"
		}
	}

	// Global fallback rate.
	if r.fallbackRate > 0 {
		total := input + output + cachedInput
		return float64(total) / 1000.0 * r.fallbackRate, "fallback"
	}

	return 0, "unknown"
}

func computeCost(p ModelPrice, input, output, cachedInput int) float64 {
	cachedRate := p.CachedInputPerMTok
	if cachedRate == 0 {
		cachedRate = p.InputPerMTok
	}
	return float64(input)/1_000_000*p.InputPerMTok +
		float64(output)/1_000_000*p.OutputPerMTok +
		float64(cachedInput)/1_000_000*cachedRate
}
