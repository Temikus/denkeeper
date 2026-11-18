package netguard

import (
	"slices"
	"strings"
)

// HasEmptyLabel reports whether h, after one trailing dot is trimmed, has an
// empty label. CheckHost trims only one dot, so it does not recognise
// "localhost.." or "127.0.0.1..." as the names they spell. Refuse such hosts.
func HasEmptyLabel(h string) bool {
	return slices.Contains(strings.Split(strings.TrimSuffix(h, "."), "."), "")
}
