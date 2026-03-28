package main

import "testing"

func TestParseChannel_ValidInput(t *testing.T) {
	adapter, id, ok := parseChannel("telegram:123456789")
	if !ok {
		t.Fatal("expected ok=true for valid channel")
	}
	if adapter != "telegram" {
		t.Errorf("adapter = %q, want telegram", adapter)
	}
	if id != "123456789" {
		t.Errorf("externalID = %q, want 123456789", id)
	}
}

func TestParseChannel_EmptyString(t *testing.T) {
	_, _, ok := parseChannel("")
	if ok {
		t.Error("expected ok=false for empty string")
	}
}

func TestParseChannel_NoColon(t *testing.T) {
	_, _, ok := parseChannel("telegram")
	if ok {
		t.Error("expected ok=false for string without colon")
	}
}

func TestParseChannel_ColonAtStart(t *testing.T) {
	_, _, ok := parseChannel(":12345")
	if ok {
		t.Error("expected ok=false when colon is at start (empty adapter)")
	}
}

func TestParseChannel_ColonAtEnd(t *testing.T) {
	_, _, ok := parseChannel("telegram:")
	if ok {
		t.Error("expected ok=false when colon is at end (empty ID)")
	}
}

func TestParseChannel_OnlyColon(t *testing.T) {
	_, _, ok := parseChannel(":")
	if ok {
		t.Error("expected ok=false for bare colon")
	}
}
