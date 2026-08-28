//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// TestConfigReload_ConcurrentWithRequests hammers the config-reading endpoints
// while the hot reload republishes the config underneath them. Under -race
// this fails if a handler dereferences a shared, mutable *config.Config
// instead of taking a snapshot through the holder.
func TestConfigReload_ConcurrentWithRequests(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")

	writeCfg := func(externalURL string) {
		t.Helper()
		body := fmt.Sprintf(`
[api]
external_url = %q

[llm]
default_provider = "mock-existing"
default_model = "mock-model"

[[llm.providers]]
name = "mock-existing"
type = "openai"
api_key = "sk-test"

[[agents]]
name = "default"
session_tier = "supervised"
description = %q
`, externalURL, "agent at "+externalURL)
		if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}
	}
	writeCfg("https://one.example.com")

	var h *Harness
	h = NewHarness(t, &HarnessOpts{
		Agents:     []agentSetup{{Name: "default", Tier: "supervised"}},
		ConfigPath: cfgPath,
		WithEval:   true,
		// Mirrors buildReloadFunc in cmd/denkeeper: re-read from disk and
		// publish the result as a new snapshot.
		ReloadFunc: func() error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			h.ConfigHolder().Store(cfg)
			return nil
		},
	})
	readPaths := []string{
		"/api/v1/server/config",
		"/api/v1/agents",
		"/api/v1/llm/providers",
		"/api/v1/onboarding",
		"/api/v1/eval/config",
		"/api/v1/costs",
	}

	const rounds = 30
	var wg sync.WaitGroup

	// Reloader: swaps the published config repeatedly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			writeCfg(fmt.Sprintf("https://reload-%d.example.com", i))
			rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/server/reload", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("reload status = %d, body = %s", rec.Code, rec.Body.String())
				return
			}
		}
	}()

	// Readers: every handler that reads config fields.
	for _, p := range readPaths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				rec := h.Do(h.AuthedRequest(http.MethodGet, path, nil))
				if rec.Code >= http.StatusInternalServerError {
					t.Errorf("GET %s status = %d, body = %s", path, rec.Code, rec.Body.String())
					return
				}
			}
		}(p)
	}

	// Writer: a config-mutating handler, racing the reload's pointer swap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/llm/config", map[string]any{
				"default_model": fmt.Sprintf("model-%d", i),
			}))
			if rec.Code >= http.StatusInternalServerError {
				t.Errorf("PATCH /llm/config status = %d, body = %s", rec.Code, rec.Body.String())
				return
			}
		}
	}()

	wg.Wait()

	// The last reload wins: the published snapshot must be one whole config,
	// not a blend of two.
	got := h.ConfigHolder().Get()
	if got == nil {
		t.Fatal("config holder is empty after reloads")
	}
	if got.API.ExternalURL == "" {
		t.Errorf("external_url is empty; snapshot looks torn: %+v", got.API)
	}
}
