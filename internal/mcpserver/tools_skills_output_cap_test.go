package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
)

func outputCapOf(t *testing.T, e *agent.Engine) int {
	t.Helper()
	sk, ok := e.GetSkill("digest")
	if !ok {
		t.Fatal("skill digest not loaded")
	}
	return sk.MaxOutputTokens
}

func updateDigest(t *testing.T, s *Server, in skillUpdateInput) {
	t.Helper()
	in.Agent, in.Name = "test-agent", "digest"
	res, _, err := s.handleSkillUpdate(writeScope(t), nil, in)
	if err != nil || res.IsError {
		t.Fatalf("skill_update: err=%v result=%s", err, toolResultText(res))
	}
}

func TestSkillMCP_MaxOutputTokensCreateUpdate(t *testing.T) {
	s, e := skillServer(t, t.TempDir())

	res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: "digest", Version: "1.0.0", Body: "Summarise.", MaxOutputTokens: 2048,
	})
	if err != nil || res.IsError {
		t.Fatalf("skill_create: err=%v result=%s", err, toolResultText(res))
	}
	if got := outputCapOf(t, e); got != 2048 {
		t.Fatalf("after create: MaxOutputTokens = %d, want 2048", got)
	}

	body, version := "Summarise briefly.", "1.0.1"
	updateDigest(t, s, skillUpdateInput{Body: &body, Version: &version})
	if got := outputCapOf(t, e); got != 2048 {
		t.Errorf("after body-only update: MaxOutputTokens = %d, want 2048 kept", got)
	}

	updateDigest(t, s, skillUpdateInput{MaxOutputTokens: intPtr(512)})
	if got := outputCapOf(t, e); got != 512 {
		t.Errorf("after set: MaxOutputTokens = %d, want 512", got)
	}

	updateDigest(t, s, skillUpdateInput{MaxOutputTokens: intPtr(0)})
	if got := outputCapOf(t, e); got != 0 {
		t.Errorf("after clear: MaxOutputTokens = %d, want 0", got)
	}
}

func TestSkillMCP_GetReportsMaxOutputTokens(t *testing.T) {
	s, _ := skillServer(t, t.TempDir())
	if res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: "digest", Version: "1.0.0", Body: "Summarise.", MaxOutputTokens: 2048,
	}); err != nil || res.IsError {
		t.Fatalf("skill_create: err=%v result=%s", err, toolResultText(res))
	}

	res, _, err := s.handleSkillGet(withScopes(context.Background(), []string{"skills:read"}), nil,
		skillGetInput{Agent: "test-agent", Name: "digest"})
	if err != nil || res.IsError {
		t.Fatalf("skill_get: err=%v result=%s", err, toolResultText(res))
	}
	var got struct{ MaxOutputTokens int }
	if err := json.Unmarshal([]byte(toolResultText(res)), &got); err != nil {
		t.Fatalf("decoding skill_get: %v (%s)", err, toolResultText(res))
	}
	if got.MaxOutputTokens != 2048 {
		t.Errorf("skill_get MaxOutputTokens = %d, want 2048", got.MaxOutputTokens)
	}
}
