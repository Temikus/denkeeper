package llm

import (
	"regexp"
	"strings"
)

// Some OpenRouter upstreams render Kimi's native call format into the text
// channel as `functions.kv_get:0{"key": "..."}`. The brace is required so a
// prose mention of a function name does not match.
var leakedCallPattern = regexp.MustCompile(`(?m)functions\.[A-Za-z0-9_-]+:\d+\s*\{`)

// Kimi's native tool-call delimiters; as plain text they mean the upstream did
// not translate the call into tool_calls.
var leakedCallMarkers = []string{
	"<|tool_call_begin|>",
	"<|tool_calls_section_begin|>",
}

// LeakedToolCallText reports whether content carries a tool call rendered as
// plain text instead of a structured tool_calls entry.
func LeakedToolCallText(content string) bool {
	if leakedCallPattern.MatchString(content) {
		return true
	}
	for _, m := range leakedCallMarkers {
		if strings.Contains(content, m) {
			return true
		}
	}
	return false
}

// LooksLikeLeakedToolCall reports whether resp is a "final" answer that is
// really an unparsed tool call: an upstream fault worth one retry, not model
// output.
func LooksLikeLeakedToolCall(resp *ChatResponse) bool {
	if resp == nil || resp.FinishReason != "stop" || len(resp.ToolCalls) > 0 {
		return false
	}
	return LeakedToolCallText(resp.Content)
}
