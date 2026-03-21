package telegram

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
)

func TestIsAllowed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	a := newWithBot(nil, []int64{111, 222, 333}, logger)

	tests := []struct {
		userID int64
		want   bool
	}{
		{111, true},
		{222, true},
		{333, true},
		{444, false},
		{0, false},
	}

	for _, tt := range tests {
		if got := a.IsAllowed(tt.userID); got != tt.want {
			t.Errorf("IsAllowed(%d) = %v, want %v", tt.userID, got, tt.want)
		}
	}
}

func TestName(t *testing.T) {
	a := newWithBot(nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if a.Name() != "telegram" {
		t.Errorf("Name() = %q, want telegram", a.Name())
	}
}

func TestSend_InvalidChatID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger)

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "not-a-number",
		Text:       "Hello",
	})
	if err == nil {
		t.Fatal("expected error for non-numeric chat ID")
	}
}

func TestIsAllowed_EmptyList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, []int64{}, logger)

	if a.IsAllowed(12345) {
		t.Error("empty allowlist should deny all users")
	}
	if a.IsAllowed(0) {
		t.Error("empty allowlist should deny user ID 0")
	}
}
