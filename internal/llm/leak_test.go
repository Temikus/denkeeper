package llm

import "testing"

func TestLeakedToolCallText_MatchesAuditShapes(t *testing.T) {
	cases := map[string]string{
		"single call after prose": "Let me start with the idempotency guard.functions.kv_get:0{\"key\": \"log:heartbeat:2026-09-05\"}",
		"two calls concatenated":  "functions.kv_get:0{\"key\": \"a\"}functions.kv_get:1{\"key\": \"b\"}",
		"hyphenated tool name":    "   functions.find-tasks-by-date:8{\"startDate\": \"today\"}",
		"space before brace":      "functions.kv_set:6 {\"key\": \"x\"}",
		"kimi native delimiter":   "thinking...<|tool_call_begin|>functions.kv_get<|tool_call_argument_begin|>{}",
		"kimi section delimiter":  "<|tool_calls_section_begin|>",
	}
	for name, content := range cases {
		if !LeakedToolCallText(content) {
			t.Errorf("%s: expected leak detection for %q", name, content)
		}
	}
}

func TestLeakedToolCallText_IgnoresProse(t *testing.T) {
	cases := []string{
		"",
		"Call functions.kv_get with the key you need.",
		"The tool `functions.kv_get:0` was invoked earlier.",
		"Here is a JSON object: {\"key\": \"value\"}",
		"Top stories:\n1. [Title](https://example.com)",
	}
	for _, content := range cases {
		if LeakedToolCallText(content) {
			t.Errorf("false positive on %q", content)
		}
	}
}

func TestLooksLikeLeakedToolCall_RequiresStopWithoutCalls(t *testing.T) {
	leaked := "functions.kv_get:0{\"key\": \"a\"}"
	if !LooksLikeLeakedToolCall(&ChatResponse{Content: leaked, FinishReason: "stop"}) {
		t.Error("stop + no tool calls + leaked text should be detected")
	}
	if LooksLikeLeakedToolCall(&ChatResponse{Content: leaked, FinishReason: "tool_calls", ToolCalls: []ToolCall{{ID: "1"}}}) {
		t.Error("a structured tool_calls response is not a leak even if the text echoes one")
	}
	if LooksLikeLeakedToolCall(&ChatResponse{Content: leaked, FinishReason: "length"}) {
		t.Error("a truncated response is a different fault, not a leak")
	}
	if LooksLikeLeakedToolCall(nil) {
		t.Error("nil response must not be a leak")
	}
}
