package webfetch

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/Temikus/denkeeper/internal/netguard"
)

// requestGuard is the URL-level check run on the first request and on every
// redirect. The dialer in netguard remains the authoritative check.
type requestGuard struct {
	policy  netguard.Policy
	allowed []string
	blocked []string
}

func newRequestGuard(opts Options) *requestGuard {
	return &requestGuard{
		policy:  netguard.Policy{AllowLoopback: opts.AllowLoopback, AllowPrivate: opts.AllowPrivate},
		allowed: normalizePatterns(opts.AllowedDomains),
		blocked: normalizePatterns(opts.BlockedDomains),
	}
}

// check validates scheme, host class and domain lists. Policy refusals come
// back without a config hint; callers add it once with withHint.
func (g *requestGuard) check(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q: only http and https are supported", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL %q has no host", u.Redacted())
	}
	host, err := netguard.HostToASCII(u.Hostname())
	if err != nil {
		return fmt.Errorf("%w: %w", err, netguard.ErrBlocked)
	}
	if slices.Contains(strings.Split(strings.TrimSuffix(host, "."), "."), "") {
		return fmt.Errorf("host %q has an empty label: %w", host, netguard.ErrBlocked)
	}
	if err := g.policy.CheckHost(host); err != nil {
		return err
	}
	if netguard.EndsInNumber(host) {
		return fmt.Errorf("host %q is a numeric IPv4 form other than a.b.c.d; write the dotted-quad address: %w", host, netguard.ErrBlocked)
	}
	return g.checkDomain(host)
}

// checkDomain applies blocked_domains, then allowed_domains to an
// IDNA-mapped host. The lists never open an IP class.
func (g *requestGuard) checkDomain(host string) error {
	h := normalizeHost(host)
	if matchAny(h, g.blocked) {
		return fmt.Errorf("host %q is listed in [web.fetch] blocked_domains: %w", host, netguard.ErrBlocked)
	}
	if len(g.allowed) > 0 && !matchAny(h, g.allowed) {
		return fmt.Errorf("host %q is not in [web.fetch] allowed_domains: %w", host, netguard.ErrBlocked)
	}
	return nil
}

// withHint appends the [web.fetch] key that opens a blocked address class,
// so the model stops retrying and the operator knows what to change.
func withHint(err error) error {
	var be *netguard.BlockedError
	if !errors.As(err, &be) {
		return err
	}
	if key := be.Class.Opener(); key != "" {
		return fmt.Errorf("%w (set [web.fetch] %s = true to allow)", err, key)
	}
	return fmt.Errorf("%w (never allowed)", err)
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func normalizePatterns(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = normalizeHost(p)
		// Map only the host part; HostToASCII rejects the "*" label.
		base, wild := strings.CutPrefix(p, "*.")
		if a, err := netguard.HostToASCII(base); err == nil {
			base = a
		}
		if wild {
			p = "*." + base
		} else {
			p = base
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func matchAny(host string, patterns []string) bool {
	for _, p := range patterns {
		if matchDomain(host, p) {
			return true
		}
	}
	return false
}

// matchDomain matches "example.com" exactly; "*.example.com" matches
// example.com and any subdomain. Same shape as [browser.url_allowlist].
func matchDomain(host, pattern string) bool {
	if base, ok := strings.CutPrefix(pattern, "*."); ok {
		return host == base || strings.HasSuffix(host, "."+base)
	}
	return host == pattern
}
