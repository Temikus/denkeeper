package api

import (
	"strings"
	"testing"
)

func TestParseClientFrame_ChatRequest(t *testing.T) {
	data := []byte(`{"type":"chat_request","session_id":"s1","agent":"default","message":"hello","seq":1}`)
	frame, err := ParseClientFrame(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := frame.(ChatRequestFrame)
	if !ok {
		t.Fatalf("expected ChatRequestFrame, got %T", frame)
	}
	if f.SessionID != "s1" {
		t.Errorf("session_id = %q, want %q", f.SessionID, "s1")
	}
	if f.Message != "hello" {
		t.Errorf("message = %q, want %q", f.Message, "hello")
	}
	if f.Agent != "default" {
		t.Errorf("agent = %q, want %q", f.Agent, "default")
	}
	if f.Seq != 1 {
		t.Errorf("seq = %d, want 1", f.Seq)
	}
}

func TestParseClientFrame_ChatRequestResume(t *testing.T) {
	data := []byte(`{"type":"chat_request","session_id":"s1","resume_after_seq":42}`)
	frame, err := ParseClientFrame(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := frame.(ChatRequestFrame)
	if !ok {
		t.Fatalf("expected ChatRequestFrame, got %T", frame)
	}
	if f.ResumeAfterSeq != 42 {
		t.Errorf("resume_after_seq = %d, want 42", f.ResumeAfterSeq)
	}
}

func TestParseClientFrame_ChatRequestMissingMessage(t *testing.T) {
	data := []byte(`{"type":"chat_request","session_id":"s1"}`)
	_, err := ParseClientFrame(data)
	if err == nil {
		t.Fatal("expected error for missing message and resume_after_seq")
	}
}

func TestParseClientFrame_ApprovalResponse(t *testing.T) {
	data := []byte(`{"type":"approval_response","approval_id":"apr_123","action":"approve"}`)
	frame, err := ParseClientFrame(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := frame.(ApprovalResponseFrame)
	if !ok {
		t.Fatalf("expected ApprovalResponseFrame, got %T", frame)
	}
	if f.ApprovalID != "apr_123" {
		t.Errorf("approval_id = %q, want %q", f.ApprovalID, "apr_123")
	}
	if f.Action != "approve" {
		t.Errorf("action = %q, want %q", f.Action, "approve")
	}
}

func TestParseClientFrame_ApprovalResponseMissingID(t *testing.T) {
	data := []byte(`{"type":"approval_response","action":"deny"}`)
	_, err := ParseClientFrame(data)
	if err == nil {
		t.Fatal("expected error for missing approval_id")
	}
}

func TestParseClientFrame_ApprovalResponseMissingAction(t *testing.T) {
	data := []byte(`{"type":"approval_response","approval_id":"apr_123"}`)
	_, err := ParseClientFrame(data)
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}

func TestParseClientFrame_Cancel(t *testing.T) {
	data := []byte(`{"type":"cancel","session_id":"s1"}`)
	frame, err := ParseClientFrame(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := frame.(CancelFrame)
	if !ok {
		t.Fatalf("expected CancelFrame, got %T", frame)
	}
	if f.SessionID != "s1" {
		t.Errorf("session_id = %q, want %q", f.SessionID, "s1")
	}
}

func TestParseClientFrame_CancelMissingSessionID(t *testing.T) {
	data := []byte(`{"type":"cancel"}`)
	_, err := ParseClientFrame(data)
	if err == nil {
		t.Fatal("expected error for missing session_id")
	}
}

func TestParseClientFrame_Pong(t *testing.T) {
	data := []byte(`{"type":"pong"}`)
	frame, err := ParseClientFrame(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok := frame.(PongFrame)
	if !ok {
		t.Fatalf("expected PongFrame, got %T", frame)
	}
}

func TestParseClientFrame_UnknownType(t *testing.T) {
	data := []byte(`{"type":"foobar"}`)
	_, err := ParseClientFrame(data)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unknown frame type") {
		t.Errorf("error = %q, want to contain 'unknown frame type'", err.Error())
	}
}

func TestParseClientFrame_MissingType(t *testing.T) {
	data := []byte(`{"message":"hello"}`)
	_, err := ParseClientFrame(data)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestParseClientFrame_InvalidJSON(t *testing.T) {
	data := []byte(`not json`)
	_, err := ParseClientFrame(data)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseClientFrame_Panic(t *testing.T) {
	data := []byte(`{"type":"panic"}`)
	frame, err := ParseClientFrame(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok := frame.(PanicFrame)
	if !ok {
		t.Fatalf("expected PanicFrame, got %T", frame)
	}
}
