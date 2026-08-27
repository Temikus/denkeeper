package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/eval"
)

func decodeProbes(t *testing.T, body []byte) evalProbesResult {
	t.Helper()
	var got evalProbesResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding response: %v (%s)", err, body)
	}
	return got
}

func TestEvalProbes_GeneratesFromTheAgentsOwnConfiguration(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeProbes(t, rec.Body.Bytes())
	if got.Agent != "default" || got.PermissionTier != "supervised" {
		t.Errorf("agent/tier = %q/%q, want default/supervised", got.Agent, got.PermissionTier)
	}
	if len(got.Probes) == 0 {
		t.Fatal("no probes generated")
	}

	kinds := map[string]int{}
	for _, p := range got.Probes {
		kinds[p.Kind]++
		if p.Category != eval.CategoryProbe {
			t.Errorf("probe %q has category %q, want %q", p.Prompt, p.Category, eval.CategoryProbe)
		}
		if p.Notes == "" {
			t.Errorf("probe %q carries no notes for the judge", p.Prompt)
		}
	}
	// The three canned families need no configuration; the skill family comes
	// from the test engine's command-triggered "greet" skill.
	for _, want := range []string{
		eval.ProbeDenialCompliance,
		eval.ProbeTierBoundary,
		eval.ProbeBudgetHint,
		eval.ProbeSkillInstruction,
	} {
		if kinds[want] == 0 {
			t.Errorf("no %s probe; kinds = %v", want, kinds)
		}
	}
}

func TestEvalProbes_SkillProbeReadsTheSkillsOwnFrontmatter(t *testing.T) {
	// The point of "spec-derived": the probe quotes the skill's declared
	// purpose, so the judge grades against what the operator actually wrote.
	srv, _ := evalTestServer(t, allScopesKey())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default", "", "dk-test-key")
	got := decodeProbes(t, rec.Body.Bytes())

	var found bool
	for _, p := range got.Probes {
		if p.Kind != eval.ProbeSkillInstruction {
			continue
		}
		if p.Source != "skill:greet" {
			t.Errorf("skill probe source = %q, want skill:greet", p.Source)
		}
		if p.Prompt == "/hello" {
			found = true
		}
	}
	if !found {
		t.Errorf("no probe invokes the greet skill's own /hello command: %+v", got.Probes)
	}
}

func TestEvalProbes_RequiresAnAgent(t *testing.T) {
	// Probes are one agent's spec; without a name there is nothing to read.
	srv, _ := evalTestServer(t, allScopesKey())
	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes", "", "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestEvalProbes_UnknownAgentIs404(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())
	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=nope", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestEvalProbes_RejectsABadLimit(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())
	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default&limit=0", "", "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestEvalProbes_LimitBoundsThePass(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())
	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default&limit=3", "", "dk-test-key")
	got := decodeProbes(t, rec.Body.Bytes())
	if len(got.Probes) != 3 {
		t.Fatalf("probes = %d, want the requested 3", len(got.Probes))
	}
}

func TestEvalProbes_ExcludesProbesTheTargetSetAlreadyCarries(t *testing.T) {
	// Generation is deterministic, so a second pass would otherwise offer the
	// whole set again the moment the operator reopened the panel.
	srv, store := evalTestServer(t, allScopesKey())
	ctx := context.Background()

	first := decodeProbes(t, evalRequest(t, srv, http.MethodGet,
		"/api/v1/eval/probes?agent=default", "", "dk-test-key").Body.Bytes())
	if len(first.Probes) == 0 {
		t.Fatal("no probes to save")
	}
	set, err := store.CreateTaskSet(ctx, "probes", "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	saved := first.Probes[0]
	if _, err := store.AddTask(ctx, set.ID, eval.Task{
		Prompt: saved.Prompt, Category: saved.Category, Notes: saved.Notes,
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	second := decodeProbes(t, evalRequest(t, srv, http.MethodGet,
		"/api/v1/eval/probes?agent=default&set=probes", "", "dk-test-key").Body.Bytes())
	for _, p := range second.Probes {
		if p.Prompt == saved.Prompt {
			t.Fatalf("probe %q was offered again after being saved into the set", p.Prompt)
		}
	}
	if len(second.Probes) != len(first.Probes)-1 {
		t.Errorf("second pass returned %d probes, want %d (one fewer)", len(second.Probes), len(first.Probes)-1)
	}
}

func TestEvalProbes_UnknownTargetSetIs404(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())
	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default&set=nope", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// probeConfigKey reads evals and agent config, but not skills or tools.
func probeConfigKey() config.APIKeyConfig {
	return config.APIKeyConfig{
		Name:   "probe-config",
		Key:    "dk-probe-config",
		Scopes: []string{"eval:read", "eval:write", "agents:read"},
	}
}

func TestEvalProbes_NeedsAgentsReadOnTopOfEvalRead(t *testing.T) {
	// Generation writes nothing, but it reads agent configuration and quotes it
	// back, so eval:read alone must not become a way around agents:read.
	srv, _ := evalTestServer(t, allScopesKey(), evalReadOnlyKey(), noEvalKey(), probeConfigKey())

	if rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default", "", "dk-eval-readonly"); rec.Code != http.StatusForbidden {
		t.Errorf("eval:read-only status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default", "", "dk-chat-only"); rec.Code != http.StatusForbidden {
		t.Errorf("chat-only status = %d, want 403", rec.Code)
	}
	if rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/probes?agent=default", "", "dk-probe-config"); rec.Code != http.StatusOK {
		t.Errorf("eval:read + agents:read status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestEvalProbes_OmitsFamiliesTheCallerCannotRead(t *testing.T) {
	// A skill probe quotes the skill's own frontmatter; the approval probe
	// names a tool and its auto-approve policy. Neither may reach a credential
	// that could not read those endpoints directly.
	srv, _ := evalTestServer(t, allScopesKey(), probeConfigKey())

	full := decodeProbes(t, evalRequest(t, srv, http.MethodGet,
		"/api/v1/eval/probes?agent=default", "", "dk-test-key").Body.Bytes())
	var sawSkill bool
	for _, p := range full.Probes {
		if p.Kind == eval.ProbeSkillInstruction {
			sawSkill = true
		}
	}
	if !sawSkill {
		t.Fatal("fully-scoped caller got no skill probe, so the narrowing test proves nothing")
	}

	narrowed := decodeProbes(t, evalRequest(t, srv, http.MethodGet,
		"/api/v1/eval/probes?agent=default", "", "dk-probe-config").Body.Bytes())
	if len(narrowed.Probes) == 0 {
		t.Fatal("no probes for a caller with eval:read + agents:read")
	}
	for _, p := range narrowed.Probes {
		if p.Kind == eval.ProbeSkillInstruction || p.Kind == eval.ProbeApprovalPolicy {
			t.Errorf("probe of kind %q reached a caller without skills:read/tools:read: %q", p.Kind, p.Prompt)
		}
	}
}
