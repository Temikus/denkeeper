package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// seedSkill creates a skill through the tool handler, so the create is
// journaled exactly as a real one would be.
func seedSkill(t *testing.T, s *Server, name, version, body string) {
	t.Helper()
	res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: name, Version: version, Body: body,
	})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	if res.IsError {
		t.Fatalf("seed create failed: %s", toolResultText(res))
	}
}

func TestSkillRevert_Tool_RequiresWriteScope(t *testing.T) {
	s, _ := skillServer(t, t.TempDir())

	ctx := withScopes(context.Background(), []string{"skills:read"})
	res, _, err := s.handleSkillRevert(ctx, nil, skillRevertInput{Agent: "test-agent", Skill: "greet"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("skills:read must not be enough to revert a skill")
	}
}

func TestSkillRevert_Tool_AgentNotFound(t *testing.T) {
	s, _ := skillServer(t, t.TempDir())

	res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{Agent: "no-such-agent"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected a tool error for an unknown agent")
	}
}

// TestSkillRevert_Tool_UnknownSkill reports "nothing to revert" rather than
// pretending something happened.
func TestSkillRevert_Tool_UnknownSkill(t *testing.T) {
	s, _ := skillServer(t, t.TempDir())

	res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{
		Agent: "test-agent", Skill: "never-existed",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool error for a skill with no history")
	}
	if !strings.Contains(toolResultText(res), "nothing to revert") {
		t.Errorf("unhelpful message: %s", toolResultText(res))
	}
}

// TestSkillRevert_Tool_UndoRedoAlternates documents what happens when a caller
// reverts twice: a revert is itself a tracked change, so reverting it *redoes*
// the original change rather than stepping further back through history.
func TestSkillRevert_Tool_UndoRedoAlternates(t *testing.T) {
	dir := t.TempDir()
	s, _ := skillServer(t, dir)

	seedSkill(t, s, "greet", "1.0.0", "body")
	path := filepath.Join(dir, "greet.md")
	created, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	revert := func(step string) {
		t.Helper()
		res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{Agent: "test-agent", Skill: "greet"})
		if err != nil {
			t.Fatalf("%s: handler error: %v", step, err)
		}
		if res.IsError {
			t.Fatalf("%s failed: %s", step, toolResultText(res))
		}
	}

	revert("undo")
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("undo did not remove the created skill: %v", statErr)
	}

	revert("redo")
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("redo did not restore the skill: %v", err)
	}
	if string(back) != string(created) {
		t.Errorf("redo did not restore the original bytes:\n got %q\nwant %q", string(back), string(created))
	}
}

// TestSkillRevert_Tool_RestoresPreviousVersion is the success shape: the file
// goes back, memory goes back, and the result names what happened.
func TestSkillRevert_Tool_RestoresPreviousVersion(t *testing.T) {
	dir := t.TempDir()
	s, e := skillServer(t, dir)

	seedSkill(t, s, "greet", "1.0.0", "old body")
	before, err := os.ReadFile(filepath.Join(dir, "greet.md"))
	if err != nil {
		t.Fatal(err)
	}

	newBody := "new body"
	newVersion := "2.0.0"
	if res, _, _ := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent: "test-agent", Name: "greet", Version: &newVersion, Body: &newBody,
	}); res.IsError {
		t.Fatalf("update failed: %s", toolResultText(res))
	}

	res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{Agent: "test-agent", Skill: "greet"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("revert failed: %s", toolResultText(res))
	}

	text := toolResultText(res)
	for _, want := range []string{"reverted revision", "update", "greet", "1.0.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("result text missing %q: %s", want, text)
		}
	}

	after, err := os.ReadFile(filepath.Join(dir, "greet.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("file not restored byte-identically:\n got %q\nwant %q", string(after), string(before))
	}
	sk, ok := e.GetSkill("greet")
	if !ok {
		t.Fatal("skill missing from memory after revert")
	}
	if sk.Version != "1.0.0" || sk.Body != "old body" {
		t.Errorf("in-memory skill not restored: version=%q body=%q", sk.Version, sk.Body)
	}
}

// TestSkillRevert_Tool_AgentWideWhenNoSelector picks the agent's most recent
// skill change when neither selector is given.
func TestSkillRevert_Tool_AgentWideWhenNoSelector(t *testing.T) {
	dir := t.TempDir()
	s, _ := skillServer(t, dir)

	seedSkill(t, s, "first", "1.0.0", "first body")
	seedSkill(t, s, "second", "1.0.0", "second body")

	res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{Agent: "test-agent"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("revert failed: %s", toolResultText(res))
	}

	if _, statErr := os.Stat(filepath.Join(dir, "second.md")); !os.IsNotExist(statErr) {
		t.Error("the most recent skill should have been removed")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "first.md")); statErr != nil {
		t.Errorf("an unrelated skill was affected: %v", statErr)
	}
}

// TestSkillRevert_Tool_RejectsBothSelectors keeps the two mutually exclusive
// modes from being silently resolved one way.
func TestSkillRevert_Tool_RejectsBothSelectors(t *testing.T) {
	s, _ := skillServer(t, t.TempDir())

	res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{
		Agent: "test-agent", Skill: "greet", TransitionID: "t1",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected a tool error when both skill and transition_id are given")
	}
}

func TestSkillRevert_Tool_RejectsPathTraversal(t *testing.T) {
	s, _ := skillServer(t, t.TempDir())

	res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{
		Agent: "test-agent", Skill: "../escape",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected a tool error for a traversal skill name")
	}
}

// TestSkillRevert_Tool_NoJournalStore explains itself instead of failing
// obscurely when no revision store is wired.
func TestSkillRevert_Tool_NoJournalStore(t *testing.T) {
	dir := t.TempDir()
	e := testEngine(t)
	e.SetSkillDirs(dir, "")
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"test-agent": e}, nil, nil, testLogger())
	s := &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}

	res, _, err := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{Agent: "test-agent", Skill: "greet"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool error with no journal wired")
	}
	if !strings.Contains(toolResultText(res), "revision journal") {
		t.Errorf("message should name the missing journal: %s", toolResultText(res))
	}
}

// TestSkill_NoJournalStore_StillWrites is the degradation guarantee: without a
// revision store the skill tools keep working, they simply have no undo.
func TestSkill_NoJournalStore_StillWrites(t *testing.T) {
	dir := t.TempDir()
	e := testEngine(t)
	e.SetSkillDirs(dir, "")
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"test-agent": e}, nil, nil, testLogger())
	s := &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}

	seedSkill(t, s, "greet", "1.0.0", "body")
	if _, err := os.Stat(filepath.Join(dir, "greet.md")); err != nil {
		t.Errorf("skill was not written without a journal: %v", err)
	}
	if _, ok := e.GetSkill("greet"); !ok {
		t.Error("skill not registered in memory without a journal")
	}
}

// recordingAuditor captures emitted events.
type recordingAuditor struct{ events []audit.Event }

func (r *recordingAuditor) Emit(_ context.Context, event audit.Event) {
	r.events = append(r.events, event)
}

// TestSkillRevert_Tool_EmitsAudit checks the event carries the limit of the
// guarantee: a revert restores files, not everything the skill did.
func TestSkillRevert_Tool_EmitsAudit(t *testing.T) {
	dir := t.TempDir()
	s, _ := skillServer(t, dir)
	auditor := &recordingAuditor{}
	s.deps.Auditor = auditor

	seedSkill(t, s, "greet", "1.0.0", "body")
	if res, _, _ := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{
		Agent: "test-agent", Skill: "greet",
	}); res.IsError {
		t.Fatalf("revert failed: %s", toolResultText(res))
	}

	if len(auditor.events) != 1 {
		t.Fatalf("emitted %d events, want exactly 1", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Category != audit.CategorySkill || ev.Action != "skill_revert" {
		t.Errorf("event = (%q, %q), want (skill, skill_revert)", ev.Category, ev.Action)
	}
	if ev.Agent != "test-agent" || ev.Status != audit.StatusOK {
		t.Errorf("event agent/status = (%q, %q)", ev.Agent, ev.Status)
	}
	for _, want := range []string{"skill files only", "messages sent", "KV keys"} {
		if !strings.Contains(ev.Summary, want) {
			t.Errorf("summary should note %q: %s", want, ev.Summary)
		}
	}
	if !strings.Contains(ev.Detail, `"op":"create"`) {
		t.Errorf("detail should name the reverted op: %s", ev.Detail)
	}
}

// TestSkillRevert_Tool_NothingToRevertEmitsNoAudit keeps a question that
// changed nothing out of the audit log.
func TestSkillRevert_Tool_NothingToRevertEmitsNoAudit(t *testing.T) {
	s, _ := skillServer(t, t.TempDir())
	auditor := &recordingAuditor{}
	s.deps.Auditor = auditor

	if res, _, _ := s.handleSkillRevert(writeScope(t), nil, skillRevertInput{
		Agent: "test-agent", Skill: "never-existed",
	}); !res.IsError {
		t.Fatal("expected a tool error")
	}
	if len(auditor.events) != 0 {
		t.Errorf("emitted %d events for a no-op revert, want 0", len(auditor.events))
	}
}

// listToolSchemas connects an in-process client and returns each tool's input
// schema as canonical JSON.
func listToolSchemas(t *testing.T) map[string]string {
	t.Helper()
	s := &Server{deps: Deps{Logger: testLogger()}}
	s.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "denkeeper", Version: "test"}, nil)
	s.registerSkillTools()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.mcpServer.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	schemas := make(map[string]string, len(result.Tools))
	for _, tool := range result.Tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshaling schema for %q: %v", tool.Name, err)
		}
		schemas[tool.Name] = string(encoded)
	}
	return schemas
}

// TestSkillTools_ExistingSchemasUnchanged pins the wire schema of every skill
// tool that existed before skill_revert. These schemas are a public contract
// with external MCP clients: adding a field to one of the shared input structs
// changes what those clients are told to send, so it must be a deliberate act,
// not a side effect of a refactor here.
func TestSkillTools_ExistingSchemasUnchanged(t *testing.T) {
	pinned := map[string]string{
		"skill_list":   `{"additionalProperties":false,"properties":{"agent":{"description":"Agent name","type":"string"}},"required":["agent"],"type":"object"}`,
		"skill_get":    `{"additionalProperties":false,"properties":{"agent":{"description":"Agent name","type":"string"},"name":{"description":"Skill name","type":"string"}},"required":["agent","name"],"type":"object"}`,
		"skill_create": `{"additionalProperties":false,"properties":{"agent":{"description":"Agent name","type":"string"},"body":{"description":"Skill content/instructions","type":"string"},"description":{"description":"Skill description","type":"string"},"max_tool_rounds":{"description":"Optional cap on tool-call ROUNDS (not calls) for turns this skill drives; 0 = no cap. Only lowers the agent's budget, never raises it.","type":"integer"},"name":{"description":"Skill name","type":"string"},"requires_tools":{"description":"Optional tool names this skill depends on (frontmatter [requires] tools).","items":{"type":"string"},"type":["null","array"]},"triggers":{"description":"Trigger keywords","items":{"type":"string"},"type":["null","array"]},"version":{"description":"Skill version (e.g. 1.0.0)","type":"string"}},"required":["agent","name","body"],"type":"object"}`,
		"skill_update": `{"additionalProperties":false,"properties":{"agent":{"description":"Agent name","type":"string"},"body":{"description":"New content","type":["null","string"]},"description":{"description":"New description","type":["null","string"]},"max_tool_rounds":{"description":"New cap on tool-call ROUNDS (not calls); 0 removes the cap. Omit to keep current.","type":["null","integer"]},"name":{"description":"Skill name to update","type":"string"},"new_name":{"description":"New skill name (rename)","type":["null","string"]},"requires_tools":{"description":"New required tool names; omit to keep current, pass [] to clear.","items":{"type":"string"},"type":["null","array"]},"triggers":{"description":"New triggers","items":{"type":"string"},"type":["null","array"]},"version":{"description":"New version (e.g. 1.0.0)","type":["null","string"]}},"required":["agent","name"],"type":"object"}`,
		"skill_delete": `{"additionalProperties":false,"properties":{"agent":{"description":"Agent name","type":"string"},"name":{"description":"Skill name to delete","type":"string"}},"required":["agent","name"],"type":"object"}`,
	}

	schemas := listToolSchemas(t)
	for name, want := range pinned {
		got, ok := schemas[name]
		if !ok {
			t.Errorf("tool %q disappeared from the skill tool set", name)
			continue
		}
		if got != want {
			t.Errorf("schema for %q changed:\n got %s\nwant %s", name, got, want)
		}
	}

	// skill_revert is purely additive.
	if _, ok := schemas["skill_revert"]; !ok {
		t.Error("skill_revert was not registered")
	}
	if len(schemas) != len(pinned)+1 {
		t.Errorf("skill tool set has %d tools, want %d", len(schemas), len(pinned)+1)
	}
}
