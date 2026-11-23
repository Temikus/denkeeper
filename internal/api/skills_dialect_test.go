package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/skill"
)

// The unknown-key refusal is the caller's file, not a server fault. No known
// input reaches the guard, so the mapping is tested on the wrapped error the
// handler sees.
func TestSkillRenderStatus_UnknownKeysIs422(t *testing.T) {
	refused := fmt.Errorf("building skill: the edit was not saved (%w)", skill.ErrUnknownKeysChanged)
	if got := skillRenderStatus(refused); got != http.StatusUnprocessableEntity {
		t.Errorf("unknown-key refusal status = %d, want 422", got)
	}
	if got := skillRenderStatus(errors.New("skill: rewriting frontmatter: encode failed")); got != http.StatusInternalServerError {
		t.Errorf("other render error status = %d, want 500", got)
	}
}

const apiYAMLSkill = "---\n" +
	"name: kelos\n" +
	"description: >-\n" +
	"  Folded description.\n" +
	"license: MIT\n" +
	"metadata:\n" +
	"  author: someone\n" +
	"---\n\n" +
	"Old body.\n"

// yamlSkillAPI loads a YAML skill from a real file into the default agent.
func yamlSkillAPI(t *testing.T) (*Server, string, skill.Skill) {
	t.Helper()
	deps := testDepsWithSkillsDir(t)
	e := deps.Dispatcher.Agent("default")
	path := filepath.Join(e.SkillsDir(), "kelos.md")
	if err := os.WriteFile(path, []byte(apiYAMLSkill), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := skill.ParseFile(path, []byte(apiYAMLSkill))
	if err != nil {
		t.Fatal(err)
	}
	e.AppendSkill(*s)
	return New(testConfig(allScopesKey()), deps, testLogger()), path, *s
}

func putYAMLSkill(t *testing.T, srv *Server, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/skills/default/kelos", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d; body: %s", rec.Code, rec.Body.String())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpdateSkill_YAMLBodyOnlyKeepsFrontmatterBytes(t *testing.T) {
	srv, path, before := yamlSkillAPI(t)
	putYAMLSkill(t, srv, `{"body":"New body."}`)

	want := "---\n" + before.Frontmatter + "\n---\n\nNew body.\n"
	if got := readFile(t, path); got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

func TestUpdateSkill_YAMLDescriptionKeepsUnknownKeys(t *testing.T) {
	srv, path, _ := yamlSkillAPI(t)
	putYAMLSkill(t, srv, `{"description":"New description"}`)

	got := readFile(t, path)
	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("dialect changed:\n%s", got)
	}
	for _, kept := range []string{"license: MIT", "metadata:", "author: someone", "description: New description"} {
		if !strings.Contains(got, kept) {
			t.Errorf("file lost %q:\n%s", kept, got)
		}
	}
}

// GET is unchanged by the dialect work: the same keys, nothing about
// frontmatter or dialect.
func TestGetSkill_YAMLReturnsUsualFields(t *testing.T) {
	srv, _, _ := yamlSkillAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/skills/default/kelos", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.Contains(strings.ToLower(k), "frontmatter") || strings.Contains(strings.ToLower(k), "dialect") {
			t.Errorf("GET carries %q: keys %v", k, keys)
		}
	}
	if string(got["name"]) != `"kelos"` || string(got["description"]) != `"Folded description."` {
		t.Errorf("name/description = %s/%s", got["name"], got["description"])
	}
}
