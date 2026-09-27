//go:build integration

package openrouter

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
)

// TestDecide_Live calls the real System One endpoint. The decisions API is
// alpha, so this is the drift check the httptest fixtures cannot be.
// Run: OPENROUTER_API_KEY=... go test -tags integration ./internal/llm/openrouter/ -run Live
func TestDecide_Live(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := New(key).Decide(ctx, llm.DecisionRequest{
		Model: "typesafe/jev-1.13",
		State: map[string]any{"message": "I was charged twice for my subscription."},
		Questions: map[string]llm.Question{
			"refund": {Type: llm.QuestionNoul, Instructions: "Is `message` asking for money back?"},
			"team": {Type: llm.QuestionChoice, Instructions: "Which team should handle `message`?", Choices: map[string]string{
				"billing": "Charges and refunds", "technical": "Bugs and outages",
			}},
			"urgency": {Type: llm.QuestionScore, Instructions: "How urgent is `message`?", Levels: []string{"low", "medium", "high"}},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	t.Logf("response: %+v", resp)

	if a := resp.Answers["refund"]; a.Noul <= 0 || a.Noul > 1 {
		t.Errorf("refund noul = %v, want (0,1]", a.Noul)
	}
	if a := resp.Answers["team"]; a.Choice != "billing" || len(a.Probabilities) != 2 {
		t.Errorf("team = %+v, want billing with 2 probabilities", a)
	}
	if a := resp.Answers["urgency"]; len(a.Probabilities) != 3 {
		t.Errorf("urgency = %+v, want 3 level probabilities", a)
	}
	if resp.Usage.Prompt == 0 {
		t.Errorf("usage not decoded: %+v", resp.Usage)
	}
}
