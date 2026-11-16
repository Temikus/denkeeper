package netguard

import (
	"net/netip"
	"strings"
)

// EndsInNumber applies the WHATWG URL "ends in a number" rule to a host that
// netip cannot parse. Browsers, and the cgo resolver, read such a host as
// IPv4 in decimal, octal, hex or short form (2852039166, 0x7f.1, 127.1);
// the pure-Go resolver fails on it instead. Refuse it, never decode it.
// Canonical dotted-quad IPv4 returns false: CheckHost judges that.
func EndsInNumber(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	last := h[strings.LastIndexByte(h, '.')+1:]
	if last == "" || (!strings.HasPrefix(last, "0x") && !allDigits(last)) {
		return false
	}
	_, err := netip.ParseAddr(h)
	return err != nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
