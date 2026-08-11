package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// readOnlySchedulesKey carries every read scope but no write scope, so it can
// reach the schedule list but not a dry run.
func readOnlySchedulesKey() config.APIKeyConfig {
	return config.APIKeyConfig{
		Name:   "readonly",
		Key:    "dk-readonly-key",
		Scopes: []string{"schedules:read", "skills:read"},
	}
}

// postDryRun issues an authenticated dry-run request against path.
func postDryRun(t *testing.T, srv *Server, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

// registerTestSchedule adds a schedule the dry-run endpoint can find.
func registerTestSchedule(t *testing.T, deps Deps, name string) {
	t.Helper()
	registerTestScheduleForSkill(t, deps, name, "greet")
}

// registerTestScheduleForSkill is registerTestSchedule for a named skill, which
// is what makes that skill scheduled rather than ambient.
func registerTestScheduleForSkill(t *testing.T, deps Deps, name, skillName string) {
	t.Helper()
	cfg := scheduler.Config{
		Name:     name,
		Type:     string(scheduler.ScheduleTypeAgent),
		Schedule: "@every 1h",
		Skill:    skillName,
		Agent:    "default",
		Channel:  "telegram:123",
		Enabled:  true,
	}
	if err := deps.Scheduler.RegisterAndStart(cfg, func(scheduler.Entry) {}); err != nil {
		t.Fatalf("RegisterAndStart: %v", err)
	}
}

func decodeTranscript(t *testing.T, rec *httptest.ResponseRecorder) dryRunTranscript {
	t.Helper()
	var got dryRunTranscript
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding transcript: %v; body: %s", err, rec.Body.String())
	}
	return got
}

func TestDryRunSchedule_Success(t *testing.T) {
	deps := testDeps()
	registerTestSchedule(t, deps, "nightly")
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/schedules/nightly/dry-run", `{"as_of":"2026-07-06T10:00:00Z"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	got := decodeTranscript(t, rec)
	if got.Agent != "default" {
		t.Errorf("Agent = %q, want default", got.Agent)
	}
	if !strings.HasPrefix(got.ConversationID, "dryrun:") {
		t.Errorf("ConversationID = %q, want a dryrun: identity", got.ConversationID)
	}
	if got.AsOf.Format("2006-01-02T15:04:05Z") != "2026-07-06T10:00:00Z" {
		t.Errorf("AsOf = %v, want the pinned time echoed back", got.AsOf)
	}
	// The preview must run the message the schedule actually fires, header
	// and all — not a re-creation that can drift from it.
	if !strings.Contains(got.Prompt, "[Scheduled: greet |") {
		t.Errorf("Prompt = %q, want the injected fire-time header", got.Prompt)
	}
	if !strings.Contains(got.Prompt, "2026-07-06T10:00:00") {
		t.Errorf("Prompt = %q, want the header rendered at as_of", got.Prompt)
	}
	if got.Response != "Hello from mock!" {
		t.Errorf("Response = %q", got.Response)
	}
}

func TestDryRunSchedule_PersistsNothing(t *testing.T) {
	deps := testDeps()
	registerTestSchedule(t, deps, "nightly")
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/schedules/nightly/dry-run", `{}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	convs, total, err := deps.Memory.ListConversations(t.Context(), agent.SessionListOpts{Limit: 50})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if total != 0 || len(convs) != 0 {
		t.Errorf("dry run created %d conversations, want 0", total)
	}
}

func TestDryRunSchedule_NotFound(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/schedules/nope/dry-run", `{}`, "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunSchedule_InvalidAsOf(t *testing.T) {
	deps := testDeps()
	registerTestSchedule(t, deps, "nightly")
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/schedules/nightly/dry-run", `{"as_of":"last tuesday"}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunSchedule_RequiresWriteScope(t *testing.T) {
	deps := testDeps()
	registerTestSchedule(t, deps, "nightly")
	srv := New(testConfig(readOnlySchedulesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/schedules/nightly/dry-run", `{}`, "dk-readonly-key")
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — a dry run spends real tokens; body: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunSkill_Success(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run", `{"message":"hello there"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	got := decodeTranscript(t, rec)
	if got.Prompt != "hello there" {
		t.Errorf("Prompt = %q, want the supplied message", got.Prompt)
	}
	if got.Response != "Hello from mock!" {
		t.Errorf("Response = %q", got.Response)
	}
	if got.ToolCalls == nil {
		t.Error("ToolCalls should be an empty array, not null — the panel renders it directly")
	}
}

func TestDryRunSkill_MessageRequiredOnlyInMessageMode(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// Asking for message mode without one is a client error...
	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run",
		`{"mode":"message","message":"   "}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("explicit message mode: status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}

	// ...but a blank message with no mode is just "no message", which is the
	// normal case for a skill that isn't invoked by typing at it.
	rec = postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run", `{"message":"   "}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("inferred mode: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if got := decodeTranscript(t, rec).Mode; got != "command" {
		t.Errorf("Mode = %q, want command (greet declares command:hello)", got)
	}
}

// The three modes are three different messages, not three labels for one.
func TestDryRunSkill_ScheduleModeSendsTheSchedulerHeader(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/help/dry-run",
		`{"mode":"schedule","as_of":"2026-07-06T07:00:00Z"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	got := decodeTranscript(t, rec)
	if got.Mode != "schedule" {
		t.Errorf("Mode = %q, want schedule", got.Mode)
	}
	// No user message: what the agent receives is the injected header, at the
	// pinned time.
	if !strings.HasPrefix(got.Prompt, "[Scheduled: help |") {
		t.Errorf("Prompt = %q, want the scheduler's fire-time header", got.Prompt)
	}
	if !strings.Contains(got.Prompt, "2026-07-06T07:00:00") {
		t.Errorf("Prompt = %q, want the header rendered at as_of", got.Prompt)
	}
}

func TestDryRunSkill_CommandModeSendsTheTrigger(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run",
		`{"mode":"command","args":"unread only"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	got := decodeTranscript(t, rec)
	if got.Prompt != "/hello unread only" {
		t.Errorf("Prompt = %q, want the command plus its arguments", got.Prompt)
	}
}

// A skill with no command: trigger has no command entry point, and saying so
// is more useful than previewing an invocation that cannot happen.
func TestDryRunSkill_CommandModeRejectedWithoutATrigger(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/help/dry-run", `{"mode":"command"}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no command: trigger") {
		t.Errorf("error should name the missing trigger, got: %s", rec.Body.String())
	}
}

func TestDryRunSkill_InferredModeFollowsTheSkillsTriggers(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// greet declares command:hello, so that is its entry point.
	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run", `{}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if got := decodeTranscript(t, rec).Mode; got != "command" {
		t.Errorf("Mode = %q, want command for a skill with a command: trigger", got)
	}
}

// A skill with no triggers is only scheduled if a schedule says so. Without
// one it is ambient: it matches an ordinary turn and reads what the user said,
// and previewing it as a scheduled run rehearses an invocation it never has.
func TestDryRunSkill_TriggerlessSkillWithNoScheduleIsAmbient(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/help/dry-run",
		`{"message":"what happened yesterday"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got := decodeTranscript(t, rec)
	if got.Mode != "message" {
		t.Errorf("Mode = %q, want message for a skill nothing schedules", got.Mode)
	}
	if got.ScheduledBy != "" {
		t.Errorf("ScheduledBy = %q, want empty when nothing schedules the skill", got.ScheduledBy)
	}

	// And with no message at all it is still a message invocation — just one
	// the caller has to supply, rather than a synthesised scheduler header.
	rec = postDryRun(t, srv, "/api/v1/skills/default/help/dry-run", `{}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (message required); body: %s", rec.Code, rec.Body.String())
	}
}

// The signal the inference was missing: [[schedules]], not frontmatter.
func TestDryRunSkill_TriggerlessSkillWithAScheduleIsScheduled(t *testing.T) {
	deps := testDeps()
	registerTestScheduleForSkill(t, deps, "nightly-help", "help")
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/help/dry-run",
		`{"as_of":"2026-07-06T07:00:00Z"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got := decodeTranscript(t, rec)
	if got.Mode != "schedule" {
		t.Errorf("Mode = %q, want schedule for a skill a schedule fires", got.Mode)
	}
	// The transcript names the schedule it stood in for, so it is self-describing.
	if got.ScheduledBy != "nightly-help" {
		t.Errorf("ScheduledBy = %q, want nightly-help", got.ScheduledBy)
	}
	if !strings.HasPrefix(got.Prompt, "[Scheduled: help |") {
		t.Errorf("Prompt = %q, want the scheduler's fire-time header", got.Prompt)
	}
}

func TestDryRunSkill_InvalidMode(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run", `{"mode":"telepathy"}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunSkill_UnknownSkill(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/nope/dry-run", `{"message":"hi"}`, "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunSkill_UnknownAgent(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/ghost/greet/dry-run", `{"message":"hi"}`, "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

func TestTruncateField(t *testing.T) {
	short := strings.Repeat("a", 10)
	if got, truncated := truncateField(short); got != short || truncated {
		t.Errorf("truncateField(short) = (%d chars, %v), want unchanged", len(got), truncated)
	}
	long := strings.Repeat("b", maxTranscriptFieldLen+100)
	got, truncated := truncateField(long)
	if len(got) != maxTranscriptFieldLen || !truncated {
		t.Errorf("truncateField(long) = (%d chars, %v), want (%d, true)", len(got), truncated, maxTranscriptFieldLen)
	}
}

func TestDryRunSkill_ModelOverrideIsEchoed(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run",
		`{"message":"hi","model":"moonshotai/kimi-k3"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	got := decodeTranscript(t, rec)
	// requested_model is what makes a transcript self-describing: the UI can
	// mark it "not your live model" without knowing the agent's config.
	if got.RequestedModel != "moonshotai/kimi-k3" {
		t.Errorf("RequestedModel = %q, want the override echoed back", got.RequestedModel)
	}
}

func TestDryRunSkill_NoOverrideLeavesRequestedModelEmpty(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run", `{"message":"hi"}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if got := decodeTranscript(t, rec).RequestedModel; got != "" {
		t.Errorf("RequestedModel = %q, want empty when the live model ran", got)
	}
}
