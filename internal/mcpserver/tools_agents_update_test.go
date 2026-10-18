package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
)

func intPtr(n int) *int { return &n }

// agentUpdateServer wires one agent, "pamela", with a config file on disk
// and a live config whose llm.max_output_tokens is global.
func agentUpdateServer(t *testing.T, global int) (*Server, *agent.Engine, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denkeeper.toml")
	if err := os.WriteFile(path, []byte("[[agents]]\nname = \"pamela\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := namedTestEngine(t, "pamela", "supervised", nil)
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"pamela": e}, nil, nil, testLogger())
	holder := config.NewHolder(&config.Config{
		LLM:    config.LLMConfig{MaxOutputTokens: global},
		Agents: []config.AgentInstanceConfig{{Name: "pamela"}},
	})
	s := &Server{deps: Deps{Dispatcher: dispatcher, Config: holder, ConfigPath: path, Logger: testLogger()}}
	return s, e, path
}

func agentWriteScope() context.Context {
	return withScopes(context.Background(), []string{"agents:write"})
}

func TestAgentUpdate_RequiresWriteScope(t *testing.T) {
	s, e, _ := agentUpdateServer(t, 0)

	res, _, err := s.handleAgentUpdate(agentReadScope(t), nil, agentUpdateInput{Agent: "pamela", MaxOutputTokens: intPtr(16384)})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a scope error result")
	}
	if e.MaxOutputTokens() != 0 {
		t.Errorf("engine changed without scope: %d", e.MaxOutputTokens())
	}
}

func TestAgentUpdate_MaxOutputTokens_AppliesAndPersists(t *testing.T) {
	s, e, path := agentUpdateServer(t, 0)

	res, _, err := s.handleAgentUpdate(agentWriteScope(), nil, agentUpdateInput{Agent: "pamela", MaxOutputTokens: intPtr(16384)})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}
	if e.MaxOutputTokens() != 16384 {
		t.Errorf("engine max_output_tokens = %d, want 16384", e.MaxOutputTokens())
	}
	if got := s.deps.Config.Get().Agents[0].MaxOutputTokens; got != 16384 {
		t.Errorf("in-memory max_output_tokens = %d, want 16384", got)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "max_output_tokens = 16384") {
		t.Errorf("TOML not persisted:\n%s", saved)
	}

	var out map[string]any
	decodeToolJSON(t, toolResultText(res), &out)
	if out["persisted"] != true {
		t.Errorf("persisted = %v, want true", out["persisted"])
	}
	if out["max_output_tokens"] != float64(16384) {
		t.Errorf("max_output_tokens = %v, want 16384", out["max_output_tokens"])
	}
}

func TestAgentUpdate_MaxOutputTokens_ZeroInheritsGlobal(t *testing.T) {
	s, e, _ := agentUpdateServer(t, 8192)
	e.SetMaxOutputTokens(16384)

	res, _, err := s.handleAgentUpdate(agentWriteScope(), nil, agentUpdateInput{Agent: "pamela", MaxOutputTokens: intPtr(0)})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}
	if e.MaxOutputTokens() != 8192 {
		t.Errorf("engine max_output_tokens = %d, want global 8192", e.MaxOutputTokens())
	}
}

func TestAgentUpdate_MaxToolRounds(t *testing.T) {
	s, e, _ := agentUpdateServer(t, 0)

	res, _, err := s.handleAgentUpdate(agentWriteScope(), nil, agentUpdateInput{Agent: "pamela", MaxToolRounds: intPtr(25)})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}
	if e.MaxToolRounds() != 25 {
		t.Errorf("engine max_tool_rounds = %d, want 25", e.MaxToolRounds())
	}
}

func TestAgentUpdate_RejectsNegativeMaxOutputTokens(t *testing.T) {
	s, _, _ := agentUpdateServer(t, 0)

	res, _, _ := s.handleAgentUpdate(agentWriteScope(), nil, agentUpdateInput{Agent: "pamela", MaxOutputTokens: intPtr(-1)})
	if !res.IsError {
		t.Fatal("expected an error result for negative max_output_tokens")
	}
}

func TestAgentUpdate_RejectsZeroMaxToolRounds(t *testing.T) {
	s, _, _ := agentUpdateServer(t, 0)

	res, _, _ := s.handleAgentUpdate(agentWriteScope(), nil, agentUpdateInput{Agent: "pamela", MaxToolRounds: intPtr(0)})
	if !res.IsError {
		t.Fatal("expected an error result for max_tool_rounds = 0")
	}
}

func TestAgentUpdate_RejectsEmptyUpdate(t *testing.T) {
	s, _, _ := agentUpdateServer(t, 0)

	res, _, _ := s.handleAgentUpdate(agentWriteScope(), nil, agentUpdateInput{Agent: "pamela"})
	if !res.IsError {
		t.Fatal("expected an error result when no field is given")
	}
}

func TestAgentUpdate_UnknownAgent(t *testing.T) {
	s, _, _ := agentUpdateServer(t, 0)

	res, _, _ := s.handleAgentUpdate(agentWriteScope(), nil, agentUpdateInput{Agent: "nobody", MaxOutputTokens: intPtr(100)})
	if !res.IsError {
		t.Fatal("expected an error result for an unknown agent")
	}
}

func TestAgentInfo_IncludesOutputAndRoundLimits(t *testing.T) {
	s, e, _ := agentUpdateServer(t, 0)
	e.SetMaxOutputTokens(16384)

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "pamela"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)
	if info["max_output_tokens"] != float64(16384) {
		t.Errorf("max_output_tokens = %v, want 16384", info["max_output_tokens"])
	}
	if info["max_tool_rounds"] != float64(50) {
		t.Errorf("max_tool_rounds = %v, want 50", info["max_tool_rounds"])
	}
}

func TestAgentInfo_OmitsMaxOutputTokensWhenProviderDefault(t *testing.T) {
	s, _, _ := agentUpdateServer(t, 0)

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "pamela"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)
	if _, ok := info["max_output_tokens"]; ok {
		t.Errorf("max_output_tokens present at provider default: %v", info["max_output_tokens"])
	}
}
