//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
	"github.com/Temikus/denkeeper/internal/tool"
)

// startExposureMCPServer serves two tools, so a skill declaring one of them
// leaves something observable to hide.
func startExposureMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-exposure", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Returns the input text"}, echoHandler)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "Returns the input text"}, echoHandler)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func exposureHarness(t *testing.T, skills []skill.Skill) *Harness {
	t.Helper()

	ts := startExposureMCPServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolMgr := tool.NewManager(logger)
	if err := toolMgr.RegisterServer(context.Background(), "exposure-tools", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}); err != nil {
		t.Fatalf("registering test MCP server: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	return NewHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{{
			Content:      "Done.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
		}},
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"api"}, Skills: skills},
		},
		ToolManager: toolMgr,
	})
}

func advertisedToolNames(req llm.ChatRequest) []string {
	names := make([]string, 0, len(req.Tools))
	for _, td := range req.Tools {
		names = append(names, td.Function.Name)
	}
	slices.Sort(names)
	return names
}

// End to end: a command-invoked skill declaring requires.tools narrows the tool
// definitions on the wire, and an ordinary turn on the same agent still sees
// everything.
func TestChat_SkillToolExposure_NarrowsAdvertisedTools(t *testing.T) {
	h := exposureHarness(t, []skill.Skill{
		skilltest.NewWithRequiresTools("focus", "Focused skill", []string{"command:focus"},
			"Do the focused work.", []string{"echo"}),
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message":    "/focus go",
		"session_id": "skill-tool-exposure",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := advertisedToolNames(h.MockLLM.LastRequest()); !slices.Equal(got, []string{"echo"}) {
		t.Errorf("advertised tools = %v, want only the skill's declared echo", got)
	}

	// No command, so the declaring skill never matches: nothing has stated a
	// requirement and the turn advertises the full surface.
	rec = h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message":    "just chatting",
		"session_id": "skill-tool-exposure-open",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := advertisedToolNames(h.MockLLM.LastRequest()); !slices.Equal(got, []string{"echo", "ping"}) {
		t.Errorf("advertised tools = %v, want every registered tool on an undeclared turn", got)
	}
}
