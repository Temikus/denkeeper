package skill

import "testing"

// tooLongForIndex relies on collapsedLen matching what collapse renders.
func TestCollapsedLen_MatchesCollapse(t *testing.T) {
	for _, s := range []string{
		"", "   ", "a", " a ", "a  b\n\tc", "ж  ж", "a b", "a  b", "\xff\xfe x", "x\r\ny ",
	} {
		if got, want := collapsedLen(s), len(collapse(s)); got != want {
			t.Errorf("collapsedLen(%q) = %d, want %d", s, got, want)
		}
	}
}
