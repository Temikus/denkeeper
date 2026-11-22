package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// getSkillOutputCap reads the skill back through GET and returns its
// max_output_tokens, and whether the key was present at all.
func getSkillOutputCap(t *testing.T, srv *Server) (float64, bool) {
	t.Helper()
	rec := lintSkillRequest(t, srv, http.MethodGet, "/api/v1/skills/default/digest", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding skill: %v", err)
	}
	v, ok := resp["max_output_tokens"].(float64)
	return v, ok
}

func putSkill(t *testing.T, srv *Server, body string) {
	t.Helper()
	if rec := lintSkillRequest(t, srv, http.MethodPut, "/api/v1/skills/default/digest", body); rec.Code != http.StatusOK {
		t.Fatalf("update %s: status = %d; body: %s", body, rec.Code, rec.Body.String())
	}
}

func TestSkillREST_MaxOutputTokensCreateGetUpdate(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithSkillsDir(t), testLogger())

	if rec := lintSkillRequest(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"digest","version":"1.0.0","body":"Summarise.","max_output_tokens":2048}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	if v, ok := getSkillOutputCap(t, srv); !ok || v != 2048 {
		t.Fatalf("after create: max_output_tokens = %v (present %v), want 2048", v, ok)
	}

	putSkill(t, srv, `{"version":"1.0.1","body":"Summarise briefly."}`)
	if v, ok := getSkillOutputCap(t, srv); !ok || v != 2048 {
		t.Errorf("after body-only update: max_output_tokens = %v (present %v), want 2048 kept", v, ok)
	}

	putSkill(t, srv, `{"max_output_tokens":512}`)
	if v, ok := getSkillOutputCap(t, srv); !ok || v != 512 {
		t.Errorf("after set: max_output_tokens = %v (present %v), want 512", v, ok)
	}

	putSkill(t, srv, `{"max_output_tokens":0}`)
	if v, ok := getSkillOutputCap(t, srv); ok {
		t.Errorf("after clear: max_output_tokens = %v, want the key absent", v)
	}
}

func TestSkillREST_NegativeMaxOutputTokensRejected(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithSkillsDir(t), testLogger())

	rec := lintSkillRequest(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"digest","body":"Summarise.","max_output_tokens":-1}`)
	if rec.Code == http.StatusCreated {
		t.Fatalf("status = %d, want the negative cap refused; body: %s", rec.Code, rec.Body.String())
	}
}
