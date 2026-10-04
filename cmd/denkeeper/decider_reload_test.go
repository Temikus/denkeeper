package main

import (
	"log/slog"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

func deciderReloadConfig() *config.Config {
	return &config.Config{
		LLM: config.LLMConfig{
			Providers: []config.ProviderInstanceConfig{
				{Name: "or", Type: "openrouter", APIKey: "test-key"},
			},
			Deciders: []config.DeciderConfig{
				{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", Timeout: "5s"},
				{Name: "jev2", Provider: "or", Model: "typesafe/jev-2", Timeout: "5s"},
			},
		},
		Agents: []config.AgentInstanceConfig{{
			Name:                       "default",
			SupervisorDecider:          "jev",
			SupervisorDeciderMode:      "shadow",
			SupervisorDeciderApproveAt: 0.95,
			SupervisorDeciderDenyAt:    0.05,
		}},
	}
}

// deciderFixture is a running "default" agent with cfg's decider bound the way
// startup binds it.
type deciderFixture struct {
	dispatcher *agent.Dispatcher
	clients    llmClients
}

func newDeciderFixture(t *testing.T, cfg *config.Config) deciderFixture {
	t.Helper()
	f := deciderFixture{dispatcher: testDispatcher(t, "default", nil), clients: initLLMClients(cfg)}
	wireSupervisors(cfg.Agents, map[string]*agent.Engine{"default": f.engine()}, f.clients.deciders, slog.Default())
	return f
}

func (f deciderFixture) engine() *agent.Engine { return f.dispatcher.Agent("default") }

// reload applies cfg the way buildReloadFunc does for providers and deciders.
func (f deciderFixture) reload(cfg *config.Config) {
	syncProviders(liveProviders{set: f.clients.providers}, cfg)
	syncDeciders(cfg, f.clients.deciders, f.dispatcher, slog.Default())
}

func TestSyncDeciders_RemovedUnbinds(t *testing.T) {
	cfg := deciderReloadConfig()
	f := newDeciderFixture(t, cfg)

	cfg.Agents[0].SupervisorDecider = ""
	f.reload(cfg)

	if d := f.engine().SupervisorDecider(); d != nil {
		t.Errorf("decider %q still bound after removal", d.Name())
	}
}

func TestSyncDeciders_SwitchedRebinds(t *testing.T) {
	cfg := deciderReloadConfig()
	f := newDeciderFixture(t, cfg)

	cfg.Agents[0].SupervisorDecider = "jev2"
	f.reload(cfg)

	if d := f.engine().SupervisorDecider(); d == nil || d.Name() != "jev2" {
		t.Errorf("bound decider = %v, want jev2", d)
	}
}

func TestSyncDeciders_ModelChangedRebinds(t *testing.T) {
	cfg := deciderReloadConfig()
	f := newDeciderFixture(t, cfg)

	cfg.LLM.Deciders[0].Model = "typesafe/jev-1.14"
	f.reload(cfg)

	if d := f.engine().SupervisorDecider(); d == nil || d.Model() != "typesafe/jev-1.14" {
		t.Errorf("bound decider = %v, want jev on typesafe/jev-1.14", d)
	}
}

func TestSyncDeciders_ProviderChangedRebinds(t *testing.T) {
	cfg := deciderReloadConfig()
	cfg.LLM.Providers = append(cfg.LLM.Providers, config.ProviderInstanceConfig{Name: "or-other", Type: "openrouter", APIKey: "k2"})
	f := newDeciderFixture(t, cfg)

	cfg.LLM.Deciders[0].Provider = "or-other"
	f.reload(cfg)

	if d := f.engine().SupervisorDecider(); d == nil || d.Provider() != "or-other" {
		t.Errorf("bound decider = %v, want jev via or-other", d)
	}
}

func TestSyncDeciders_UnchangedKeepsBinding(t *testing.T) {
	cfg := deciderReloadConfig()
	f := newDeciderFixture(t, cfg)
	before := f.engine().SupervisorDecider()

	cfg.Agents[0].SupervisorDeciderApproveAt = 0.9
	f.reload(cfg)

	if f.engine().SupervisorDecider() != before {
		t.Error("re-tuning thresholds must keep the bound decider")
	}
}

// A rotated key rebuilds the provider but not the decider: the bound decider
// looks its provider up per call, so it reaches the new client unchanged.
func TestSyncDeciders_KeyRotationKeepsBinding(t *testing.T) {
	cfg := deciderReloadConfig()
	f := newDeciderFixture(t, cfg)
	before := f.engine().SupervisorDecider()
	oldProvider, _ := f.clients.providers.Get("or")

	cfg.LLM.Providers[0].APIKey = "rotated-key"
	f.reload(cfg)

	if newProvider, _ := f.clients.providers.Get("or"); newProvider == oldProvider {
		t.Fatal("setup: provider was not rebuilt")
	}
	if f.engine().SupervisorDecider() != before {
		t.Error("a key rotation must not rebind the decider")
	}
}

func TestSyncDeciders_AddedBindsOnReload(t *testing.T) {
	cfg := deciderReloadConfig()
	cfg.Agents[0].SupervisorDecider = ""
	cfg.LLM.Deciders = cfg.LLM.Deciders[1:]
	f := newDeciderFixture(t, cfg)

	cfg.LLM.Deciders = append(cfg.LLM.Deciders, config.DeciderConfig{Name: "fresh", Provider: "or", Model: "typesafe/jev-1.13", Timeout: "5s"})
	cfg.Agents[0].SupervisorDecider = "fresh"
	f.reload(cfg)

	if d := f.engine().SupervisorDecider(); d == nil || d.Name() != "fresh" {
		t.Errorf("bound decider = %v, want fresh without a restart", d)
	}
}

func TestJudgeConfigFrom_ReadsTheLiveSet(t *testing.T) {
	cfg := deciderReloadConfig()
	cfg.Eval.JudgeDecider = "jev2"
	cfg.Eval.JudgeDeciderTimeout = "60s"
	set := llm.NewDeciderSet(nil, nil)

	if jc := judgeConfigFrom(cfg, set, slog.Default()); jc.Decider != nil {
		t.Fatal("judge decider bound before the set holds it")
	}
	set.Sync(deciderConfigs(cfg))
	if jc := judgeConfigFrom(cfg, set, slog.Default()); jc.Decider == nil || jc.Decider.Name() != "jev2" {
		t.Errorf("judge decider = %v, want jev2 from the live set", jc.Decider)
	}
}
