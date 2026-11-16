package netguard

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
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
// pre-checks in callers still apply. To ignore proxies instead, set
// t.Proxy = nil here and drop the exemption.
func applyProxy(t *http.Transport, guarded *net.Dialer) {
	cfg := httpproxy.FromEnvironment()
	proxyFor := cfg.ProxyFunc()
	t.Proxy = func(req *http.Request) (*url.URL, error) { return proxyFor(req.URL) }

	exempt := proxyAddrs(cfg.HTTPProxy, cfg.HTTPSProxy)
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
		out[net.JoinHostPort(strings.ToLower(u.Hostname()), port)] = true
	}
	return out
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
