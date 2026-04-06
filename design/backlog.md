# Backlog

## Resolved-IP SSRF validation (net.Dialer.Control)

**Priority**: Medium
**Area**: `internal/tool/urlvalidate.go`, `internal/tool/manager.go`

### Problem

The current SSRF protection in `validateToolURL` is string-based — it checks the hostname/IP literal in the URL but never resolves DNS. This means:

- A hostname that resolves to `127.0.0.1` bypasses loopback blocking
- DNS rebinding attacks can redirect to internal IPs after initial validation
- Any K8s Service name pointing to a loopback or link-local address sails through

The `allow_loopback` per-tool flag was added as a pragmatic workaround for sidecar MCP servers, but the underlying validation remains shallow.

### Proposed solution

Use Go's `net.Dialer.Control` callback to validate the **resolved IP** at TCP connect time, after DNS resolution but before the connection is established:

```go
dialer := &net.Dialer{
    Control: func(network, address string, c syscall.RawConn) error {
        host, _, _ := net.SplitHostPort(address)
        ip := net.ParseIP(host)
        if ip != nil && isBlockedIP(ip, allowLoopback) {
            return fmt.Errorf("resolved IP %s is blocked", ip)
        }
        return nil
    },
}
transport := &http.Transport{DialContext: dialer.DialContext}
```

This makes the SSRF protection robust regardless of what the URL string looks like, because the check happens on the actual resolved address.

### Changes needed

1. Create a custom `http.Transport` with `DialContext` using a `net.Dialer.Control` that calls `isBlockedHost` (or a variant) on the resolved IP
2. Use this transport as the base for `redirectCheckingRoundTripper` instead of `http.DefaultTransport`
3. The string-based `isBlockedHost` check in `validateToolURL` can remain as a fast-path reject (avoids DNS lookup for obvious cases)
4. Add tests with a local DNS-rebinding test server or by mocking the dialer

### Notes

- Cloud metadata endpoints (`169.254.169.254`, `metadata.google.internal`) must remain blocked even with `allow_loopback`
- The `allow_loopback` flag should still be respected at the Dialer level
- Consider also validating that resolved IPs are not in RFC 1918 private ranges unless explicitly allowed (future enhancement)
