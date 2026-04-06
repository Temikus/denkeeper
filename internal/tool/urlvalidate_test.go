package tool

import (
	"testing"
)

func TestValidateToolURL_ValidHTTPS(t *testing.T) {
	err := validateToolURL("https://mcp.example.com/events", nil, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateToolURL_ValidHTTP(t *testing.T) {
	err := validateToolURL("http://mcp.example.com/events", nil, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateToolURL_BlocksNonHTTPScheme(t *testing.T) {
	err := validateToolURL("ftp://mcp.example.com/events", nil, false)
	if err == nil {
		t.Fatal("expected error for ftp scheme")
	}
}

func TestValidateToolURL_BlocksLocalhost(t *testing.T) {
	err := validateToolURL("http://localhost:8080/events", nil, false)
	if err == nil {
		t.Fatal("expected error for localhost")
	}
}

func TestValidateToolURL_BlocksLoopbackIP(t *testing.T) {
	err := validateToolURL("http://127.0.0.1:8080/events", nil, false)
	if err == nil {
		t.Fatal("expected error for 127.0.0.1")
	}
}

func TestValidateToolURL_BlocksLoopbackIPAlt(t *testing.T) {
	err := validateToolURL("http://127.0.0.2:8080/events", nil, false)
	if err == nil {
		t.Fatal("expected error for 127.0.0.2")
	}
}

func TestValidateToolURL_BlocksLinkLocal(t *testing.T) {
	err := validateToolURL("http://169.254.169.254/latest/meta-data/", nil, false)
	if err == nil {
		t.Fatal("expected error for link-local metadata endpoint")
	}
}

func TestValidateToolURL_BlocksMetadataHostname(t *testing.T) {
	err := validateToolURL("http://metadata.google.internal/computeMetadata/v1/", nil, false)
	if err == nil {
		t.Fatal("expected error for metadata.google.internal")
	}
}

func TestValidateToolURL_BlocksIPv6Loopback(t *testing.T) {
	err := validateToolURL("http://[::1]:8080/events", nil, false)
	if err == nil {
		t.Fatal("expected error for IPv6 loopback")
	}
}

func TestValidateToolURL_AllowlistPermits(t *testing.T) {
	err := validateToolURL("https://api.example.com/mcp", []string{"api.example.com"}, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateToolURL_AllowlistWildcard(t *testing.T) {
	err := validateToolURL("https://mcp.internal.corp/events", []string{"*.internal.corp"}, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateToolURL_AllowlistRejects(t *testing.T) {
	err := validateToolURL("https://evil.com/mcp", []string{"api.example.com"}, false)
	if err == nil {
		t.Fatal("expected error for host not in allowlist")
	}
}

func TestValidateToolURL_AllowlistStillBlocksLocalhost(t *testing.T) {
	// Even if localhost is in the allowlist, it should be blocked.
	err := validateToolURL("http://localhost:8080/events", []string{"localhost"}, false)
	if err == nil {
		t.Fatal("expected error for localhost even when in allowlist")
	}
}

func TestValidateHeaders_Valid(t *testing.T) {
	err := validateHeaders(map[string]string{
		"Authorization": "Bearer token123",
		"X-Custom":      "value",
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateHeaders_CRLFInValue(t *testing.T) {
	err := validateHeaders(map[string]string{
		"X-Evil": "value\r\nInjected: header",
	})
	if err == nil {
		t.Fatal("expected error for CRLF in header value")
	}
}

func TestValidateHeaders_NULInValue(t *testing.T) {
	err := validateHeaders(map[string]string{
		"X-Evil": "value\x00null",
	})
	if err == nil {
		t.Fatal("expected error for NUL in header value")
	}
}

func TestValidateHeaders_InvalidKey(t *testing.T) {
	err := validateHeaders(map[string]string{
		"Invalid Key": "value",
	})
	if err == nil {
		t.Fatal("expected error for header key with space")
	}
}

func TestValidateHeaders_ForbiddenHost(t *testing.T) {
	err := validateHeaders(map[string]string{
		"Host": "evil.com",
	})
	if err == nil {
		t.Fatal("expected error for forbidden Host header")
	}
}

func TestValidateHeaders_ForbiddenContentLength(t *testing.T) {
	err := validateHeaders(map[string]string{
		"Content-Length": "999",
	})
	if err == nil {
		t.Fatal("expected error for forbidden Content-Length header")
	}
}

func TestValidateHeaders_ForbiddenTransferEncoding(t *testing.T) {
	err := validateHeaders(map[string]string{
		"Transfer-Encoding": "chunked",
	})
	if err == nil {
		t.Fatal("expected error for forbidden Transfer-Encoding header")
	}
}

// --- allow_loopback tests ---

func TestValidateToolURL_AllowLoopbackPermitsLocalhost(t *testing.T) {
	err := validateToolURL("http://localhost:8080/events", nil, true)
	if err != nil {
		t.Fatalf("expected localhost to be allowed with allowLoopback, got: %v", err)
	}
}

func TestValidateToolURL_AllowLoopbackPermits127(t *testing.T) {
	err := validateToolURL("http://127.0.0.1:8080/events", nil, true)
	if err != nil {
		t.Fatalf("expected 127.0.0.1 to be allowed with allowLoopback, got: %v", err)
	}
}

func TestValidateToolURL_AllowLoopbackPermitsIPv6Loopback(t *testing.T) {
	err := validateToolURL("http://[::1]:8080/events", nil, true)
	if err != nil {
		t.Fatalf("expected ::1 to be allowed with allowLoopback, got: %v", err)
	}
}

func TestValidateToolURL_AllowLoopbackStillBlocksLinkLocal(t *testing.T) {
	err := validateToolURL("http://169.254.169.254/latest/meta-data/", nil, true)
	if err == nil {
		t.Fatal("expected link-local to remain blocked even with allowLoopback")
	}
}

func TestValidateToolURL_AllowLoopbackStillBlocksMetadata(t *testing.T) {
	err := validateToolURL("http://metadata.google.internal/computeMetadata/v1/", nil, true)
	if err == nil {
		t.Fatal("expected metadata hostname to remain blocked even with allowLoopback")
	}
}

func TestRedactURL_MasksQueryParams(t *testing.T) {
	result := redactURL("https://api.example.com/mcp?token=secret123&key=abc")
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if contains(result, "secret123") || contains(result, "abc") {
		t.Fatalf("expected query values to be redacted, got: %s", result)
	}
}

func TestRedactURL_MasksUserinfo(t *testing.T) {
	result := redactURL("https://user:password@api.example.com/mcp")
	if contains(result, "password") {
		t.Fatalf("expected userinfo to be redacted, got: %s", result)
	}
}

func TestRedactURL_InvalidURL(t *testing.T) {
	result := redactURL("://not-a-url")
	if result != "<invalid-url>" {
		t.Fatalf("expected <invalid-url>, got: %s", result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
