package pricing

// loadDefaults populates the registry with bundled pricing for common models.
// Prices are per million tokens (input / output / cached-input).
// Sources: Anthropic, OpenAI, Google, and Meta published pricing pages.
func (r *Registry) loadDefaults() {
	// --- Anthropic (cached = 10% of input) ---
	r.RegisterPrefix("claude-opus-4", ModelPrice{15.0, 75.0, 1.50})
	r.RegisterPrefix("claude-sonnet-4", ModelPrice{3.0, 15.0, 0.30})
	r.RegisterPrefix("claude-3-7-sonnet", ModelPrice{3.0, 15.0, 0.30})
	r.RegisterPrefix("claude-3-5-sonnet", ModelPrice{3.0, 15.0, 0.30})
	r.RegisterPrefix("claude-3-5-haiku", ModelPrice{0.80, 4.0, 0.08})
	r.RegisterPrefix("claude-3-opus", ModelPrice{15.0, 75.0, 1.50})
	r.RegisterPrefix("claude-3-sonnet", ModelPrice{3.0, 15.0, 0.30})
	r.RegisterPrefix("claude-3-haiku", ModelPrice{0.25, 1.25, 0.03})

	// OpenRouter wraps model names with provider prefix; register those too.
	r.RegisterPrefix("anthropic/claude-opus-4", ModelPrice{15.0, 75.0, 1.50})
	r.RegisterPrefix("anthropic/claude-sonnet-4", ModelPrice{3.0, 15.0, 0.30})
	r.RegisterPrefix("anthropic/claude-3-7-sonnet", ModelPrice{3.0, 15.0, 0.30})
	r.RegisterPrefix("anthropic/claude-3-5-sonnet", ModelPrice{3.0, 15.0, 0.30})
	r.RegisterPrefix("anthropic/claude-3-5-haiku", ModelPrice{0.80, 4.0, 0.08})
	r.RegisterPrefix("anthropic/claude-3-opus", ModelPrice{15.0, 75.0, 1.50})
	r.RegisterPrefix("anthropic/claude-3-haiku", ModelPrice{0.25, 1.25, 0.03})

	// --- OpenAI (cached = 50% of input) ---
	r.RegisterPrefix("gpt-4.1-nano", ModelPrice{0.10, 0.40, 0.025})
	r.RegisterPrefix("gpt-4.1-mini", ModelPrice{0.40, 1.60, 0.10})
	r.RegisterPrefix("gpt-4.1", ModelPrice{2.0, 8.0, 0.50})
	r.RegisterPrefix("gpt-4o-mini", ModelPrice{0.15, 0.60, 0.075})
	r.RegisterPrefix("gpt-4o", ModelPrice{2.50, 10.0, 1.25})
	r.RegisterPrefix("o4-mini", ModelPrice{1.10, 4.40, 0.275})
	r.RegisterPrefix("o3-mini", ModelPrice{1.10, 4.40, 0.275})
	r.RegisterPrefix("o3", ModelPrice{2.0, 8.0, 0.50})
	r.RegisterPrefix("o1-mini", ModelPrice{1.10, 4.40, 0.275})
	r.RegisterPrefix("o1", ModelPrice{15.0, 60.0, 7.50})

	// OpenRouter-prefixed OpenAI models.
	r.RegisterPrefix("openai/gpt-4.1-nano", ModelPrice{0.10, 0.40, 0.025})
	r.RegisterPrefix("openai/gpt-4.1-mini", ModelPrice{0.40, 1.60, 0.10})
	r.RegisterPrefix("openai/gpt-4.1", ModelPrice{2.0, 8.0, 0.50})
	r.RegisterPrefix("openai/gpt-4o-mini", ModelPrice{0.15, 0.60, 0.075})
	r.RegisterPrefix("openai/gpt-4o", ModelPrice{2.50, 10.0, 1.25})
	r.RegisterPrefix("openai/o4-mini", ModelPrice{1.10, 4.40, 0.275})
	r.RegisterPrefix("openai/o3-mini", ModelPrice{1.10, 4.40, 0.275})
	r.RegisterPrefix("openai/o3", ModelPrice{2.0, 8.0, 0.50})
	r.RegisterPrefix("openai/o1-mini", ModelPrice{1.10, 4.40, 0.275})
	r.RegisterPrefix("openai/o1", ModelPrice{15.0, 60.0, 7.50})

	// --- Google Gemini ---
	r.RegisterPrefix("gemini-2.5-pro", ModelPrice{1.25, 10.0, 0.3125})
	r.RegisterPrefix("gemini-2.5-flash", ModelPrice{0.15, 0.60, 0.0375})
	r.RegisterPrefix("gemini-2.0-flash", ModelPrice{0.10, 0.40, 0.025})
	r.RegisterPrefix("gemini-1.5-pro", ModelPrice{1.25, 5.0, 0.3125})
	r.RegisterPrefix("gemini-1.5-flash", ModelPrice{0.075, 0.30, 0.01875})

	r.RegisterPrefix("google/gemini-2.5-pro", ModelPrice{1.25, 10.0, 0.3125})
	r.RegisterPrefix("google/gemini-2.5-flash", ModelPrice{0.15, 0.60, 0.0375})
	r.RegisterPrefix("google/gemini-2.0-flash", ModelPrice{0.10, 0.40, 0.025})
	r.RegisterPrefix("google/gemini-1.5-pro", ModelPrice{1.25, 5.0, 0.3125})
	r.RegisterPrefix("google/gemini-1.5-flash", ModelPrice{0.075, 0.30, 0.01875})

	// --- Meta Llama (open models, typical hosted pricing) ---
	r.RegisterPrefix("meta-llama/llama-4", ModelPrice{0.20, 0.80, 0})
	r.RegisterPrefix("meta-llama/llama-3.3", ModelPrice{0.10, 0.30, 0})
	r.RegisterPrefix("meta-llama/llama-3.1-405b", ModelPrice{2.0, 6.0, 0})
	r.RegisterPrefix("meta-llama/llama-3.1-70b", ModelPrice{0.40, 0.40, 0})
	r.RegisterPrefix("meta-llama/llama-3.1-8b", ModelPrice{0.05, 0.08, 0})

	// --- Mistral ---
	r.RegisterPrefix("mistralai/mistral-large", ModelPrice{2.0, 6.0, 0})
	r.RegisterPrefix("mistralai/mistral-medium", ModelPrice{2.7, 8.1, 0})
	r.RegisterPrefix("mistralai/mistral-small", ModelPrice{0.20, 0.60, 0})
	r.RegisterPrefix("mistralai/mixtral-8x22b", ModelPrice{0.90, 0.90, 0})
	r.RegisterPrefix("mistralai/mixtral-8x7b", ModelPrice{0.24, 0.24, 0})

	// --- DeepSeek ---
	r.RegisterPrefix("deepseek/deepseek-r1", ModelPrice{0.55, 2.19, 0.14})
	r.RegisterPrefix("deepseek/deepseek-chat", ModelPrice{0.14, 0.28, 0.014})
}
