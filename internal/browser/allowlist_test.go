package browser

import "testing"

func TestAllowlist_EmptyIsUnrestricted(t *testing.T) {
	a := NewAllowlist(nil)
	if !a.IsEmpty() {
		t.Error("nil allowlist should be empty")
	}

	ok, err := a.Allowed("https://anything.com/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("empty allowlist should allow any non-blocked URL")
	}
}

func TestAllowlist_ExactDomainMatch(t *testing.T) {
	a := NewAllowlist([]string{"github.com", "example.org"})

	tests := []struct {
		url  string
		want bool
	}{
		{"https://github.com/repo", true},
		{"https://GITHUB.COM/repo", true},
		{"https://example.org", true},
		{"https://evil.com", false},
		{"https://notgithub.com", false},
		{"https://sub.github.com", false}, // exact match, not wildcard
	}

	for _, tt := range tests {
		got, err := a.Allowed(tt.url)
		if err != nil {
			t.Fatalf("Allowed(%q): %v", tt.url, err)
		}
		if got != tt.want {
			t.Errorf("Allowed(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestAllowlist_WildcardSubdomain(t *testing.T) {
	a := NewAllowlist([]string{"*.example.com"})

	tests := []struct {
		url  string
		want bool
	}{
		{"https://sub.example.com/path", true},
		{"https://deep.sub.example.com", true},
		{"https://example.com", true},     // bare domain also matches *.example.com
		{"https://notexample.com", false}, // not a subdomain
		{"https://example.com.evil.com", false},
	}

	for _, tt := range tests {
		got, err := a.Allowed(tt.url)
		if err != nil {
			t.Fatalf("Allowed(%q): %v", tt.url, err)
		}
		if got != tt.want {
			t.Errorf("Allowed(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestAllowlist_BlockedRanges(t *testing.T) {
	// Even with an empty (unrestricted) allowlist, blocked ranges are denied.
	a := NewAllowlist(nil)

	blocked := []string{
		"http://169.254.169.254/latest/meta-data",
		"http://localhost:8080/admin",
		"http://127.0.0.1:9090/",
		"http://[::1]/path",
		"http://metadata.google.internal/",
		"http://metadata.google.com/",
	}

	for _, u := range blocked {
		ok, err := a.Allowed(u)
		if err != nil {
			t.Fatalf("Allowed(%q): %v", u, err)
		}
		if ok {
			t.Errorf("Allowed(%q) = true, want false (blocked range)", u)
		}
	}
}

func TestAllowlist_BlockedRangesOverrideAllowlist(t *testing.T) {
	// Even if explicitly in the allowlist, blocked ranges are denied.
	a := NewAllowlist([]string{"169.254.169.254", "localhost", "metadata.google.internal"})

	blocked := []string{
		"http://169.254.169.254/latest/meta-data",
		"http://localhost/admin",
		"http://metadata.google.internal/",
	}

	for _, u := range blocked {
		ok, err := a.Allowed(u)
		if err != nil {
			t.Fatalf("Allowed(%q): %v", u, err)
		}
		if ok {
			t.Errorf("Allowed(%q) = true, want false (blocked even if in allowlist)", u)
		}
	}
}

func TestAllowlist_AllowlistedPrivateIPRefused(t *testing.T) {
	a := NewAllowlist([]string{"10.0.0.1", "192.168.1.10"})

	for _, u := range []string{"http://10.0.0.1/", "http://192.168.1.10:8080/admin"} {
		ok, err := a.Allowed(u)
		if err != nil {
			t.Fatalf("Allowed(%q): %v", u, err)
		}
		if ok {
			t.Errorf("Allowed(%q) = true, want false (private range blocked even if in allowlist)", u)
		}
	}
}

func TestAllowlist_MatchesIDNAMappedHost(t *testing.T) {
	a := NewAllowlist([]string{"github.com"})

	// Fullwidth "ｇｉｔｈｕｂ.com" maps to github.com, as a browser would load it.
	ok, err := a.Allowed("https://ｇｉｔｈｕｂ.com/repo")
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !ok {
		t.Error("Allowed = false, want true for a host that maps to an allowlisted domain")
	}
}

func TestAllowlist_UnmappableHostRefused(t *testing.T) {
	a := NewAllowlist(nil)

	// U+FFFD is disallowed by IDNA, so the host cannot be mapped.
	ok, _ := a.Allowed("https://bad�host.com/")
	if ok {
		t.Error("Allowed = true, want false when the host cannot be mapped to ASCII")
	}
}

func TestAllowlist_InvalidURL(t *testing.T) {
	a := NewAllowlist([]string{"example.com"})

	_, err := a.Allowed("://bad")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestAllowlist_Domains(t *testing.T) {
	a := NewAllowlist([]string{"  GitHub.COM  ", "*.Example.Org"})
	domains := a.Domains()
	if len(domains) != 2 {
		t.Fatalf("Domains() length = %d, want 2", len(domains))
	}
	if domains[0] != "github.com" {
		t.Errorf("Domains()[0] = %q, want github.com", domains[0])
	}
	if domains[1] != "*.example.org" {
		t.Errorf("Domains()[1] = %q, want *.example.org", domains[1])
	}
}

func TestAllowlist_EmptyStringsFiltered(t *testing.T) {
	a := NewAllowlist([]string{"", "  ", "github.com", ""})
	if len(a.Domains()) != 1 {
		t.Errorf("expected 1 domain after filtering, got %d", len(a.Domains()))
	}
}

func TestMatchDomain(t *testing.T) {
	tests := []struct {
		host    string
		pattern string
		want    bool
	}{
		{"github.com", "github.com", true},
		{"github.com", "example.com", false},
		{"sub.example.com", "*.example.com", true},
		{"example.com", "*.example.com", true},
		{"deep.sub.example.com", "*.example.com", true},
		{"notexample.com", "*.example.com", false},
	}

	for _, tt := range tests {
		got := matchDomain(tt.host, tt.pattern)
		if got != tt.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.host, tt.pattern, got, tt.want)
		}
	}
}

func TestIsBlockedHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"169.254.169.254", true},
		{"169.254.0.1", true},
		{"::1", true},
		{"metadata.google.internal", true},
		{"metadata.google.com", true},
		{"8.8.8.8", false},
		{"github.com", false},
		{"LOCALHOST", true},
		{"app.localhost", true},
		{"metadata.google.internal.", true},
		{"fe80::1", true},
		// Private: RFC1918, CGNAT, ULA.
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"100.64.0.1", true},
		{"fd00::1", true},
		// Metadata inside private or public space.
		{"100.100.100.200", true},
		{"168.63.129.16", true},
		// Unspecified, multicast, broadcast.
		{"0.0.0.0", true},
		{"::", true},
		{"224.0.0.1", true},
		{"ff02::1", true},
		{"255.255.255.255", true},
		// IPv6 forms carrying a blocked IPv4 address.
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"64:ff9b::a9fe:a9fe", true},
		{"2002:a9fe:a9fe::", true},
		// Public neighbours of the private ranges stay allowed.
		{"172.32.0.1", false},
		{"100.128.0.1", false},
		{"2606:4700:4700::1111", false},
		// Non-canonical IPv4 forms a browser reads as an address.
		{"2130706433", true},
		{"0x7f000001", true},
		{"0X7F000001", true},
		{"0x7f.1", true},
		{"017700000001", true},
		{"127.1", true},
		{"2852039166", true},
		{"8.8.8.8.", false},
		{"134744072", true}, // 8.8.8.8 in decimal: refused, not decoded
		// Fullwidth forms that IDNA maps to a blocked host.
		{"１２７.０.０.１", true},
		{"１６９.２５４.１６９.２５４", true},
		{"ｌｏｃａｌｈｏｓｔ", true},
		// A numeric label that is not the last one is a hostname.
		{"123.example.com", false},
		{"example.com1", false},
	}

	for _, tt := range tests {
		got := isBlockedHost(tt.host)
		if got != tt.want {
			t.Errorf("isBlockedHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}
