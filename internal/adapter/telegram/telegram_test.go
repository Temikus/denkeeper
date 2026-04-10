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

	a := newWithBot(nil, []int64{111, 222, 333}, logger, nil)

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
	a := newWithBot(nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), nil)
	if a.Name() != "telegram" {
		t.Errorf("Name() = %q, want telegram", a.Name())
	}
}

func TestSend_InvalidChatID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger, nil)

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
	a := newWithBot(nil, []int64{}, logger, nil)

	if a.IsAllowed(12345) {
		t.Error("empty allowlist should deny all users")
	}
	if a.IsAllowed(0) {
		t.Error("empty allowlist should deny user ID 0")
	}
}

func TestNewWithBot_VoiceOpts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	a := newWithBot(nil, nil, logger, &VoiceOpts{
		TTSVoice:       "nova",
		AutoVoiceReply: true,
	})
	if a.ttsVoice != "nova" {
		t.Errorf("ttsVoice = %q, want nova", a.ttsVoice)
	}
	if !a.autoVoiceReply {
		t.Error("autoVoiceReply should be true")
	}
}

func TestNewWithBot_NilVoiceOpts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	a := newWithBot(nil, nil, logger, nil)
	if a.stt != nil {
		t.Error("stt should be nil when no voice opts")
	}
	if a.tts != nil {
		t.Error("tts should be nil when no voice opts")
	}
	if a.autoVoiceReply {
		t.Error("autoVoiceReply should default to false")
	}
}

// mockCallbackResolver satisfies adapter.CallbackResolver for wiring tests.
type mockCallbackResolver struct {
	called bool
	retStr string
}

func (m *mockCallbackResolver) Resolve(_ context.Context, _ string) (string, error) {
	m.called = true
	return m.retStr, nil
}

func TestDebugToggle(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger, nil)

	chatID := int64(42)

	if a.IsDebug(chatID) {
		t.Fatal("debug should be off by default")
	}

	// Toggle on.
	a.debugMu.Lock()
	a.debugChats[chatID] = true
	a.debugMu.Unlock()

	if !a.IsDebug(chatID) {
		t.Fatal("debug should be on after toggle")
	}

	// Toggle off.
	a.debugMu.Lock()
	a.debugChats[chatID] = false
	a.debugMu.Unlock()

	if a.IsDebug(chatID) {
		t.Fatal("debug should be off after second toggle")
	}
}

func TestIsDebugByExternalID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger, nil)

	a.debugMu.Lock()
	a.debugChats[123] = true
	a.debugMu.Unlock()

	if !a.IsDebugByExternalID("123") {
		t.Fatal("expected debug for externalID '123'")
	}
	if a.IsDebugByExternalID("456") {
		t.Fatal("did not expect debug for externalID '456'")
	}
	if a.IsDebugByExternalID("not-a-number") {
		t.Fatal("invalid externalID should return false")
	}
}

func TestBuildButtonRows_NoLayout(t *testing.T) {
	buttons := []adapter.KeyboardButton{
		{Label: "A", CallbackData: "a"},
		{Label: "B", CallbackData: "b"},
		{Label: "C", CallbackData: "c"},
	}
	rows := buildButtonRows(buttons, nil)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i, row := range rows {
		if len(row) != 1 {
			t.Errorf("row %d: expected 1 button, got %d", i, len(row))
		}
	}
}

func TestBuildButtonRows_WithLayout(t *testing.T) {
	buttons := []adapter.KeyboardButton{
		{Label: "A", CallbackData: "a"},
		{Label: "B", CallbackData: "b"},
		{Label: "C", CallbackData: "c"},
		{Label: "D", CallbackData: "d"},
	}
	rows := buildButtonRows(buttons, []int{2, 2})
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if len(rows[0]) != 2 {
		t.Errorf("row 0: expected 2 buttons, got %d", len(rows[0]))
	}
	if len(rows[1]) != 2 {
		t.Errorf("row 1: expected 2 buttons, got %d", len(rows[1]))
	}
	if rows[0][0].Text != "A" || rows[0][1].Text != "B" {
		t.Errorf("row 0 labels wrong: %q %q", rows[0][0].Text, rows[0][1].Text)
	}
	if rows[1][0].Text != "C" || rows[1][1].Text != "D" {
		t.Errorf("row 1 labels wrong: %q %q", rows[1][0].Text, rows[1][1].Text)
	}
}

func TestBuildButtonRows_LayoutExceedsButtons(t *testing.T) {
	buttons := []adapter.KeyboardButton{
		{Label: "A", CallbackData: "a"},
		{Label: "B", CallbackData: "b"},
	}
	rows := buildButtonRows(buttons, []int{3, 2})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (second row skipped), got %d", len(rows))
	}
	if len(rows[0]) != 2 {
		t.Errorf("row 0: expected 2 buttons (clamped), got %d", len(rows[0]))
	}
}

func TestDebugChecker_Interface(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger, nil)

	// Verify the adapter satisfies the DebugChecker interface.
	var dc adapter.DebugChecker = a
	if dc.IsDebugByExternalID("999") {
		t.Fatal("fresh adapter should not have debug enabled")
	}
}

func TestMessageEditor_Interface(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger, nil)

	// Verify the adapter satisfies the MessageEditor interface.
	var _ adapter.MessageEditor = a
}

func TestSetCallbackResolver_Wiring(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger, nil)

	if a.callbackResolver != nil {
		t.Error("callbackResolver should be nil before SetCallbackResolver")
	}

	mock := &mockCallbackResolver{}
	a.SetCallbackResolver(mock)

	if a.callbackResolver == nil {
		t.Error("callbackResolver should not be nil after SetCallbackResolver")
	}
	if a.callbackResolver != mock {
		t.Error("callbackResolver should be the exact resolver passed to SetCallbackResolver")
	}
}

func TestCallbackResolver_NotSetByDefault(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	a := newWithBot(nil, nil, logger, nil)

	// The adapter must not call callbackResolver when it is nil.
	// Validate the guard branch: Start() checks `a.callbackResolver != nil`
	// before routing callback queries, so a nil resolver is safe.
	if a.callbackResolver != nil {
		t.Error("fresh adapter must have nil callbackResolver")
	}
}
