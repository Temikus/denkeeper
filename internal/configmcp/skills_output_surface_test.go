package configmcp_test

import (
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// skillMapServer is a config-MCP test server whose skill deps share one map,
// so create, get and update see each other's writes.
func skillMapServer(t *testing.T) (*mcp.ClientSession, func(string) skill.Skill) {
	t.Helper()
	var mu sync.RWMutex
	skills := map[string]skill.Skill{}
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		if err := os.MkdirAll(d.AgentSkillsDir, 0o755); err != nil {
			t.Fatalf("creating skills dir: %v", err)
		}
		d.AppendSkill = func(s skill.Skill) {
			mu.Lock()
			defer mu.Unlock()
			skills[s.Name] = s
		}
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			s, ok := skills[name]
			return s, ok
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := skills[name]; !ok {
				return false
			}
			delete(skills, name)
			skills[s.Name] = s
			return true
		}
	})
	get := func(name string) skill.Skill {
		mu.RLock()
		defer mu.RUnlock()
		return skills[name]
	}
	return session, get
}

func mustCall(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	text, isErr := callTool(t, session, tool, args)
	if isErr {
		t.Fatalf("%s %v: %s", tool, args, text)
	}
	return text
}

func TestSkillConfigMCP_MaxOutputTokensCreateGetUpdate(t *testing.T) {
	session, get := skillMapServer(t)

	mustCall(t, session, "skill_create", map[string]any{
		"name": "digest", "version": "1.0.0", "body": "Summarise.", "max_output_tokens": 2048,
	})
	if got := get("digest").MaxOutputTokens; got != 2048 {
		t.Fatalf("after create: MaxOutputTokens = %d, want 2048", got)
	}

	var detail struct {
		MaxOutputTokens int `json:"max_output_tokens"`
	}
	if err := json.Unmarshal([]byte(mustCall(t, session, "skill_get", map[string]any{"name": "digest"})), &detail); err != nil {
		t.Fatalf("decoding skill_get: %v", err)
	}
	if detail.MaxOutputTokens != 2048 {
		t.Errorf("skill_get max_output_tokens = %d, want 2048", detail.MaxOutputTokens)
	}

	mustCall(t, session, "skill_update", map[string]any{"name": "digest", "version": "1.0.1", "body": "Summarise briefly."})
	if got := get("digest").MaxOutputTokens; got != 2048 {
		t.Errorf("after body-only update: MaxOutputTokens = %d, want 2048 kept", got)
	}

	mustCall(t, session, "skill_update", map[string]any{"name": "digest", "max_output_tokens": 512})
	if got := get("digest").MaxOutputTokens; got != 512 {
		t.Errorf("after set: MaxOutputTokens = %d, want 512", got)
	}

	mustCall(t, session, "skill_update", map[string]any{"name": "digest", "max_output_tokens": 0})
	if got := get("digest").MaxOutputTokens; got != 0 {
		t.Errorf("after clear: MaxOutputTokens = %d, want 0", got)
	}
	var cleared map[string]any
	if err := json.Unmarshal([]byte(mustCall(t, session, "skill_get", map[string]any{"name": "digest"})), &cleared); err != nil {
		t.Fatalf("decoding skill_get after clear: %v", err)
	}
	if v, ok := cleared["max_output_tokens"]; ok {
		t.Errorf("skill_get after clear carries max_output_tokens = %v, want the key absent", v)
	}
}
