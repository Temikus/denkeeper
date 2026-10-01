package main

import (
	"log/slog"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
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

// wiredDeciderEngine returns an engine with cfg's decider bound the way
// startup binds it.
func wiredDeciderEngine(t *testing.T, cfg *config.Config) *agent.Engine {
	t.Helper()
	e := testDispatcher(t, "default", nil).Agent("default")
	wireSupervisors(cfg.Agents, map[string]*agent.Engine{"default": e}, initLLMClients(cfg).deciders, slog.Default())
	if e.SupervisorDecider() == nil {
		t.Fatal("setup: decider not wired")
	}
	return e
}

func TestReconcileSupervisorDecider_RemovedUnbinds(t *testing.T) {
	cfg := deciderReloadConfig()
	e := wiredDeciderEngine(t, cfg)

	cfg.Agents[0].SupervisorDecider = ""
	reconcileSupervisorDecider(e, cfg.Agents[0], cfg, slog.Default())

	if d := e.SupervisorDecider(); d != nil {
		t.Errorf("decider %q still bound after removal", d.Name())
	}
}

func TestReconcileSupervisorDecider_RenamedUnbinds(t *testing.T) {
	cfg := deciderReloadConfig()
	e := wiredDeciderEngine(t, cfg)

	cfg.Agents[0].SupervisorDecider = "jev2"
	reconcileSupervisorDecider(e, cfg.Agents[0], cfg, slog.Default())

	if d := e.SupervisorDecider(); d != nil {
		t.Errorf("decider %q still bound after switching to jev2", d.Name())
	}
}

func TestReconcileSupervisorDecider_ModelChangedUnbinds(t *testing.T) {
	cfg := deciderReloadConfig()
	e := wiredDeciderEngine(t, cfg)

	cfg.LLM.Deciders[0].Model = "typesafe/jev-1.14"
	reconcileSupervisorDecider(e, cfg.Agents[0], cfg, slog.Default())

	if d := e.SupervisorDecider(); d != nil {
		t.Errorf("decider still bound to %q after its model changed", d.Model())
	}
}

func TestReconcileSupervisorDecider_ProviderChangedUnbinds(t *testing.T) {
	cfg := deciderReloadConfig()
	e := wiredDeciderEngine(t, cfg)

	cfg.LLM.Deciders[0].Provider = "or-other"
	reconcileSupervisorDecider(e, cfg.Agents[0], cfg, slog.Default())

	if e.SupervisorDecider() != nil {
		t.Error("decider still bound after its provider changed")
	}
}

func TestReconcileSupervisorDecider_SameDestinationKeepsBinding(t *testing.T) {
	cfg := deciderReloadConfig()
	e := wiredDeciderEngine(t, cfg)
	before := e.SupervisorDecider()

	cfg.Agents[0].SupervisorDeciderApproveAt = 0.9
	cfg.LLM.Deciders[0].Timeout = "10s"
	reconcileSupervisorDecider(e, cfg.Agents[0], cfg, slog.Default())

	if e.SupervisorDecider() != before {
		t.Error("re-tuning thresholds or timeout must keep the bound decider")
	}
}

func TestReconcileSupervisorDecider_AddedStaysUnbound(t *testing.T) {
	cfg := deciderReloadConfig()
	cfg.Agents[0].SupervisorDecider = ""
	e := testDispatcher(t, "default", nil).Agent("default")

	cfg.Agents[0].SupervisorDecider = "jev"
	reconcileSupervisorDecider(e, cfg.Agents[0], cfg, slog.Default())

	if e.SupervisorDecider() != nil {
		t.Error("binding a decider on reload must wait for a restart")
	}
}
