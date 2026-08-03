package configmcp_test

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

func TestBuildSkillPayload_RequiresRoundTrips(t *testing.T) {
	payload := configmcp.BuildSkillPayload("gated", "desc", "1.0.0", []string{"command:gated"}, "body", 0,
		[]string{"web_fetch", "kv_get"})

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(s.Requires.Tools) != 2 || s.Requires.Tools[0] != "web_fetch" || s.Requires.Tools[1] != "kv_get" {
		t.Errorf("Requires.Tools = %v, want [web_fetch kv_get]", s.Requires.Tools)
	}
}

// requires is a TOML *table*, so its header must land after every top-level
// scalar. A scalar emitted below "[requires]" would be silently reparsed as a
// member of that table — this asserts the layout, not just the round-trip.
func TestBuildSkillPayload_RequiresTableComesLast(t *testing.T) {
	payload := configmcp.BuildSkillPayload("gated", "desc", "1.0.0", []string{"command:gated"}, "body", 12,
		[]string{"web_fetch"})

	tableIdx := strings.Index(payload, "[requires]")
	if tableIdx == -1 {
		t.Fatalf("payload has no [requires] table:\n%s", payload)
	}
	for _, key := range []string{"name =", "description =", "version =", "triggers =", "max_tool_rounds ="} {
		idx := strings.Index(payload, key)
		if idx == -1 {
			t.Errorf("payload missing %q:\n%s", key, payload)
			continue
		}
		if idx > tableIdx {
			t.Errorf("%q appears after [requires]; it would parse as a table member:\n%s", key, payload)
		}
	}

	// Belt and braces: the scalar that follows the table in struct order still
	// parses back as a top-level field.
	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if s.MaxToolRounds != 12 {
		t.Errorf("MaxToolRounds = %d, want 12", s.MaxToolRounds)
	}
}

func TestBuildSkillPayload_EmptyRequiresOmitsTable(t *testing.T) {
	payload := configmcp.BuildSkillPayload("plain", "desc", "1.0.0", nil, "body", 0, nil)

	if strings.Contains(payload, "requires") {
		t.Errorf("payload mentions requires when unset:\n%s", payload)
	}
}

// The regression this guards: skill_patch and a partial skill_update rebuild
// the whole payload from stored fields, so an omitted requires_tools must be
// carried over rather than erased. B2 plans to make this set load-bearing for
// tool gating, so a silent drop would quietly widen a skill's capabilities.
func TestMergeSkillFields_PreservesExistingRequires(t *testing.T) {
	existing := skill.Skill{
		Name:        "gated",
		Description: "desc",
		Version:     "1.0.0",
		Requires:    skill.SkillRequires{Tools: []string{"web_fetch", "kv_get"}},
		Body:        "old body",
	}
	newBody := "new body"

	payload := configmcp.MergeSkillFields("gated", existing, nil, nil, nil, &newBody, nil, nil)

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(s.Requires.Tools) != 2 {
		t.Errorf("Requires.Tools = %v, want the existing 2 entries preserved", s.Requires.Tools)
	}
	if s.Body != newBody {
		t.Errorf("Body = %q, want %q", s.Body, newBody)
	}
}

// Non-nil but empty clears, matching the established triggers convention on
// these inputs (absent = keep, [] = clear).
func TestMergeSkillFields_EmptyRequiresClears(t *testing.T) {
	existing := skill.Skill{
		Name:     "gated",
		Version:  "1.0.0",
		Requires: skill.SkillRequires{Tools: []string{"web_fetch"}},
		Body:     "body",
	}

	payload := configmcp.MergeSkillFields("gated", existing, nil, nil, nil, nil, nil, []string{})

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(s.Requires.Tools) != 0 {
		t.Errorf("Requires.Tools = %v, want cleared", s.Requires.Tools)
	}
}

// End-to-end through the real skill_patch handler — the path an agent actually
// takes to edit its own skill body, and the one that used to erase the
// declaration on the way through.
func TestSkillPatch_PreservesRequiresAndRoundCap(t *testing.T) {
	sk := &skill.Skill{
		Name:          "gated",
		Description:   "Gated skill",
		Version:       "1.2.3",
		MaxToolRounds: 6,
		Requires:      skill.SkillRequires{Tools: []string{"web_fetch", "kv_get"}},
		Body:          "Hello world, welcome!",
	}

	var mu sync.RWMutex
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		if err := os.MkdirAll(d.AgentSkillsDir, 0o755); err != nil {
			t.Fatalf("creating skills dir: %v", err)
		}
		d.PermissionTier = func() string { return "autonomous" }
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			if name == sk.Name {
				return *sk, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			if name == sk.Name {
				*sk = s
				return true
			}
			return false
		}
	})

	text, isErr := callTool(t, session, "skill_patch", map[string]string{
		"name":       "gated",
		"old_string": "Hello world",
		"new_string": "Hi there",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	mu.RLock()
	defer mu.RUnlock()
	if !strings.Contains(sk.Body, "Hi there") {
		t.Errorf("Body = %q, want the patched text", sk.Body)
	}
	if len(sk.Requires.Tools) != 2 || sk.Requires.Tools[0] != "web_fetch" {
		t.Errorf("Requires.Tools = %v, want [web_fetch kv_get] preserved through the patch", sk.Requires.Tools)
	}
	if sk.MaxToolRounds != 6 {
		t.Errorf("MaxToolRounds = %d, want 6 preserved through the patch", sk.MaxToolRounds)
	}
	if sk.Version != "1.2.3" || sk.Description != "Gated skill" {
		t.Errorf("version/description = %q/%q, want them preserved", sk.Version, sk.Description)
	}
}

func TestMergeSkillFields_ReplacesRequires(t *testing.T) {
	existing := skill.Skill{
		Name:     "gated",
		Version:  "1.0.0",
		Requires: skill.SkillRequires{Tools: []string{"web_fetch"}},
		Body:     "body",
	}

	payload := configmcp.MergeSkillFields("gated", existing, nil, nil, nil, nil, nil, []string{"kv_get", "kv_set"})

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(s.Requires.Tools) != 2 || s.Requires.Tools[0] != "kv_get" {
		t.Errorf("Requires.Tools = %v, want [kv_get kv_set]", s.Requires.Tools)
	}
}

// Every non-edited frontmatter field must survive a body-only rewrite together,
// which is the exact shape of a skill_patch.
func TestMergeSkillFields_PreservesAllFieldsOnBodyOnlyEdit(t *testing.T) {
	existing := skill.Skill{
		Name:          "gated",
		Description:   "desc",
		Version:       "1.2.3",
		Triggers:      []string{"command:gated"},
		MaxToolRounds: 6,
		Requires:      skill.SkillRequires{Tools: []string{"web_fetch"}},
		Body:          "old body",
	}
	newBody := "new body"

	payload := configmcp.MergeSkillFields("gated", existing, nil, nil, nil, &newBody, nil, nil)

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if s.Description != "desc" || s.Version != "1.2.3" {
		t.Errorf("description/version = %q/%q, want desc/1.2.3", s.Description, s.Version)
	}
	if len(s.Triggers) != 1 || s.Triggers[0] != "command:gated" {
		t.Errorf("Triggers = %v, want [command:gated]", s.Triggers)
	}
	if s.MaxToolRounds != 6 {
		t.Errorf("MaxToolRounds = %d, want 6", s.MaxToolRounds)
	}
	if len(s.Requires.Tools) != 1 || s.Requires.Tools[0] != "web_fetch" {
		t.Errorf("Requires.Tools = %v, want [web_fetch]", s.Requires.Tools)
	}
	if s.Body != newBody {
		t.Errorf("Body = %q, want %q", s.Body, newBody)
	}
}
