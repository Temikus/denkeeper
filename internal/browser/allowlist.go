// Package browser provides browser automation utilities including URL
// allowlist enforcement for sandboxed browser plugins.
package browser

import (
	"net/url"
	"strings"

	"github.com/Temikus/denkeeper/internal/netguard"
)

// Allowlist enforces domain restrictions on browser navigation.
// A nil or empty Allowlist permits all domains (except blocked ranges).
type Allowlist struct {
	domains []string // e.g. ["github.com", "*.example.com"]
}

// NewAllowlist creates an Allowlist from a list of domain patterns.
// Supports exact matches ("github.com") and wildcard subdomains ("*.example.com").
// An empty list means unrestricted (all non-blocked domains are allowed).
func NewAllowlist(domains []string) *Allowlist {
	if len(domains) == 0 {
		return &Allowlist{}
	}
	// Normalize: lowercase, trim whitespace.
	normalized := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.TrimSpace(strings.ToLower(d))
		if d != "" {
			normalized = append(normalized, d)
		}
	}
	return &Allowlist{domains: normalized}
}

// IsEmpty returns true if the allowlist has no domain restrictions.
func (a *Allowlist) IsEmpty() bool {
	return a == nil || len(a.domains) == 0
}

// Allowed checks whether a URL is permitted by the allowlist.
// Returns false, regardless of the allowlist, if the URL's host is blocked by
// isBlockedHost: localhost, cloud metadata, or a literal loopback, private,
// link-local, unspecified, multicast or broadcast address.
// If the allowlist is empty, all non-blocked URLs are allowed.
func (a *Allowlist) Allowed(rawURL string) (bool, error) {
	host, err := extractHost(rawURL)
	if err != nil {
		return false, err
	}

	// Always block dangerous ranges regardless of allowlist.
	if isBlockedHost(host) {
		return false, nil
	}

	// Empty allowlist = unrestricted.
	if a.IsEmpty() {
		return true, nil
	}

	host = strings.ToLower(host)
	for _, pattern := range a.domains {
		if matchDomain(host, pattern) {
			return true, nil
		}
	}
	return false, nil
}

// Domains returns the list of allowed domain patterns.
func (a *Allowlist) Domains() []string {
	if a == nil {
		return nil
	}
	return a.domains
}

// extractHost parses a URL and returns the hostname (without port).
func extractHost(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	if host == "" {
		return "", &url.Error{Op: "parse", URL: rawURL, Err: url.InvalidHostError("")}
	}
	return host, nil
}

// matchDomain checks if host matches a domain pattern.
// "github.com" matches "github.com" exactly.
// "*.example.com" matches "sub.example.com" and "deep.sub.example.com".
func matchDomain(host, pattern string) bool {
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // e.g. ".example.com"
		return host == pattern[2:] || strings.HasSuffix(host, suffix)
	}
	return host == pattern
}

// isBlockedHost reports whether host is refused by the netguard default
// policy: localhost names, cloud metadata names and IPs, and literal
// loopback, private (RFC1918, CGNAT, ULA), link-local, unspecified, multicast
// and broadcast addresses, including IPv6 forms that carry one. Hostnames
// that resolve to such addresses are not caught here.
func isBlockedHost(host string) bool {
	return netguard.Policy{}.CheckHost(host) != nil
}
