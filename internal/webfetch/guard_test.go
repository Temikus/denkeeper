package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/netguard"
)

// countingServer returns a live server and a counter of handler runs.
func countingServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if h != nil {
			h(w, r)
			return
		}
		_, _ = fmt.Fprint(w, "reached")
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// newTestJina points a JinaFetcher at a local server. Its guarded transport
// is swapped for one that admits loopback, or the server would be unreachable
// and "zero hits" assertions would pass vacuously.
func newTestJina(baseURL string) *JinaFetcher {
	j := NewJinaFetcher(5*time.Second, nil)
	j.baseURL = baseURL
	j.client.Transport = netguard.Policy{AllowLoopback: true}.Transport()
	return j
}

func assertBlocked(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a blocked error, got nil")
	}
	if !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("err = %v, want netguard.ErrBlocked", err)
	}
}

func TestDefaultFetcher_DefaultPolicyBlocksLoopback(t *testing.T) {
	srv, hits := countingServer(t, nil)

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	_, err := f.Fetch(context.Background(), srv.URL)
	assertBlocked(t, err)
	if hits.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", hits.Load())
	}
	if !strings.Contains(err.Error(), "[web.fetch] allow_loopback") {
		t.Errorf("error should name the opening key: %v", err)
	}
}

func TestDefaultFetcher_RedirectToMetadataBlocked(t *testing.T) {
	srv, _ := countingServer(t, redirectTo("http://169.254.169.254/latest/meta-data/"))

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second, AllowLoopback: true})
	_, err := f.Fetch(context.Background(), srv.URL)
	assertBlocked(t, err)
	if strings.Contains(err.Error(), "allow_") {
		t.Errorf("metadata has no opener; error must not suggest one: %v", err)
	}
}

func TestDefaultFetcher_RedirectToPrivateBlocked(t *testing.T) {
	srv, _ := countingServer(t, redirectTo("http://10.0.0.1/"))

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second, AllowLoopback: true})
	_, err := f.Fetch(context.Background(), srv.URL)
	assertBlocked(t, err)
	if !strings.Contains(err.Error(), "[web.fetch] allow_private") {
		t.Errorf("error should name allow_private: %v", err)
	}
}

func TestDefaultFetcher_RobotsLookupBlockedByDefault(t *testing.T) {
	srv, hits := countingServer(t, nil)

	f := NewDefaultFetcher(Options{
		Timeout:          5 * time.Second,
		RespectRobotsTxt: true,
		RespectAgentsTxt: true,
	})
	_, err := f.Fetch(context.Background(), srv.URL+"/page")
	assertBlocked(t, err)
	if hits.Load() != 0 {
		t.Errorf("server saw %d requests, want 0", hits.Load())
	}
}

func TestDefaultFetcher_MetadataHostnameBlocked(t *testing.T) {
	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second, AllowLoopback: true, AllowPrivate: true})
	_, err := f.Fetch(context.Background(), "http://metadata.google.internal/computeMetadata/v1/")
	assertBlocked(t, err)
}

func TestChainFetcher_BlockedPrimarySkipsJina(t *testing.T) {
	target, _ := countingServer(t, nil)
	jinaSrv, jinaHits := countingServer(t, nil)

	jina := newTestJina(jinaSrv.URL)
	primary := NewDefaultFetcher(Options{Timeout: 5 * time.Second})

	c := NewChainFetcher(primary, jina, nil)
	_, err := c.Fetch(context.Background(), target.URL)
	assertBlocked(t, err)
	if jinaHits.Load() != 0 {
		t.Errorf("Jina saw %d requests, want 0", jinaHits.Load())
	}
}

func TestChainFetcher_BlockedRedirectSkipsJina(t *testing.T) {
	target, _ := countingServer(t, redirectTo("http://169.254.169.254/"))
	jinaSrv, jinaHits := countingServer(t, nil)

	jina := newTestJina(jinaSrv.URL)
	primary := NewDefaultFetcher(Options{Timeout: 5 * time.Second, AllowLoopback: true})

	_, err := NewChainFetcher(primary, jina, nil).Fetch(context.Background(), target.URL)
	assertBlocked(t, err)
	if jinaHits.Load() != 0 {
		t.Errorf("Jina saw %d requests, want 0", jinaHits.Load())
	}
}

func TestJinaFetcher_RefusesBlockedTarget(t *testing.T) {
	jinaSrv, jinaHits := countingServer(t, nil)
	jina := newTestJina(jinaSrv.URL)

	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/api/v1/sessions",
		"http://10.0.0.1/",
		"http://localhost/",
	} {
		_, err := jina.Fetch(context.Background(), u)
		if !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("Fetch(%s) err = %v, want ErrBlocked", u, err)
		}
	}
	if jinaHits.Load() != 0 {
		t.Errorf("Jina saw %d requests, want 0", jinaHits.Load())
	}
}

func TestJinaFetcher_RefusesNonHTTPScheme(t *testing.T) {
	jinaSrv, jinaHits := countingServer(t, nil)
	jina := newTestJina(jinaSrv.URL)

	if _, err := jina.Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("expected scheme error")
	}
	if jinaHits.Load() != 0 {
		t.Errorf("Jina saw %d requests, want 0", jinaHits.Load())
	}
}

func TestDefaultFetcher_AllowedDomainMatch(t *testing.T) {
	srv, hits := countingServer(t, nil)

	f := NewDefaultFetcher(Options{
		Timeout:        5 * time.Second,
		AllowLoopback:  true,
		AllowedDomains: []string{"127.0.0.1"},
	})
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
}

func TestDefaultFetcher_AllowedDomainMiss(t *testing.T) {
	srv, hits := countingServer(t, nil)

	f := NewDefaultFetcher(Options{
		Timeout:        5 * time.Second,
		AllowLoopback:  true,
		AllowedDomains: []string{"example.com"},
	})
	_, err := f.Fetch(context.Background(), srv.URL)
	assertBlocked(t, err)
	if !strings.Contains(err.Error(), "allowed_domains") {
		t.Errorf("error should name allowed_domains: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("hits = %d, want 0", hits.Load())
	}
}

func TestDefaultFetcher_BlockedDomainWinsOverAllowed(t *testing.T) {
	f := NewDefaultFetcher(Options{
		Timeout:        5 * time.Second,
		AllowedDomains: []string{"*.example.com"},
		BlockedDomains: []string{"evil.example.com"},
	})
	_, err := f.Fetch(context.Background(), "https://evil.example.com/?q=secret")
	assertBlocked(t, err)
	if !strings.Contains(err.Error(), "blocked_domains") {
		t.Errorf("error should name blocked_domains: %v", err)
	}
}

func TestDefaultFetcher_WildcardDomain(t *testing.T) {
	g := newRequestGuard(Options{AllowedDomains: []string{"*.Example.com"}})
	for _, h := range []string{"example.com", "docs.example.com", "a.b.example.com", "DOCS.EXAMPLE.COM."} {
		if err := g.checkDomain(h); err != nil {
			t.Errorf("checkDomain(%q) = %v, want nil", h, err)
		}
	}
	for _, h := range []string{"notexample.com", "example.com.evil.net"} {
		if err := g.checkDomain(h); !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("checkDomain(%q) = %v, want ErrBlocked", h, err)
		}
	}
}

func TestDefaultFetcher_RedirectToBlockedDomain(t *testing.T) {
	srv, _ := countingServer(t, redirectTo("http://localhost/landing"))

	f := NewDefaultFetcher(Options{
		Timeout:        5 * time.Second,
		AllowLoopback:  true,
		BlockedDomains: []string{"localhost"},
	})
	_, err := f.Fetch(context.Background(), srv.URL)
	assertBlocked(t, err)
	if !strings.Contains(err.Error(), "blocked_domains") {
		t.Errorf("error should name blocked_domains: %v", err)
	}
}

func TestDefaultFetcher_DomainListDoesNotOpenIPClass(t *testing.T) {
	srv, hits := countingServer(t, nil)

	f := NewDefaultFetcher(Options{
		Timeout:        5 * time.Second,
		AllowedDomains: []string{"127.0.0.1"},
	})
	_, err := f.Fetch(context.Background(), srv.URL)
	assertBlocked(t, err)
	if hits.Load() != 0 {
		t.Errorf("hits = %d, want 0", hits.Load())
	}
}

// net/http dials the IDNA-mapped name, so the domain lists must compare
// against it too.
func TestDefaultFetcher_FullwidthHostBlockedDomain(t *testing.T) {
	srv, hits := countingServer(t, nil)
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	f := NewDefaultFetcher(Options{
		Timeout:        5 * time.Second,
		AllowLoopback:  true,
		BlockedDomains: []string{"localhost"},
	})
	_, err := f.Fetch(context.Background(), "http://ｌｏｃａｌｈｏｓｔ:"+port+"/")
	assertBlocked(t, err)
	if hits.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", hits.Load())
	}
}

func TestDefaultFetcher_IdeographicDotBlockedDomain(t *testing.T) {
	g := newRequestGuard(Options{BlockedDomains: []string{"evil.com"}})
	u, err := url.Parse("https://evil。com/?q=secret")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := g.check(u); !errors.Is(err, netguard.ErrBlocked) {
		t.Errorf("check = %v, want ErrBlocked", err)
	}
}

func TestDefaultFetcher_InvalidIDNAHostRefused(t *testing.T) {
	g := newRequestGuard(Options{})
	// Mixed-direction label: fails the IDNA bidi rule.
	u := &url.URL{Scheme: "https", Host: "a\u05d0.example"}
	if err := g.check(u); !errors.Is(err, netguard.ErrBlocked) {
		t.Errorf("check = %v, want ErrBlocked", err)
	}
}

// The production client must refuse non-public dials, so a 3xx from Jina
// cannot steer it at this host's network.
func TestJinaFetcher_ClientUsesGuardedTransport(t *testing.T) {
	jinaSrv, hits := countingServer(t, nil)
	jina := NewJinaFetcher(5*time.Second, nil)
	jina.baseURL = jinaSrv.URL // loopback: the default policy must refuse it

	_, err := jina.Fetch(context.Background(), "https://example.com")
	assertBlocked(t, err)
	if hits.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", hits.Load())
	}
}

// HostToASCII rejects "*", so only the part after "*." may be mapped.
func TestDefaultFetcher_NonASCIIWildcardPattern(t *testing.T) {
	g := newRequestGuard(Options{BlockedDomains: []string{"*.ｅｖｉｌ.com"}})
	for _, h := range []string{"evil.com", "sub.evil.com"} {
		if err := g.checkDomain(h); !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("checkDomain(%q) = %v, want ErrBlocked", h, err)
		}
	}
}

// setTestProxy points both proxy env spellings at proxyURL and clears
// NO_PROXY. httpproxy reads the lowercase names first.
func setTestProxy(t *testing.T, proxyURL string) {
	t.Helper()
	for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY"} {
		t.Setenv(k, proxyURL)
	}
	for _, k := range []string{"no_proxy", "NO_PROXY"} {
		t.Setenv(k, "")
	}
}

// With a proxy set, the proxy dial is exempt from the dial check, so a
// redirect from Jina to a private literal must be refused before the hop.
func TestJinaFetcher_RedirectToPrivateViaProxyRefused(t *testing.T) {
	proxy, proxyHits := countingServer(t, nil)
	setTestProxy(t, proxy.URL)
	// Loopback is never proxied, so Jina itself is reached directly.
	jinaSrv, _ := countingServer(t, redirectTo("http://10.0.0.5/secret"))
	jina := newTestJina(jinaSrv.URL)

	_, err := jina.Fetch(context.Background(), "https://example.com")
	assertBlocked(t, err)
	if proxyHits.Load() != 0 {
		t.Errorf("proxy saw %d requests, want 0", proxyHits.Load())
	}
}

func TestJinaFetcher_RedirectToMetadataViaProxyRefused(t *testing.T) {
	proxy, proxyHits := countingServer(t, nil)
	setTestProxy(t, proxy.URL)
	jinaSrv, _ := countingServer(t, redirectTo("http://169.254.169.254/latest/meta-data/"))
	jina := newTestJina(jinaSrv.URL)

	_, err := jina.Fetch(context.Background(), "https://example.com")
	assertBlocked(t, err)
	if proxyHits.Load() != 0 {
		t.Errorf("proxy saw %d requests, want 0", proxyHits.Load())
	}
}

// A hand-set CheckRedirect drops net/http's default cap, so it needs its own.
func TestJinaCheckRedirect_CapsAtTenHops(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/next", nil)
	if err := jinaCheckRedirect(req, make([]*http.Request, 9)); err != nil {
		t.Errorf("hop 10 to a public host = %v, want nil", err)
	}
	if err := jinaCheckRedirect(req, make([]*http.Request, 10)); err == nil {
		t.Error("hop 11 = nil, want a redirect-limit error")
	}
}

// netip rejects these forms; a cgo resolver may decode them to the address
// in the comment, the pure-Go one fails with a DNS error that is not
// ErrBlocked. Either way they must be refused up front.
func TestRequestGuard_DecimalIPv4HostRefused(t *testing.T) {
	g := newRequestGuard(Options{})
	u := &url.URL{Scheme: "http", Host: "2852039166"} // 169.254.169.254
	if err := g.check(u); !errors.Is(err, netguard.ErrBlocked) {
		t.Errorf("check = %v, want ErrBlocked", err)
	}
}

func TestRequestGuard_ShortIPv4HostRefused(t *testing.T) {
	g := newRequestGuard(Options{})
	u := &url.URL{Scheme: "http", Host: "127.1"}
	if err := g.check(u); !errors.Is(err, netguard.ErrBlocked) {
		t.Errorf("check = %v, want ErrBlocked", err)
	}
}

func TestRequestGuard_HexIPv4HostRefused(t *testing.T) {
	g := newRequestGuard(Options{})
	u := &url.URL{Scheme: "http", Host: "0x7f.1"}
	if err := g.check(u); !errors.Is(err, netguard.ErrBlocked) {
		t.Errorf("check = %v, want ErrBlocked", err)
	}
}

// Opt-ins don't open these forms: the guard cannot tell what they name.
func TestRequestGuard_NumericHostRefusedWithOptIns(t *testing.T) {
	g := newRequestGuard(Options{AllowLoopback: true, AllowPrivate: true})
	u := &url.URL{Scheme: "http", Host: "127.1"}
	if err := g.check(u); !errors.Is(err, netguard.ErrBlocked) {
		t.Errorf("check = %v, want ErrBlocked", err)
	}
}

func TestRequestGuard_EmptyLabelRefused(t *testing.T) {
	g := newRequestGuard(Options{AllowLoopback: true, AllowPrivate: true})
	for _, h := range []string{"2852039166..", "127.1..", "example..com", ".example.com"} {
		u := &url.URL{Scheme: "http", Host: h}
		if err := g.check(u); !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("check(%q) = %v, want ErrBlocked", h, err)
		}
	}
}

// One trailing dot is the fully qualified form, not an empty label.
func TestRequestGuard_SingleTrailingDotAllowed(t *testing.T) {
	g := newRequestGuard(Options{})
	u := &url.URL{Scheme: "https", Host: "example.com."}
	if err := g.check(u); err != nil {
		t.Errorf("check(example.com.) = %v, want nil", err)
	}
}

func TestChainFetcher_EmptyLabelNumericHostSkipsJina(t *testing.T) {
	jinaSrv, jinaHits := countingServer(t, nil)
	jina := newTestJina(jinaSrv.URL)
	primary := NewDefaultFetcher(Options{Timeout: 5 * time.Second})

	_, err := NewChainFetcher(primary, jina, nil).Fetch(context.Background(), "http://2852039166../latest/meta-data/")
	assertBlocked(t, err)
	if jinaHits.Load() != 0 {
		t.Errorf("Jina saw %d requests, want 0", jinaHits.Load())
	}
}

// pinnedTarget is a public name, so checkJinaTarget lets it through and a
// Jina hit count of 0 means the chain stopped, not that Jina refused it.
const pinnedTarget = "http://example.com"

// pinnedFetcher is a real DefaultFetcher whose dials all go to srv. Only the
// dial is replaced; the guard, robots/agents lookups and fetch run as usual.
func pinnedFetcher(srv *httptest.Server, opts Options) *DefaultFetcher {
	opts.Timeout = 5 * time.Second
	f := NewDefaultFetcher(opts)
	addr := srv.Listener.Addr().String()
	f.client.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
	}
	return f
}

// disallowServer serves policyPath disallowing /private and counts page hits.
func disallowServer(t *testing.T, policyPath string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var pageHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == policyPath {
			_, _ = fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
			return
		}
		pageHits.Add(1)
		_, _ = fmt.Fprint(w, "page")
	}))
	t.Cleanup(srv.Close)
	return srv, &pageHits
}

func assertDisallowedSkippedJina(t *testing.T, err error, pageHits, jinaHits *atomic.Int32) {
	t.Helper()
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("err = %v, want ErrDisallowed", err)
	}
	if errors.Is(err, netguard.ErrBlocked) {
		t.Errorf("err = %v, must not be netguard.ErrBlocked", err)
	}
	if n := jinaHits.Load(); n != 0 {
		t.Errorf("Jina saw %d requests, want 0", n)
	}
	if n := pageHits.Load(); n != 0 {
		t.Errorf("target page saw %d requests, want 0", n)
	}
}

func TestChainFetcher_RobotsDisallowSkipsJina(t *testing.T) {
	target, pageHits := disallowServer(t, "/robots.txt")
	jinaSrv, jinaHits := countingServer(t, nil)

	primary := pinnedFetcher(target, Options{RespectRobotsTxt: true})
	c := NewChainFetcher(primary, newTestJina(jinaSrv.URL), nil)
	_, err := c.Fetch(context.Background(), pinnedTarget+"/private")
	assertDisallowedSkippedJina(t, err, pageHits, jinaHits)
}

func TestChainFetcher_AgentsDisallowSkipsJina(t *testing.T) {
	target, pageHits := disallowServer(t, "/agents.txt")
	jinaSrv, jinaHits := countingServer(t, nil)

	primary := pinnedFetcher(target, Options{RespectAgentsTxt: true})
	c := NewChainFetcher(primary, newTestJina(jinaSrv.URL), nil)
	_, err := c.Fetch(context.Background(), pinnedTarget+"/private")
	assertDisallowedSkippedJina(t, err, pageHits, jinaHits)
}

func TestChainFetcher_RobotsLookupFailureStillFallsBack(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(target.Close)
	jinaSrv, jinaHits := countingServer(t, nil)

	primary := pinnedFetcher(target, Options{RespectRobotsTxt: true})
	c := NewChainFetcher(primary, newTestJina(jinaSrv.URL), nil)
	if _, err := c.Fetch(context.Background(), pinnedTarget+"/page"); err != nil {
		t.Fatalf("Fetch = %v, want the Jina result", err)
	}
	if n := jinaHits.Load(); n != 1 {
		t.Errorf("Jina saw %d requests, want 1", n)
	}
}

func TestChainFetcher_RobotsAllowedPathStillFallsBack(t *testing.T) {
	target, pageHits := disallowServer(t, "/robots.txt")
	jinaSrv, jinaHits := countingServer(t, nil)

	primary := pinnedFetcher(target, Options{RespectRobotsTxt: true})
	c := NewChainFetcher(primary, newTestJina(jinaSrv.URL), nil)
	if _, err := c.Fetch(context.Background(), pinnedTarget+"/public"); err != nil {
		t.Fatalf("Fetch = %v, want the Jina result", err)
	}
	if n := pageHits.Load(); n != 1 {
		t.Errorf("target page saw %d requests, want 1", n)
	}
	if n := jinaHits.Load(); n != 1 {
		t.Errorf("Jina saw %d requests, want 1 (page is under 100 chars)", n)
	}
}

func TestChainFetcher_NumericHostSkipsJina(t *testing.T) {
	jinaSrv, jinaHits := countingServer(t, nil)
	jina := newTestJina(jinaSrv.URL)
	primary := NewDefaultFetcher(Options{Timeout: 5 * time.Second})

	_, err := NewChainFetcher(primary, jina, nil).Fetch(context.Background(), "http://2852039166/latest/meta-data/")
	assertBlocked(t, err)
	if jinaHits.Load() != 0 {
		t.Errorf("Jina saw %d requests, want 0", jinaHits.Load())
	}
}
