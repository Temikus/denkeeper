package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/security"
)

// namedTestEngine builds a minimal engine with the given name, permission tier
// and (optionally) persona, suitable for the read-only agent tools.
func namedTestEngine(t *testing.T, name, tier string, p *persona.Persona) *agent.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mem, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating memory: %v", err)
	}
	perms, err := security.NewPermissionEngine(tier)
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	router := llm.NewRouter("stub", "test-model", nil)
	router.RegisterProvider(stubLLM{})
	return agent.NewEngine(name, router, mem, nil, perms, p,
		"test agent", nil, nil, nil, logger)
}

// agentReadScope returns a context carrying the scope the agent tools require.
func agentReadScope(t *testing.T) context.Context {
	t.Helper()
	return withScopes(context.Background(), []string{"agents:read"})
}

// decodeToolJSON unmarshals a tool result's JSON payload into v.
func decodeToolJSON(t *testing.T, text string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), v); err != nil {
		t.Fatalf("decoding tool payload %q: %v", text, err)
	}
}

// supervisedServer wires a supervised agent whose supervisor is another agent.
func supervisedServer(t *testing.T) *Server {
	t.Helper()
	sup := namedTestEngine(t, "argus", "autonomous", nil)
	worker := namedTestEngine(t, "pamela", "supervised", nil)
	worker.SetSupervisor(sup)
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{
		"pamela": worker,
		"argus":  sup,
	}, nil, nil, testLogger())
	return &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}
}

func TestAgentInfo_IncludesSupervisor(t *testing.T) {
	s := supervisedServer(t)

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "pamela"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}

	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)

	if info["supervisor"] != "argus" {
		t.Errorf("expected supervisor \"argus\", got %v", info["supervisor"])
	}
	if info["permission_tier"] != "supervised" {
		t.Errorf("expected supervised tier, got %v", info["permission_tier"])
	}
}

func TestAgentInfo_OmitsSupervisorWhenUnset(t *testing.T) {
	s := supervisedServer(t)

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "argus"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)

	if _, ok := info["supervisor"]; ok {
		t.Errorf("supervisor key should be absent for an unsupervised agent, got %v", info["supervisor"])
	}
}

func TestAgentInfo_IncludesPersonaSections(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("I am a test agent."), 0o600); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	e := namedTestEngine(t, "withpersona", "autonomous", p)
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"withpersona": e}, nil, nil, testLogger())
	s := &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "withpersona"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)

	sections, ok := info["persona_sections"].(map[string]any)
	if !ok {
		t.Fatalf("expected persona_sections map, got %T", info["persona_sections"])
	}
	if sections["soul"] != true {
		t.Errorf("expected soul section to be present, got %v", sections["soul"])
	}
	if sections["memory"] != false {
		t.Errorf("expected memory section to be absent, got %v", sections["memory"])
	}
}

func TestAgentInfo_OmitsPersonaSectionsWhenNoPersona(t *testing.T) {
	s := supervisedServer(t)

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "argus"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)

	if _, ok := info["persona_sections"]; ok {
		t.Errorf("persona_sections should be absent without a persona, got %v", info["persona_sections"])
	}
}

func TestAgentInfo_IncludesChannelBindings(t *testing.T) {
	e := namedTestEngine(t, "pamela", "autonomous", nil)
	other := namedTestEngine(t, "argus", "autonomous", nil)
	channels := []*agent.Channel{
		{Name: "work", AgentName: "pamela", Adapters: []string{"telegram:1"}, Delivery: "broadcast"},
		{Name: "admin", AgentName: "pamela", Adapters: []string{"discord"}, Implicit: true},
		{Name: "ops", AgentName: "argus", Adapters: []string{"telegram:2"}},
	}
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{
		"pamela": e,
		"argus":  other,
	}, nil, nil, testLogger(), agent.WithChannels(channels, nil))
	s := &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "pamela"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var info struct {
		Channels []channelBinding `json:"channels"`
	}
	decodeToolJSON(t, toolResultText(res), &info)

	if len(info.Channels) != 2 {
		t.Fatalf("expected 2 channels for pamela, got %d: %+v", len(info.Channels), info.Channels)
	}
	// Sorted by name: admin before work.
	if info.Channels[0].Name != "admin" || info.Channels[1].Name != "work" {
		t.Errorf("expected channels sorted by name, got %+v", info.Channels)
	}
	if !info.Channels[0].Implicit {
		t.Error("expected admin channel to be marked implicit")
	}
	if info.Channels[1].Delivery != "broadcast" {
		t.Errorf("expected work channel delivery \"broadcast\", got %q", info.Channels[1].Delivery)
	}
	if len(info.Channels[1].Adapters) != 1 || info.Channels[1].Adapters[0] != "telegram:1" {
		t.Errorf("unexpected adapters for work channel: %v", info.Channels[1].Adapters)
	}
}

func TestAgentInfo_OmitsChannelsWhenNotConfigured(t *testing.T) {
	s := supervisedServer(t)

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "pamela"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)

	if _, ok := info["channels"]; ok {
		t.Errorf("channels should be absent when none are configured, got %v", info["channels"])
	}
}

func TestAgentInfo_AgentNotFound(t *testing.T) {
	s := supervisedServer(t)

	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: "nobody"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result for an unknown agent")
	}
}

func TestAgentInfo_RequiresScope(t *testing.T) {
	s := supervisedServer(t)

	res, _, err := s.handleAgentInfo(withScopes(context.Background(), []string{"chat"}), nil, agentInfoInput{Agent: "pamela"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected a scope error result")
	}
}

func TestAgentList_IncludesSupervisor(t *testing.T) {
	s := supervisedServer(t)

	res, _, err := s.handleAgentList(agentReadScope(t), nil, agentListInput{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}

	var agents []map[string]any
	decodeToolJSON(t, toolResultText(res), &agents)

	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	byName := make(map[string]map[string]any, len(agents))
	for _, a := range agents {
		name, _ := a["name"].(string)
		byName[name] = a
	}

	worker, ok := byName["pamela"]
	if !ok {
		t.Fatal("pamela missing from agent list")
	}
	if worker["supervisor"] != "argus" {
		t.Errorf("expected supervisor \"argus\", got %v", worker["supervisor"])
	}

	sup, ok := byName["argus"]
	if !ok {
		t.Fatal("argus missing from agent list")
	}
	if _, present := sup["supervisor"]; present {
		t.Errorf("supervisor key should be omitted for an unsupervised agent, got %v", sup["supervisor"])
	}
}

func TestSupervisorName_NoSupervisor(t *testing.T) {
	e := namedTestEngine(t, "solo", "autonomous", nil)
	if got := supervisorName(e); got != "" {
		t.Errorf("expected empty supervisor name, got %q", got)
	}
}

func TestAgentChannels_NilDispatcher(t *testing.T) {
	if got := agentChannels(nil, "pamela"); got != nil {
		t.Errorf("expected nil for a nil dispatcher, got %v", got)
	}
}

// deciderServer is supervisedServer with a decision model wired ahead of
// pamela's supervisor. The provider is never called by the read-only tools.
func deciderServer(t *testing.T, stage agent.DeciderStageConfig) (*Server, *agent.Engine) {
	t.Helper()
	s := supervisedServer(t)
	worker := s.deps.Dispatcher.Agent("pamela")
	d := llm.NewDecider(llm.DeciderConfig{Name: "jev", Model: "typesafe/jev-1.13"}, nil, nil)
	worker.SetSupervisorDecider(d, stage)
	return s, worker
}

func agentInfoMap(t *testing.T, s *Server, name string) map[string]any {
	t.Helper()
	res, _, err := s.handleAgentInfo(agentReadScope(t), nil, agentInfoInput{Agent: name})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}
	var info map[string]any
	decodeToolJSON(t, toolResultText(res), &info)
	return info
}

func TestAgentInfo_IncludesSupervisorDecider(t *testing.T) {
	s, _ := deciderServer(t, agent.DeciderStageConfig{Mode: "enforce", ApproveAt: 0.9, DenyAt: 0.1})

	got, ok := agentInfoMap(t, s, "pamela")["supervisor_decider"].(map[string]any)
	if !ok {
		t.Fatal("expected a supervisor_decider object")
	}
	want := map[string]any{"name": "jev", "model": "typesafe/jev-1.13", "mode": "enforce", "approve_at": 0.9, "deny_at": 0.1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("supervisor_decider.%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestAgentInfo_SupervisorDeciderUnknownModeReportsShadow(t *testing.T) {
	s, _ := deciderServer(t, agent.DeciderStageConfig{ApproveAt: 0.95, DenyAt: 0.05})

	got, _ := agentInfoMap(t, s, "pamela")["supervisor_decider"].(map[string]any)
	if got["mode"] != "shadow" {
		t.Errorf("an empty mode runs as shadow, so it must report shadow; got %v", got["mode"])
	}
}

func TestAgentInfo_SupervisorDeciderReflectsRetune(t *testing.T) {
	s, worker := deciderServer(t, agent.DeciderStageConfig{Mode: "shadow", ApproveAt: 0.95, DenyAt: 0.05})
	worker.SetSupervisorDeciderConfig(agent.DeciderStageConfig{Mode: "enforce", ApproveAt: 0.8, DenyAt: 0.2})

	got, _ := agentInfoMap(t, s, "pamela")["supervisor_decider"].(map[string]any)
	if got["mode"] != "enforce" || got["approve_at"] != 0.8 || got["deny_at"] != 0.2 {
		t.Errorf("expected the re-tuned stage, got %v", got)
	}
}

func TestAgentInfo_OmitsSupervisorDeciderWhenUnset(t *testing.T) {
	s, worker := deciderServer(t, agent.DeciderStageConfig{Mode: "shadow"})
	worker.SetSupervisorDecider(nil, agent.DeciderStageConfig{})

	if d, ok := agentInfoMap(t, s, "pamela")["supervisor_decider"]; ok {
		t.Errorf("supervisor_decider should be absent once unbound, got %v", d)
	}
}

func TestAgentList_IncludesSupervisorDecider(t *testing.T) {
	s, _ := deciderServer(t, agent.DeciderStageConfig{Mode: "shadow"})

	res, _, err := s.handleAgentList(agentReadScope(t), nil, agentListInput{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var agents []map[string]any
	decodeToolJSON(t, toolResultText(res), &agents)

	for _, a := range agents {
		switch a["name"] {
		case "pamela":
			if a["supervisor_decider"] != "jev" {
				t.Errorf("expected supervisor_decider \"jev\", got %v", a["supervisor_decider"])
			}
		case "argus":
			if d, ok := a["supervisor_decider"]; ok {
				t.Errorf("supervisor_decider should be omitted for argus, got %v", d)
			}
		}
	}
}
