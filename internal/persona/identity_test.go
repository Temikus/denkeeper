package persona

import "testing"

func TestFormatIdentity_RoundTripThroughParseIdentity(t *testing.T) {
	cases := []Identity{
		{Name: "Den", Emoji: "🦊", Theme: "helpful general-purpose assistant", Body: "Extra notes."},
		{Name: `Say "hi"`, Emoji: "🛰️", Theme: "key: value, with # and 'quotes'"},
		{Name: "", Emoji: "", Theme: ""},
	}
	for _, want := range cases {
		out, err := FormatIdentity(want)
		if err != nil {
			t.Fatalf("FormatIdentity(%+v): %v", want, err)
		}
		got, err := ParseIdentity(out)
		if err != nil {
			t.Fatalf("ParseIdentity(%q): %v", out, err)
		}
		if got.Name != want.Name || got.Emoji != want.Emoji || got.Theme != want.Theme || got.Body != want.Body {
			t.Errorf("round trip = %+v, want %+v\n%s", *got, want, out)
		}
	}
}

func TestFormatIdentity_YAMLInjectionStaysScalar(t *testing.T) {
	// The old client built this with string interpolation, so a theme like
	// this one added an emoji key of its own.
	theme := "x\"\nemoji: \"evil"
	out, err := FormatIdentity(Identity{Name: "Den", Emoji: "🦊", Theme: theme})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseIdentity(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Emoji != "🦊" || got.Theme != theme {
		t.Errorf("emoji=%q theme=%q, want the theme kept whole and the emoji untouched", got.Emoji, got.Theme)
	}
}

func TestFormatIdentity_MultilineNameRejected(t *testing.T) {
	if _, err := FormatIdentity(Identity{Name: "a\nb"}); err == nil {
		t.Error("expected error for a multi-line name")
	}
}
