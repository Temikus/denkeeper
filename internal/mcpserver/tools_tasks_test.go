package mcpserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/mcpserver"
	"github.com/Temikus/denkeeper/internal/task"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	taskWriterKey = "dk-test-task-writer-key"
	taskReaderKey = "dk-test-task-reader-key"
)

// startTaskServer serves the real MCP handler, auth middleware included, with
// a claude-code key that may write tasks and a reader key that may only read.
func startTaskServer(t *testing.T) (*httptest.Server, *task.Service) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := task.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := task.NewService(store, task.DirectoryFunc(func(context.Context) ([]task.Member, error) {
		return []task.Member{{ID: "ext:claude-code", Name: "claude-code", Kind: "external"}}, nil
	}), nil)
	srv := mcpserver.New(config.APIMCPServerConfig{Transport: "streamable", SessionTimeout: "30m"}, mcpserver.Deps{
		Dispatcher: agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger),
		TOMLKeys: []config.APIKeyConfig{
			{Name: "claude-code", Key: taskWriterKey, Scopes: []string{"tasks:read", "tasks:write"}},
			{Name: "watcher", Key: taskReaderKey, Scopes: []string{"tasks:read"}},
		},
		Tasks:  svc,
		Logger: logger,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, svc
}

type taskBearer struct{ token string }

func (b taskBearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

func taskClient(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint: url, HTTPClient: &http.Client{Transport: taskBearer{token: token}},
		MaxRetries: -1, DisableStandaloneSSE: true,
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "task-test", Version: "test"}, nil).Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func taskCall(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: content is %T, want text", name, res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestTaskTools_KeyNameIsTheMember(t *testing.T) {
	ts, svc := startTaskServer(t)
	tk, err := svc.Create(context.Background(), task.Human, task.CreateRequest{Title: "Check rate limits", Assignee: "ext:claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	cc := taskClient(t, ts.URL, taskWriterKey)

	text, isErr := taskCall(t, cc, "task_list", nil)
	if isErr || !strings.Contains(text, tk.Key()) {
		t.Fatalf("claude's list = %s", text)
	}
	if text, isErr := taskCall(t, cc, "task_checkout", map[string]any{"id": tk.Key()}); isErr {
		t.Fatalf("checkout: %s", text)
	}
	got, _ := svc.Get(context.Background(), tk.ID)
	if got.Holder != "ext:claude-code" {
		t.Errorf("holder = %q, want ext:claude-code", got.Holder)
	}
}

func TestTaskTools_ScopesGateWrites(t *testing.T) {
	ts, svc := startTaskServer(t)
	if _, err := svc.Create(context.Background(), task.Human, task.CreateRequest{Title: "Seeded"}); err != nil {
		t.Fatal(err)
	}
	reader := taskClient(t, ts.URL, taskReaderKey)

	if text, isErr := taskCall(t, reader, "task_list", map[string]any{"assignee": "any"}); isErr || !strings.Contains(text, "DK-1") {
		t.Errorf("reader list = %s", text)
	}
	text, isErr := taskCall(t, reader, "task_checkout", map[string]any{"id": "DK-1"})
	if !isErr || !strings.Contains(text, `requires "tasks:write"`) {
		t.Errorf("reader checkout = %q, %v", text, isErr)
	}
}

func TestTaskTools_OffMeansNoToolsAndNoInstructions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := mcpserver.New(config.APIMCPServerConfig{Transport: "streamable", SessionTimeout: "30m"}, mcpserver.Deps{
		Dispatcher: agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger),
		TOMLKeys:   []config.APIKeyConfig{{Name: "claude-code", Key: taskWriterKey, Scopes: []string{"tasks:read", "tasks:write"}}},
		Logger:     logger,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	cc := taskClient(t, ts.URL, taskWriterKey)

	if got := cc.InitializeResult().Instructions; strings.Contains(got, "task_") {
		t.Errorf("instructions mention task tools with tasks off: %q", got)
	}
	tools, err := cc.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "task_") {
			t.Errorf("%s offered with tasks off", tool.Name)
		}
	}

	on, _ := startTaskServer(t)
	if got := taskClient(t, on.URL, taskWriterKey).InitializeResult().Instructions; !strings.Contains(got, "task_checkout") {
		t.Errorf("instructions with tasks on = %q, want the claim rule", got)
	}
}
