# Backlog

## ~~Resolved-IP SSRF validation (net.Dialer.Control)~~ ✅ DONE

**Status**: Implemented — `SSRFSafeTransport()` in `internal/tool/urlvalidate.go` uses `net.Dialer.Control` with IP-level blocking, wired into `manager.go`. String-based fast-path reject retained alongside the connect-time check.
