package skill

import "testing"

// FuzzParseFile checks that no input makes ParseFile panic, in either dialect.
func FuzzParseFile(f *testing.F) {
	for _, seed := range []string{
		"+++\nname = \"a\"\n+++\nbody",
		"---\nname: a\n---\nbody",
		"---\n" + kelosFrontmatter + "\n---\nbody",
		"---\nname: a\nname: b\n---\n",
		"---\nname: n\na: &a [x,x]\nb: [*a,*a]\n---\n",
		"---\n\tname: a\n---\n",
		"---\nname: [\n---\n",
		"---\n- a\n---\n",
		"+++\nname = \n+++\n",
		"---",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := ParseFile("fuzz.md", data)
		if err == nil && s.Name == "" {
			t.Errorf("ParseFile accepted a skill with no name: %q", data)
		}
	})
}
