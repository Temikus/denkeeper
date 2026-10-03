package llm

import (
	"sort"
	"sync"
)

// ProviderSet is a concurrency-safe registry of providers keyed by name.
// Routers built with NewRouterWithProviders share one set, so a provider
// added or replaced at runtime (the providers API, a config reload) is
// visible to every agent on its next request.
type ProviderSet struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewProviderSet returns an empty set.
func NewProviderSet() *ProviderSet {
	return &ProviderSet{providers: make(map[string]Provider)}
}

// Get returns the provider registered under name.
func (s *ProviderSet) Get(name string) (Provider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[name]
	return p, ok
}

// Put registers p under p.Name(), replacing any provider of that name.
// A request already holding the old provider finishes on it.
func (s *ProviderSet) Put(p Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[p.Name()] = p
}

// Remove unregisters name. Removing an absent name is a no-op.
func (s *ProviderSet) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.providers, name)
}

// Names returns the registered names, sorted.
func (s *ProviderSet) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.providers))
	for n := range s.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Snapshot returns a copy of the registry, safe to range over while the set
// changes.
func (s *ProviderSet) Snapshot() map[string]Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Provider, len(s.providers))
	for n, p := range s.providers {
		out[n] = p
	}
	return out
}
