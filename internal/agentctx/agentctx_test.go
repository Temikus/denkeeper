package agentctx

import (
	"context"
	"testing"
)

func TestAdapter_RoundTrip(t *testing.T) {
	ctx := WithAdapter(context.Background(), "telegram")
	if got := Adapter(ctx); got != "telegram" {
		t.Errorf("Adapter() = %q, want %q", got, "telegram")
	}
}

func TestAdapter_Unset(t *testing.T) {
	if got := Adapter(context.Background()); got != "" {
		t.Errorf("Adapter() on empty ctx = %q, want empty", got)
	}
}

func TestExternalID_RoundTrip(t *testing.T) {
	ctx := WithExternalID(context.Background(), "msg-42")
	if got := ExternalID(ctx); got != "msg-42" {
		t.Errorf("ExternalID() = %q, want %q", got, "msg-42")
	}
}

func TestExternalID_Unset(t *testing.T) {
	if got := ExternalID(context.Background()); got != "" {
		t.Errorf("ExternalID() on empty ctx = %q, want empty", got)
	}
}

func TestConversationID_RoundTrip(t *testing.T) {
	ctx := WithConversationID(context.Background(), "conv-99")
	if got := ConversationID(ctx); got != "conv-99" {
		t.Errorf("ConversationID() = %q, want %q", got, "conv-99")
	}
}

func TestConversationID_Unset(t *testing.T) {
	if got := ConversationID(context.Background()); got != "" {
		t.Errorf("ConversationID() on empty ctx = %q, want empty", got)
	}
}

func TestSkillContext_RoundTrip(t *testing.T) {
	s := &SkillSummary{
		Name:         "heartbeat",
		Description:  "Periodic health check",
		IsScheduled:  true,
		ScheduleName: "heartbeat-hourly",
	}
	ctx := WithSkillContext(context.Background(), s)
	got := SkillContext(ctx)
	if got == nil {
		t.Fatal("SkillContext() returned nil")
	}
	if got.Name != "heartbeat" {
		t.Errorf("Name = %q, want %q", got.Name, "heartbeat")
	}
	if got.Description != "Periodic health check" {
		t.Errorf("Description = %q, want %q", got.Description, "Periodic health check")
	}
	if !got.IsScheduled {
		t.Error("IsScheduled = false, want true")
	}
	if got.ScheduleName != "heartbeat-hourly" {
		t.Errorf("ScheduleName = %q, want %q", got.ScheduleName, "heartbeat-hourly")
	}
}

func TestSkillContext_Unset(t *testing.T) {
	if got := SkillContext(context.Background()); got != nil {
		t.Errorf("SkillContext() on empty ctx = %+v, want nil", got)
	}
}

func TestAllKeys_Composed(t *testing.T) {
	ctx := context.Background()
	ctx = WithAdapter(ctx, "discord")
	ctx = WithExternalID(ctx, "ext-1")
	ctx = WithConversationID(ctx, "conv-1")

	if got := Adapter(ctx); got != "discord" {
		t.Errorf("Adapter() = %q, want %q", got, "discord")
	}
	if got := ExternalID(ctx); got != "ext-1" {
		t.Errorf("ExternalID() = %q, want %q", got, "ext-1")
	}
	if got := ConversationID(ctx); got != "conv-1" {
		t.Errorf("ConversationID() = %q, want %q", got, "conv-1")
	}
}
