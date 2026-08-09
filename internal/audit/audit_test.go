package audit

import "testing"

func TestParseFilterList_SplitsCommas(t *testing.T) {
	got := ParseFilterList("tool_call,llm,approval")
	want := []string{"tool_call", "llm", "approval"}
	if len(got) != len(want) {
		t.Fatalf("expected %d values, got %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestParseFilterList_MergesRepeatedParams(t *testing.T) {
	got := ParseFilterList("llm", "tool_call,llm")
	if len(got) != 2 {
		t.Fatalf("expected duplicates collapsed to 2 values, got %v", got)
	}
	if got[0] != "llm" || got[1] != "tool_call" {
		t.Errorf("expected first-seen order [llm tool_call], got %v", got)
	}
}

func TestParseFilterList_BlanksYieldNoFilter(t *testing.T) {
	for _, in := range []string{"", " ", ",", " , "} {
		if got := ParseFilterList(in); got != nil {
			t.Errorf("ParseFilterList(%q): expected nil, got %v", in, got)
		}
	}
	if got := ParseFilterList(); got != nil {
		t.Errorf("ParseFilterList(): expected nil, got %v", got)
	}
}

func TestParseFilterList_TrimsWhitespace(t *testing.T) {
	got := ParseFilterList(" tool_call , llm ")
	if len(got) != 2 || got[0] != "tool_call" || got[1] != "llm" {
		t.Errorf("expected [tool_call llm], got %v", got)
	}
}
