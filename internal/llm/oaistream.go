package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// OAIStreamResult holds the accumulated data from an OpenAI-compatible
// streaming response. Providers call ReadOAIStream to parse the SSE body
// and then convert this into an llm.ChatResponse.
type OAIStreamResult struct {
	Content      string
	ToolCalls    []ToolCall
	Model        string
	FinishReason string
	Usage        *OAIStreamUsage
	// ReasoningContent captures reasoning_content deltas (OpenRouter).
	ReasoningContent string
}

// OAIStreamUsage mirrors the usage block in the final SSE chunk.
type OAIStreamUsage struct {
	PromptTokens        int                   `json:"prompt_tokens"`
	CompletionTokens    int                   `json:"completion_tokens"`
	TotalTokens         int                   `json:"total_tokens"`
	PromptTokensDetails *OAIPromptTokenDetail `json:"prompt_tokens_details,omitempty"`
	Cost                float64               `json:"cost"` // OpenRouter reports cost here
}

// OAIPromptTokenDetail holds cached token info from the usage block.
type OAIPromptTokenDetail struct {
	CachedTokens int `json:"cached_tokens"`
}

// oaiStreamAccumulator tracks state while reading an OpenAI SSE stream.
type oaiStreamAccumulator struct {
	result       OAIStreamResult
	contentBuf   strings.Builder
	reasoningBuf strings.Builder
	toolAccum    map[int]*oaiToolAccum
	onStream     StreamCallback
}

type oaiToolAccum struct {
	ID      string
	Type    string
	Name    string
	ArgsBuf strings.Builder
}

// processChunk handles a single parsed SSE chunk.
func (a *oaiStreamAccumulator) processChunk(chunk *oaiStreamChunk) {
	if chunk.Model != "" {
		a.result.Model = chunk.Model
	}
	if len(chunk.Choices) > 0 {
		a.processChoice(&chunk.Choices[0])
	}
	if chunk.Usage != nil {
		a.result.Usage = chunk.Usage
	}
}

// processChoice handles the first choice in a streaming chunk.
func (a *oaiStreamAccumulator) processChoice(choice *oaiStreamChoice) {
	delta := &choice.Delta

	if delta.Content != "" {
		a.contentBuf.WriteString(delta.Content)
		if a.onStream != nil {
			a.onStream(StreamChunk{ContentDelta: delta.Content})
		}
	}

	// OpenRouter sends reasoning in either `reasoning` or `reasoning_content`.
	reasoning := delta.ReasoningContent
	if reasoning == "" {
		reasoning = delta.Reasoning
	}
	if reasoning != "" {
		a.reasoningBuf.WriteString(reasoning)
		if a.onStream != nil {
			a.onStream(StreamChunk{ThinkingDelta: reasoning})
		}
	}

	for _, tc := range delta.ToolCalls {
		acc, ok := a.toolAccum[tc.Index]
		if !ok {
			acc = &oaiToolAccum{}
			a.toolAccum[tc.Index] = acc
		}
		if tc.ID != "" {
			acc.ID = tc.ID
		}
		if tc.Type != "" {
			acc.Type = tc.Type
		}
		if tc.Function.Name != "" {
			acc.Name = tc.Function.Name
		}
		acc.ArgsBuf.WriteString(tc.Function.Arguments)
	}

	if choice.FinishReason != nil {
		a.result.FinishReason = *choice.FinishReason
	}
}

// finish builds the final OAIStreamResult from accumulated state.
func (a *oaiStreamAccumulator) finish() *OAIStreamResult {
	a.result.Content = a.contentBuf.String()
	a.result.ReasoningContent = a.reasoningBuf.String()

	if len(a.toolAccum) > 0 {
		a.result.ToolCalls = make([]ToolCall, 0, len(a.toolAccum))
		for i := 0; i < len(a.toolAccum); i++ {
			acc, ok := a.toolAccum[i]
			if !ok {
				continue
			}
			a.result.ToolCalls = append(a.result.ToolCalls, ToolCall{
				ID:   acc.ID,
				Type: acc.Type,
				Function: FunctionCall{
					Name:      acc.Name,
					Arguments: acc.ArgsBuf.String(),
				},
			})
		}
	}

	return &a.result
}

// ReadOAIStream reads an OpenAI-compatible SSE stream body and calls onStream
// for each content/reasoning delta. It accumulates tool calls and usage and
// returns the full result. onStream may be nil.
//
// If idle is non-nil, the body is wrapped with an IdleTimeoutReader that
// cancels the stream if no data arrives within idle.Timeout. When the idle
// timeout fires, ErrStreamIdleTimeout is returned directly so callers can
// match it with errors.Is.
func ReadOAIStream(body io.Reader, onStream StreamCallback, idle *StreamIdleConfig) (*OAIStreamResult, error) {
	if idle != nil && idle.Timeout > 0 {
		itr := NewIdleTimeoutReader(body, idle.Timeout, idle.Cancel)
		defer itr.Stop()
		body = itr
	}

	scanner := NewSSEScanner(body)
	acc := &oaiStreamAccumulator{
		toolAccum: make(map[int]*oaiToolAccum),
		onStream:  onStream,
	}

	for scanner.Next() {
		evt := scanner.Event()

		// Some OpenAI-compatible servers (e.g. LM Studio) send errors as
		// SSE events with "event: error" and HTTP 200. Detect and surface them.
		// Returned as LLMError with status 400 so the router treats it as
		// non-retryable (context-length, invalid model, etc.).
		if evt.Type == "error" {
			msg := extractSSEErrorMessage(evt.Data)
			return nil, &LLMError{StatusCode: 400, Message: msg}
		}

		var chunk oaiStreamChunk
		if err := json.Unmarshal([]byte(evt.Data), &chunk); err != nil {
			continue // skip malformed chunks
		}
		acc.processChunk(&chunk)
	}

	if err := scanner.Err(); err != nil {
		// Surface idle timeout as the root cause instead of a generic
		// "context canceled" read error.
		if idle != nil && errors.Is(context.Cause(idle.Ctx), ErrStreamIdleTimeout) {
			return nil, ErrStreamIdleTimeout
		}
		return nil, fmt.Errorf("reading SSE stream: %w", err)
	}

	return acc.finish(), nil
}

// OpenAI streaming wire types.

type oaiStreamChunk struct {
	ID      string            `json:"id"`
	Model   string            `json:"model"`
	Choices []oaiStreamChoice `json:"choices"`
	Usage   *OAIStreamUsage   `json:"usage,omitempty"`
}

type oaiStreamChoice struct {
	Delta        oaiStreamDelta `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type oaiStreamDelta struct {
	Content          string              `json:"content,omitempty"`
	Reasoning        string              `json:"reasoning,omitempty"`         // OpenRouter reasoning field
	ReasoningContent string              `json:"reasoning_content,omitempty"` // alias used by some models
	ToolCalls        []oaiStreamToolCall `json:"tool_calls,omitempty"`
}

type oaiStreamToolCall struct {
	Index    int           `json:"index"`
	ID       string        `json:"id,omitempty"`
	Type     string        `json:"type,omitempty"`
	Function oaiStreamFunc `json:"function,omitempty"`
}

type oaiStreamFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// extractSSEErrorMessage tries to pull a human-readable message from an SSE
// error event payload. It handles both {"error":{"message":"..."}} (OpenAI)
// and {"message":"..."} / {"error":"..."} (LM Studio) shapes.
func extractSSEErrorMessage(data string) string {
	// Try {"error":{"message":"..."}} (OpenAI standard).
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &nested); err == nil {
		if nested.Error.Message != "" {
			return nested.Error.Message
		}
		if nested.Message != "" {
			return nested.Message
		}
	}

	// Try {"error":"..."} (flat string).
	var flat struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &flat); err == nil && flat.Error != "" {
		return flat.Error
	}

	// Fall back to raw data.
	return data
}
