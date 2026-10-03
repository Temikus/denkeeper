package api

import "github.com/Temikus/denkeeper/internal/config"

// ProviderRuntime keeps the live LLM provider set in step with provider
// edits, so a provider created, changed or deleted through the API is usable
// by every agent without a restart. Implemented in cmd/denkeeper over the
// shared llm.ProviderSet.
type ProviderRuntime interface {
	// Apply builds pc's client from snap (which carries the openrouter knobs)
	// and puts it in the live set, replacing any provider of the same name.
	// Per-provider pricing overrides still take effect only on restart.
	Apply(pc config.ProviderInstanceConfig, snap *config.Config) error
	// Remove drops name from the live set.
	Remove(name string)
}

// applyProviderLive pushes the named provider from snap into the live set.
// It reports whether the change is live; false means a restart is needed.
func (s *Server) applyProviderLive(name string, snap *config.Config) bool {
	if s.deps.Providers == nil {
		return false
	}
	pc := findProviderIn(snap, name)
	if pc == nil {
		return false
	}
	if err := s.deps.Providers.Apply(*pc, snap); err != nil {
		s.logger.Warn("provider saved but not applied live", "provider", name, "error", err)
		return false
	}
	return true
}
