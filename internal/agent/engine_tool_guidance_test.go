package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/tool"
)

type guidanceToolArgs struct{}

// startGuidanceMCPServer serves an MCP server over HTTP so the tool manager
// registers it with a real config.ToolConfig — in-process RegisterSession
// servers carry the zero config and can never hold guidance.
func startGuidanceMCPServer(t *testing.T, label string, toolNames ...string) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: label, Version: "v1"}, nil)
	for _, name := range toolNames {
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: name},
			func(_ context.Context, _ *mcp.CallToolRequest, _ guidanceToolArgs) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
			})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func registerGuidanceServer(t *testing.T, mgr *tool.Manager, name, url, guidance string, disabled ...string) {
	t.Helper()
	if err := mgr.RegisterServer(context.Background(), name, config.ToolConfig{
		Transport:     "sse",
		URL:           url,
		AllowLoopback: true,
		Guidance:      guidance,
		DisabledTools: disabled,
	}); err != nil {
		t.Fatalf("RegisterServer(%s): %v", name, err)
	}
}

func newGuidanceToolManager(t *testing.T) *tool.Manager {
	t.Helper()
	mgr := tool.NewManager(testLogger())
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

func TestBuildSystemPrompt_ServerGuidanceInjected(t *testing.T) {
	ts := startGuidanceMCPServer(t, "todoist", "find-tasks", "add-tasks")
	mgr := newGuidanceToolManager(t)
	registerGuidanceServer(t, mgr, "todoist", ts.URL, "Never send workspaceId — this API has no workspace concept.")

	e := newSatisfactionEngine(t, mgr, nil, nil)
	prompt := e.buildSystemPrompt(nil, satisfactionMessage("guidance-1", "hi"), nil).prompt

	for _, want := range []string{
		"## Tool Server Notes",
		"### todoist",
		"Tools: add-tasks, find-tasks",
		"Never send workspaceId — this API has no workspace concept.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt is missing %q:\n%s", want, prompt)
		}
	}
	// Guidance is the most stable appended block, so it sits ahead of the
	// sections below it in the prompt-cache prefix.
	if strings.Index(prompt, "## Tool Server Notes") > strings.Index(prompt, "## Session Context") {
		t.Error("guidance section must precede ## Session Context")
	}
}

func TestBuildSystemPrompt_GuidanceOmittedForDisabledServer(t *testing.T) {
	live := startGuidanceMCPServer(t, "todoist", "find-tasks")
	muted := startGuidanceMCPServer(t, "muted", "muted-tool")
	mgr := newGuidanceToolManager(t)
	registerGuidanceServer(t, mgr, "todoist", live.URL, "LIVE-GUIDANCE-MARKER")
	// Every tool disabled: the model can call nothing here, so its guidance is
	// noise in the prompt.
	registerGuidanceServer(t, mgr, "muted", muted.URL, "MUTED-GUIDANCE-MARKER", "muted-tool")

	e := newSatisfactionEngine(t, mgr, nil, nil)
	prompt := e.buildSystemPrompt(nil, satisfactionMessage("guidance-2", "hi"), nil).prompt

	if !strings.Contains(prompt, "LIVE-GUIDANCE-MARKER") {
		t.Error("guidance for the reachable server is missing from the prompt")
	}
	if strings.Contains(prompt, "MUTED-GUIDANCE-MARKER") {
		t.Error("guidance for a server with no callable tools leaked into the prompt")
	}
	if strings.Contains(prompt, "### muted") {
		t.Error("disabled server rendered a guidance subsection")
	}
}

// TestBuildSystemPrompt_NoGuidanceIsByteIdentical pins the prompt-cache
// contract: with no server declaring guidance, the prompt must match one built
// by an agent with no tool manager at all.
func TestBuildSystemPrompt_NoGuidanceIsByteIdentical(t *testing.T) {
	ts := startGuidanceMCPServer(t, "plain", "search")
	mgr := newGuidanceToolManager(t)
	registerGuidanceServer(t, mgr, "plain", ts.URL, "")

	msg := satisfactionMessage("guidance-3", "hi")
	withTools := newSatisfactionEngine(t, mgr, nil, nil).buildSystemPrompt(nil, msg, nil).prompt
	withoutTools := newSatisfactionEngine(t, nil, nil, nil).buildSystemPrompt(nil, msg, nil).prompt

	if withTools != withoutTools {
		t.Errorf("prompt changed with no guidance declared:\n--- with tools ---\n%s\n--- without ---\n%s", withTools, withoutTools)
	}
	if strings.Contains(withTools, "Tool Server Notes") {
		t.Error("empty guidance rendered a section header")
	}
}

func TestFormatGuidanceTools_ElidesPastCap(t *testing.T) {
	names := make([]string, maxGuidanceToolNames+5)
	for i := range names {
		names[i] = "tool"
	}
	got := formatGuidanceTools(names)
	if !strings.HasSuffix(got, "(+5 more)") {
		t.Errorf("formatGuidanceTools(%d names) = %q, want a (+5 more) suffix", len(names), got)
	}
}
