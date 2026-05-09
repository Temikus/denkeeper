//go:build integration

package integration

import (
	"context"
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

func TestSkills_TelemetryBumpedOnChat(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{
				Name: "default", Tier: "autonomous", Adapters: []string{"api"},
				Skills: []skill.Skill{
					skill.NewTestSkill("greet", "Greeting skill", []string{"command:hello"}, "Greet the user warmly."),
				},
			},
		},
	})

	store := h.Memory // *SQLiteMemoryStore implements TelemetryStore

	// No telemetry before chat.
	usage, err := store.GetSkillUsage(context.Background(), "default", "greet")
	if err != nil {
		t.Fatalf("GetSkillUsage before chat: %v", err)
	}
	if usage != nil {
		t.Fatal("expected nil usage before chat")
	}

	// Send a /hello command — triggers the greet skill.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "/hello",
		"session_id": "telemetry-test",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify global skill_usage counters were bumped.
	usage, err = store.GetSkillUsage(context.Background(), "default", "greet")
	if err != nil {
		t.Fatalf("GetSkillUsage after chat: %v", err)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage after chat")
	}
	if usage.UseCount != 1 {
		t.Errorf("use_count = %d, want 1", usage.UseCount)
	}
	if usage.LastUsedAt == nil {
		t.Error("expected last_used_at to be set")
	}

	// Verify per-session skill usage via REST endpoint.
	skillsRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/sessions/telemetry-test/skills", nil))
	if skillsRec.Code != http.StatusOK {
		t.Fatalf("session skills status = %d, want %d", skillsRec.Code, http.StatusOK)
	}
	var records []map[string]any
	DecodeJSON(t, skillsRec, &records)
	if len(records) == 0 {
		t.Fatal("expected at least one skill usage record")
	}
	found := false
	for _, r := range records {
		if r["skill_name"] == "greet" {
			found = true
			if r["match_type"] != "command" {
				t.Errorf("match_type = %v, want command", r["match_type"])
			}
		}
	}
	if !found {
		t.Errorf("greet skill not found in session skill usages: %v", records)
	}

	// A second chat bumps the counter again.
	rec2 := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "/hello again",
		"session_id": "telemetry-test",
	}))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second chat status = %d", rec2.Code)
	}
	usage2, _ := store.GetSkillUsage(context.Background(), "default", "greet")
	if usage2 == nil || usage2.UseCount != 2 {
		t.Errorf("use_count after second chat = %v, want 2", usage2)
	}
}

func TestSkills_TelemetryListByAgent(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{
				Name: "default", Tier: "autonomous", Adapters: []string{"api"},
				Skills: []skill.Skill{
					skill.NewTestSkill("greet", "Greeting", []string{"command:hello"}, "Greet."),
					skill.NewTestSkill("help", "Help", []string{"command:help"}, "Help."),
				},
			},
		},
	})

	store := h.Memory

	// Trigger both skills.
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "/hello", "session_id": "s1",
	}))
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "/help", "session_id": "s2",
	}))

	// ListSkillUsage returns both.
	all, err := store.ListSkillUsage(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListSkillUsage: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 skill usage rows, got %d", len(all))
	}
	names := map[string]bool{}
	for _, s := range all {
		names[s.SkillName] = true
		if s.UseCount != 1 {
			t.Errorf("skill %s use_count = %d, want 1", s.SkillName, s.UseCount)
		}
	}
	if !names["greet"] || !names["help"] {
		t.Errorf("expected greet and help; got %v", names)
	}
}
