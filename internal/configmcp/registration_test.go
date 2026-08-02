package configmcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/persona"
)

// Registration is the capability boundary: a tool whose backing dependency is
// absent is never advertised, rather than advertised and failing at call time.
// These tests pin that mapping, because engines that must not mutate state
// (the post-turn reviewer) are constrained solely by which deps they are
// handed.

// listToolNames returns the set of tools the session advertises.
func listToolNames(t *testing.T, session *mcp.ClientSession) map[string]bool {
	t.Helper()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(result.Tools))
	for _, tl := range result.Tools {
		names[tl.Name] = true
	}
	return names
}

func TestRegisterTools_NilGates(t *testing.T) {
	tests := []struct {
		name    string
		drop    func(*configmcp.Deps)
		gone    []string
		remains []string
	}{
		{
			name:    "no skills dir",
			drop:    func(d *configmcp.Deps) { d.AgentSkillsDir = "" },
			gone:    []string{"skill_create"},
			remains: []string{"skill_list", "schedule_add"},
		},
		{
			name:    "no skill appender",
			drop:    func(d *configmcp.Deps) { d.AppendSkill = nil },
			gone:    []string{"skill_create"},
			remains: []string{"skill_list"},
		},
		{
			name:    "no skill lister",
			drop:    func(d *configmcp.Deps) { d.GetSkills = nil },
			gone:    []string{"skill_list"},
			remains: []string{"skill_create"},
		},
		{
			name:    "no scheduler",
			drop:    func(d *configmcp.Deps) { d.Sched = nil },
			gone:    []string{"schedule_add", "schedule_list", "schedule_update", "schedule_delete"},
			remains: []string{"skill_create", "tool_list"},
		},
		{
			name:    "no message handler",
			drop:    func(d *configmcp.Deps) { d.HandleMessage = nil },
			gone:    []string{"schedule_add", "schedule_update"},
			remains: []string{"schedule_list", "schedule_delete"},
		},
		{
			name: "no lifecycle manager",
			drop: func(d *configmcp.Deps) { d.LifecycleMgr = nil },
			gone: []string{
				"tool_list", "tool_add", "tool_remove", "tool_restart",
				"plugin_list", "plugin_add", "plugin_remove",
			},
			remains: []string{"skill_create", "schedule_add"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			session, _ := newTestServer(t, tc.drop)
			names := listToolNames(t, session)

			for _, name := range tc.gone {
				if names[name] {
					t.Errorf("tool %q still registered after dropping its dependency", name)
				}
			}
			for _, name := range tc.remains {
				if !names[name] {
					t.Errorf("tool %q unexpectedly disappeared", name)
				}
			}
		})
	}
}

// TestRegisterTools_NewlyGatedToolsPresent is the canary for silent capability
// loss. Every tool listed here is registration-gated on a dependency that
// production wiring always supplies; if one stops being wired, an agent loses
// the tool entirely instead of getting a readable runtime error.
func TestRegisterTools_NewlyGatedToolsPresent(t *testing.T) {
	session, _ := newTestServer(t, nil)
	names := listToolNames(t, session)

	for _, name := range []string{
		"skill_create", "skill_list",
		"schedule_add", "schedule_list", "schedule_update", "schedule_delete",
		"tool_list", "tool_add", "tool_remove", "tool_restart",
		"plugin_list", "plugin_add", "plugin_remove",
	} {
		if !names[name] {
			t.Errorf("tool %q not registered under production-shaped deps", name)
		}
	}
}

func TestPersonaTools_ReadOnlyGate(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetPersonaSection = func(string) (string, bool, bool, bool) {
			return "content", true, true, true
		}
	})

	assertToolRegistered(t, session, "persona_get", true)
	assertToolRegistered(t, session, "persona_update", false)
	assertToolRegistered(t, session, "persona_memory_manage", false)
}

func TestPersonaTools_AppendOnly(t *testing.T) {
	var appended []string
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetPersonaSection = func(string) (string, bool, bool, bool) {
			return "content", true, true, true
		}
		d.AppendMemoryEntry = func(entry string) error {
			appended = append(appended, entry)
			return nil
		}
	})

	assertToolRegistered(t, session, "persona_update", false)
	assertToolRegistered(t, session, "persona_memory_manage", true)

	if got := memoryOperationEnum(t, session); len(got) != 1 || got[0] != "append" {
		t.Fatalf("operation enum = %v, want [append]", got)
	}

	if text, isErr := callTool(t, session, "persona_memory_manage", map[string]any{
		"operation": "append",
		"content":   "a new fact",
	}); isErr {
		t.Fatalf("append failed: %s", text)
	}
	if len(appended) != 1 || appended[0] != "a new fact" {
		t.Errorf("appended = %v, want [a new fact]", appended)
	}

	// Belt and braces: the schema forbids these, and the handlers reject them
	// too. Both layers matter — a client can send whatever it likes.
	for _, op := range []string{"remove", "replace"} {
		if _, isErr := callTool(t, session, "persona_memory_manage", map[string]any{
			"operation": op,
			"content":   "x",
			"heading":   "x",
		}); !isErr {
			t.Errorf("operation %q succeeded without its dependency", op)
		}
	}
}

func TestPersonaTools_FullGate(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	assertToolRegistered(t, env.session, "persona_get", true)
	assertToolRegistered(t, env.session, "persona_update", true)
	assertToolRegistered(t, env.session, "persona_memory_manage", true)

	got := memoryOperationEnum(t, env.session)
	want := []string{"append", "remove", "replace"}
	if len(got) != len(want) {
		t.Fatalf("operation enum = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("operation enum = %v, want %v", got, want)
		}
	}
}

func TestMemoryFull_HintWithoutPruneDeps(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetPersonaSection = func(string) (string, bool, bool, bool) {
			return "content", true, true, true
		}
		d.AppendMemoryEntry = func(string) error {
			return &persona.MemoryFullError{Section: "memory", Current: 2300, Limit: 2200}
		}
	})

	text, isErr := callTool(t, session, "persona_memory_manage", map[string]any{
		"operation": "append",
		"content":   "overflowing",
	})
	if isErr {
		t.Fatalf("expected a structured response, got error: %s", text)
	}

	var resp struct {
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshaling response: %v", err)
	}
	for _, forbidden := range []string{"operation=remove", "operation=replace"} {
		if strings.Contains(resp.Hint, forbidden) {
			t.Errorf("hint %q suggests %q, which this agent cannot do", resp.Hint, forbidden)
		}
	}
}

// memoryOperationEnum extracts the advertised operation enum from the
// persona_memory_manage input schema. The schema is the capability statement,
// so it must never list an operation whose dependency is absent.
func memoryOperationEnum(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range result.Tools {
		if tl.Name != "persona_memory_manage" {
			continue
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("marshaling schema: %v", err)
		}
		var schema struct {
			Properties struct {
				Operation struct {
					Enum []string `json:"enum"`
				} `json:"operation"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshaling schema: %v", err)
		}
		return schema.Properties.Operation.Enum
	}
	t.Fatal("persona_memory_manage not registered")
	return nil
}
