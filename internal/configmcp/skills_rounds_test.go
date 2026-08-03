package configmcp_test

import (
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// BuildSkillPayload is the single frontmatter writer behind every CRUD surface,
// so a cap it drops is a cap silently erased from disk on the next update.
func TestBuildSkillPayload_MaxToolRoundsRoundTrips(t *testing.T) {
	payload := configmcp.BuildSkillPayload("capped", "desc", "1.0.0", []string{"command:capped"}, "body", 12, nil)

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if s.MaxToolRounds != 12 {
		t.Errorf("MaxToolRounds = %d, want 12", s.MaxToolRounds)
	}
}

// An unset cap must not appear in the frontmatter at all, so uncapped skills
// keep round-tripping exactly as they did before the field existed.
func TestBuildSkillPayload_ZeroOmitsKey(t *testing.T) {
	payload := configmcp.BuildSkillPayload("plain", "desc", "1.0.0", nil, "body", 0, nil)

	if strings.Contains(payload, "max_tool_rounds") {
		t.Errorf("payload mentions max_tool_rounds when unset:\n%s", payload)
	}
}

// The regression this guards: skill_patch and a partial skill_update rebuild
// the whole payload from stored fields, so an omitted cap must be carried over
// rather than reset to 0.
func TestMergeSkillFields_PreservesExistingCap(t *testing.T) {
	existing := skill.Skill{
		Name:          "capped",
		Description:   "desc",
		Version:       "1.0.0",
		MaxToolRounds: 12,
		Body:          "old body",
	}
	newBody := "new body"

	payload := configmcp.MergeSkillFields("capped", existing, nil, nil, nil, &newBody, nil, nil)

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if s.MaxToolRounds != 12 {
		t.Errorf("MaxToolRounds = %d, want 12 (an omitted cap must be preserved)", s.MaxToolRounds)
	}
	if s.Body != newBody {
		t.Errorf("Body = %q, want %q", s.Body, newBody)
	}
}

func TestMergeSkillFields_ExplicitZeroClearsCap(t *testing.T) {
	existing := skill.Skill{Name: "capped", Version: "1.0.0", MaxToolRounds: 12, Body: "body"}
	clear := 0

	payload := configmcp.MergeSkillFields("capped", existing, nil, nil, nil, nil, &clear, nil)

	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if s.MaxToolRounds != 0 {
		t.Errorf("MaxToolRounds = %d, want 0 (an explicit 0 clears the cap)", s.MaxToolRounds)
	}
}

// Validation is inherited from ParseFile, so every CRUD surface rejects a
// negative cap without per-surface code.
func TestApplySkillCreate_NegativeCapRejected(t *testing.T) {
	payload := "+++\nname = \"bad\"\nmax_tool_rounds = -1\n+++\n\nbody"

	err := configmcp.ApplySkillCreate(t.TempDir(), func(skill.Skill) {
		t.Error("appendSkill must not be called for an invalid payload")
	}, ioTestLogger(), payload, 0)
	if err == nil {
		t.Fatal("expected error for negative max_tool_rounds")
	}
	if !strings.Contains(err.Error(), "max_tool_rounds") {
		t.Errorf("error = %q, want it to name the max_tool_rounds field", err.Error())
	}
}
