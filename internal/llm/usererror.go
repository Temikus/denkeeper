package llm

import (
	"context"
	"errors"
)

// UserFacingError translates an LLM pipeline error into a short, actionable
// message for end users. Unknown errors get a generic fallback.
func UserFacingError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "The request to the LLM provider timed out. Please try again or consider using a faster model."
	}

	var llmErr *LLMError
	if errors.As(err, &llmErr) {
		switch llmErr.StatusCode {
		case 401:
			return "The LLM provider rejected the API key. Please check your provider configuration."
		case 402:
			return "The LLM provider ran out of credits or the request exceeds the available balance. Please top up your account or adjust model limits."
		case 404:
			return "The configured model was not found by the provider. Please check the agent's model setting."
		case 422:
			return "The LLM provider could not process the request. This usually indicates a configuration issue."
		case 429:
			return "The LLM provider is rate-limiting requests. Please try again in a moment."
		}
	}

	return "Sorry, I encountered an error processing your message. Please try again."
}
