//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
)

func systemText(req llm.ChatRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role == "system" {
			b.WriteString(m.Content)
		}
	}
	return b.String()
}

// A question that mentions /briefing mid-sentence does not trigger the skill,
// but the prompt still tells the model the skill exists.
func TestChat_SkillIndex_ListsUnmatchedCommandSkill(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{{
			Content:      "It compiles a briefing.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
		}},
		Agents: []agentSetup{{
			Name: "default", Tier: "autonomous", Adapters: []string{"api"},
			Skills: []skill.Skill{skilltest.New("briefing", "Compile and deliver a daily briefing",
				[]string{"command:briefing"}, "BRIEFING-BODY-MARKER")},
		}},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message":    "what does /briefing do?",
		"session_id": "skill-index",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	system := systemText(h.MockLLM.LastRequest())
	if !strings.Contains(system, "\n- briefing (/briefing): Compile and deliver a daily briefing") {
		t.Errorf("index line missing from the system prompt:\n%s", system)
	}
	if strings.Contains(system, "BRIEFING-BODY-MARKER") {
		t.Error("unmatched skill body reached the system prompt")
	}
}
