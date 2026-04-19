package configmcp_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/configmcp"
)

// personaTestEnv bundles test server state for persona tests.
type personaTestEnv struct {
	state   *testPersonaState
	session *mcp.ClientSession
}

func (e *personaTestEnv) call(t *testing.T, name string, args any) (string, bool) {
	t.Helper()
	return callTool(t, e.session, name, args)
}

// newTestServerWithPersona creates a test server with persona callbacks wired.
func newTestServerWithPersona(t *testing.T, tier string) *personaTestEnv {
	t.Helper()

	state := &testPersonaState{
		sections: map[string]string{
			"soul":   "I am a helpful assistant.",
			"user":   "User likes Go.",
			"memory": "Last talked about testing.",
		},
	}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return tier }
		d.GetPersonaSection = func(section string) (string, bool, bool, bool) {
			content, ok := state.sections[section]
			if !ok {
				return "", false, false, false
			}
			agentMutable := section == "soul" || section == "user" || section == "memory"
			return content, true, agentMutable, true
		}
		d.SavePersonaSection = func(section, content string) error {
			state.sections[section] = content
			return nil
		}
		d.AppendMemoryEntry = func(entry string) error {
			current := state.sections["memory"]
			if current == "" {
				state.sections["memory"] = entry
			} else {
				state.sections["memory"] = current + "\n\n---\n\n" + entry
			}
			return nil
		}
		d.RemoveMemoryEntry = func(heading string) error {
			// Simplified: just check if heading is mentioned, clear it.
			mem := state.sections["memory"]
			target := "## " + heading
			if !strings.Contains(mem, target) {
				return fmt.Errorf("no entry with heading %q found", heading)
			}
			// For test purposes, replace the entire memory without the entry.
			state.sections["memory"] = strings.Replace(mem, target, "", 1)
			return nil
		}
	})

	return &personaTestEnv{state: state, session: session}
}

type testPersonaState struct {
	sections map[string]string
}

func TestPersonaGet_Soul(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	text, isErr := env.call(t, "persona_get", map[string]string{"section": "soul"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var resp struct {
		Section      string `json:"section"`
		Content      string `json:"content"`
		Editable     bool   `json:"editable"`
		AgentMutable bool   `json:"agent_mutable"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshaling response: %v", err)
	}
	if resp.Section != "soul" {
		t.Errorf("section = %q, want soul", resp.Section)
	}
	if resp.Content != env.state.sections["soul"] {
		t.Errorf("content = %q, want %q", resp.Content, env.state.sections["soul"])
	}
	if !resp.AgentMutable {
		t.Error("soul should be agent_mutable")
	}
}

func TestPersonaGet_UnknownSection(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	text, isErr := env.call(t, "persona_get", map[string]string{"section": "unknown"})
	if !isErr {
		t.Fatalf("expected error for unknown section, got: %s", text)
	}
	if !strings.Contains(text, "unknown section") {
		t.Errorf("error should mention unknown section, got: %s", text)
	}
}

func TestPersonaUpdate_Soul_Autonomous(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	text, isErr := env.call(t, "persona_update", map[string]string{
		"section": "soul",
		"content": "I am a curious explorer.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "Done") {
		t.Errorf("expected Done response, got: %s", text)
	}
	if env.state.sections["soul"] != "I am a curious explorer." {
		t.Errorf("soul not updated: %q", env.state.sections["soul"])
	}
}

func TestPersonaUpdate_Soul_Supervised(t *testing.T) {
	env := newTestServerWithPersona(t, "supervised")

	text, isErr := env.call(t, "persona_update", map[string]string{
		"section": "soul",
		"content": "I am a curious explorer.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "Done:") {
		t.Errorf("expected done message, got: %s", text)
	}
	if env.state.sections["soul"] != "I am a curious explorer." {
		t.Errorf("soul should be updated: %q", env.state.sections["soul"])
	}
}

func TestPersonaUpdate_Soul_Restricted(t *testing.T) {
	env := newTestServerWithPersona(t, "restricted")

	text, isErr := env.call(t, "persona_update", map[string]string{
		"section": "soul",
		"content": "I am free now.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "Done:") {
		t.Errorf("expected done message, got: %s", text)
	}
	if env.state.sections["soul"] != "I am free now." {
		t.Errorf("soul should be updated: %q", env.state.sections["soul"])
	}
}

func TestPersonaUpdate_Memory_DirectWrite(t *testing.T) {
	env := newTestServerWithPersona(t, "restricted")

	text, isErr := env.call(t, "persona_update", map[string]string{
		"section": "memory",
		"content": "User discussed soul updates.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	// Memory should be written directly regardless of tier.
	if env.state.sections["memory"] != "User discussed soul updates." {
		t.Errorf("memory not updated: %q", env.state.sections["memory"])
	}
}

// ---------------------------------------------------------------------------
// persona_memory_manage tool tests
// ---------------------------------------------------------------------------

func TestPersonaMemoryManage_Append(t *testing.T) {
	env := newTestServerWithPersona(t, "restricted")

	text, isErr := env.call(t, "persona_memory_manage", map[string]string{
		"operation": "append",
		"content":   "## New Entry\n\nUser mentioned Go testing.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, `"ok": true`) {
		t.Errorf("expected ok response, got: %s", text)
	}
	mem := env.state.sections["memory"]
	if !strings.Contains(mem, "Last talked about testing.") {
		t.Errorf("original memory should be preserved, got: %q", mem)
	}
	if !strings.Contains(mem, "New Entry") {
		t.Errorf("new entry should be appended, got: %q", mem)
	}
	if !strings.Contains(mem, "---") {
		t.Error("entries should be separated by ---")
	}
}

func TestPersonaMemoryManage_Append_EmptyContent(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	text, isErr := env.call(t, "persona_memory_manage", map[string]string{
		"operation": "append",
		"content":   "  ",
	})
	if !isErr {
		t.Fatalf("expected error for empty content, got: %s", text)
	}
}

func TestPersonaMemoryManage_Remove(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")
	// Pre-seed memory with a heading that RemoveMemoryEntry can find.
	env.state.sections["memory"] = "## Old\n\nOld stuff."

	text, isErr := env.call(t, "persona_memory_manage", map[string]string{
		"operation": "remove",
		"heading":   "Old",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, `"ok": true`) {
		t.Errorf("expected ok response, got: %s", text)
	}
}

func TestPersonaMemoryManage_Remove_NotFound(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	text, isErr := env.call(t, "persona_memory_manage", map[string]string{
		"operation": "remove",
		"heading":   "NonExistent",
	})
	if !isErr {
		t.Fatalf("expected error for missing heading, got: %s", text)
	}
}

func TestPersonaMemoryManage_Replace(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	text, isErr := env.call(t, "persona_memory_manage", map[string]string{
		"operation": "replace",
		"content":   "Completely new memory.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if env.state.sections["memory"] != "Completely new memory." {
		t.Errorf("memory not replaced: %q", env.state.sections["memory"])
	}
	_ = text
}

func TestPersonaMemoryManage_UnknownOperation(t *testing.T) {
	env := newTestServerWithPersona(t, "autonomous")

	text, isErr := env.call(t, "persona_memory_manage", map[string]string{
		"operation": "delete",
	})
	if !isErr {
		t.Fatalf("expected error for unknown operation, got: %s", text)
	}
	if !strings.Contains(text, "unknown operation") {
		t.Errorf("error should mention unknown operation, got: %s", text)
	}
}
