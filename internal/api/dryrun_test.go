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
	cfg := scheduler.Config{
		Name:     name,
		Type:     string(scheduler.ScheduleTypeAgent),
		Schedule: "@every 1h",
		Skill:    "greet",
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

func TestDryRunSkill_MissingMessage(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := postDryRun(t, srv, "/api/v1/skills/default/greet/dry-run", `{"message":"   "}`, "dk-test-key")
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
