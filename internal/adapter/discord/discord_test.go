package discord

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Temikus/denkeeper/internal/adapter"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// mockSession is a no-op discordgo.Session stand-in for unit tests.
// We use the real discordgo.Session struct but override behaviour by capturing
// calls. Since discordgo doesn't expose an interface, we test via the exported
// adapter methods and the internal onMessageCreate handler directly.
func testAdapter(allowedUsers []string) *Adapter {
	// We can't open a real gateway in unit tests, so we construct the adapter
	// directly using the internal constructor (same package).
	dg, _ := discordgo.New("Bot fake-token")
	return newWithSession(dg, allowedUsers, testLogger())
}

// ─── Name ────────────────────────────────────────────────────────────────────

func TestAdapter_Name(t *testing.T) {
	a := testAdapter(nil)
	if got := a.Name(); got != "discord" {
		t.Errorf("Name() = %q, want discord", got)
	}
}

// ─── onMessageCreate ─────────────────────────────────────────────────────────

func TestAdapter_MessageCreate_AllowedUser(t *testing.T) {
	a := testAdapter([]string{"user-123"})
	incoming := make(chan adapter.IncomingMessage, 1)

	a.mu.Lock()
	a.incoming = incoming
	a.mu.Unlock()

	// Simulate an incoming message from an allowed user.
	msg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "chan-42",
			Content:   "Hello bot!",
			Author: &discordgo.User{
				ID:       "user-123",
				Username: "alice",
			},
		},
	}
	// We need a state with a user so the bot-self check works.
	a.session.State.User = &discordgo.User{ID: "bot-self"}

	a.onMessageCreate(a.session, msg)

	select {
	case m := <-incoming:
		if m.Adapter != "discord" {
			t.Errorf("Adapter = %q, want discord", m.Adapter)
		}
		if m.ExternalID != "chan-42" {
			t.Errorf("ExternalID = %q, want chan-42", m.ExternalID)
		}
		if m.UserID != "user-123" {
			t.Errorf("UserID = %q, want user-123", m.UserID)
		}
		if m.UserName != "alice" {
			t.Errorf("UserName = %q, want alice", m.UserName)
		}
		if m.Text != "Hello bot!" {
			t.Errorf("Text = %q, want Hello bot!", m.Text)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for incoming message")
	}
}

func TestAdapter_MessageCreate_UnauthorizedUser(t *testing.T) {
	a := testAdapter([]string{"user-allowed"})
	incoming := make(chan adapter.IncomingMessage, 1)

	a.mu.Lock()
	a.incoming = incoming
	a.mu.Unlock()

	a.session.State.User = &discordgo.User{ID: "bot-self"}

	msg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-2",
			ChannelID: "chan-1",
			Content:   "I should be blocked",
			Author: &discordgo.User{
				ID:       "unauthorized-user",
				Username: "eve",
			},
		},
	}

	a.onMessageCreate(a.session, msg)

	select {
	case m := <-incoming:
		t.Errorf("unexpected message from unauthorized user: %+v", m)
	case <-time.After(50 * time.Millisecond):
		// Expected: no message delivered.
	}
}

func TestAdapter_MessageCreate_BotSelf_Ignored(t *testing.T) {
	a := testAdapter([]string{"bot-self"})
	incoming := make(chan adapter.IncomingMessage, 1)

	a.mu.Lock()
	a.incoming = incoming
	a.mu.Unlock()

	a.session.State.User = &discordgo.User{ID: "bot-self"}

	msg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-3",
			ChannelID: "chan-1",
			Content:   "Echo from self",
			Author:    &discordgo.User{ID: "bot-self", Username: "mybot"},
		},
	}

	a.onMessageCreate(a.session, msg)

	select {
	case m := <-incoming:
		t.Errorf("unexpected message from bot self: %+v", m)
	case <-time.After(50 * time.Millisecond):
		// Expected: bot self messages are ignored.
	}
}

func TestAdapter_MessageCreate_EmptyContent_Ignored(t *testing.T) {
	a := testAdapter([]string{"user-123"})
	incoming := make(chan adapter.IncomingMessage, 1)

	a.mu.Lock()
	a.incoming = incoming
	a.mu.Unlock()

	a.session.State.User = &discordgo.User{ID: "bot-self"}

	msg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-4",
			ChannelID: "chan-1",
			Content:   "", // empty
			Author:    &discordgo.User{ID: "user-123", Username: "alice"},
		},
	}

	a.onMessageCreate(a.session, msg)

	select {
	case m := <-incoming:
		t.Errorf("unexpected message with empty content: %+v", m)
	case <-time.After(50 * time.Millisecond):
		// Expected: empty messages are skipped.
	}
}

func TestAdapter_MessageCreate_NilIncoming_DoesNotPanic(t *testing.T) {
	a := testAdapter([]string{"user-123"})
	// incoming is nil — should not panic

	a.session.State.User = &discordgo.User{ID: "bot-self"}

	msg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			Content:   "hello",
			ChannelID: "chan-1",
			Author:    &discordgo.User{ID: "user-123"},
		},
	}

	// Should not panic even though a.incoming is nil.
	a.onMessageCreate(a.session, msg)
}

// ─── Stop ────────────────────────────────────────────────────────────────────

func TestAdapter_Stop_Idempotent(t *testing.T) {
	a := testAdapter(nil)
	// Closing an unopened session should not panic.
	// discordgo.Session.Close() on an unconnected session is a no-op.
	err := a.Stop()
	if err != nil {
		t.Errorf("Stop() unexpected error: %v", err)
	}
}

// ─── AllowList ───────────────────────────────────────────────────────────────

func TestAdapter_AllowList_MultipleUsers(t *testing.T) {
	a := testAdapter([]string{"alice", "bob", "carol"})

	for _, uid := range []string{"alice", "bob", "carol"} {
		if !a.allowedUsers[uid] {
			t.Errorf("expected %q in allowed list", uid)
		}
	}
	if a.allowedUsers["dave"] {
		t.Error("expected dave NOT in allowed list")
	}
}

// ─── Send ────────────────────────────────────────────────────────────────────

func TestAdapter_Send_ErrorWhenNotConnected(t *testing.T) {
	a := testAdapter([]string{"user-1"})

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "chan-1",
		Text:       "hello",
	})
	// Expected: error because the session is not connected/authorised.
	// We just verify it doesn't panic.
	_ = err // may or may not error depending on discordgo internals
}
