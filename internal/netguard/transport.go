package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/idna"
)

// Transport returns an *http.Transport cloned from http.DefaultTransport
// whose dialer refuses any IP the policy blocks.
func (p Policy) Transport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	t := base.Clone()
	guarded := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: p.Control}
	t.DialContext = guarded.DialContext
	applyProxy(t, guarded)
	return t
}

// applyProxy is the single place that decides proxy behaviour.
//
// HTTP_PROXY / HTTPS_PROXY / NO_PROXY are honoured. The dial to the
// configured proxy itself skips the policy check, since a proxy usually sits
// on loopback or a private range; the dial-time check then sees only the
// proxy's IP, so the proxy must enforce egress for proxied requests. URL
// pre-checks in callers still apply. A request that goes direct to the
// proxy's own host:port is refused, or it would ride the exemption. To ignore
// proxies instead, set t.Proxy = nil here and drop the exemption.
func applyProxy(t *http.Transport, guarded *net.Dialer) {
	cfg := httpproxy.FromEnvironment()
	proxyFor := cfg.ProxyFunc()
	exempt := proxyAddrs(cfg.HTTPProxy, cfg.HTTPSProxy)

	// t.Proxy runs for every request and redirect hop.
	t.Proxy = func(req *http.Request) (*url.URL, error) {
		p, err := proxyFor(req.URL)
		if err == nil && p == nil && exempt[canonicalAddr(req.URL)] {
			return nil, &BlockedError{Host: req.URL.Host, Class: ClassProxy}
		}
		return p, err
	}

	if len(exempt) == 0 {
		return
	}
	direct := &net.Dialer{Timeout: guarded.Timeout, KeepAlive: guarded.KeepAlive}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if exempt[strings.ToLower(addr)] {
			return direct.DialContext(ctx, network, addr)
		}
		return guarded.DialContext(ctx, network, addr)
	}
}

// proxyAddrs returns the host:port keys http.Transport dials for each proxy
// setting, with the scheme's default port filled in.
func proxyAddrs(raw ...string) map[string]bool {
	out := make(map[string]bool)
	for _, s := range raw {
		if s == "" {
			continue
		}
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			u, err = url.Parse("http://" + s)
			if err != nil || u.Host == "" {
				continue
			}
		}
		port := u.Port()
		if port == "" {
			port = defaultProxyPort(u.Scheme)
		}
		out[net.JoinHostPort(asciiHost(u.Hostname()), port)] = true
	}
	return out
}

// canonicalAddr returns the host:port http.Transport dials for a direct
// request to u.
func canonicalAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(asciiHost(u.Hostname()), port)
}

// asciiHost is HostToASCII that keeps the input on error, as net/http does.
func asciiHost(h string) string {
	if v, err := HostToASCII(h); err == nil {
		return v
	}
	return strings.ToLower(h)
}

// HostToASCII maps a hostname the way net/http does before dialing: ASCII
// names pass through, others go through idna.Lookup. The result is
// lowercased. Compare hosts in this form, or fullwidth letters and U+3002
// dots slip past string checks while the socket reaches the mapped name.
func HostToASCII(h string) (string, error) {
	if !isASCII(h) {
		v, err := idna.Lookup.ToASCII(h)
		if err != nil {
			return "", fmt.Errorf("mapping host %q to ASCII: %w", h, err)
		}
		h = v
	}
	return strings.ToLower(h), nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func defaultProxyPort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "socks5", "socks5h":
		return "1080"
	default:
		return "80"
	}
}
