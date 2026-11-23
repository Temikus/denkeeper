package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/skill"
)

const mcpYAMLSkill = "---\n" +
	"name: kelos\n" +
	"description: Looks after things.\n" +
	"license: MIT\n" +
	"metadata:\n" +
	"  author: someone\n" +
	"---\n\n" +
	"Old body.\n"

// yamlSkillMCP loads a YAML skill from a real file into test-agent.
func yamlSkillMCP(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, e := skillServer(t, dir)
	path := filepath.Join(dir, "kelos.md")
	if err := os.WriteFile(path, []byte(mcpYAMLSkill), 0o600); err != nil {
		t.Fatal(err)
	}
	sk, err := skill.ParseFile(path, []byte(mcpYAMLSkill))
	if err != nil {
		t.Fatal(err)
	}
	e.AppendSkill(*sk)
	return s, path
}

func TestSkillUpdate_YAMLKeepsDialectAndUnknownKeys(t *testing.T) {
	s, path := yamlSkillMCP(t)
	ver := "2.0"
	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{Agent: "test-agent", Name: "kelos", Version: &ver})
	if err != nil || res.IsError {
		t.Fatalf("skill_update: %v %s", err, toolResultText(res))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("dialect changed:\n%s", got)
	}
	for _, kept := range []string{"license: MIT", "author: someone", "version: \"2.0\""} {
		if !strings.Contains(got, kept) {
			t.Errorf("file lost %q:\n%s", kept, got)
		}
	}
}

func TestSkillGet_NoFrontmatterOrDialectKey(t *testing.T) {
	s, _ := yamlSkillMCP(t)
	ctx := withScopes(context.Background(), []string{"skills:read"})
	res, _, err := s.handleSkillGet(ctx, nil, skillGetInput{Agent: "test-agent", Name: "kelos"})
	if err != nil || res.IsError {
		t.Fatalf("skill_get: %v %s", err, toolResultText(res))
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(toolResultText(res)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for k := range got {
		if l := strings.ToLower(k); strings.Contains(l, "frontmatter") || strings.Contains(l, "dialect") {
			t.Errorf("skill_get carries %q: %s", k, toolResultText(res))
		}
	}
}
