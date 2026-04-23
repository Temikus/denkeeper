//go:build integration

package integration

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// scheduleHarness returns a harness with ConfigPath set to a temp TOML file,
// which is required for schedule CRUD via the API.
func scheduleHarness(t *testing.T) *Harness {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	return NewHarness(t, &HarnessOpts{
		ConfigPath: cfgPath,
	})
}

func TestScheduleAPI_CreateAndList(t *testing.T) {
	h := scheduleHarness(t)

	// Create a schedule via API.
	createRec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name":     "daily-greet",
		"schedule": "@daily",
		"skill":    "greet",
		"channel":  "telegram:12345",
	}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}

	var createResp map[string]string
	DecodeJSON(t, createRec, &createResp)
	if createResp["status"] != "created" {
		t.Errorf("status = %v, want created", createResp["status"])
	}
	if createResp["name"] != "daily-greet" {
		t.Errorf("name = %v, want daily-greet", createResp["name"])
	}

	// List schedules — should include the new one.
	listRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/schedules", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRec.Code)
	}

	var schedules []map[string]any
	DecodeJSON(t, listRec, &schedules)
	if len(schedules) != 1 {
		t.Fatalf("schedules count = %d, want 1", len(schedules))
	}
	if schedules[0]["name"] != "daily-greet" {
		t.Errorf("schedule name = %v, want daily-greet", schedules[0]["name"])
	}
}

func TestScheduleAPI_CreateDuplicate_Returns409(t *testing.T) {
	h := scheduleHarness(t)

	body := map[string]any{
		"name":     "dup-schedule",
		"schedule": "@hourly",
		"channel":  "telegram:1",
	}

	rec1 := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", body))
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first create status = %d", rec1.Code)
	}

	rec2 := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", body))
	if rec2.Code != http.StatusConflict {
		t.Errorf("duplicate create status = %d, want %d", rec2.Code, http.StatusConflict)
	}
}

func TestScheduleAPI_CreateValidationErrors(t *testing.T) {
	h := scheduleHarness(t)

	// Missing name.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"schedule": "@daily",
		"channel":  "telegram:1",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Missing schedule.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name":    "test",
		"channel": "telegram:1",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing schedule: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Missing channel.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name":     "test",
		"schedule": "@daily",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing channel: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Invalid channel format (no colon).
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name":     "test",
		"schedule": "@daily",
		"channel":  "bad-format",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad channel: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestScheduleAPI_DeleteExisting(t *testing.T) {
	h := scheduleHarness(t)

	// Create.
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name":     "delete-me",
		"schedule": "@hourly",
		"channel":  "telegram:1",
	}))

	// Delete.
	delRec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/schedules/delete-me", nil))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d; body: %s", delRec.Code, http.StatusNoContent, delRec.Body.String())
	}

	// Verify gone from list.
	listRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/schedules", nil))
	var schedules []map[string]any
	DecodeJSON(t, listRec, &schedules)
	if len(schedules) != 0 {
		t.Errorf("schedules after delete = %d, want 0", len(schedules))
	}
}

func TestScheduleAPI_DeleteNotFound(t *testing.T) {
	h := scheduleHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/schedules/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestScheduleAPI_UpdateExisting(t *testing.T) {
	h := scheduleHarness(t)

	// Create.
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name":     "update-me",
		"schedule": "@hourly",
		"channel":  "telegram:1",
	}))

	// Update schedule expression.
	updateRec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/schedules/update-me", map[string]any{
		"schedule": "@daily",
	}))
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body: %s", updateRec.Code, http.StatusOK, updateRec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, updateRec, &resp)
	if resp["status"] != "updated" {
		t.Errorf("status = %v, want updated", resp["status"])
	}
	if resp["schedule"] != "@daily" {
		t.Errorf("schedule = %v, want @daily", resp["schedule"])
	}
}

func TestScheduleAPI_UpdateNotFound(t *testing.T) {
	h := scheduleHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/schedules/nonexistent", map[string]any{
		"schedule": "@daily",
	}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestScheduleAPI_ChannelRefAccepted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	h := NewHarness(t, &HarnessOpts{
		ConfigPath: cfgPath,
		Channels: []*agent.Channel{
			{Name: "work", AgentName: "default", Adapters: []string{"telegram:12345"}},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name":     "chan-ref-sched",
		"schedule": "@daily",
		"channel":  "@work",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestScheduleAPI_BroadcastAuditOnPartialFailure(t *testing.T) {
	h := NewHarness(t, nil)

	// Build a channel resolver that returns a broadcast channel with two bindings.
	resolver := func(name string) *configmcp.ChannelResolveResult {
		if name != "bcast" {
			return nil
		}
		return &configmcp.ChannelResolveResult{
			ConversationID: "chan:bcast",
			Bindings: []agent.AdapterBinding{
				{Adapter: "telegram", ExternalID: "12345"},
				{Adapter: "ghost", ExternalID: "99999"},
			},
			Broadcast: true,
		}
	}

	// handleMsg succeeds for telegram, fails for ghost — partial failure.
	handleMsg := func(_ context.Context, msg adapter.IncomingMessage) error {
		if msg.Adapter == "ghost" {
			return errors.New("adapter not found")
		}
		return nil
	}

	logger := slog.Default()
	job := configmcp.BuildScheduleJob(
		scheduler.Config{Name: "bcast-sched", Channel: "@bcast"},
		handleMsg, logger, resolver,
		configmcp.BuildScheduleJobOpts{Auditor: h.Auditor},
	)

	// Fire the job directly.
	job(scheduler.Entry{Name: "bcast-sched", Channel: "@bcast"})

	// Flush audit buffer and query the store.
	h.FlushAudit(t)

	events, _, err := h.AuditStore.List(context.Background(), audit.ListOpts{
		Category: audit.CategorySchedule,
	})
	if err != nil {
		t.Fatalf("listing audit events: %v", err)
	}

	var found bool
	for _, ev := range events {
		if ev.Action == "broadcast_partial_failure" {
			found = true
			if ev.Status != audit.StatusError {
				t.Errorf("audit event status = %q, want %q", ev.Status, audit.StatusError)
			}
			if ev.ConversationID != "chan:bcast" {
				t.Errorf("conversation_id = %q, want chan:bcast", ev.ConversationID)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected broadcast_partial_failure audit event, got %d schedule events", len(events))
	}
}
