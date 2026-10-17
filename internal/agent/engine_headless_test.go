package agent

import (
	"context"
	"testing"
)

// A headless scheduled run has no chat: the turn runs and is stored, and
// nothing is sent.
func TestHandleMessage_HeadlessRunsAndSendsNothing(t *testing.T) {
	e, store, sent, _ := newGuardTestEngine(t, "No tasks.", 5, defaultTestReplyGuard())
	msg := scheduledMsg("")
	msg.Adapter, msg.ExternalID = "", ""
	msg.ConversationID = "sched:check-tasks-pamela"
	msg.Headless = true

	if err := e.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if len(sent.msgs) != 0 {
		t.Errorf("sent %d messages, want none", len(sent.msgs))
	}
	got, err := store.GetMessages(context.Background(), "sched:check-tasks-pamela", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Content != "No tasks." {
		t.Errorf("stored = %+v, want the scheduled prompt and the reply", got)
	}
}
