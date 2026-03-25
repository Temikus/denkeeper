package agent

import (
	"context"
	"strings"
	"testing"
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
		if err := store.AddMessage(ctx, convID, msg); err != nil {
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

	// Test limit
	limited, err := store.GetMessages(ctx, convID, 2)
	if err != nil {
		t.Fatalf("GetMessages with limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("got %d messages with limit 2, want 2", len(limited))
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
		if err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: c}); err != nil {
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
	if err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: largeContent}); err != nil {
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
	if err := store.GetOrCreateConversationByID(ctx, convID); err != nil {
		t.Fatalf("GetOrCreateConversationByID: %v", err)
	}

	// Messages can be stored against the created conversation.
	if err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "trigger"}); err != nil {
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
	if err := store.GetOrCreateConversationByID(ctx, convID); err != nil {
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
	_ = store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "hi"})
	_ = store.AddMessage(ctx, convID, StoredMessage{Role: "assistant", Content: "hello"})

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
	convos, err := store.ListConversations(ctx)
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

	if err := store.AddMessage(ctx, conv1, StoredMessage{Role: "user", Content: "msg for conv1"}); err != nil {
		t.Fatalf("AddMessage conv1: %v", err)
	}
	if err := store.AddMessage(ctx, conv2, StoredMessage{Role: "user", Content: "msg for conv2"}); err != nil {
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
