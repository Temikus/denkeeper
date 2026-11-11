package pricing

// price builds a single-rate ModelPrice from $/M input, output, cached input.
func price(input, output, cached float64) ModelPrice {
	return ModelPrice{InputPerMTok: input, OutputPerMTok: output, CachedInputPerMTok: cached}
}

// loadDefaults populates the registry with bundled pricing for common models.
// Prices are per million tokens (input / output / cached-input).
// Sources: Anthropic, OpenAI, Google, and Meta published pricing pages.
func (r *Registry) loadDefaults() {
	// --- Anthropic (cached = 10% of input unless noted) ---
	r.RegisterPrefix("claude-fable-5-1", price(10.0, 50.0, 0.25))
	r.RegisterPrefix("claude-fable-5", price(10.0, 50.0, 1.0))
	r.RegisterPrefix("claude-opus-5-5", price(4.0, 20.0, 0.20))
	r.RegisterPrefix("claude-opus-5", price(5.0, 25.0, 0.50))
	r.RegisterPrefix("claude-sonnet-5-5", price(2.0, 10.0, 0.10)) // reads at 5% of input
	r.RegisterPrefix("claude-sonnet-5", price(2.0, 10.0, 0.20))
	haiku5 := price(0.10, 0.50, 0.01)
	haiku5.LongPrompt = PromptTier{AboveTokens: 100_000, InputPerMTok: 0.50, OutputPerMTok: 2.50, CachedInputPerMTok: 0.05}
	r.RegisterPrefix("claude-haiku-5", haiku5)
	r.RegisterPrefix("claude-opus-4-8", price(5.0, 25.0, 0.50))
	r.RegisterPrefix("claude-opus-4-7", price(5.0, 25.0, 0.50))
	r.RegisterPrefix("claude-opus-4-6", price(5.0, 25.0, 0.50))
	r.RegisterPrefix("claude-opus-4-5", price(5.0, 25.0, 0.50))
	r.RegisterPrefix("claude-haiku-4-5", price(1.0, 5.0, 0.10))
	r.RegisterPrefix("claude-opus-4", price(15.0, 75.0, 1.50))
	r.RegisterPrefix("claude-sonnet-4", price(3.0, 15.0, 0.30))
	r.RegisterPrefix("claude-3-7-sonnet", price(3.0, 15.0, 0.30))
	r.RegisterPrefix("claude-3-5-sonnet", price(3.0, 15.0, 0.30))
	r.RegisterPrefix("claude-3-5-haiku", price(0.80, 4.0, 0.08))
	r.RegisterPrefix("claude-3-opus", price(15.0, 75.0, 1.50))
	r.RegisterPrefix("claude-3-sonnet", price(3.0, 15.0, 0.30))
	r.RegisterPrefix("claude-3-haiku", price(0.25, 1.25, 0.03))

	// OpenRouter wraps model names with provider prefix; register those too.
	r.RegisterPrefix("anthropic/claude-opus-4", price(15.0, 75.0, 1.50))
	r.RegisterPrefix("anthropic/claude-sonnet-4", price(3.0, 15.0, 0.30))
	r.RegisterPrefix("anthropic/claude-3-7-sonnet", price(3.0, 15.0, 0.30))
	r.RegisterPrefix("anthropic/claude-3-5-sonnet", price(3.0, 15.0, 0.30))
	r.RegisterPrefix("anthropic/claude-3-5-haiku", price(0.80, 4.0, 0.08))
	r.RegisterPrefix("anthropic/claude-3-opus", price(15.0, 75.0, 1.50))
	r.RegisterPrefix("anthropic/claude-3-haiku", price(0.25, 1.25, 0.03))

	// --- OpenAI (cached = 50% of input) ---
	r.RegisterPrefix("gpt-4.1-nano", price(0.10, 0.40, 0.025))
	r.RegisterPrefix("gpt-4.1-mini", price(0.40, 1.60, 0.10))
	r.RegisterPrefix("gpt-4.1", price(2.0, 8.0, 0.50))
	r.RegisterPrefix("gpt-4o-mini", price(0.15, 0.60, 0.075))
	r.RegisterPrefix("gpt-4o", price(2.50, 10.0, 1.25))
	r.RegisterPrefix("o4-mini", price(1.10, 4.40, 0.275))
	r.RegisterPrefix("o3-mini", price(1.10, 4.40, 0.275))
	r.RegisterPrefix("o3", price(2.0, 8.0, 0.50))
	r.RegisterPrefix("o1-mini", price(1.10, 4.40, 0.275))
	r.RegisterPrefix("o1", price(15.0, 60.0, 7.50))

	// OpenRouter-prefixed OpenAI models.
	r.RegisterPrefix("openai/gpt-4.1-nano", price(0.10, 0.40, 0.025))
	r.RegisterPrefix("openai/gpt-4.1-mini", price(0.40, 1.60, 0.10))
	r.RegisterPrefix("openai/gpt-4.1", price(2.0, 8.0, 0.50))
	r.RegisterPrefix("openai/gpt-4o-mini", price(0.15, 0.60, 0.075))
	r.RegisterPrefix("openai/gpt-4o", price(2.50, 10.0, 1.25))
	r.RegisterPrefix("openai/o4-mini", price(1.10, 4.40, 0.275))
	r.RegisterPrefix("openai/o3-mini", price(1.10, 4.40, 0.275))
	r.RegisterPrefix("openai/o3", price(2.0, 8.0, 0.50))
	r.RegisterPrefix("openai/o1-mini", price(1.10, 4.40, 0.275))
	r.RegisterPrefix("openai/o1", price(15.0, 60.0, 7.50))

	// --- Google Gemini ---
	r.RegisterPrefix("gemini-2.5-pro", price(1.25, 10.0, 0.3125))
	r.RegisterPrefix("gemini-2.5-flash", price(0.15, 0.60, 0.0375))
	r.RegisterPrefix("gemini-2.0-flash", price(0.10, 0.40, 0.025))
	r.RegisterPrefix("gemini-1.5-pro", price(1.25, 5.0, 0.3125))
	r.RegisterPrefix("gemini-1.5-flash", price(0.075, 0.30, 0.01875))

	r.RegisterPrefix("google/gemini-2.5-pro", price(1.25, 10.0, 0.3125))
	r.RegisterPrefix("google/gemini-2.5-flash", price(0.15, 0.60, 0.0375))
	r.RegisterPrefix("google/gemini-2.0-flash", price(0.10, 0.40, 0.025))
	r.RegisterPrefix("google/gemini-1.5-pro", price(1.25, 5.0, 0.3125))
	r.RegisterPrefix("google/gemini-1.5-flash", price(0.075, 0.30, 0.01875))

	// --- Meta Llama (open models, typical hosted pricing) ---
	r.RegisterPrefix("meta-llama/llama-4", price(0.20, 0.80, 0))
	r.RegisterPrefix("meta-llama/llama-3.3", price(0.10, 0.30, 0))
	r.RegisterPrefix("meta-llama/llama-3.1-405b", price(2.0, 6.0, 0))
	r.RegisterPrefix("meta-llama/llama-3.1-70b", price(0.40, 0.40, 0))
	r.RegisterPrefix("meta-llama/llama-3.1-8b", price(0.05, 0.08, 0))

	// --- Mistral ---
	r.RegisterPrefix("mistralai/mistral-large", price(2.0, 6.0, 0))
	r.RegisterPrefix("mistralai/mistral-medium", price(2.7, 8.1, 0))
	r.RegisterPrefix("mistralai/mistral-small", price(0.20, 0.60, 0))
	r.RegisterPrefix("mistralai/mixtral-8x22b", price(0.90, 0.90, 0))
	r.RegisterPrefix("mistralai/mixtral-8x7b", price(0.24, 0.24, 0))

	// --- DeepSeek ---
	r.RegisterPrefix("deepseek/deepseek-r1", price(0.55, 2.19, 0.14))
	r.RegisterPrefix("deepseek/deepseek-chat", price(0.14, 0.28, 0.014))
}
