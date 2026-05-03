//go:build integration

package integration

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestServer_Reload(t *testing.T) {
	var called int32
	h := NewHarness(t, &HarnessOpts{
		ReloadFunc: func() error {
			atomic.AddInt32(&called, 1)
			return nil
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/server/reload", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	DecodeJSON(t, rec, &body)
	if body["status"] != "reloaded" {
		t.Errorf("status field = %q, want reloaded", body["status"])
	}
	if got := atomic.LoadInt32(&called); got != 1 {
		t.Errorf("ReloadFunc called %d times, want 1", got)
	}
}

func TestServer_Restart(t *testing.T) {
	done := make(chan struct{})
	h := NewHarness(t, &HarnessOpts{
		RestartFunc: func() error {
			close(done)
			return nil
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/server/restart", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	DecodeJSON(t, rec, &body)
	if body["status"] != "restarting" {
		t.Errorf("status field = %q, want restarting", body["status"])
	}

	// The restart handler invokes RestartFunc in a goroutine after a 500ms delay.
	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("RestartFunc was not invoked within 1500ms")
	}
}
