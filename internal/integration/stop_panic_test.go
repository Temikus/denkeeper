//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
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

func TestPanic_CancelsInFlightRequest(t *testing.T) {
	// Use a slow mock that blocks until context is cancelled.
	h := NewHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{
			// This response won't actually be used because the mock will block.
		},
	})

	// Manually register an in-flight request in the dispatcher's tracking map
	// to test that /panic cancels it. The REST API chat handler bypasses the
	// dispatcher's Run() loop, so we simulate an adapter-originated request.
	chatCtx, chatCancel := context.WithCancel(context.Background())
	defer chatCancel()

	h.Dispatcher.RegisterInFlight("api", "test-session", chatCancel)

	// In a goroutine, simulate waiting for the in-flight request.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-chatCtx.Done()
	}()

	// Trigger panic via API.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/panic", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("panic status = %d", rec.Code)
	}

	// The in-flight request should have been cancelled.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success — the goroutine was cancelled
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request was not cancelled by panic")
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
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (insufficient scope)", rec.Code, http.StatusUnauthorized)
	}
}

func TestStopSession_RequiresChatScope(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"admin"}, // no chat scope
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/sessions/s1/stop", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (insufficient scope)", rec.Code, http.StatusUnauthorized)
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

// --- helpers ---

// blockingProvider blocks ChatCompletion until context is cancelled.
type blockingProvider struct {
	started chan struct{}
}

func (p *blockingProvider) Name() string { return "blocking" }
func (p *blockingProvider) ChatCompletion(ctx context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *blockingProvider) HealthCheck(_ context.Context) error { return nil }

// Ensure json is used (prevent "imported and not used" error).
var _ = json.Marshal
