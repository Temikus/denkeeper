package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return a
}

func assertClass(t *testing.T, addr string, want Class) {
	t.Helper()
	if got := Classify(mustAddr(t, addr)); got != want {
		t.Errorf("Classify(%s) = %v, want %v", addr, got, want)
	}
}

// assertBlockedUnderAll checks an address stays blocked with every opt-in on.
func assertBlockedUnderAll(t *testing.T, addr string) {
	t.Helper()
	open := Policy{AllowLoopback: true, AllowPrivate: true}
	err := open.CheckIP(mustAddr(t, addr))
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("CheckIP(%s) with all opt-ins = %v, want ErrBlocked", addr, err)
	}
}

func TestClassify_LinkLocalAndMetadata(t *testing.T) {
	for _, a := range []string{"169.254.169.254", "169.254.0.1", "fe80::1", "fe80::1%en0"} {
		assertClass(t, a, ClassLinkLocal)
		assertBlockedUnderAll(t, a)
	}
}

func TestClassify_Unspecified(t *testing.T) {
	for _, a := range []string{"0.0.0.0", "0.1.2.3", "::"} {
		assertClass(t, a, ClassUnspecified)
		assertBlockedUnderAll(t, a)
	}
}

func TestClassify_Multicast(t *testing.T) {
	for _, a := range []string{"224.0.0.1", "239.255.255.250", "ff02::1", "ff0e::1"} {
		assertClass(t, a, ClassMulticast)
		assertBlockedUnderAll(t, a)
	}
}

func TestClassify_Broadcast(t *testing.T) {
	assertClass(t, "255.255.255.255", ClassBroadcast)
	assertBlockedUnderAll(t, "255.255.255.255")
}

func TestClassify_Loopback(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "127.255.0.9", "::1"} {
		assertClass(t, a, ClassLoopback)
	}
}

func TestClassify_Private(t *testing.T) {
	for _, a := range []string{
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"100.64.0.1", "100.127.255.255", "fc00::1", "fd12:3456::1",
	} {
		assertClass(t, a, ClassPrivate)
	}
}

func TestClassify_PublicStaysAllowed(t *testing.T) {
	for _, a := range []string{
		"93.184.216.34", "8.8.8.8", "172.32.0.1", "100.128.0.1",
		"2606:4700:4700::1111",
	} {
		assertClass(t, a, ClassPublic)
		if err := (Policy{}).CheckIP(mustAddr(t, a)); err != nil {
			t.Errorf("default policy CheckIP(%s) = %v, want nil", a, err)
		}
	}
}

func TestClassify_IPv4MappedIPv6(t *testing.T) {
	assertClass(t, "::ffff:127.0.0.1", ClassLoopback)
	assertClass(t, "::ffff:169.254.169.254", ClassLinkLocal)
	assertClass(t, "::ffff:10.0.0.1", ClassPrivate)
	assertClass(t, "::ffff:8.8.8.8", ClassPublic)
}

func TestClassify_NAT64(t *testing.T) {
	assertClass(t, "64:ff9b::a9fe:a9fe", ClassLinkLocal) // 169.254.169.254
	assertClass(t, "64:ff9b::7f00:1", ClassLoopback)
	assertClass(t, "64:ff9b::808:808", ClassPublic)
}

func TestClassify_6to4(t *testing.T) {
	assertClass(t, "2002:a9fe:a9fe::", ClassLinkLocal)
	assertClass(t, "2002:c0a8:0101::1", ClassPrivate) // 192.168.1.1
	assertClass(t, "2002:0808:0808::1", ClassPublic)
}

func TestPolicy_DefaultBlocksLoopbackAndPrivate(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "::1", "10.0.0.1", "fd00::1"} {
		if err := (Policy{}).CheckIP(mustAddr(t, a)); !errors.Is(err, ErrBlocked) {
			t.Errorf("default CheckIP(%s) = %v, want ErrBlocked", a, err)
		}
	}
}

func TestPolicy_AllowLoopbackOpensOnlyLoopback(t *testing.T) {
	p := Policy{AllowLoopback: true}
	if err := p.CheckIP(mustAddr(t, "127.0.0.1")); err != nil {
		t.Errorf("loopback: %v", err)
	}
	if err := p.CheckIP(mustAddr(t, "::1")); err != nil {
		t.Errorf("::1: %v", err)
	}
	for _, a := range []string{"10.0.0.1", "192.168.0.1", "169.254.169.254", "0.0.0.0"} {
		if err := p.CheckIP(mustAddr(t, a)); !errors.Is(err, ErrBlocked) {
			t.Errorf("AllowLoopback CheckIP(%s) = %v, want ErrBlocked", a, err)
		}
	}
}

func TestPolicy_AllowPrivateOpensOnlyPrivate(t *testing.T) {
	p := Policy{AllowPrivate: true}
	for _, a := range []string{"10.0.0.1", "172.20.0.1", "192.168.0.1", "100.64.0.1", "fd00::1"} {
		if err := p.CheckIP(mustAddr(t, a)); err != nil {
			t.Errorf("AllowPrivate CheckIP(%s) = %v", a, err)
		}
	}
	for _, a := range []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0"} {
		if err := p.CheckIP(mustAddr(t, a)); !errors.Is(err, ErrBlocked) {
			t.Errorf("AllowPrivate CheckIP(%s) = %v, want ErrBlocked", a, err)
		}
	}
}

func TestBlockedError_NamesOpener(t *testing.T) {
	err := (Policy{}).CheckIP(mustAddr(t, "127.0.0.1"))
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("want *BlockedError, got %T", err)
	}
	if be.Class.Opener() != "allow_loopback" {
		t.Errorf("opener = %q, want allow_loopback", be.Class.Opener())
	}
	if ClassLinkLocal.Opener() != "" {
		t.Errorf("link-local must have no opener")
	}
	if ClassPrivate.Opener() != "allow_private" {
		t.Errorf("private opener = %q", ClassPrivate.Opener())
	}
}

func TestCheckHost_Localhost(t *testing.T) {
	for _, h := range []string{"localhost", "LOCALHOST", "localhost.", "foo.localhost"} {
		if err := (Policy{}).CheckHost(h); !errors.Is(err, ErrBlocked) {
			t.Errorf("CheckHost(%q) = %v, want ErrBlocked", h, err)
		}
	}
	if err := (Policy{AllowLoopback: true}).CheckHost("localhost"); err != nil {
		t.Errorf("AllowLoopback CheckHost(localhost) = %v", err)
	}
}

func TestCheckHost_MetadataNamesNeverOpen(t *testing.T) {
	open := Policy{AllowLoopback: true, AllowPrivate: true}
	for _, h := range []string{"metadata.google.internal", "Metadata.Google.Internal.", "metadata.google.com"} {
		if err := open.CheckHost(h); !errors.Is(err, ErrBlocked) {
			t.Errorf("CheckHost(%q) = %v, want ErrBlocked", h, err)
		}
	}
}

func TestCheckHost_LiteralIPs(t *testing.T) {
	for _, h := range []string{"169.254.169.254", "10.0.0.1", "::1", "::ffff:127.0.0.1", "0.0.0.0"} {
		if err := (Policy{}).CheckHost(h); !errors.Is(err, ErrBlocked) {
			t.Errorf("CheckHost(%q) = %v, want ErrBlocked", h, err)
		}
	}
}

func TestCheckHost_PublicNameAllowed(t *testing.T) {
	for _, h := range []string{"example.com", "93.184.216.34", "localhost.example.com"} {
		if err := (Policy{}).CheckHost(h); err != nil {
			t.Errorf("CheckHost(%q) = %v, want nil", h, err)
		}
	}
}

func TestControl_RejectsUnparseableAddress(t *testing.T) {
	if err := (Policy{}).Control("tcp", "not-an-address", nil); !errors.Is(err, ErrBlocked) {
		t.Errorf("Control(bad) = %v, want ErrBlocked", err)
	}
}

// The transport has no URL-string check, so this refusal can only come from
// the dialer's Control hook.
func TestTransport_DialTimeBlocksLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("reached"))
	}))
	defer srv.Close()

	client := &http.Client{Transport: Policy{}.Transport(), Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected dial to be refused")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", hits.Load())
	}
}

func TestTransport_AllowLoopbackConnects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("reached"))
	}))
	defer srv.Close()

	client := &http.Client{Transport: Policy{AllowLoopback: true}.Transport(), Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
}

func TestProxyAddrs_DefaultPortsAndUserinfo(t *testing.T) {
	// Built with url.UserPassword so the secret scanner doesn't read it as a credential.
	withUserinfo := (&url.URL{Scheme: "http", User: url.UserPassword("user", "pw"), Host: "127.0.0.1:3128"}).String()
	got := proxyAddrs(withUserinfo, "proxy.corp", "socks5://10.1.1.1", "")
	for _, want := range []string{"127.0.0.1:3128", "proxy.corp:80", "10.1.1.1:1080"} {
		if !got[want] {
			t.Errorf("proxyAddrs missing %q (got %v)", want, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3: %v", len(got), got)
	}
	for k := range got {
		if strings.Contains(k, "pw") {
			t.Errorf("credentials leaked into key %q", k)
		}
	}
}

// A loopback proxy must stay reachable under the default policy, or every
// fetch fails when HTTP_PROXY points at a local sidecar.
func TestTransport_ProxyDialExempt(t *testing.T) {
	var gotURI atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI.Store(r.RequestURI)
		_, _ = w.Write([]byte("via proxy"))
	}))
	defer proxy.Close()
	// httpproxy reads the lowercase names first.
	for _, k := range []string{"http_proxy", "HTTP_PROXY"} {
		t.Setenv(k, proxy.URL)
	}
	for _, k := range []string{"no_proxy", "NO_PROXY"} {
		t.Setenv(k, "")
	}

	client := &http.Client{Transport: Policy{}.Transport(), Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/page", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxied request failed: %v", err)
	}
	_ = resp.Body.Close()
	if gotURI.Load() != "http://example.com/page" {
		t.Errorf("proxy saw %v, want absolute URI", gotURI.Load())
	}
}

// Some metadata endpoints sit inside private ranges; allow_private must not
// open them.
func TestClassify_MetadataInsidePrivateRanges(t *testing.T) {
	for _, a := range []string{"fd00:ec2::254", "100.100.100.200", "::ffff:100.100.100.200"} {
		assertClass(t, a, ClassMetadata)
		assertBlockedUnderAll(t, a)
	}
}

func TestClassify_AzureWireServer(t *testing.T) {
	assertClass(t, "168.63.129.16", ClassMetadata)
	assertBlockedUnderAll(t, "168.63.129.16")
}

// RFC 8215 leaves the local-use layout to the site, so the prefix is private
// whatever an embedded IPv4 decodes to.
func TestClassify_LocalUseNAT64_Private(t *testing.T) {
	assertClass(t, "64:ff9b:1::808:808", ClassPrivate)    // 8.8.8.8, /96 layout
	assertClass(t, "64:ff9b:1:808:8:800::", ClassPrivate) // 8.8.8.8, /48 layout
}

// The /48 layout of 169.254.169.254 reads as 0.0.0.0 under /96; it must
// not come out public.
func TestClassify_LocalUseNAT64_48LayoutBlockedByDefault(t *testing.T) {
	a := mustAddr(t, "64:ff9b:1:a9fe:a9:fe00::")
	if err := (Policy{}).CheckIP(a); !errors.Is(err, ErrBlocked) {
		t.Errorf("CheckIP(%s) = %v, want ErrBlocked", a, err)
	}
}

func TestClassify_LocalUseNAT64_OpenedByAllowPrivate(t *testing.T) {
	a := mustAddr(t, "64:ff9b:1::808:808")
	if err := (Policy{AllowPrivate: true}).CheckIP(a); err != nil {
		t.Errorf("CheckIP(%s) with allow_private = %v, want nil", a, err)
	}
}

// A never-open IPv4 in the /96 position stays closed under allow_private.
func TestClassify_LocalUseNAT64_NeverOpenStaysBlocked(t *testing.T) {
	assertClass(t, "64:ff9b:1::a9fe:a9fe", ClassLinkLocal) // 169.254.169.254
	assertBlockedUnderAll(t, "64:ff9b:1::a9fe:a9fe")
	assertClass(t, "64:ff9b:1::6464:64c8", ClassMetadata) // 100.100.100.200
}

// 169.254.169.254 in the /48 layout: allow_private must not open it.
func TestClassify_LocalUseNAT64_48LayoutNeverOpenStaysBlocked(t *testing.T) {
	assertClass(t, "64:ff9b:1:a9fe:a9:fe00::", ClassLinkLocal)
	assertBlockedUnderAll(t, "64:ff9b:1:a9fe:a9:fe00::")
}

func TestClassify_LocalUseNAT64_56LayoutNeverOpenStaysBlocked(t *testing.T) {
	assertClass(t, "64:ff9b:1:a9:fe:a9fe::", ClassLinkLocal)
	assertBlockedUnderAll(t, "64:ff9b:1:a9:fe:a9fe::")
}

func TestClassify_LocalUseNAT64_64LayoutNeverOpenStaysBlocked(t *testing.T) {
	assertClass(t, "64:ff9b:1:0:a9:fea9:fe00::", ClassLinkLocal)
	assertBlockedUnderAll(t, "64:ff9b:1:0:a9:fea9:fe00::")
}

func TestEndsInNumber_Decimal(t *testing.T) {
	if !EndsInNumber("2852039166") {
		t.Error("EndsInNumber(2852039166) = false, want true")
	}
}

func TestEndsInNumber_ShortForm(t *testing.T) {
	if !EndsInNumber("127.1") {
		t.Error("EndsInNumber(127.1) = false, want true")
	}
}

func TestEndsInNumber_Hex(t *testing.T) {
	for _, h := range []string{"0x7f.1", "0X7F.0.0.1", "0x7f000001"} {
		if !EndsInNumber(h) {
			t.Errorf("EndsInNumber(%q) = false, want true", h)
		}
	}
}

func TestEndsInNumber_Octal(t *testing.T) {
	if !EndsInNumber("0177.0.0.01") {
		t.Error("EndsInNumber(0177.0.0.01) = false, want true")
	}
}

func TestEndsInNumber_TrailingDot(t *testing.T) {
	if !EndsInNumber("127.1.") {
		t.Error("EndsInNumber(127.1.) = false, want true")
	}
}

func TestEndsInNumber_SeveralTrailingDots(t *testing.T) {
	for _, h := range []string{"2852039166..", "127.1..", "0x7f.1..."} {
		if !EndsInNumber(h) {
			t.Errorf("EndsInNumber(%q) = false, want true", h)
		}
	}
}

// netip parses the canonical form, so CheckHost judges it instead.
func TestEndsInNumber_CanonicalIPv4False(t *testing.T) {
	if EndsInNumber("10.0.0.5") {
		t.Error("EndsInNumber(10.0.0.5) = true, want false")
	}
}

func TestEndsInNumber_HostNamesFalse(t *testing.T) {
	for _, h := range []string{"example.com", "1password.com", "123abc", "v6.example", "::1", ""} {
		if EndsInNumber(h) {
			t.Errorf("EndsInNumber(%q) = true, want false", h)
		}
	}
}

func TestHasEmptyLabel_ExtraTrailingDots(t *testing.T) {
	for _, h := range []string{"localhost..", "127.0.0.1...", "metadata.google.internal.."} {
		if !HasEmptyLabel(h) {
			t.Errorf("HasEmptyLabel(%q) = false, want true", h)
		}
	}
}

func TestHasEmptyLabel_LeadingOrInnerDot(t *testing.T) {
	for _, h := range []string{".example.com", "example..com", "", "."} {
		if !HasEmptyLabel(h) {
			t.Errorf("HasEmptyLabel(%q) = false, want true", h)
		}
	}
}

// One trailing dot is the fully qualified form, not an empty label.
func TestHasEmptyLabel_WellFormedFalse(t *testing.T) {
	for _, h := range []string{"example.com", "example.com.", "localhost", "10.0.0.1", "::1", "fe80::1"} {
		if HasEmptyLabel(h) {
			t.Errorf("HasEmptyLabel(%q) = true, want false", h)
		}
	}
}

func TestClassify_IPv4Compatible(t *testing.T) {
	assertClass(t, "::7f00:1", ClassLoopback)     // ::127.0.0.1
	assertClass(t, "::a9fe:a9fe", ClassLinkLocal) // ::169.254.169.254
	assertClass(t, "::1", ClassLoopback)
	assertClass(t, "::", ClassUnspecified)
}

// The ClassProxy check compares text, but only the exact text the dial
// exemption matches; any other spelling of the proxy's address goes through
// the guarded dialer and the policy.
func TestTransport_OtherSpellingOfProxyAddrUsesPolicy(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	for _, k := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
		t.Setenv(k, "http://localhost:"+port)
	}
	for _, k := range []string{"no_proxy", "NO_PROXY"} {
		t.Setenv(k, "")
	}

	client := &http.Client{Transport: Policy{}.Transport(), Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:"+port+"/", nil)
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	var be *BlockedError
	if !errors.As(err, &be) || be.Class != ClassLoopback {
		t.Errorf("err = %v, want a loopback BlockedError from the dialer", err)
	}
	if hits.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", hits.Load())
	}
}

// A request that goes direct (no proxy for its scheme, or a NO_PROXY match)
// must not inherit the proxy's dial exemption.
func TestTransport_DirectRequestToProxyAddrBlocked(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	proxyURL := "http://localhost:" + port

	// httpproxy reads the lowercase names first.
	for _, k := range []string{"https_proxy", "HTTPS_PROXY"} {
		t.Setenv(k, proxyURL)
	}
	for _, k := range []string{"http_proxy", "HTTP_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(k, "")
	}

	client := &http.Client{Transport: Policy{}.Transport(), Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL+"/", nil)
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", hits.Load())
	}
}
