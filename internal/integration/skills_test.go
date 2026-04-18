//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/skill"
)

func TestSkills_ListAll(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{
				Name: "default", Tier: "supervised", Adapters: []string{"telegram"},
				Skills: []skill.Skill{
					{Name: "greet", Description: "Greeting skill"},
					{Name: "help", Description: "Help system"},
				},
			},
			{
				Name: "work", Tier: "autonomous", Adapters: []string{"discord"},
				Skills: []skill.Skill{
					{Name: "summarize", Description: "Summarize text"},
				},
			},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var skills []map[string]any
	DecodeJSON(t, rec, &skills)
	if len(skills) != 3 {
		t.Fatalf("skills count = %d, want 3", len(skills))
	}

	names := map[string]bool{}
	for _, s := range skills {
		names[s["name"].(string)] = true
	}
	if !names["greet"] || !names["help"] || !names["summarize"] {
		t.Errorf("expected greet, help, summarize; got %v", names)
	}
}

func TestSkills_ListByAgent(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{
				Name: "default", Tier: "supervised", Adapters: []string{"telegram"},
				Skills: []skill.Skill{
					{Name: "greet", Description: "Greeting skill"},
				},
			},
			{
				Name: "work", Tier: "autonomous", Adapters: []string{"discord"},
				Skills: []skill.Skill{
					{Name: "summarize", Description: "Summarize text"},
				},
			},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/skills/work", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var skills []map[string]any
	DecodeJSON(t, rec, &skills)
	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(skills))
	}
	if skills[0]["name"] != "summarize" {
		t.Errorf("skill name = %v, want summarize", skills[0]["name"])
	}
}

func TestSkills_GetByName(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{
				Name: "default", Tier: "supervised", Adapters: []string{"telegram"},
				Skills: []skill.Skill{
					{Name: "greet", Description: "Greeting skill", Version: "2.0", Triggers: []string{"command:hello"}},
				},
			},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/skills/default/greet", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var sk map[string]any
	DecodeJSON(t, rec, &sk)
	if sk["name"] != "greet" {
		t.Errorf("name = %v, want greet", sk["name"])
	}
	if sk["description"] != "Greeting skill" {
		t.Errorf("description = %v", sk["description"])
	}
	if sk["version"] != "2.0" {
		t.Errorf("version = %v, want 2.0", sk["version"])
	}
	if sk["agent"] != "default" {
		t.Errorf("agent = %v, want default", sk["agent"])
	}
}

func TestSkills_GetNotFound(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/skills/default/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSkills_GetNotFound_BadAgent(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/skills/no-such-agent/greet", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSkills_CreateAndGet(t *testing.T) {
	h := NewHarness(t, nil)

	// Set up skills dir so create is allowed.
	e := h.Dispatcher.Agent("default")
	dir := t.TempDir()
	e.SetSkillDirs(dir, "")

	createRec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/skills/default", map[string]any{
		"name":        "new-skill",
		"description": "A new skill",
		"body":        "Do something useful.",
		"triggers":    []string{"command:new"},
	}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}

	var createResp map[string]string
	DecodeJSON(t, createRec, &createResp)
	if createResp["status"] != "created" {
		t.Errorf("status = %v, want created", createResp["status"])
	}

	// Verify the skill is now retrievable.
	getRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/skills/default/new-skill", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", getRec.Code, http.StatusOK)
	}

	var sk map[string]any
	DecodeJSON(t, getRec, &sk)
	if sk["name"] != "new-skill" {
		t.Errorf("name = %v, want new-skill", sk["name"])
	}
	if sk["body"] != "Do something useful." {
		t.Errorf("body = %v", sk["body"])
	}
}

func TestSkills_CreateMissingName(t *testing.T) {
	h := NewHarness(t, nil)

	e := h.Dispatcher.Agent("default")
	e.SetSkillDirs(t.TempDir(), "")

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/skills/default", map[string]any{
		"body": "some body",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSkills_CreateMissingBody(t *testing.T) {
	h := NewHarness(t, nil)

	e := h.Dispatcher.Agent("default")
	e.SetSkillDirs(t.TempDir(), "")

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/skills/default", map[string]any{
		"name": "test",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSkills_CreateNoSkillsDir_Returns503(t *testing.T) {
	h := NewHarness(t, nil)

	// Don't set skills dir — should return 503.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/skills/default", map[string]any{
		"name": "test",
		"body": "test body",
	}))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSkills_DeleteExisting(t *testing.T) {
	h := NewHarness(t, nil)

	e := h.Dispatcher.Agent("default")
	e.SetSkillDirs(t.TempDir(), "")

	// Create a skill first.
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/skills/default", map[string]any{
		"name": "doomed",
		"body": "Will be deleted.",
	}))

	// Delete it.
	delRec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/skills/default/doomed", nil))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d; body: %s", delRec.Code, http.StatusNoContent, delRec.Body.String())
	}

	// Verify it's gone.
	getRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/skills/default/doomed", nil))
	if getRec.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want %d", getRec.Code, http.StatusNotFound)
	}
}

func TestSkills_DeleteNotFound(t *testing.T) {
	h := NewHarness(t, nil)

	e := h.Dispatcher.Agent("default")
	e.SetSkillDirs(t.TempDir(), "")

	rec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/skills/default/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
