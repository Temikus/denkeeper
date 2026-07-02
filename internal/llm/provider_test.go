package llm

import "testing"

func TestTokenUsageAdd_AllFields(t *testing.T) {
	var total TokenUsage
	total.Add(TokenUsage{Prompt: 10, Completion: 5, CachedPrompt: 100, Total: 15})
	total.Add(TokenUsage{Prompt: 20, Completion: 8, CachedPrompt: 200, Total: 28})

	if total.Prompt != 30 {
		t.Errorf("Prompt = %d, want 30", total.Prompt)
	}
	if total.Completion != 13 {
		t.Errorf("Completion = %d, want 13", total.Completion)
	}
	if total.CachedPrompt != 300 {
		t.Errorf("CachedPrompt = %d, want 300", total.CachedPrompt)
	}
	if total.Total != 43 {
		t.Errorf("Total = %d, want 43", total.Total)
	}
}
