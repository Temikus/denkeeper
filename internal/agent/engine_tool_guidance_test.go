package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
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

// newSupervisedGuidanceEngine wires a supervised engine whose primary model
// calls toolName once, with a capturing supervisor that approves it.
func newSupervisedGuidanceEngine(t *testing.T, mgr *tool.Manager, toolName string) (*Engine, *capturingProvider) {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: toolName, Arguments: `{}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "Done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	supervisorProv := &capturingProvider{
		responses: []*llm.ChatResponse{
			{Content: "APPROVE: conforms", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)
	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = approvalStore.Close() })
	apprMgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "You are a test assistant.", nil, mgr, apprMgr, testLogger())

	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)
	return engine, supervisorProv
}

func supervisorReviewPrompt(t *testing.T, prov *capturingProvider) string {
	t.Helper()
	if len(prov.requests) != 1 {
		t.Fatalf("supervisor received %d requests, want 1", len(prov.requests))
	}
	for _, m := range prov.requests[0].Messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	t.Fatal("no user message found in supervisor request")
	return ""
}

func TestSupervisorReview_IncludesServerGuidance(t *testing.T) {
	ts := startGuidanceMCPServer(t, "todoist", "find-tasks")
	mgr := newGuidanceToolManager(t)
	registerGuidanceServer(t, mgr, "todoist", ts.URL, "Never send workspaceId — this API has no workspace concept.")

	engine, supervisorProv := newSupervisedGuidanceEngine(t, mgr, "find-tasks")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-guidance",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "find my tasks",
		Timestamp:  time.Now(),
	}, nil); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}

	review := supervisorReviewPrompt(t, supervisorProv)
	if !strings.Contains(review, "**Operator guidance for this tool's server**") {
		t.Errorf("review prompt missing guidance header:\n%s", review)
	}
	if !strings.Contains(review, "Never send workspaceId — this API has no workspace concept.") {
		t.Errorf("review prompt missing guidance text:\n%s", review)
	}
}

func TestSupervisorReview_NoGuidanceOmitsSection(t *testing.T) {
	ts := startGuidanceMCPServer(t, "plain", "search")
	mgr := newGuidanceToolManager(t)
	registerGuidanceServer(t, mgr, "plain", ts.URL, "")

	engine, supervisorProv := newSupervisedGuidanceEngine(t, mgr, "search")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-noguidance",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "search",
		Timestamp:  time.Now(),
	}, nil); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}

	review := supervisorReviewPrompt(t, supervisorProv)
	if strings.Contains(review, "Operator guidance for this tool's server") {
		t.Errorf("review prompt rendered a guidance section for an unguided server:\n%s", review)
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
