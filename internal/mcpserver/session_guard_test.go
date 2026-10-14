package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const guardTestKey = "dk-test-session-guard-key"

func startGuardServer(t *testing.T, stateless bool) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := mcpserver.Deps{
		Dispatcher: agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger),
		TOMLKeys: []config.APIKeyConfig{
			{Name: "guard-key", Key: guardTestKey, Scopes: []string{"admin"}},
		},
		Logger: logger,
	}
	cfg := config.APIMCPServerConfig{Transport: "streamable", SessionTimeout: "30m", Stateless: stateless}
	ts := httptest.NewServer(mcpserver.New(cfg, deps).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// postRPC sends one raw JSON-RPC message, like a client that skips the SDK.
func postRPC(t *testing.T, ts *httptest.Server, token, sessionID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

const toolsListBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`

func TestSessionGuard_ToolsListWithoutSession_Returns400(t *testing.T) {
	ts := startGuardServer(t, false)

	resp := postRPC(t, ts, guardTestKey, "", toolsListBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSessionGuard_UnknownSessionPassesThrough(t *testing.T) {
	ts := startGuardServer(t, false)

	// The SDK owns session lookup and answers 404 for an expired session.
	resp := postRPC(t, ts, guardTestKey, "no-such-session", toolsListBody)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSessionGuard_AuthCheckedFirst(t *testing.T) {
	ts := startGuardServer(t, false)

	resp := postRPC(t, ts, "", "", toolsListBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestSessionGuard_HandshakeThenToolsList(t *testing.T) {
	ts := startGuardServer(t, false)

	init := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26",` +
		`"capabilities":{},"clientInfo":{"name":"raw-client","version":"0.0.1"}}}`
	resp := postRPC(t, ts, guardTestKey, "", init)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", resp.StatusCode)
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize returned no Mcp-Session-Id")
	}

	postRPC(t, ts, guardTestKey, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	resp = postRPC(t, ts, guardTestKey, sessionID, toolsListBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/list status = %d, want 200", resp.StatusCode)
	}
	assertToolsInBody(t, resp)
}

func TestSessionGuard_SDKClientConnects(t *testing.T) {
	ts := startGuardServer(t, false)

	transport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+guardTestKey)
			return http.DefaultTransport.RoundTrip(r)
		})},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "guard-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}
}

func TestSessionGuard_StatelessAllowsToolsListWithoutSession(t *testing.T) {
	ts := startGuardServer(t, true)

	resp := postRPC(t, ts, guardTestKey, "", toolsListBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	assertToolsInBody(t, resp)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// assertToolsInBody accepts a plain JSON or a single-event SSE response.
func assertToolsInBody(t *testing.T, resp *http.Response) {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	payload := string(raw)
	if i := strings.Index(payload, "data: "); i >= 0 {
		payload = strings.TrimSpace(strings.SplitN(payload[i+len("data: "):], "\n", 2)[0])
	}
	var msg struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
		Error *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if msg.Error != nil {
		t.Fatalf("JSON-RPC error: %s", *msg.Error)
	}
	if len(msg.Result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}
}
