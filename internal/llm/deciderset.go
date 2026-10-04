package llm

import (
	"sort"
	"sync"
)

// DeciderSet is a concurrency-safe registry of live deciders keyed by name.
// Consumers look a decider up when they bind it, and Sync rebuilds the set
// from config, so a decider added or changed at runtime needs no restart.
type DeciderSet struct {
	mu        sync.RWMutex
	deciders  map[string]*Decider
	providers *ProviderSet
	costs     *CostTracker
}

// NewDeciderSet returns an empty set whose deciders resolve their provider in
// providers and bill to costs (either may be nil).
func NewDeciderSet(providers *ProviderSet, costs *CostTracker) *DeciderSet {
	return &DeciderSet{deciders: make(map[string]*Decider), providers: providers, costs: costs}
}

// Get returns the decider registered under name, or nil.
func (s *DeciderSet) Get(name string) *Decider {
	if s == nil || name == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deciders[name]
}

// Put registers d under d.Name(), replacing any decider of that name.
func (s *DeciderSet) Put(d *Decider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deciders[d.Name()] = d
}

// Names returns the registered names, sorted.
func (s *DeciderSet) Names() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.deciders))
	for n := range s.deciders {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Sync makes the set match cfgs. An entry whose config is unchanged keeps its
// decider, so consumers can tell "nothing changed" by pointer equality; any
// other entry gets a new live decider, and names not in cfgs are dropped.
func (s *DeciderSet) Sync(cfgs []DeciderConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]*Decider, len(cfgs))
	for _, cfg := range cfgs {
		if cur := s.deciders[cfg.Name]; cur != nil && cur.cfg == cfg && cur.providers == s.providers {
			next[cfg.Name] = cur
			continue
		}
		next[cfg.Name] = NewLiveDecider(cfg, s.providers, s.costs)
	}
	s.deciders = next
}
