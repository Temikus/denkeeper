//go:build integration

package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnboarding_Checklist(t *testing.T) {
	h := NewHarness(t, nil) // default: one "default" agent with telegram adapter, no provider/skills/auth.

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/onboarding", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		ShowOnboarding bool `json:"show_onboarding"`
		Dismissed      bool `json:"dismissed"`
		Steps          []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
			Done  bool   `json:"done"`
		} `json:"steps"`
	}
	DecodeJSON(t, rec, &body)

	if body.Dismissed {
		t.Errorf("dismissed = true, want false")
	}
	if !body.ShowOnboarding {
		t.Errorf("show_onboarding = false, want true")
	}
	if len(body.Steps) != 5 {
		t.Fatalf("len(steps) = %d, want 5", len(body.Steps))
	}

	want := map[string]bool{
		"auth":     false,
		"agent":    true,
		"adapter":  true,
		"provider": false,
		"skill":    false,
	}
	wantOrder := []string{"auth", "agent", "adapter", "provider", "skill"}
	for i, step := range body.Steps {
		if step.ID != wantOrder[i] {
			t.Errorf("step[%d].id = %q, want %q", i, step.ID, wantOrder[i])
		}
		if step.Done != want[step.ID] {
			t.Errorf("step %q: done = %v, want %v", step.ID, step.Done, want[step.ID])
		}
	}
}

func TestOnboarding_Dismiss(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("[api]\nlisten = \":0\"\n"), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	h := NewHarness(t, &HarnessOpts{ConfigPath: cfgPath})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/dismiss", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("dismiss: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Re-check — must show dismissed=true and show_onboarding=false.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/onboarding", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ShowOnboarding bool `json:"show_onboarding"`
		Dismissed      bool `json:"dismissed"`
	}
	DecodeJSON(t, rec, &body)
	if !body.Dismissed {
		t.Errorf("dismissed = false, want true")
	}
	if body.ShowOnboarding {
		t.Errorf("show_onboarding = true, want false")
	}

	// On-disk TOML must contain the dismissed flag.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("re-reading config: %v", err)
	}
	if !strings.Contains(string(data), "onboarding_dismissed = true") {
		t.Errorf("TOML does not contain dismissed flag:\n%s", string(data))
	}
}

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
