package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/persona"
)

// personaServer wires one agent, "pamela", whose persona is loaded from a temp
// directory holding SOUL.md and MEMORY.md, and returns that directory.
func personaServer(t *testing.T) (*Server, *persona.Persona, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"SOUL.md": "I am Pamela.", "MEMORY.md": "Old memory."} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("persona.Load: %v", err)
	}
	e := namedTestEngine(t, "pamela", "supervised", p)
	bare := namedTestEngine(t, "bare", "supervised", nil)
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"pamela": e, "bare": bare}, nil, nil, testLogger())
	return &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}, p, dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestPersonaGet_ReturnsSection(t *testing.T) {
	s, _, _ := personaServer(t)

	res, _, err := s.handlePersonaGet(agentReadScope(t), nil, personaGetInput{Agent: "pamela", Section: "soul"})
	if err != nil || res.IsError {
		t.Fatalf("persona_get: %v %s", err, toolResultText(res))
	}
	var got map[string]any
	decodeToolJSON(t, toolResultText(res), &got)
	want := map[string]any{"agent": "pamela", "section": "soul", "content": "I am Pamela.", "editable": true, "agent_mutable": true}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestPersonaGet_UnknownAgent(t *testing.T) {
	s, _, _ := personaServer(t)

	res, _, err := s.handlePersonaGet(agentReadScope(t), nil, personaGetInput{Agent: "nobody", Section: "soul"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || !strings.Contains(toolResultText(res), "agent not found: nobody") {
		t.Errorf("got %q, want an agent-not-found error", toolResultText(res))
	}
}

func TestPersonaGet_UnknownSectionNamesTheFour(t *testing.T) {
	s, _, _ := personaServer(t)

	res, _, err := s.handlePersonaGet(agentReadScope(t), nil, personaGetInput{Agent: "pamela", Section: "../etc/passwd"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := toolResultText(res)
	if !res.IsError || !strings.Contains(text, "must be one of: identity, soul, user, memory") {
		t.Errorf("got %q, want the section list", text)
	}
}

func TestPersonaGet_AgentWithoutPersona(t *testing.T) {
	s, _, _ := personaServer(t)

	res, _, err := s.handlePersonaGet(agentReadScope(t), nil, personaGetInput{Agent: "bare", Section: "soul"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || toolResultText(res) != `agent "bare" has no persona configured` {
		t.Errorf("got %q, want the no-persona error", toolResultText(res))
	}
}

func TestPersonaUpdate_AgentWithoutPersona(t *testing.T) {
	s, _, _ := personaServer(t)

	res, _, err := s.handlePersonaUpdate(agentWriteScope(), nil, personaUpdateInput{Agent: "bare", Section: "memory", Content: "x"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || toolResultText(res) != `agent "bare" has no persona configured` {
		t.Errorf("got %q, want the no-persona error", toolResultText(res))
	}
}

func TestPersonaGet_RequiresReadScope(t *testing.T) {
	s, _, _ := personaServer(t)

	res, _, err := s.handlePersonaGet(agentWriteScope(), nil, personaGetInput{Agent: "pamela", Section: "soul"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || strings.Contains(toolResultText(res), "I am Pamela") {
		t.Errorf("got %q, want a scope error", toolResultText(res))
	}
}

func TestPersonaUpdate_ReplacesMemory(t *testing.T) {
	s, _, dir := personaServer(t)

	res, _, err := s.handlePersonaUpdate(agentWriteScope(), nil, personaUpdateInput{Agent: "pamela", Section: "memory", Content: "New memory."})
	if err != nil || res.IsError {
		t.Fatalf("persona_update: %v %s", err, toolResultText(res))
	}
	var got map[string]any
	decodeToolJSON(t, toolResultText(res), &got)
	if got["status"] != "updated" || got["agent"] != "pamela" || got["section"] != "memory" {
		t.Errorf("result = %v", got)
	}

	res, _, _ = s.handlePersonaGet(agentReadScope(t), nil, personaGetInput{Agent: "pamela", Section: "memory"})
	decodeToolJSON(t, toolResultText(res), &got)
	if got["content"] != "New memory." {
		t.Errorf("persona_get content = %v, want the new text", got["content"])
	}
	if disk := readFile(t, filepath.Join(dir, "MEMORY.md")); strings.TrimSpace(disk) != "New memory." {
		t.Errorf("MEMORY.md = %q, want the new text", disk)
	}
}

func TestPersonaUpdate_UnknownSectionWritesNothing(t *testing.T) {
	s, _, dir := personaServer(t)
	before := dirEntries(t, dir)

	res, _, err := s.handlePersonaUpdate(agentWriteScope(), nil, personaUpdateInput{Agent: "pamela", Section: "../escape", Content: "x"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("got %q, want a tool error", toolResultText(res))
	}
	if after := dirEntries(t, dir); len(after) != len(before) {
		t.Errorf("directory changed: %v → %v", before, after)
	}
	if entries := dirEntries(t, filepath.Dir(dir)); len(entries) != 1 {
		t.Errorf("parent directory gained files: %v", entries)
	}
}

func TestPersonaUpdate_UnknownAgent(t *testing.T) {
	s, _, _ := personaServer(t)

	res, _, err := s.handlePersonaUpdate(agentWriteScope(), nil, personaUpdateInput{Agent: "nobody", Section: "memory", Content: "x"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || !strings.Contains(toolResultText(res), "agent not found: nobody") {
		t.Errorf("got %q, want an agent-not-found error", toolResultText(res))
	}
}

func TestPersonaUpdate_RequiresWriteScope(t *testing.T) {
	s, _, dir := personaServer(t)

	res, _, err := s.handlePersonaUpdate(agentReadScope(t), nil, personaUpdateInput{Agent: "pamela", Section: "memory", Content: "New memory."})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a scope error result")
	}
	if disk := readFile(t, filepath.Join(dir, "MEMORY.md")); disk != "Old memory." {
		t.Errorf("MEMORY.md = %q, want it unchanged", disk)
	}
}

func TestPersonaUpdate_UserOverLimitKeepsOldContent(t *testing.T) {
	s, p, _ := personaServer(t)
	p.SetCharLimits(0, 10)
	if err := p.Save("user", "Short."); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	res, _, err := s.handlePersonaUpdate(agentWriteScope(), nil, personaUpdateInput{Agent: "pamela", Section: "user", Content: strings.Repeat("x", 11)})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if want := "user section would be 11 chars; the limit is 10. Send a shorter text."; toolResultText(res) != want {
		t.Fatalf("got %q, want %q", toolResultText(res), want)
	}
	if p.GetUser() != "Short." {
		t.Errorf("user = %q, want the old content", p.GetUser())
	}
}

func TestPersonaUpdate_EmptySoulIsToolError(t *testing.T) {
	s, p, dir := personaServer(t)

	res, _, err := s.handlePersonaUpdate(agentWriteScope(), nil, personaUpdateInput{Agent: "pamela", Section: "soul", Content: "  \n"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || !strings.Contains(toolResultText(res), "soul cannot be empty: it defines the agent. Send its full text") {
		t.Fatalf("got %q, want the empty-soul error", toolResultText(res))
	}
	if p.GetSoul() != "I am Pamela." || readFile(t, filepath.Join(dir, "SOUL.md")) != "I am Pamela." {
		t.Error("soul changed after a refused write")
	}
}
