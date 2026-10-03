package main

import (
	"log/slog"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/llm/llmfactory"
)

// liveProviders implements api.ProviderRuntime over the provider set that
// every agent router shares.
type liveProviders struct {
	set *llm.ProviderSet
}

func (l liveProviders) Apply(pc config.ProviderInstanceConfig, snap *config.Config) error {
	p, err := llmfactory.New(pc, snap.LLM.OpenRouter, nil)
	if err != nil {
		return err
	}
	l.set.Put(p)
	return nil
}

func (l liveProviders) Remove(name string) { l.set.Remove(name) }

// newCatalogRouter returns a router used only to list models across every
// registered provider. Unlike the dispatcher's lister it works with zero
// agents, which the setup wizard needs right after adding the first provider.
func newCatalogRouter(clients llmClients) *llm.Router {
	r := llm.NewRouterWithProviders("", "", clients.cost, clients.providers)
	if clients.pricing != nil {
		r.SetPricing(clients.pricing)
	}
	return r
}

// syncProviders makes the live set match cfg after a reload: every configured
// instance is rebuilt (picking up key or URL edits), and instances no longer
// in cfg are dropped.
func syncProviders(l liveProviders, cfg *config.Config) {
	keep := make(map[string]bool, len(cfg.LLM.Providers))
	for _, pc := range cfg.LLM.Providers {
		keep[pc.Name] = true
		if err := l.Apply(pc, cfg); err != nil {
			slog.Warn("reload: provider not rebuilt", "provider", pc.Name, "error", err)
		}
	}
	for _, name := range l.set.Names() {
		if !keep[name] {
			l.Remove(name)
		}
	}
}
