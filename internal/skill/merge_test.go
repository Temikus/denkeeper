package skill

import "testing"

func TestMergeSkills_NoOverlap(t *testing.T) {
	global := []Skill{{Name: "a"}, {Name: "b"}}
	agent := []Skill{{Name: "c"}}

	merged := MergeSkills(global, agent)
	if len(merged) != 3 {
		t.Fatalf("len = %d, want 3", len(merged))
	}
}

func TestMergeSkills_Override(t *testing.T) {
	global := []Skill{{Name: "briefing", Body: "global version"}, {Name: "other"}}
	agent := []Skill{{Name: "briefing", Body: "agent version"}}

	merged := MergeSkills(global, agent)
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2", len(merged))
	}

	// The "briefing" skill should be the agent version.
	for _, s := range merged {
		if s.Name == "briefing" && s.Body != "agent version" {
			t.Errorf("briefing body = %q, want agent version", s.Body)
		}
	}
}

func TestMergeSkills_EmptyAgent(t *testing.T) {
	global := []Skill{{Name: "a"}, {Name: "b"}}
	merged := MergeSkills(global, nil)
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2", len(merged))
	}
}

func TestMergeSkills_EmptyGlobal(t *testing.T) {
	agent := []Skill{{Name: "a"}}
	merged := MergeSkills(nil, agent)
	if len(merged) != 1 {
		t.Fatalf("len = %d, want 1", len(merged))
	}
}

func TestMergeSkills_BothEmpty(t *testing.T) {
	merged := MergeSkills(nil, nil)
	if len(merged) != 0 {
		t.Fatalf("len = %d, want 0", len(merged))
	}
}
