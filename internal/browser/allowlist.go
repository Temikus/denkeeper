// Package browser provides browser automation utilities including URL
// allowlist enforcement for sandboxed browser plugins.
package browser

import (
	"fmt"
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
// Patterns are IDNA-mapped like hosts in Allowed. An unmappable pattern is an
// error, not dropped: dropping every entry would leave the list unrestricted.
func NewAllowlist(domains []string) (*Allowlist, error) {
	if len(domains) == 0 {
		return &Allowlist{}, nil
	}
	normalized := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		p, err := normalizePattern(d)
		if err != nil {
			return nil, fmt.Errorf("allowlist: %w", err)
		}
		normalized = append(normalized, p)
	}
	return &Allowlist{domains: normalized}, nil
}

// normalizePattern maps the host part of a pattern, after any "*." prefix.
// An empty label is refused: "*." would match every host with a trailing dot,
// and IDNA maps some characters, such as U+00AD, to nothing.
func normalizePattern(p string) (string, error) {
	prefix, rest := "", p
	if strings.HasPrefix(rest, "*.") {
		prefix, rest = "*.", rest[2:]
	}
	h, err := netguard.HostToASCII(rest)
	if err != nil {
		return "", err
	}
	if h == "" || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") || strings.Contains(h, "..") {
		return "", fmt.Errorf("pattern %q has an empty label", p)
	}
	return prefix + h, nil
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
	// Map and lowercase as a browser would, so both checks see the host it loads.
	host, err = netguard.HostToASCII(host)
	if err != nil {
		return false, fmt.Errorf("allowlist: %w", err)
	}
	// A browser loads "github.com." as github.com. Trim only one dot, so
	// "github.com.." still matches nothing.
	host = strings.TrimSuffix(host, ".")

	// Always block dangerous ranges regardless of allowlist.
	if isBlockedHost(host) {
		return false, nil
	}

	// Empty allowlist = unrestricted.
	if a.IsEmpty() {
		return true, nil
	}

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
// and broadcast addresses, including IPv6 forms that carry one. The host is
// IDNA-mapped first; an unmappable host, a host with an empty label and a
// non-canonical IPv4 literal are refused. Hostnames that resolve to such
// addresses are not caught here.
func isBlockedHost(host string) bool {
	h, err := netguard.HostToASCII(host)
	if err != nil {
		return true
	}
	return netguard.HasEmptyLabel(h) || netguard.Policy{}.CheckHost(h) != nil || netguard.EndsInNumber(h)
}
