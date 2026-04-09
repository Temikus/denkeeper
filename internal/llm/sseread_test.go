package llm

import (
	"strings"
	"testing"
)

func TestSSEScanner_BasicEvents(t *testing.T) {
	input := "data: hello\n\ndata: world\n\n"
	s := NewSSEScanner(strings.NewReader(input))

	if !s.Next() {
		t.Fatal("expected first event")
	}
	if got := s.Event().Data; got != "hello" {
		t.Errorf("first event data = %q, want %q", got, "hello")
	}

	if !s.Next() {
		t.Fatal("expected second event")
	}
	if got := s.Event().Data; got != "world" {
		t.Errorf("second event data = %q, want %q", got, "world")
	}

	if s.Next() {
		t.Error("expected no more events")
	}
	if err := s.Err(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSSEScanner_EventTypes(t *testing.T) {
	input := "event: content_block_delta\ndata: {\"type\":\"text\"}\n\nevent: message_stop\ndata: {}\n\n"
	s := NewSSEScanner(strings.NewReader(input))

	if !s.Next() {
		t.Fatal("expected first event")
	}
	evt := s.Event()
	if evt.Type != "content_block_delta" {
		t.Errorf("type = %q, want %q", evt.Type, "content_block_delta")
	}
	if evt.Data != `{"type":"text"}` {
		t.Errorf("data = %q", evt.Data)
	}

	if !s.Next() {
		t.Fatal("expected second event")
	}
	evt = s.Event()
	if evt.Type != "message_stop" {
		t.Errorf("type = %q, want %q", evt.Type, "message_stop")
	}

	if s.Next() {
		t.Error("expected no more events")
	}
}

func TestSSEScanner_DoneSentinel(t *testing.T) {
	input := "data: first\n\ndata: [DONE]\n\ndata: should_not_appear\n\n"
	s := NewSSEScanner(strings.NewReader(input))

	if !s.Next() {
		t.Fatal("expected first event")
	}
	if got := s.Event().Data; got != "first" {
		t.Errorf("data = %q, want %q", got, "first")
	}

	if s.Next() {
		t.Error("expected stop at [DONE]")
	}
}

func TestSSEScanner_Comments(t *testing.T) {
	input := ": this is a comment\ndata: actual\n\n"
	s := NewSSEScanner(strings.NewReader(input))

	if !s.Next() {
		t.Fatal("expected event")
	}
	if got := s.Event().Data; got != "actual" {
		t.Errorf("data = %q, want %q", got, "actual")
	}
}

func TestSSEScanner_MultiLineData(t *testing.T) {
	input := "data: line1\ndata: line2\n\n"
	s := NewSSEScanner(strings.NewReader(input))

	if !s.Next() {
		t.Fatal("expected event")
	}
	if got := s.Event().Data; got != "line1\nline2" {
		t.Errorf("data = %q, want %q", got, "line1\nline2")
	}
}

func TestSSEScanner_EmptyStream(t *testing.T) {
	s := NewSSEScanner(strings.NewReader(""))
	if s.Next() {
		t.Error("expected no events")
	}
}

func TestSSEScanner_NoTrailingNewline(t *testing.T) {
	input := "data: final"
	s := NewSSEScanner(strings.NewReader(input))

	if !s.Next() {
		t.Fatal("expected event at EOF without trailing blank line")
	}
	if got := s.Event().Data; got != "final" {
		t.Errorf("data = %q, want %q", got, "final")
	}
}

func TestSSEScanner_SpaceAfterDataColon(t *testing.T) {
	input := "data:nospace\n\ndata: onespace\n\n"
	s := NewSSEScanner(strings.NewReader(input))

	if !s.Next() {
		t.Fatal("expected first")
	}
	if got := s.Event().Data; got != "nospace" {
		t.Errorf("data = %q, want %q", got, "nospace")
	}

	if !s.Next() {
		t.Fatal("expected second")
	}
	if got := s.Event().Data; got != "onespace" {
		t.Errorf("data = %q, want %q", got, "onespace")
	}
}
