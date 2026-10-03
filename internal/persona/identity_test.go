package persona

import "testing"

// assertRoundTrip checks want survives FormatIdentity then ParseIdentity.
func assertRoundTrip(t *testing.T, want Identity) {
	t.Helper()
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

func TestFormatIdentity_RoundTrip_WithBody(t *testing.T) {
	assertRoundTrip(t, Identity{Name: "Den", Emoji: "🦊", Theme: "helpful general-purpose assistant", Body: "Extra notes."})
}

func TestFormatIdentity_RoundTrip_QuotesAndColons(t *testing.T) {
	assertRoundTrip(t, Identity{Name: `Say "hi"`, Emoji: "🛰️", Theme: "key: value, with # and 'quotes'"})
}

func TestFormatIdentity_RoundTrip_Empty(t *testing.T) {
	assertRoundTrip(t, Identity{})
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

func TestFormatIdentity_ReadableOutput(t *testing.T) {
	// Plain values stay unquoted and the emoji stays an emoji, so the file
	// reads like a hand-written one (yaml.v3 would write \U0001F98A).
	out, err := FormatIdentity(Identity{Name: "Den", Emoji: "🦊", Theme: "helpful general-purpose assistant"})
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: Den\nemoji: \"🦊\"\ntheme: helpful general-purpose assistant\n---\n"
	if out != want {
		t.Errorf("FormatIdentity =\n%s\nwant\n%s", out, want)
	}
}

func TestFormatIdentity_RoundTrip_LeadingIndicators(t *testing.T) {
	assertRoundTrip(t, Identity{Name: "- dash", Emoji: "", Theme: "#hash then: colon, and a trailing space "})
}

func TestFormatIdentity_MultilineNameRejected(t *testing.T) {
	if _, err := FormatIdentity(Identity{Name: "a\nb"}); err == nil {
		t.Error("expected error for a multi-line name")
	}
}
