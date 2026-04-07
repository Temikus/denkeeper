package tool

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
)

// validateToolURL checks that rawURL is a valid HTTP(S) URL that does not
// target blocked hosts (SSRF protection). If allowlist is non-empty, the
// host must also match at least one allowed pattern. When allowLoopback is
// true, localhost and loopback IPs (127.x, ::1) are permitted but link-local
// and cloud metadata endpoints remain blocked.
func validateToolURL(rawURL string, allowlist []string, allowLoopback bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Scheme enforcement.
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported URL scheme %q (must be http or https)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	if allowLoopback {
		if isNonLoopbackBlockedHost(host) {
			return fmt.Errorf("URL host %q is blocked (link-local or metadata endpoint)", host)
		}
	} else if isBlockedHost(host) {
		return fmt.Errorf("URL host %q is blocked (localhost, link-local, or metadata endpoint)", host)
	}

	// If an allowlist is configured, the host must match.
	if len(allowlist) > 0 {
		lowerHost := strings.ToLower(host)
		matched := false
		for _, pattern := range allowlist {
			if matchDomain(lowerHost, strings.ToLower(strings.TrimSpace(pattern))) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("URL host %q is not in the MCP URL allowlist", host)
		}
	}

	return nil
}

// headerKeyRegexp matches valid HTTP header field names (RFC 7230 token).
var headerKeyRegexp = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+\-.^_` + "`" + `|~]+$`)

// forbiddenHeaders are header keys that cannot be overridden by tool config.
var forbiddenHeaders = map[string]bool{
	"host":              true,
	"content-length":    true,
	"transfer-encoding": true,
	"connection":        true,
}

// validateHeaders checks that header keys and values are safe for injection
// into HTTP requests. Rejects CRLF injection, non-token keys, and forbidden
// header names.
func validateHeaders(headers map[string]string) error {
	for k, v := range headers {
		if !headerKeyRegexp.MatchString(k) {
			return fmt.Errorf("invalid header key %q: must contain only token characters", k)
		}
		if forbiddenHeaders[strings.ToLower(k)] {
			return fmt.Errorf("header %q cannot be overridden", k)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("header %q value contains illegal characters (CR, LF, or NUL)", k)
		}
	}
	return nil
}

// isBlockedHost returns true for hosts that should always be blocked:
// link-local addresses, cloud metadata endpoints, and localhost.
// Mirrors internal/browser/allowlist.go:isBlockedHost.
func isBlockedHost(host string) bool {
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Not an IP — check known metadata hostnames.
		return host == "metadata.google.internal" ||
			host == "metadata.google.com"
	}

	// IPv4 link-local: 169.254.0.0/16 (includes AWS/GCP/Azure metadata at 169.254.169.254).
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		// Loopback: 127.0.0.0/8.
		if ip4[0] == 127 {
			return true
		}
		return false
	}

	// IPv6 loopback (::1) and link-local (fe80::/10).
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	return false
}

// isNonLoopbackBlockedHost returns true for blocked hosts excluding loopback
// addresses. Used when allow_loopback is set — link-local and cloud metadata
// endpoints are still blocked.
func isNonLoopbackBlockedHost(host string) bool {
	// Cloud metadata hostnames.
	if host == "metadata.google.internal" || host == "metadata.google.com" {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false // non-IP hostnames other than metadata are fine
	}

	// IPv4 link-local: 169.254.0.0/16 (includes AWS/GCP/Azure metadata at 169.254.169.254).
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 169 && ip4[1] == 254
	}

	// IPv6 link-local (fe80::/10).
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
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

// isBlockedIP checks whether a resolved IP address should be blocked for SSRF
// protection. Link-local (169.254.0.0/16, fe80::/10) and cloud metadata IPs
// are always blocked. Loopback (127.0.0.0/8, ::1) is blocked unless
// allowLoopback is true.
func isBlockedIP(ip net.IP, allowLoopback bool) bool {
	if ip == nil {
		return false
	}

	// IPv4 checks.
	if ip4 := ip.To4(); ip4 != nil {
		// Link-local: 169.254.0.0/16 (includes AWS/GCP/Azure metadata at 169.254.169.254).
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		// Loopback: 127.0.0.0/8.
		if ip4[0] == 127 {
			return !allowLoopback
		}
		return false
	}

	// IPv6 link-local (fe80::/10) — always blocked.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// IPv6 loopback (::1).
	if ip.IsLoopback() {
		return !allowLoopback
	}

	return false
}

// SSRFSafeTransport returns an *http.Transport that blocks connections to
// SSRF-sensitive IP addresses at TCP connect time via net.Dialer.Control.
// This prevents DNS-rebinding attacks where a hostname resolves to a blocked IP
// after passing the initial string-based URL validation.
func SSRFSafeTransport(allowLoopback bool) *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.DialContext = (&net.Dialer{
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("SSRF check: invalid address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip != nil && isBlockedIP(ip, allowLoopback) {
				return fmt.Errorf("resolved IP %s is blocked (SSRF protection)", ip)
			}
			return nil
		},
	}).DialContext

	return transport
}

// redirectCheckingRoundTripper wraps a base RoundTripper and validates each
// redirect target against the SSRF blocklist.
type redirectCheckingRoundTripper struct {
	base          http.RoundTripper
	allowlist     []string
	allowLoopback bool
}

func (rt *redirectCheckingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Validate the request URL (covers both initial and redirect targets
	// when used with http.Client's CheckRedirect).
	if err := validateToolURL(req.URL.String(), rt.allowlist, rt.allowLoopback); err != nil {
		return nil, fmt.Errorf("redirect blocked: %w", err)
	}
	return rt.base.RoundTrip(req)
}

// headerRoundTripper injects static headers into every outgoing request.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (rt *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range rt.headers {
		req.Header.Set(k, v)
	}
	return rt.base.RoundTrip(req)
}

// redactURL masks query parameter values and userinfo in a URL for safe logging.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	// Mask userinfo.
	if u.User != nil {
		u.User = url.User("***")
	}
	// Mask query param values.
	q := u.Query()
	for k := range q {
		q.Set(k, "***")
	}
	u.RawQuery = q.Encode()
	return u.String()
}
