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
	exact        map[string]ModelPrice
	prefixes     []prefixEntry // sorted longest-first for greedy matching
	fallbackRate float64       // per-1K-token rate; 0 = no fallback
}

type prefixEntry struct {
	prefix string
	price  ModelPrice
}

// New creates a Registry pre-loaded with bundled defaults.
func New() *Registry {
	r := &Registry{exact: make(map[string]ModelPrice)}
	r.loadDefaults()
	return r
}

// NewEmpty creates a Registry with no bundled defaults.
func NewEmpty() *Registry {
	return &Registry{exact: make(map[string]ModelPrice)}
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
// Returns the cost and a source string: "provider" if providerCost > 0,
// "registry" if the model was found, "fallback" if the default rate was
// used, or "unknown" if no pricing is available.
func (r *Registry) Cost(model string, input, output, cachedInput int, providerCost float64) (float64, string) {
	if providerCost > 0 {
		return providerCost, "provider"
	}

	if p, ok := r.Lookup(model); ok {
		cachedRate := p.CachedInputPerMTok
		if cachedRate == 0 {
			cachedRate = p.InputPerMTok
		}
		cost := float64(input)/1_000_000*p.InputPerMTok +
			float64(output)/1_000_000*p.OutputPerMTok +
			float64(cachedInput)/1_000_000*cachedRate
		return cost, "registry"
	}

	if r.fallbackRate > 0 {
		total := input + output + cachedInput
		return float64(total) / 1000.0 * r.fallbackRate, "fallback"
	}

	return 0, "unknown"
}
