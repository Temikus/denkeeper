package configmcp_test

import (
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

func mustParse(t *testing.T, payload string) *skill.Skill {
	t.Helper()
	s, err := skill.ParseFile("(test)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v\n%s", err, payload)
	}
	return s
}

func TestBuildSkillPayload_MaxOutputTokensRoundTrips(t *testing.T) {
	payload := configmcp.BuildSkillPayload("digest", "desc", "1.0.0", []string{"command:digest"}, "body", 0, 2048, nil)

	if s := mustParse(t, payload); s.MaxOutputTokens != 2048 {
		t.Errorf("MaxOutputTokens = %d, want 2048", s.MaxOutputTokens)
	}
}

func TestBuildSkillPayload_ZeroOutputCapOmitsKey(t *testing.T) {
	payload := configmcp.BuildSkillPayload("plain", "desc", "1.0.0", nil, "body", 0, 0, nil)

	if strings.Contains(payload, "max_output_tokens") {
		t.Errorf("payload mentions max_output_tokens when unset:\n%s", payload)
	}
}

func TestMergeSkillFields_OmittedOutputCapIsKept(t *testing.T) {
	existing := skill.Skill{Name: "digest", Version: "1.0.0", MaxOutputTokens: 2048, Body: "old"}
	newBody := "new"

	s := mustParse(t, configmcp.MergeSkillFields("digest", existing, nil, nil, nil, &newBody, nil, nil, nil))
	if s.MaxOutputTokens != 2048 {
		t.Errorf("MaxOutputTokens = %d, want 2048 (an omitted cap must be preserved)", s.MaxOutputTokens)
	}
}

func TestMergeSkillFields_SetsOutputCap(t *testing.T) {
	existing := skill.Skill{Name: "digest", Version: "1.0.0", MaxOutputTokens: 2048, Body: "body"}
	set := 512

	s := mustParse(t, configmcp.MergeSkillFields("digest", existing, nil, nil, nil, nil, nil, &set, nil))
	if s.MaxOutputTokens != 512 {
		t.Errorf("MaxOutputTokens = %d, want 512", s.MaxOutputTokens)
	}
}

func TestMergeSkillFields_ExplicitZeroClearsOutputCap(t *testing.T) {
	existing := skill.Skill{Name: "digest", Version: "1.0.0", MaxOutputTokens: 2048, Body: "body"}
	clear := 0

	s := mustParse(t, configmcp.MergeSkillFields("digest", existing, nil, nil, nil, nil, nil, &clear, nil))
	if s.MaxOutputTokens != 0 {
		t.Errorf("MaxOutputTokens = %d, want 0 (an explicit 0 clears the cap)", s.MaxOutputTokens)
	}
}

func TestApplySkillCreate_NegativeOutputCapRejected(t *testing.T) {
	payload := "+++\nname = \"bad\"\nmax_output_tokens = -1\n+++\n\nbody"

	err := configmcp.ApplySkillCreate(t.TempDir(), func(skill.Skill) {
		t.Error("appendSkill must not be called for an invalid payload")
	}, ioTestLogger(), payload, 0)
	if err == nil {
		t.Fatal("expected error for negative max_output_tokens")
	}
	if !strings.Contains(err.Error(), "max_output_tokens") {
		t.Errorf("error = %q, want it to name the max_output_tokens field", err.Error())
	}
}
