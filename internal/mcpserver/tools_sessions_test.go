package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const schedConvID = "sched:daily:1700000000000000000"

// sessionsServer runs an interactive channel turn and an isolated scheduled
// turn against the same chat, mirroring a schedule that posts to a channel
// the user also talks in.
func sessionsServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mem, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating memory: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	perms, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	router := llm.NewRouter("stub", "test-model", llm.NewCostTracker(llm.SessionLimits{}, nil))
	router.RegisterProvider(stubLLM{})
	e := agent.NewEngine("test-agent", router, mem, nil, perms, nil,
		"test agent", nil, nil, nil, logger)

	ctx := context.Background()
	if _, err := e.Chat(ctx, adapter.IncomingMessage{
		Adapter: "telegram", ExternalID: "123", ConversationID: "chan:main", Text: "hi",
	}); err != nil {
		t.Fatalf("interactive turn: %v", err)
	}
	if _, err := e.Chat(ctx, adapter.IncomingMessage{
		Adapter: "telegram", ExternalID: "123", ConversationID: schedConvID,
		UserName: "scheduler", Text: "run daily", IsScheduled: true, ScheduleName: "daily",
	}); err != nil {
		t.Fatalf("scheduled turn: %v", err)
	}

	return &Server{deps: Deps{Memory: mem, Logger: logger}}
}

func listSessionIDs(t *testing.T, s *Server, input sessionListInput) []string {
	t.Helper()
	ctx := withScopes(context.Background(), []string{"sessions:read"})
	result, _, err := s.handleSessionList(ctx, nil, input)
	if err != nil {
		t.Fatalf("session_list: %v", err)
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if result.IsError {
		t.Fatalf("session_list error: %s", text.Text)
	}
	var out struct {
		Conversations []agent.ConversationInfo `json:"conversations"`
	}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("decoding session_list: %v", err)
	}
	ids := make([]string, len(out.Conversations))
	for i, c := range out.Conversations {
		ids[i] = c.ID
	}
	return ids
}

func containsID(ids []string, id string) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

func TestSessionList_ScheduledHiddenByDefault(t *testing.T) {
	s := sessionsServer(t)
	ids := listSessionIDs(t, s, sessionListInput{})
	if containsID(ids, schedConvID) {
		t.Errorf("scheduled conversation listed without include_scheduled: %v", ids)
	}
	if !containsID(ids, "chan:main") {
		t.Errorf("interactive conversation missing: %v", ids)
	}
}

func TestSessionList_IncludeScheduled(t *testing.T) {
	s := sessionsServer(t)
	ids := listSessionIDs(t, s, sessionListInput{IncludeScheduled: true})
	if !containsID(ids, schedConvID) {
		t.Errorf("scheduled conversation not listed with include_scheduled: %v", ids)
	}
	if !containsID(ids, "chan:main") {
		t.Errorf("interactive conversation missing: %v", ids)
	}
}

func TestSessionList_IncludeScheduled_AgentFilter(t *testing.T) {
	s := sessionsServer(t)
	ids := listSessionIDs(t, s, sessionListInput{Agent: "test-agent", IncludeScheduled: true})
	if !containsID(ids, schedConvID) {
		t.Errorf("agent-filtered listing missing the agent's scheduled run: %v", ids)
	}
	if other := listSessionIDs(t, s, sessionListInput{Agent: "other", IncludeScheduled: true}); len(other) != 0 {
		t.Errorf("other agent's listing = %v, want empty", other)
	}
}

func TestSessionMessages_ScheduledConversation(t *testing.T) {
	s := sessionsServer(t)
	ctx := withScopes(context.Background(), []string{"sessions:read"})
	result, _, err := s.handleSessionMessages(ctx, nil, sessionMessagesInput{ConversationID: schedConvID})
	if err != nil {
		t.Fatalf("session_messages: %v", err)
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if result.IsError {
		t.Fatalf("session_messages error: %s", text.Text)
	}
	var msgs []agent.StoredMessage
	if err := json.Unmarshal([]byte(text.Text), &msgs); err != nil {
		t.Fatalf("decoding session_messages: %v", err)
	}
	if len(msgs) != 2 || msgs[1].Role != "assistant" || msgs[1].Content != "stub" {
		t.Errorf("messages = %+v, want user trigger + assistant reply", msgs)
	}
}
