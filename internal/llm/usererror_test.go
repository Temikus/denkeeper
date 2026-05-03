package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestUserFacingError_LLMErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		substr string
	}{
		{"402 credits", fmt.Errorf("LLM completion: %w", &LLMError{StatusCode: 402, Message: "insufficient credits"}), "ran out of credits"},
		{"401 auth", &LLMError{StatusCode: 401, Message: "invalid key"}, "rejected the API key"},
		{"404 model", &LLMError{StatusCode: 404, Message: "model not found"}, "model was not found"},
		{"422 unprocessable", &LLMError{StatusCode: 422, Message: "bad params"}, "configuration issue"},
		{"429 rate limit", &LLMError{StatusCode: 429, Message: "slow down"}, "rate-limiting"},
		{"500 generic LLM", &LLMError{StatusCode: 500, Message: "internal"}, "Sorry, I encountered an error"},
		{"non-LLM error", fmt.Errorf("network timeout"), "Sorry, I encountered an error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UserFacingError(tt.err)
			if !strings.Contains(got, tt.substr) {
				t.Errorf("UserFacingError(%v) = %q, want substring %q", tt.err, got, tt.substr)
			}
		})
	}
}

func TestUserFacingError_Timeout(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", context.DeadlineExceeded)
	got := UserFacingError(err)
	if !strings.Contains(got, "timed out") {
		t.Errorf("UserFacingError(DeadlineExceeded) = %q, want substring %q", got, "timed out")
	}
}
