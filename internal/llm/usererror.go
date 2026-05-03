package llm

import (
	"context"
	"errors"
)

// HTTPStatusForError returns the HTTP status code that best represents the
// error to an API client. Provider-side failures (auth, credits, model not
// found, server errors) map to 502 Bad Gateway; rate limits pass through as
// 429; cancellations and timeouts get their own codes.
func HTTPStatusForError(err error) int {
	if errors.Is(err, context.Canceled) {
		return 499 // nginx-style "client closed request"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrStreamIdleTimeout) {
		return 504 // Gateway Timeout
	}

	var llmErr *LLMError
	if errors.As(err, &llmErr) {
		if llmErr.StatusCode == 429 {
			return 429
		}
		return 502
	}

	return 500
}

// UserFacingError translates an LLM pipeline error into a short, actionable
// message for end users. Unknown errors get a generic fallback.
func UserFacingError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "The request was cancelled."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The request to the LLM provider timed out. Please try again or consider using a faster model."
	}
	if errors.Is(err, ErrStreamIdleTimeout) {
		return "The LLM provider stopped responding. Please try again."
	}

	var llmErr *LLMError
	if errors.As(err, &llmErr) {
		switch {
		case llmErr.StatusCode == 401:
			return "The LLM provider rejected the API key. Please check your provider configuration."
		case llmErr.StatusCode == 402:
			return "The LLM provider ran out of credits or the request exceeds the available balance. Please top up your account or adjust model limits."
		case llmErr.StatusCode == 404:
			return "The configured model was not found by the provider. Please check the agent's model setting."
		case llmErr.StatusCode == 422:
			return "The LLM provider could not process the request. This usually indicates a configuration issue."
		case llmErr.StatusCode == 429:
			return "The LLM provider is rate-limiting requests. Please try again in a moment."
		case llmErr.StatusCode >= 500:
			return "The LLM provider is experiencing issues. Please try again later."
		}
	}

	return "Sorry, I encountered an error processing your message. Please try again."
}
