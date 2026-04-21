//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestPanicStatus_NotPanicked(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/panic", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["panicked"] != false {
		t.Errorf("panicked = %v, want false", resp["panicked"])
	}
}

func TestPanic_SetsPanicState(t *testing.T) {
	h := NewHarness(t, nil)

	// Trigger panic.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /panic status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	// Verify state.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/panic", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /panic status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["panicked"] != true {
		t.Errorf("panicked = %v, want true", resp["panicked"])
	}
}

func TestResume_ClearsPanicState(t *testing.T) {
	h := NewHarness(t, nil)

	// Panic then resume.
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/resume", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /resume status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	// Verify state cleared.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/panic", nil))
	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["panicked"] != false {
		t.Errorf("panicked = %v, want false after resume", resp["panicked"])
	}
}

func TestPanic_Idempotent(t *testing.T) {
	h := NewHarness(t, nil)

	// Double panic should not error.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first panic status = %d", rec.Code)
	}
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second panic status = %d", rec.Code)
	}

	// Still panicked.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/panic", nil))
	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["panicked"] != true {
		t.Errorf("panicked = %v, want true", resp["panicked"])
	}
}

func TestResume_Idempotent(t *testing.T) {
	h := NewHarness(t, nil)

	// Resume without prior panic should succeed.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/resume", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume without panic status = %d", rec.Code)
	}
}

func TestStopSession_NoInFlight(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/sessions/nonexistent/stop", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stop status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestPanic_ThenChatStillWorks(t *testing.T) {
	// Verify that chat works before panic, is rejected during panic (at the
	// dispatcher level for adapter messages), and works again after resume.
	// Note: REST API chat bypasses the dispatcher Run() loop, so this tests
	// the REST round-trip. In-flight cancellation is tested by unit tests.
	h := NewHarness(t, nil)

	// Chat works before panic.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{"message": "hi"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat before panic: status = %d, want 200", rec.Code)
	}

	// Panic.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("panic: status = %d", rec.Code)
	}
	if !h.Dispatcher.IsPanicked() {
		t.Fatal("dispatcher should be panicked")
	}

	// Resume.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/resume", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume: status = %d", rec.Code)
	}

	// Chat works again after resume.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{"message": "hello again"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat after resume: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

func TestPanic_PanicTimeReturned(t *testing.T) {
	h := NewHarness(t, nil)

	before := time.Now()
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/panic", nil))
	var resp map[string]any
	DecodeJSON(t, rec, &resp)

	panicTimeStr, ok := resp["panic_time"].(string)
	if !ok {
		t.Fatalf("panic_time is not a string: %v", resp["panic_time"])
	}
	panicTime, err := time.Parse(time.RFC3339Nano, panicTimeStr)
	if err != nil {
		t.Fatalf("parsing panic_time: %v", err)
	}
	if panicTime.Before(before) {
		t.Errorf("panic_time %v is before test start %v", panicTime, before)
	}
}

func TestPanic_RequiresAdminScope(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"chat"}, // no admin scope
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (insufficient scope)", rec.Code, http.StatusForbidden)
	}
}

func TestStopSession_RequiresChatScope(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"admin"}, // no chat scope
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/sessions/s1/stop", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (insufficient scope)", rec.Code, http.StatusForbidden)
	}
}

func TestScheduler_PausedByPanic(t *testing.T) {
	h := NewHarness(t, nil)

	// The scheduler is accessible through the harness.
	if h.Scheduler.IsPaused() {
		t.Fatal("scheduler should not be paused initially")
	}

	// Wire the OnPanic/OnResume hooks like main.go does.
	h.Dispatcher.OnPanic = func() {
		h.Scheduler.Pause()
	}
	h.Dispatcher.OnResume = func() {
		h.Scheduler.Resume()
	}

	// Trigger panic via API.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("panic status = %d", rec.Code)
	}
	if !h.Scheduler.IsPaused() {
		t.Error("scheduler should be paused after panic")
	}

	// Resume via API.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/resume", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d", rec.Code)
	}
	if h.Scheduler.IsPaused() {
		t.Error("scheduler should not be paused after resume")
	}
}

func TestPanicStatus_NoAuth(t *testing.T) {
	h := NewHarness(t, nil)

	// No auth header — should fail.
	req := h.AuthedRequest(http.MethodGet, "/api/v1/panic", nil)
	req.Header.Del("Authorization")
	rec := h.Do(req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// Ensure json is used (prevent "imported and not used" error).
var _ = json.Marshal
