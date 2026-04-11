package llm

import (
	"errors"
	"strings"
	"testing"
)

func sseBody(chunks ...string) string {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString("data: ")
		sb.WriteString(c)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

func TestReadOAIStream_ContentDelta(t *testing.T) {
	body := sseBody(
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
	)

	var chunks []string
	result, err := ReadOAIStream(strings.NewReader(body), func(c StreamChunk) {
		if c.ContentDelta != "" {
			chunks = append(chunks, c.ContentDelta)
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content != "Hello world" {
		t.Errorf("content = %q, want %q", result.Content, "Hello world")
	}
	if result.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", result.Model)
	}
	if result.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", result.FinishReason)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 7 {
		t.Errorf("usage not populated correctly: %+v", result.Usage)
	}
	if len(chunks) != 2 || chunks[0] != "Hello" || chunks[1] != " world" {
		t.Errorf("callback chunks = %v, want [Hello  world]", chunks)
	}
}

func TestReadOAIStream_NilCallback(t *testing.T) {
	body := sseBody(
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{"content":"Hi"},"finish_reason":null}]}`,
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	result, err := ReadOAIStream(strings.NewReader(body), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Hi" {
		t.Errorf("content = %q, want Hi", result.Content)
	}
}

func TestReadOAIStream_ReasoningDelta(t *testing.T) {
	body := sseBody(
		`{"id":"1","model":"deepseek","choices":[{"delta":{"reasoning_content":"thinking..."},"finish_reason":null}]}`,
		`{"id":"1","model":"deepseek","choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
	)

	var reasoning, content string
	result, err := ReadOAIStream(strings.NewReader(body), func(c StreamChunk) {
		reasoning += c.ThinkingDelta
		content += c.ContentDelta
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "answer" {
		t.Errorf("content = %q, want answer", result.Content)
	}
	if result.ReasoningContent != "thinking..." {
		t.Errorf("reasoning = %q, want thinking...", result.ReasoningContent)
	}
	if reasoning != "thinking..." {
		t.Errorf("callback reasoning = %q", reasoning)
	}
}

func TestReadOAIStream_ToolCallAccumulation(t *testing.T) {
	body := sseBody(
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc1","type":"function","function":{"name":"search","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]},"finish_reason":null}]}`,
		`{"id":"1","model":"gpt-4o","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"hello\"}"}}]},"finish_reason":"tool_calls"}]}`,
	)

	var contentCallbacks int
	result, err := ReadOAIStream(strings.NewReader(body), func(c StreamChunk) {
		if c.ContentDelta != "" {
			contentCallbacks++
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contentCallbacks != 0 {
		t.Errorf("tool call deltas should not trigger content callbacks, got %d", contentCallbacks)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "tc1" {
		t.Errorf("tool call ID = %q, want tc1", tc.ID)
	}
	if tc.Function.Name != "search" {
		t.Errorf("tool call name = %q, want search", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"q":"hello"}` {
		t.Errorf("tool call args = %q", tc.Function.Arguments)
	}
}

func TestReadOAIStream_SSEErrorEvent_NestedMessage(t *testing.T) {
	// LM Studio sends {"error":{"message":"..."},"message":"..."} with event: error.
	body := "event: error\ndata: {\"error\":{\"message\":\"context length exceeded\"},\"message\":\"context length exceeded\"}\n\n"
	_, err := ReadOAIStream(strings.NewReader(body), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "context length exceeded") {
		t.Errorf("error = %q, want it to contain 'context length exceeded'", err.Error())
	}
	var llmErr *LLMError
	if !errors.As(err, &llmErr) {
		t.Fatal("expected *LLMError")
	}
	if llmErr.StatusCode != 400 {
		t.Errorf("status = %d, want 400", llmErr.StatusCode)
	}
	if llmErr.Retryable() {
		t.Error("SSE error events should not be retryable")
	}
}

func TestReadOAIStream_SSEErrorEvent_FlatString(t *testing.T) {
	// Some servers send {"error":"plain string"}.
	body := "event: error\ndata: {\"error\":\"model not found\"}\n\n"
	_, err := ReadOAIStream(strings.NewReader(body), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Errorf("error = %q, want it to contain 'model not found'", err.Error())
	}
}

func TestReadOAIStream_SSEErrorEvent_Unparseable(t *testing.T) {
	// Fall back to raw data when JSON doesn't match known shapes.
	body := "event: error\ndata: something went wrong\n\n"
	_, err := ReadOAIStream(strings.NewReader(body), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error = %q, want it to contain 'something went wrong'", err.Error())
	}
}

func TestReadOAIStream_MalformedChunksSkipped(t *testing.T) {
	body := "data: not-json\n\ndata: {\"id\":\"1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	result, err := ReadOAIStream(strings.NewReader(body), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "hi" {
		t.Errorf("content = %q, want hi", result.Content)
	}
}
