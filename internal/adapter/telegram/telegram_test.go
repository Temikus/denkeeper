package telegram

import (
	"log/slog"
	"os"
	"testing"
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
