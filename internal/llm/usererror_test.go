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
		{"500 server error", &LLMError{StatusCode: 500, Message: "internal"}, "experiencing issues"},
		{"502 server error", &LLMError{StatusCode: 502, Message: "bad gateway"}, "experiencing issues"},
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

func TestUserFacingError_Canceled(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", context.Canceled)
	got := UserFacingError(err)
	if !strings.Contains(got, "cancelled") {
		t.Errorf("UserFacingError(Canceled) = %q, want substring %q", got, "cancelled")
	}
}

func TestUserFacingError_StreamIdleTimeout(t *testing.T) {
	err := fmt.Errorf("LLM completion: %w", ErrStreamIdleTimeout)
	got := UserFacingError(err)
	if !strings.Contains(got, "stopped responding") {
		t.Errorf("UserFacingError(ErrStreamIdleTimeout) = %q, want substring %q", got, "stopped responding")
	}
}

func TestHTTPStatusForError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"canceled", context.Canceled, 499},
		{"deadline exceeded", context.DeadlineExceeded, 504},
		{"stream idle timeout", ErrStreamIdleTimeout, 504},
		{"429 rate limit", &LLMError{StatusCode: 429, Message: "slow down"}, 429},
		{"401 auth", &LLMError{StatusCode: 401, Message: "bad key"}, 502},
		{"402 credits", &LLMError{StatusCode: 402, Message: "no credits"}, 502},
		{"500 provider error", &LLMError{StatusCode: 500, Message: "internal"}, 502},
		{"wrapped LLM error", fmt.Errorf("chat: %w", &LLMError{StatusCode: 402, Message: "x"}), 502},
		{"generic error", fmt.Errorf("something broke"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HTTPStatusForError(tt.err)
			if got != tt.want {
				t.Errorf("HTTPStatusForError(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
