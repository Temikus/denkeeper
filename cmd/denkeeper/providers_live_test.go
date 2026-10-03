package main

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

func TestSyncProviders_AddsReplacesRemoves(t *testing.T) {
	set := llm.NewProviderSet()
	live := liveProviders{set: set}
	old := &config.Config{LLM: config.LLMConfig{Providers: []config.ProviderInstanceConfig{
		{Name: "keep", Type: "anthropic", APIKey: "old"},
		{Name: "gone", Type: "openai", APIKey: "k"},
	}}}
	syncProviders(live, old)
	before, _ := set.Get("keep")

	reloaded := &config.Config{LLM: config.LLMConfig{Providers: []config.ProviderInstanceConfig{
		{Name: "keep", Type: "anthropic", APIKey: "new"},
		{Name: "added", Type: "ollama"},
	}}}
	syncProviders(live, reloaded)

	if got := fmt.Sprint(set.Names()); got != "[added keep]" {
		t.Errorf("Names() = %s, want [added keep]", got)
	}
	if after, _ := set.Get("keep"); after == before {
		t.Error("keep was not rebuilt on reload, so a key edit would not apply")
	}
}

func TestReloadNewAgents_BuildsMissing(t *testing.T) {
	d := testDispatcher(t, "default", nil)
	cfg := &config.Config{Agents: []config.AgentInstanceConfig{
		{Name: "default"},
		{Name: "assistant", Supervisor: "checker"},
		{Name: "checker"},
	}}
	var built []string
	rt := reloadRuntime{buildAgent: func(ac config.AgentInstanceConfig) (*agent.Engine, []agent.Binding, error) {
		built = append(built, ac.Name)
		return testDispatcher(t, ac.Name, nil).Agent(ac.Name), nil, nil
	}}

	reloadNewAgents(cfg, d, rt, slog.Default())

	if fmt.Sprint(built) != "[assistant checker]" {
		t.Errorf("built = %v, want only the agents not already running", built)
	}
	a := d.Agent("assistant")
	if a == nil || d.Agent("checker") == nil {
		t.Fatal("new agents not registered with the dispatcher")
	}
	if a.Supervisor() != d.Agent("checker") {
		t.Error("supervisor not wired for an agent added on reload")
	}
}

func TestReloadNewAgents_BuildErrorSkipsAgent(t *testing.T) {
	d := testDispatcher(t, "default", nil)
	cfg := &config.Config{Agents: []config.AgentInstanceConfig{{Name: "default"}, {Name: "broken"}}}
	rt := reloadRuntime{buildAgent: func(config.AgentInstanceConfig) (*agent.Engine, []agent.Binding, error) {
		return nil, nil, errors.New("boom")
	}}

	reloadNewAgents(cfg, d, rt, slog.Default())

	if d.Agent("broken") != nil {
		t.Error("an agent that failed to build was registered")
	}
}
