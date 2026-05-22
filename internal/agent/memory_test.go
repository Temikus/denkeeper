package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_GetOrCreateConversation(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	id1, err := store.GetOrCreateConversation(ctx, "telegram", "12345")
	if err != nil {
		t.Fatalf("GetOrCreateConversation: %v", err)
	}
	if id1 != "telegram:12345" {
		t.Errorf("id = %q, want telegram:12345", id1)
	}

	// Calling again returns same ID (idempotent)
	id2, err := store.GetOrCreateConversation(ctx, "telegram", "12345")
	if err != nil {
		t.Fatalf("second GetOrCreateConversation: %v", err)
	}
	if id1 != id2 {
		t.Errorf("id mismatch: %q != %q", id1, id2)
	}
}

func TestMemoryStore_AddAndGetMessages(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "123")

	messages := []StoredMessage{
		{Role: "user", Content: "Hello", TokensUsed: 5},
		{Role: "assistant", Content: "Hi there!", TokensUsed: 10, Cost: 0.001},
		{Role: "user", Content: "How are you?", TokensUsed: 8},
	}

	for _, msg := range messages {
		if _, err := store.AddMessage(ctx, convID, msg); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	// Get all messages
	got, err := store.GetMessages(ctx, convID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}

	if got[0].Role != "user" || got[0].Content != "Hello" {
		t.Errorf("message[0] = %+v, want user/Hello", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "Hi there!" {
		t.Errorf("message[1] = %+v, want assistant/Hi there!", got[1])
	}

	// Test limit — must return the NEWEST messages, not oldest.
	limited, err := store.GetMessages(ctx, convID, 2)
	if err != nil {
		t.Fatalf("GetMessages with limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("got %d messages with limit 2, want 2", len(limited))
	}
	if limited[0].Content != "Hi there!" {
		t.Errorf("limited[0].Content = %q, want %q", limited[0].Content, "Hi there!")
	}
	if limited[1].Content != "How are you?" {
		t.Errorf("limited[1].Content = %q, want %q", limited[1].Content, "How are you?")
	}
}

func TestMemoryStore_GetMessages_LimitReturnsNewest(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "test", "limit-newest")

	// Insert 60 messages to exceed the typical context limit of 50.
	for i := 1; i <= 60; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		if _, err := store.AddMessage(ctx, convID, StoredMessage{
			Role:    role,
			Content: fmt.Sprintf("message-%d", i),
		}); err != nil {
			t.Fatalf("AddMessage(%d): %v", i, err)
		}
	}

	// Fetch with limit=50 — must return messages 11-60 (newest), not 1-50.
	got, err := store.GetMessages(ctx, convID, 50)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("got %d messages, want 50", len(got))
	}

	// First returned message should be the 11th overall.
	if got[0].Content != "message-11" {
		t.Errorf("first message = %q, want %q (expected newest messages)", got[0].Content, "message-11")
	}
	// Last returned message should be the most recent (60th).
	if got[49].Content != "message-60" {
		t.Errorf("last message = %q, want %q", got[49].Content, "message-60")
	}

	// Verify chronological order is preserved.
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.Before(got[i-1].CreatedAt) {
			t.Errorf("messages not in chronological order: [%d]=%v > [%d]=%v",
				i-1, got[i-1].CreatedAt, i, got[i].CreatedAt)
			break
		}
	}
}

func TestMemoryStore_EmptyConversation(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "empty")

	got, err := store.GetMessages(ctx, convID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d messages, want 0", len(got))
	}
}

func TestMemoryStore_MessageOrdering(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "test", "order")

	// Insert messages — SQLite AUTOINCREMENT ensures ordering by insertion
	contents := []string{"first", "second", "third", "fourth"}
	for _, c := range contents {
		if _, err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: c}); err != nil {
			t.Fatalf("AddMessage(%s): %v", c, err)
		}
	}

	got, err := store.GetMessages(ctx, convID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4", len(got))
	}
	for i, want := range contents {
		if got[i].Content != want {
			t.Errorf("message[%d].Content = %q, want %q", i, got[i].Content, want)
		}
	}
}

func TestMemoryStore_LargeContent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "test", "large")

	largeContent := strings.Repeat("A", 10000)
	if _, err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: largeContent}); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	got, err := store.GetMessages(ctx, convID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if len(got[0].Content) != 10000 {
		t.Errorf("content length = %d, want 10000", len(got[0].Content))
	}
}

func TestMemoryStore_GetOrCreateConversationByID(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID := "sched:daily-briefing:1234567890"

	// First call creates the row.
	if err := store.GetOrCreateConversationByID(ctx, convID, "sched", convID); err != nil {
		t.Fatalf("GetOrCreateConversationByID: %v", err)
	}

	// Messages can be stored against the created conversation.
	if _, err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "trigger"}); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	got, err := store.GetMessages(ctx, convID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 1 || got[0].Content != "trigger" {
		t.Errorf("got %+v, want one message with content 'trigger'", got)
	}

	// Second call is idempotent — no error, row not duplicated.
	if err := store.GetOrCreateConversationByID(ctx, convID, "sched", convID); err != nil {
		t.Fatalf("second GetOrCreateConversationByID: %v", err)
	}
	got2, _ := store.GetMessages(ctx, convID, 100)
	if len(got2) != 1 {
		t.Errorf("message count changed after idempotent call: got %d, want 1", len(got2))
	}
}

func TestMemoryStore_DeleteConversation(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "del-user")
	_, _ = store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "hi"})
	_, _ = store.AddMessage(ctx, convID, StoredMessage{Role: "assistant", Content: "hello"})

	// Delete the conversation.
	if err := store.DeleteConversation(ctx, convID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	// Messages should be gone.
	got, err := store.GetMessages(ctx, convID, 100)
	if err != nil {
		t.Fatalf("GetMessages after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d messages after delete, want 0", len(got))
	}

	// Conversations list should not include it.
	convos, _, err := store.ListConversations(ctx, SessionListOpts{})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	for _, c := range convos {
		if c.ID == convID {
			t.Errorf("conversation %q still in list after delete", convID)
		}
	}
}

func TestMemoryStore_DeleteConversation_NonExistent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	// Should not error on non-existent ID.
	if err := store.DeleteConversation(ctx, "does-not-exist"); err != nil {
		t.Errorf("DeleteConversation on non-existent: %v", err)
	}
}

func TestMemoryStore_MultipleConversations(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	conv1, _ := store.GetOrCreateConversation(ctx, "telegram", "user1")
	conv2, _ := store.GetOrCreateConversation(ctx, "telegram", "user2")

	if _, err := store.AddMessage(ctx, conv1, StoredMessage{Role: "user", Content: "msg for conv1"}); err != nil {
		t.Fatalf("AddMessage conv1: %v", err)
	}
	if _, err := store.AddMessage(ctx, conv2, StoredMessage{Role: "user", Content: "msg for conv2"}); err != nil {
		t.Fatalf("AddMessage conv2: %v", err)
	}

	got1, err := store.GetMessages(ctx, conv1, 100)
	if err != nil {
		t.Fatalf("GetMessages conv1: %v", err)
	}
	got2, err := store.GetMessages(ctx, conv2, 100)
	if err != nil {
		t.Fatalf("GetMessages conv2: %v", err)
	}

	if len(got1) != 1 || got1[0].Content != "msg for conv1" {
		t.Errorf("conv1 messages leaked or wrong: %+v", got1)
	}
	if len(got2) != 1 || got2[0].Content != "msg for conv2" {
		t.Errorf("conv2 messages leaked or wrong: %+v", got2)
	}
}

func TestConversationCost_Sum(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "cost-test")

	_, _ = store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "hi", Cost: 0.001})
	_, _ = store.AddMessage(ctx, convID, StoredMessage{Role: "assistant", Content: "hey", Cost: 0.002})
	_, _ = store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "bye", Cost: 0.003})

	cost, err := store.ConversationCost(ctx, convID)
	if err != nil {
		t.Fatalf("ConversationCost: %v", err)
	}
	if cost < 0.005 || cost > 0.007 {
		t.Errorf("cost = %f, want ~0.006", cost)
	}
}

func TestConversationCost_NoMessages(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "empty-cost")

	cost, err := store.ConversationCost(ctx, convID)
	if err != nil {
		t.Fatalf("ConversationCost: %v", err)
	}
	if cost != 0 {
		t.Errorf("cost = %f, want 0", cost)
	}
}

func TestPruneConversations_RemovesOld(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Create two conversations: one old, one new.
	oldID, _ := store.GetOrCreateConversation(ctx, "telegram", "old-conv")
	_, _ = store.AddMessage(ctx, oldID, StoredMessage{Role: "user", Content: "old msg"})

	// Backdate the old conversation.
	_, _ = store.db.ExecContext(ctx, `UPDATE conversations SET created_at = datetime('now', '-60 days') WHERE id = ?`, oldID)

	newID, _ := store.GetOrCreateConversation(ctx, "telegram", "new-conv")
	_, _ = store.AddMessage(ctx, newID, StoredMessage{Role: "user", Content: "new msg"})

	// Prune conversations older than 30 days.
	cutoff := time.Now().Add(-30 * 24 * time.Hour) // 30 days
	pruned, err := store.PruneConversations(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneConversations: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}

	// Verify old is gone, new remains.
	convos, _, _ := store.ListConversations(ctx, SessionListOpts{})
	if len(convos) != 1 {
		t.Fatalf("remaining conversations = %d, want 1", len(convos))
	}
	if convos[0].ID != newID {
		t.Errorf("remaining conversation = %q, want %q", convos[0].ID, newID)
	}

	// Verify old messages are gone too.
	msgs, _ := store.GetMessages(ctx, oldID, 100)
	if len(msgs) != 0 {
		t.Errorf("old messages remain: %d", len(msgs))
	}
}

func TestCountConversationsBefore(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	_, _ = store.GetOrCreateConversation(ctx, "telegram", "a")
	_, _ = store.db.ExecContext(ctx, `UPDATE conversations SET created_at = datetime('now', '-60 days') WHERE id = 'telegram:a'`)

	_, _ = store.GetOrCreateConversation(ctx, "telegram", "b")
	_, _ = store.db.ExecContext(ctx, `UPDATE conversations SET created_at = datetime('now', '-60 days') WHERE id = 'telegram:b'`)

	_, _ = store.GetOrCreateConversation(ctx, "telegram", "c") // recent

	cutoff := time.Now().Add(-30 * 24 * time.Hour) // 30 days
	count, err := store.CountConversationsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("CountConversationsBefore: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

// ---------------------------------------------------------------------------
// Telemetry persistence tests
// ---------------------------------------------------------------------------

func TestAddMessage_ReturnsID(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")

	id1, err := store.AddMessage(ctx, "test:1", StoredMessage{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if id1 <= 0 {
		t.Errorf("expected positive ID, got %d", id1)
	}

	id2, err := store.AddMessage(ctx, "test:1", StoredMessage{Role: "assistant", Content: "hello"})
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if id2 <= id1 {
		t.Errorf("second ID (%d) should be > first (%d)", id2, id1)
	}
}

func TestAddMessage_TelemetryFields(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")

	_, err = store.AddMessage(ctx, "test:1", StoredMessage{
		Role:             "assistant",
		Content:          "hello",
		TokensUsed:       150,
		Cost:             0.005,
		Model:            "claude-3-opus",
		Provider:         "anthropic",
		TokensPrompt:     100,
		TokensCompletion: 50,
		TokensCached:     20,
	})
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	msgs, err := store.GetMessages(ctx, "test:1", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Model != "claude-3-opus" {
		t.Errorf("model = %q, want claude-3-opus", m.Model)
	}
	if m.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", m.Provider)
	}
	if m.TokensPrompt != 100 {
		t.Errorf("tokens_prompt = %d, want 100", m.TokensPrompt)
	}
	if m.TokensCompletion != 50 {
		t.Errorf("tokens_completion = %d, want 50", m.TokensCompletion)
	}
	if m.TokensCached != 20 {
		t.Errorf("tokens_cached = %d, want 20", m.TokensCached)
	}
	if m.Cost != 0.005 {
		t.Errorf("cost = %f, want 0.005", m.Cost)
	}
}

func TestAddToolCalls_RoundTrip(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")

	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "assistant", Content: "used tools"})

	calls := []ToolCallRecord{
		{ToolName: "web_search", ServerName: "web-tools", Round: 1, DurationMs: 200, Success: true},
		{ToolName: "read_file", ServerName: "filesystem", Round: 1, DurationMs: 50, Success: false, ErrorMsg: "not found"},
	}
	if err := store.AddToolCalls(ctx, "test:1", msgID, calls); err != nil {
		t.Fatalf("AddToolCalls: %v", err)
	}

	got, err := store.GetToolCalls(ctx, "test:1")
	if err != nil {
		t.Fatalf("GetToolCalls: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(got))
	}
	if got[0].ToolName != "web_search" || got[0].ServerName != "web-tools" || !got[0].Success {
		t.Errorf("first tool call mismatch: %+v", got[0])
	}
	if got[1].ToolName != "read_file" || got[1].Success || got[1].ErrorMsg != "not found" {
		t.Errorf("second tool call mismatch: %+v", got[1])
	}
}

func TestAddToolCalls_Empty(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Should not error on empty slice.
	if err := store.AddToolCalls(context.Background(), "x", 1, nil); err != nil {
		t.Fatalf("AddToolCalls(nil): %v", err)
	}
}

func TestAddSkillUsages_RoundTrip(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")

	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "user", Content: "hello"})

	skills := []SkillUsageRecord{
		{SkillName: "greeting", MatchType: "always"},
		{SkillName: "search", MatchType: "command"},
	}
	if err := store.AddSkillUsages(ctx, "test:1", msgID, skills); err != nil {
		t.Fatalf("AddSkillUsages: %v", err)
	}

	got, err := store.GetSkillUsages(ctx, "test:1")
	if err != nil {
		t.Fatalf("GetSkillUsages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 skill usages, got %d", len(got))
	}
	if got[0].SkillName != "greeting" || got[0].MatchType != "always" {
		t.Errorf("first skill mismatch: %+v", got[0])
	}
}

func TestUpdateConversationStats_Incremental(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")

	// First message.
	msg1 := StoredMessage{
		Cost: 0.01, Model: "gpt-4", Provider: "openai",
		TokensPrompt: 100, TokensCompletion: 50, TokensCached: 10,
	}
	if err := store.UpdateConversationStats(ctx, "test:1", "test", msg1, 2, 1); err != nil {
		t.Fatalf("UpdateConversationStats: %v", err)
	}

	stats, err := store.GetConversationStats(ctx, "test:1")
	if err != nil {
		t.Fatalf("GetConversationStats: %v", err)
	}
	if stats.TotalMessages != 1 || stats.TotalCost != 0.01 || stats.LastModel != "gpt-4" {
		t.Errorf("after first: messages=%d cost=%f model=%s", stats.TotalMessages, stats.TotalCost, stats.LastModel)
	}
	if stats.TotalToolCalls != 2 || stats.TotalToolErrors != 1 {
		t.Errorf("after first: tool_calls=%d errors=%d", stats.TotalToolCalls, stats.TotalToolErrors)
	}

	// Second message with different model.
	msg2 := StoredMessage{
		Cost: 0.02, Model: "claude-3-opus", Provider: "anthropic",
		TokensPrompt: 200, TokensCompletion: 100, TokensCached: 0,
	}
	if err := store.UpdateConversationStats(ctx, "test:1", "test", msg2, 0, 0); err != nil {
		t.Fatalf("UpdateConversationStats: %v", err)
	}

	stats, err = store.GetConversationStats(ctx, "test:1")
	if err != nil {
		t.Fatalf("GetConversationStats: %v", err)
	}
	if stats.TotalMessages != 2 {
		t.Errorf("total_messages = %d, want 2", stats.TotalMessages)
	}
	if stats.TotalCost < 0.029 || stats.TotalCost > 0.031 {
		t.Errorf("total_cost = %f, want ~0.03", stats.TotalCost)
	}
	if stats.LastModel != "claude-3-opus" || stats.LastProvider != "anthropic" {
		t.Errorf("last_model=%s last_provider=%s", stats.LastModel, stats.LastProvider)
	}
	if stats.TotalPrompt != 300 || stats.TotalCompletion != 150 {
		t.Errorf("prompt=%d completion=%d", stats.TotalPrompt, stats.TotalCompletion)
	}
}

func TestUpdateConversationStats_PersistsAgent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")

	if err := store.UpdateConversationStats(ctx, "test:1", "myagent", StoredMessage{
		Cost: 0.05, Model: "gpt-4", Provider: "openai",
	}, 0, 0); err != nil {
		t.Fatalf("UpdateConversationStats: %v", err)
	}

	stats, err := store.GetConversationStats(ctx, "test:1")
	if err != nil {
		t.Fatalf("GetConversationStats: %v", err)
	}
	if stats.Agent != "myagent" {
		t.Errorf("agent = %q, want myagent", stats.Agent)
	}
}

func TestGetCostsByAgent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	// Create conversations for two agents.
	_, _ = store.GetOrCreateConversation(ctx, "alice", "1")
	_, _ = store.GetOrCreateConversation(ctx, "alice", "2")
	_, _ = store.GetOrCreateConversation(ctx, "bob", "1")

	_ = store.UpdateConversationStats(ctx, "alice:tg:1", "alice", StoredMessage{
		Cost: 0.10, TokensPrompt: 100, TokensCompletion: 50,
	}, 0, 0)
	_ = store.UpdateConversationStats(ctx, "alice:tg:2", "alice", StoredMessage{
		Cost: 0.05, TokensPrompt: 50, TokensCompletion: 25,
	}, 0, 0)
	_ = store.UpdateConversationStats(ctx, "bob:tg:1", "bob", StoredMessage{
		Cost: 0.20, TokensPrompt: 200, TokensCompletion: 100,
	}, 0, 0)

	results, err := store.GetCostsByAgent(ctx)
	if err != nil {
		t.Fatalf("GetCostsByAgent: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(results))
	}

	// Results are ordered by cost DESC, so bob first.
	if results[0].Agent != "bob" {
		t.Errorf("results[0].Agent = %q, want bob", results[0].Agent)
	}
	if results[0].Cost != 0.20 {
		t.Errorf("bob cost = %f, want 0.20", results[0].Cost)
	}
	if results[0].Sessions != 1 {
		t.Errorf("bob sessions = %d, want 1", results[0].Sessions)
	}

	if results[1].Agent != "alice" {
		t.Errorf("results[1].Agent = %q, want alice", results[1].Agent)
	}
	if results[1].Cost < 0.14 || results[1].Cost > 0.16 {
		t.Errorf("alice cost = %f, want ~0.15", results[1].Cost)
	}
	if results[1].Sessions != 2 {
		t.Errorf("alice sessions = %d, want 2", results[1].Sessions)
	}
	if results[1].InputTokens != 150 {
		t.Errorf("alice input_tokens = %d, want 150", results[1].InputTokens)
	}
}

func TestGetCostsByAgent_ChannelConversations(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	// Channel-based conversation IDs: agent name comes from the agent parameter.
	_, _ = store.GetOrCreateConversation(ctx, "ws", "chan:work")
	_ = store.UpdateConversationStats(ctx, "chan:work", "assistant", StoredMessage{
		Cost: 0.30, TokensPrompt: 300, TokensCompletion: 150,
	}, 0, 0)

	results, err := store.GetCostsByAgent(ctx)
	if err != nil {
		t.Fatalf("GetCostsByAgent: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(results))
	}
	if results[0].Agent != "assistant" {
		t.Errorf("agent = %q, want assistant", results[0].Agent)
	}
}

func TestGetCostsByProvider(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_, _ = store.GetOrCreateConversation(ctx, "tg", "1")

	// Seed assistant messages with different providers.
	_, _ = store.AddMessage(ctx, "tg:1", StoredMessage{
		Role: "assistant", Content: "a1", Model: "claude-3", Provider: "anthropic",
		Cost: 0.10, TokensPrompt: 100, TokensCompletion: 50, TokensCached: 10,
	})
	_, _ = store.AddMessage(ctx, "tg:1", StoredMessage{
		Role: "assistant", Content: "a2", Model: "claude-3", Provider: "anthropic",
		Cost: 0.05, TokensPrompt: 80, TokensCompletion: 30, TokensCached: 5,
	})
	_, _ = store.AddMessage(ctx, "tg:1", StoredMessage{
		Role: "assistant", Content: "o1", Model: "gpt-4", Provider: "openai",
		Cost: 0.20, TokensPrompt: 200, TokensCompletion: 100, TokensCached: 0,
	})
	// User messages should be excluded.
	_, _ = store.AddMessage(ctx, "tg:1", StoredMessage{
		Role: "user", Content: "u1", Provider: "anthropic", Cost: 0.01,
	})
	// Empty provider should be excluded.
	_, _ = store.AddMessage(ctx, "tg:1", StoredMessage{
		Role: "assistant", Content: "e1", Model: "unknown", Provider: "",
		Cost: 0.01,
	})

	results, err := store.GetCostsByProvider(ctx)
	if err != nil {
		t.Fatalf("GetCostsByProvider: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(results))
	}

	// Ordered by cost DESC: openai (0.20) first, then anthropic (0.15).
	if results[0].Provider != "openai" {
		t.Errorf("results[0].Provider = %q, want openai", results[0].Provider)
	}
	if results[0].Cost != 0.20 {
		t.Errorf("openai cost = %f, want 0.20", results[0].Cost)
	}
	if results[0].Messages != 1 {
		t.Errorf("openai messages = %d, want 1", results[0].Messages)
	}
	if results[0].InputTokens != 200 {
		t.Errorf("openai input_tokens = %d, want 200", results[0].InputTokens)
	}
	if results[0].OutputTokens != 100 {
		t.Errorf("openai output_tokens = %d, want 100", results[0].OutputTokens)
	}

	if results[1].Provider != "anthropic" {
		t.Errorf("results[1].Provider = %q, want anthropic", results[1].Provider)
	}
	if results[1].Cost < 0.14 || results[1].Cost > 0.16 {
		t.Errorf("anthropic cost = %f, want ~0.15", results[1].Cost)
	}
	if results[1].Messages != 2 {
		t.Errorf("anthropic messages = %d, want 2", results[1].Messages)
	}
	if results[1].InputTokens != 180 {
		t.Errorf("anthropic input_tokens = %d, want 180", results[1].InputTokens)
	}
	if results[1].CachedTokens != 15 {
		t.Errorf("anthropic cached_tokens = %d, want 15", results[1].CachedTokens)
	}
}

func TestGetConversationStats_NotFound(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	stats, err := store.GetConversationStats(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("GetConversationStats: %v", err)
	}
	if stats != nil {
		t.Errorf("expected nil stats for nonexistent conversation, got %+v", stats)
	}
}

func TestPruneConversations_CascadesToTelemetry(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_, _ = store.GetOrCreateConversation(ctx, "tg", "old")
	_, _ = store.db.ExecContext(ctx, `UPDATE conversations SET created_at = datetime('now', '-90 days') WHERE id = 'tg:old'`)
	msgID, _ := store.AddMessage(ctx, "tg:old", StoredMessage{Role: "assistant", Content: "hi"})
	_ = store.AddToolCalls(ctx, "tg:old", msgID, []ToolCallRecord{{ToolName: "t1", Success: true}})
	_ = store.AddSkillUsages(ctx, "tg:old", msgID, []SkillUsageRecord{{SkillName: "s1", MatchType: "always"}})
	_ = store.UpdateConversationStats(ctx, "tg:old", "tg", StoredMessage{Cost: 0.01}, 1, 0)

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	n, err := store.PruneConversations(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneConversations: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}

	// All telemetry should be gone.
	tc, _ := store.GetToolCalls(ctx, "tg:old")
	if len(tc) != 0 {
		t.Errorf("tool calls remain: %d", len(tc))
	}
	su, _ := store.GetSkillUsages(ctx, "tg:old")
	if len(su) != 0 {
		t.Errorf("skill usages remain: %d", len(su))
	}
	stats, _ := store.GetConversationStats(ctx, "tg:old")
	if stats != nil {
		t.Errorf("conversation stats remain: %+v", stats)
	}
}

func TestPruneByCount_RemovesOldest(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = store.GetOrCreateConversation(ctx, "tg", fmt.Sprintf("%d", i))
		_, _ = store.AddMessage(ctx, fmt.Sprintf("tg:%d", i), StoredMessage{Role: "user", Content: "msg"})
	}

	n, err := store.PruneByCount(ctx, 3)
	if err != nil {
		t.Fatalf("PruneByCount: %v", err)
	}
	if n != 2 {
		t.Errorf("pruned %d, want 2", n)
	}

	convos, _, _ := store.ListConversations(ctx, SessionListOpts{})
	if len(convos) != 3 {
		t.Errorf("remaining conversations: %d, want 3", len(convos))
	}
}

func TestDeleteConversation_CascadesToTelemetry(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_, _ = store.GetOrCreateConversation(ctx, "test", "1")
	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "assistant", Content: "hi"})
	_ = store.AddToolCalls(ctx, "test:1", msgID, []ToolCallRecord{{ToolName: "t1", Success: true}})
	_ = store.AddSkillUsages(ctx, "test:1", msgID, []SkillUsageRecord{{SkillName: "s1", MatchType: "always"}})
	_ = store.UpdateConversationStats(ctx, "test:1", "test", StoredMessage{Cost: 0.01}, 1, 0)

	if err := store.DeleteConversation(ctx, "test:1"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	tc, _ := store.GetToolCalls(ctx, "test:1")
	if len(tc) != 0 {
		t.Errorf("tool calls remain: %d", len(tc))
	}
	su, _ := store.GetSkillUsages(ctx, "test:1")
	if len(su) != 0 {
		t.Errorf("skill usages remain: %d", len(su))
	}
	stats, _ := store.GetConversationStats(ctx, "test:1")
	if stats != nil {
		t.Errorf("conversation stats remain: %+v", stats)
	}
}

func TestListConversationsWithStats(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_, _ = store.GetOrCreateConversation(ctx, "test", "1")
	_, _ = store.AddMessage(ctx, "test:1", StoredMessage{Role: "user", Content: "hi"})
	_ = store.UpdateConversationStats(ctx, "test:1", "test", StoredMessage{
		Cost: 0.05, Model: "gpt-4", Provider: "openai",
		TokensPrompt: 100, TokensCompletion: 50,
	}, 0, 0)

	convos, _, err := store.ListConversationsWithStats(ctx, SessionListOpts{})
	if err != nil {
		t.Fatalf("ListConversationsWithStats: %v", err)
	}
	if len(convos) != 1 {
		t.Fatalf("expected 1, got %d", len(convos))
	}
	if convos[0].TotalCost != 0.05 || convos[0].LastModel != "gpt-4" {
		t.Errorf("stats mismatch: cost=%f model=%s", convos[0].TotalCost, convos[0].LastModel)
	}
	if convos[0].UpdatedAt == nil {
		t.Error("expected UpdatedAt to be populated after UpdateConversationStats")
	}
}

func TestListConversationsWithStats_SortsByLastActivity(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	// Create two conversations. "old" is created first but will have newer activity.
	_, _ = store.GetOrCreateConversation(ctx, "test", "old")
	_, _ = store.GetOrCreateConversation(ctx, "test", "new")

	// Add stats to "new" first, then "old" — so "old" has the more recent updated_at.
	_ = store.UpdateConversationStats(ctx, "test:new", "test", StoredMessage{
		Cost: 0.01, Model: "m1", Provider: "p1",
	}, 0, 0)
	_ = store.UpdateConversationStats(ctx, "test:old", "test", StoredMessage{
		Cost: 0.02, Model: "m2", Provider: "p2",
	}, 0, 0)

	convos, _, err := store.ListConversationsWithStats(ctx, SessionListOpts{})
	if err != nil {
		t.Fatalf("ListConversationsWithStats: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("expected 2, got %d", len(convos))
	}
	// "old" had the more recent UpdateConversationStats call, so it should sort first.
	if convos[0].ID != "test:old" {
		t.Errorf("expected test:old first (most recent activity), got %s", convos[0].ID)
	}
	if convos[1].ID != "test:new" {
		t.Errorf("expected test:new second, got %s", convos[1].ID)
	}
}

func TestListConversations_Pagination(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	// Create 5 conversations.
	for i := 0; i < 5; i++ {
		_, _ = store.GetOrCreateConversation(ctx, "test", fmt.Sprintf("user%d", i))
	}

	// List all — no limit.
	all, total, err := store.ListConversations(ctx, SessionListOpts{})
	if err != nil {
		t.Fatalf("listing all: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(all) != 5 {
		t.Errorf("len = %d, want 5", len(all))
	}

	// Page 1: limit 2, offset 0.
	page1, total1, err := store.ListConversations(ctx, SessionListOpts{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total1 != 5 {
		t.Errorf("page1 total = %d, want 5", total1)
	}
	if len(page1) != 2 {
		t.Errorf("page1 len = %d, want 2", len(page1))
	}

	// Page 2: limit 2, offset 2.
	page2, _, err := store.ListConversations(ctx, SessionListOpts{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2 len = %d, want 2", len(page2))
	}

	// Page 3: limit 2, offset 4 — only 1 remaining.
	page3, _, err := store.ListConversations(ctx, SessionListOpts{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Errorf("page3 len = %d, want 1", len(page3))
	}
}

func TestListConversations_AgentFilter(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	// Create conversations with different agent prefixes.
	_ = store.GetOrCreateConversationByID(ctx, "alpha:tg:1", "tg", "alpha-1")
	_ = store.GetOrCreateConversationByID(ctx, "alpha:tg:2", "tg", "alpha-2")
	_ = store.GetOrCreateConversationByID(ctx, "beta:tg:1", "tg", "beta-1")

	// Filter by agent "alpha".
	filtered, total, err := store.ListConversations(ctx, SessionListOpts{Agent: "alpha"})
	if err != nil {
		t.Fatalf("filtering: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(filtered) != 2 {
		t.Errorf("len = %d, want 2", len(filtered))
	}
	for _, c := range filtered {
		if !strings.HasPrefix(c.ID, "alpha:") {
			t.Errorf("unexpected conversation ID: %s", c.ID)
		}
	}

	// Filter by agent "beta".
	beta, betaTotal, err := store.ListConversations(ctx, SessionListOpts{Agent: "beta"})
	if err != nil {
		t.Fatalf("beta filter: %v", err)
	}
	if betaTotal != 1 {
		t.Errorf("beta total = %d, want 1", betaTotal)
	}
	if len(beta) != 1 {
		t.Errorf("beta len = %d, want 1", len(beta))
	}
}

func TestGetTelemetrySummary(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_, _ = store.GetOrCreateConversation(ctx, "test", "1")
	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{
		Role: "assistant", Content: "hi", Model: "gpt-4", Provider: "openai",
		Cost: 0.01, TokensPrompt: 100, TokensCompletion: 50,
	})
	_ = store.AddToolCalls(ctx, "test:1", msgID, []ToolCallRecord{
		{ToolName: "search", ServerName: "web", DurationMs: 100, Success: true},
	})
	userMsgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "user", Content: "query"})
	_ = store.AddSkillUsages(ctx, "test:1", userMsgID, []SkillUsageRecord{
		{SkillName: "greeting", MatchType: "always"},
	})

	summary, err := store.GetTelemetrySummary(ctx, nil, nil)
	if err != nil {
		t.Fatalf("GetTelemetrySummary: %v", err)
	}
	if len(summary.ByModel) != 1 || summary.ByModel[0].Model != "gpt-4" {
		t.Errorf("by_model: %+v", summary.ByModel)
	}
	if len(summary.ByTool) != 1 || summary.ByTool[0].ToolName != "search" {
		t.Errorf("by_tool: %+v", summary.ByTool)
	}
	if len(summary.BySkill) != 1 || summary.BySkill[0].SkillName != "greeting" {
		t.Errorf("by_skill: %+v", summary.BySkill)
	}
}

// ---------------------------------------------------------------------------
// ActiveChannelStore tests
// ---------------------------------------------------------------------------

func TestActiveChannelStore_SetAndGet(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Initially no active channel.
	name, err := store.GetActiveChannel(ctx, "telegram:12345")
	if err != nil {
		t.Fatalf("GetActiveChannel: %v", err)
	}
	if name != "" {
		t.Errorf("expected empty, got %q", name)
	}

	// Set active channel.
	if err := store.SetActiveChannel(ctx, "telegram:12345", "work"); err != nil {
		t.Fatalf("SetActiveChannel: %v", err)
	}

	name, err = store.GetActiveChannel(ctx, "telegram:12345")
	if err != nil {
		t.Fatalf("GetActiveChannel: %v", err)
	}
	if name != "work" {
		t.Errorf("expected work, got %q", name)
	}
}

func TestActiveChannelStore_Upsert(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	if err := store.SetActiveChannel(ctx, "telegram:12345", "work"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveChannel(ctx, "telegram:12345", "personal"); err != nil {
		t.Fatal(err)
	}

	name, err := store.GetActiveChannel(ctx, "telegram:12345")
	if err != nil {
		t.Fatal(err)
	}
	if name != "personal" {
		t.Errorf("expected personal after upsert, got %q", name)
	}
}

func TestActiveChannelStore_Clear(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	if err := store.SetActiveChannel(ctx, "telegram:12345", "work"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearActiveChannel(ctx, "telegram:12345"); err != nil {
		t.Fatal(err)
	}

	name, err := store.GetActiveChannel(ctx, "telegram:12345")
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		t.Errorf("expected empty after clear, got %q", name)
	}
}

func TestActiveChannelStore_ListActiveChannels(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	if err := store.SetActiveChannel(ctx, "telegram:111", "work"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveChannel(ctx, "discord:222", "personal"); err != nil {
		t.Fatal(err)
	}

	all, err := store.ListActiveChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if all["telegram:111"] != "work" {
		t.Errorf("telegram:111 = %q, want work", all["telegram:111"])
	}
	if all["discord:222"] != "personal" {
		t.Errorf("discord:222 = %q, want personal", all["discord:222"])
	}
}

func TestAddMessage_WithReasoningContent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "test", "1")
	_, err = store.AddMessage(ctx, convID, StoredMessage{
		Role:             "assistant",
		Content:          "Hello!",
		ReasoningContent: "The user said hi, I should greet them.",
	})
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	msgs, err := store.GetMessages(ctx, convID, 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].ReasoningContent != "The user said hi, I should greet them." {
		t.Errorf("reasoning_content = %q, want %q", msgs[0].ReasoningContent, "The user said hi, I should greet them.")
	}
}

func TestClearMessages_KeepsConversation(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_, _ = store.GetOrCreateConversation(ctx, "test", "1")
	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "user", Content: "hello"})
	_, _ = store.AddMessage(ctx, "test:1", StoredMessage{Role: "assistant", Content: "hi"})
	_ = store.AddToolCalls(ctx, "test:1", msgID, []ToolCallRecord{{ToolName: "t1", Success: true}})
	_ = store.AddSkillUsages(ctx, "test:1", msgID, []SkillUsageRecord{{SkillName: "s1", MatchType: "always"}})
	_ = store.UpdateConversationStats(ctx, "test:1", "test", StoredMessage{Cost: 0.01}, 1, 0)

	if err := store.ClearMessages(ctx, "test:1"); err != nil {
		t.Fatalf("ClearMessages: %v", err)
	}

	// Messages should be gone.
	msgs, err := store.GetMessages(ctx, "test:1", 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("messages remain: %d", len(msgs))
	}

	// Telemetry should be gone.
	tc, _ := store.GetToolCalls(ctx, "test:1")
	if len(tc) != 0 {
		t.Errorf("tool calls remain: %d", len(tc))
	}
	su, _ := store.GetSkillUsages(ctx, "test:1")
	if len(su) != 0 {
		t.Errorf("skill usages remain: %d", len(su))
	}
	stats, _ := store.GetConversationStats(ctx, "test:1")
	if stats != nil {
		t.Errorf("conversation stats remain: %+v", stats)
	}

	// Conversation row should still exist.
	convos, _, err := store.ListConversations(ctx, SessionListOpts{})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convos) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convos))
	}
	if convos[0].ID != "test:1" {
		t.Errorf("conversation ID = %q, want %q", convos[0].ID, "test:1")
	}
	if convos[0].MessageCount != 0 {
		t.Errorf("message count = %d, want 0", convos[0].MessageCount)
	}
}

func TestSkillTelemetry_BumpFreshSkill(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	if err := store.BumpSkillUse(ctx, "agent-a", "daily-report"); err != nil {
		t.Fatalf("BumpSkillUse: %v", err)
	}

	stats, err := store.GetSkillUsage(ctx, "agent-a", "daily-report")
	if err != nil {
		t.Fatalf("GetSkillUsage: %v", err)
	}
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats.UseCount != 1 {
		t.Errorf("use_count = %d, want 1", stats.UseCount)
	}
	if stats.ViewCount != 0 {
		t.Errorf("view_count = %d, want 0", stats.ViewCount)
	}
	if stats.LastUsedAt == nil {
		t.Error("last_used_at should be set")
	}
	if stats.CreatedAt.IsZero() {
		t.Error("created_at should be set")
	}
}

func TestSkillTelemetry_BumpExistingSkill(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	if err := store.BumpSkillUse(ctx, "a", "sk1"); err != nil {
		t.Fatal(err)
	}
	if err := store.BumpSkillView(ctx, "a", "sk1"); err != nil {
		t.Fatal(err)
	}
	if err := store.BumpSkillUse(ctx, "a", "sk1"); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetSkillUsage(ctx, "a", "sk1")
	if err != nil {
		t.Fatal(err)
	}
	if stats.UseCount != 2 {
		t.Errorf("use_count = %d, want 2", stats.UseCount)
	}
	if stats.ViewCount != 1 {
		t.Errorf("view_count = %d, want 1", stats.ViewCount)
	}
	if stats.PatchCount != 0 {
		t.Errorf("patch_count = %d, want 0", stats.PatchCount)
	}
}

func TestSkillTelemetry_ConcurrentBumps(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	store, err := NewSQLiteMemoryStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_ = store.BumpSkillUse(ctx, "a", "concurrent-skill")
		}()
	}
	wg.Wait()

	stats, err := store.GetSkillUsage(ctx, "a", "concurrent-skill")
	if err != nil {
		t.Fatal(err)
	}
	if stats.UseCount != n {
		t.Errorf("use_count = %d, want %d", stats.UseCount, n)
	}
}

func TestSkillTelemetry_ListFiltersByAgent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_ = store.BumpSkillUse(ctx, "agent-a", "skill-1")
	_ = store.BumpSkillUse(ctx, "agent-a", "skill-2")
	_ = store.BumpSkillUse(ctx, "agent-b", "skill-3")

	listA, err := store.ListSkillUsage(ctx, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 2 {
		t.Errorf("agent-a skills = %d, want 2", len(listA))
	}

	listB, err := store.ListSkillUsage(ctx, "agent-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(listB) != 1 {
		t.Errorf("agent-b skills = %d, want 1", len(listB))
	}
}

func TestSkillTelemetry_SetSkillState(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_ = store.BumpSkillUse(ctx, "a", "sk")
	if err := store.SetSkillState(ctx, "a", "sk", "archived"); err != nil {
		t.Fatal(err)
	}

	stats, _ := store.GetSkillUsage(ctx, "a", "sk")
	if stats.State != "archived" {
		t.Errorf("state = %q, want archived", stats.State)
	}
	if stats.ArchivedAt == nil {
		t.Error("archived_at should be set")
	}
}

func TestSkillTelemetry_SetSkillPinned(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_ = store.BumpSkillUse(ctx, "a", "sk")
	if err := store.SetSkillPinned(ctx, "a", "sk", true); err != nil {
		t.Fatal(err)
	}

	stats, _ := store.GetSkillUsage(ctx, "a", "sk")
	if !stats.Pinned {
		t.Error("pinned = false, want true")
	}

	if err := store.SetSkillPinned(ctx, "a", "sk", false); err != nil {
		t.Fatal(err)
	}
	stats, _ = store.GetSkillUsage(ctx, "a", "sk")
	if stats.Pinned {
		t.Error("pinned = true, want false")
	}
}

func TestSkillTelemetry_SetSkillOrigin(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	if err := store.SetSkillOrigin(ctx, "a", "sk", "operator"); err != nil {
		t.Fatal(err)
	}
	stats, _ := store.GetSkillUsage(ctx, "a", "sk")
	if stats.Origin != "operator" {
		t.Errorf("origin = %q, want operator", stats.Origin)
	}

	if err := store.SetSkillOrigin(ctx, "a", "sk", "agent"); err != nil {
		t.Fatal(err)
	}
	stats, _ = store.GetSkillUsage(ctx, "a", "sk")
	if stats.Origin != "agent" {
		t.Errorf("origin = %q, want agent", stats.Origin)
	}
}

func TestSkillTelemetry_GetSkillUsage_NotFound(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	stats, err := store.GetSkillUsage(context.Background(), "x", "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats != nil {
		t.Error("expected nil stats for nonexistent skill")
	}
}

func TestFTS5_BasicSearch(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "test", "1")
	_ = store.UpdateConversationStats(ctx, convID, "default", StoredMessage{}, 0, 0)

	_, _ = store.AddMessage(ctx, convID, StoredMessage{
		Role: "user", Content: "Tell me about quantum computing research",
	})
	_, _ = store.AddMessage(ctx, convID, StoredMessage{
		Role: "assistant", Content: "Quantum computing uses qubits instead of classical bits",
	})

	hits, err := store.SearchMessages(ctx, "quantum", 10, "")
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit for 'quantum'")
	}
	if hits[0].ConversationID != convID {
		t.Errorf("conversation_id = %q, want %q", hits[0].ConversationID, convID)
	}
	if !strings.Contains(hits[0].Snippet, "quantum") && !strings.Contains(hits[0].Snippet, "Quantum") {
		t.Errorf("snippet should contain the search term, got: %s", hits[0].Snippet)
	}
}

func TestFTS5_TriggerKeepsIndexInSync(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "test", "1")
	_ = store.UpdateConversationStats(ctx, convID, "default", StoredMessage{}, 0, 0)

	_, _ = store.AddMessage(ctx, convID, StoredMessage{
		Role: "user", Content: "uniquetoken_xyzzy for deletion test",
	})

	hits, err := store.SearchMessages(ctx, "uniquetoken_xyzzy", 10, "")
	if err != nil {
		t.Fatalf("search after insert: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit after insert, got %d", len(hits))
	}

	// Delete the conversation (which cascades to messages).
	if err := store.DeleteConversation(ctx, convID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	hits, err = store.SearchMessages(ctx, "uniquetoken_xyzzy", 10, "")
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits after delete, got %d", len(hits))
	}
}

func TestFTS5_AgentFilter(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convA, _ := store.GetOrCreateConversation(ctx, "test", "a1")
	_ = store.UpdateConversationStats(ctx, convA, "alice", StoredMessage{}, 0, 0)
	_, _ = store.AddMessage(ctx, convA, StoredMessage{
		Role: "user", Content: "secret recipe for chocolate cake",
	})

	convB, _ := store.GetOrCreateConversation(ctx, "test", "b1")
	_ = store.UpdateConversationStats(ctx, convB, "bob", StoredMessage{}, 0, 0)
	_, _ = store.AddMessage(ctx, convB, StoredMessage{
		Role: "user", Content: "recipe for banana bread",
	})

	// Alice should only see her own conversation.
	hits, err := store.SearchMessages(ctx, "recipe", 10, "alice")
	if err != nil {
		t.Fatalf("SearchMessages alice: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("alice expected 1 hit, got %d", len(hits))
	}
	if hits[0].ConversationID != convA {
		t.Errorf("hit conversation = %q, want %q", hits[0].ConversationID, convA)
	}

	// Bob should only see his own conversation.
	hits, err = store.SearchMessages(ctx, "recipe", 10, "bob")
	if err != nil {
		t.Fatalf("SearchMessages bob: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("bob expected 1 hit, got %d", len(hits))
	}
	if hits[0].ConversationID != convB {
		t.Errorf("hit conversation = %q, want %q", hits[0].ConversationID, convB)
	}

	// No filter returns both.
	hits, err = store.SearchMessages(ctx, "recipe", 10, "")
	if err != nil {
		t.Fatalf("SearchMessages no filter: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("no filter expected 2 hits, got %d", len(hits))
	}
}

func TestFTS5_EmptyQuery(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, err = store.SearchMessages(context.Background(), "", 10, "")
	if err == nil {
		t.Error("expected error for empty query")
	}
	_, err = store.SearchMessages(context.Background(), "   ", 10, "")
	if err == nil {
		t.Error("expected error for whitespace-only query")
	}
}

func TestFTS5_PhraseSearch(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "test", "1")
	_ = store.UpdateConversationStats(ctx, convID, "default", StoredMessage{}, 0, 0)

	_, _ = store.AddMessage(ctx, convID, StoredMessage{
		Role: "user", Content: "the quick brown fox jumps over the lazy dog",
	})
	_, _ = store.AddMessage(ctx, convID, StoredMessage{
		Role: "user", Content: "the fox is brown and quick",
	})

	// Phrase search should only match the exact sequence.
	hits, err := store.SearchMessages(ctx, `"quick brown fox"`, 10, "")
	if err != nil {
		t.Fatalf("SearchMessages phrase: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("phrase search expected 1 hit, got %d", len(hits))
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain words", "hello world", "hello world"},
		{"quoted phrase unchanged", `"hello world"`, `"hello world"`},
		{"bare date gets quoted", "2026-05-16", `"2026-05-16"`},
		{"multiple dates", "2026-05-16 2026-05-17", `"2026-05-16" "2026-05-17"`},
		{"mixed quoted and bare dates",
			`"Scheduled" "daily-github-trending" 2026-05-16 2026-05-17`,
			`"Scheduled" "daily-github-trending" "2026-05-16" "2026-05-17"`},
		{"leading hyphen NOT preserved", "-excluded", "-excluded"},
		{"hyphenated word quoted", "session-search", `"session-search"`},
		{"OR preserved", "foo OR bar", "foo OR bar"},
		{"NOT preserved", "NOT secret", "NOT secret"},
		{"NEAR preserved", "NEAR(a b, 5)", "NEAR(a b, 5)"},
		{"no change for plain", "quantum", "quantum"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFTS5_DateSearch(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "test", "1")
	_ = store.UpdateConversationStats(ctx, convID, "default", StoredMessage{}, 0, 0)

	_, _ = store.AddMessage(ctx, convID, StoredMessage{
		Role: "user", Content: "Scheduled daily-github-trending run on 2026-05-16",
	})

	// Bare date query that previously caused "no such column: 05".
	hits, err := store.SearchMessages(ctx, "2026-05-16", 10, "")
	if err != nil {
		t.Fatalf("SearchMessages with date: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("date search expected 1 hit, got %d", len(hits))
	}
}

func TestFTS5_HyphenatedWordSearch(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "test", "1")
	_ = store.UpdateConversationStats(ctx, convID, "default", StoredMessage{}, 0, 0)

	_, _ = store.AddMessage(ctx, convID, StoredMessage{
		Role: "user", Content: "the session-search tool returned an error",
	})

	hits, err := store.SearchMessages(ctx, "session-search", 10, "")
	if err != nil {
		t.Fatalf("SearchMessages with hyphenated word: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("hyphenated word search expected 1 hit, got %d", len(hits))
	}
}
