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
		{"192.168.1.1", false},
		{"10.0.0.1", false},
	}

	for _, tt := range tests {
		got := isBlockedHost(tt.host)
		if got != tt.want {
			t.Errorf("isBlockedHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}
