// Package netguard decides which network addresses an outbound HTTP client
// may reach. It blocks link-local, cloud metadata, unspecified, multicast and
// broadcast addresses always, and loopback and private ranges unless a Policy
// opts in. The authoritative check runs on the connected IP at dial time.
package netguard

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"syscall"
)

// ErrBlocked is wrapped by every refusal from this package.
var ErrBlocked = errors.New("blocked by network policy")

// Class is the category an address falls into.
type Class int

// Address classes. Only ClassPublic is allowed by the zero Policy.
const (
	ClassPublic Class = iota
	ClassLoopback
	ClassPrivate
	ClassLinkLocal
	ClassMetadata
	ClassUnspecified
	ClassMulticast
	ClassBroadcast
	ClassProxy
	ClassInvalid
)

func (c Class) String() string {
	switch c {
	case ClassPublic:
		return "public"
	case ClassLoopback:
		return "loopback"
	case ClassPrivate:
		return "private network"
	case ClassLinkLocal:
		return "link-local or cloud metadata"
	case ClassMetadata:
		return "cloud metadata"
	case ClassUnspecified:
		return "unspecified"
	case ClassMulticast:
		return "multicast"
	case ClassBroadcast:
		return "broadcast"
	case ClassProxy:
		return "configured proxy"
	default:
		return "unrecognised"
	}
}

// Opener returns the policy key that opens this class, or "" when nothing
// does.
func (c Class) Opener() string {
	switch c {
	case ClassLoopback:
		return "allow_loopback"
	case ClassPrivate:
		return "allow_private"
	default:
		return ""
	}
}

// BlockedError reports a refused host or address. It unwraps to ErrBlocked.
type BlockedError struct {
	Host  string
	Class Class
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s is a %s address: %v", e.Host, e.Class, ErrBlocked)
}

func (e *BlockedError) Unwrap() error { return ErrBlocked }

// Policy selects which optional classes are reachable. The zero value allows
// public addresses only.
type Policy struct {
	AllowLoopback bool
	AllowPrivate  bool
}

// Allows reports whether the policy permits class c.
func (p Policy) Allows(c Class) bool {
	switch c {
	case ClassPublic:
		return true
	case ClassLoopback:
		return p.AllowLoopback
	case ClassPrivate:
		return p.AllowPrivate
	default:
		return false
	}
}

// CheckIP returns a *BlockedError when the policy refuses addr.
func (p Policy) CheckIP(addr netip.Addr) error {
	c := Classify(addr)
	if p.Allows(c) {
		return nil
	}
	return &BlockedError{Host: addr.String(), Class: c}
}

var metadataHosts = map[string]bool{
	"metadata.google.internal": true,
	"metadata.google.com":      true,
}

// CheckHost checks a URL hostname before any request is made: literal IPs,
// localhost names and metadata names. Other names pass; the dialer checks
// what they resolve to.
func (p Policy) CheckHost(host string) error {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if addr, err := netip.ParseAddr(h); err == nil {
		return p.CheckIP(addr)
	}
	if metadataHosts[h] {
		return &BlockedError{Host: host, Class: ClassMetadata}
	}
	if (h == "localhost" || strings.HasSuffix(h, ".localhost")) && !p.AllowLoopback {
		return &BlockedError{Host: host, Class: ClassLoopback}
	}
	return nil
}

// Control is a net.Dialer.Control hook. It runs after DNS resolution, on the
// exact IP being connected, so it also stops DNS rebinding.
func (p Policy) Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return &BlockedError{Host: address, Class: ClassInvalid}
	}
	return p.CheckIP(ap.Addr())
}

var rules = []struct {
	prefix netip.Prefix
	class  Class
}{
	// First match wins. These metadata endpoints sit inside private ranges
	// (AWS IPv6 in ULA, Alibaba in CGNAT) or public space (Azure WireServer)
	// and must stay closed under every policy.
	{netip.MustParsePrefix("fd00:ec2::254/128"), ClassMetadata},
	{netip.MustParsePrefix("100.100.100.200/32"), ClassMetadata},
	{netip.MustParsePrefix("168.63.129.16/32"), ClassMetadata},
	{netip.MustParsePrefix("0.0.0.0/8"), ClassUnspecified},
	{netip.MustParsePrefix("127.0.0.0/8"), ClassLoopback},
	{netip.MustParsePrefix("169.254.0.0/16"), ClassLinkLocal},
	{netip.MustParsePrefix("10.0.0.0/8"), ClassPrivate},
	{netip.MustParsePrefix("172.16.0.0/12"), ClassPrivate},
	{netip.MustParsePrefix("192.168.0.0/16"), ClassPrivate},
	{netip.MustParsePrefix("100.64.0.0/10"), ClassPrivate},
	{netip.MustParsePrefix("224.0.0.0/4"), ClassMulticast},
	{netip.MustParsePrefix("255.255.255.255/32"), ClassBroadcast},
	{netip.MustParsePrefix("::/128"), ClassUnspecified},
	{netip.MustParsePrefix("::1/128"), ClassLoopback},
	{netip.MustParsePrefix("fe80::/10"), ClassLinkLocal},
	{netip.MustParsePrefix("fc00::/7"), ClassPrivate},
	{netip.MustParsePrefix("ff00::/8"), ClassMulticast},
}

var (
	nat64         = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalUse = netip.MustParsePrefix("64:ff9b:1::/48")
	ipv4Compat    = netip.MustParsePrefix("::/96")
	sixToFour     = netip.MustParsePrefix("2002::/16")
)

// Classify returns the class of addr. IPv6 forms that carry an IPv4 address
// (mapped, IPv4-compatible, well-known NAT64, 6to4) are classified by that
// IPv4 address. Local-use NAT64 is private; see classifyLocalUseNAT64.
func Classify(addr netip.Addr) Class {
	if !addr.IsValid() {
		return ClassInvalid
	}
	addr = addr.WithZone("")
	if nat64LocalUse.Contains(addr) {
		return classifyLocalUseNAT64(addr)
	}
	return classifyRules(embeddedIPv4(addr))
}

func classifyRules(addr netip.Addr) Class {
	for _, r := range rules {
		if r.prefix.Contains(addr) {
			return r.class
		}
	}
	return ClassPublic
}

// classifyLocalUseNAT64 treats 64:ff9b:1::/48 as private: RFC 8215 §5 leaves
// its layout to the site, and IANA lists it as not globally reachable. A
// never-open IPv4 in the /96 position keeps its class, so allow_private does
// not open it. 0.0.0.0/8 there is skipped: it is never a destination, and
// other layouts put zeros in those bits.
func classifyLocalUseNAT64(a netip.Addr) Class {
	b := a.As16()
	inner := classifyRules(netip.AddrFrom4([4]byte(b[12:16])))
	if inner != ClassPublic && inner != ClassUnspecified && inner.Opener() == "" {
		return inner
	}
	return ClassPrivate
}

// embeddedIPv4 returns the IPv4 address carried by an IPv6 form, or a
// unchanged.
func embeddedIPv4(a netip.Addr) netip.Addr {
	if a.Is4In6() {
		return a.Unmap()
	}
	if a == netip.IPv6Unspecified() || a == netip.IPv6Loopback() {
		return a
	}
	b := a.As16()
	switch {
	case nat64.Contains(a), ipv4Compat.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:16]))
	case sixToFour.Contains(a):
		return netip.AddrFrom4([4]byte(b[2:6]))
	}
	return a
}
