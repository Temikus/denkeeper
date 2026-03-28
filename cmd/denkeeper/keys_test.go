package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDBPath_NoConfigFile(t *testing.T) {
	got := resolveDBPath("/nonexistent/path/denkeeper.toml")
	if got == "" {
		t.Fatal("expected non-empty path")
	}
	// Should be the default path.
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".denkeeper", "data", "memory.db")
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_EmptyCfgPath(t *testing.T) {
	// Empty cfgFile falls back to DefaultConfigPath which (likely) doesn't
	// exist in CI — should still return the default DB path without panicking.
	got := resolveDBPath("")
	if got == "" {
		t.Fatal("expected non-empty path")
	}
}

func TestResolveDBPath_CustomDBPath(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `[memory]
db_path = "/custom/path/memory.db"

[llm]
default_provider = "openrouter"
`
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := resolveDBPath(cfgPath)
	want := "/custom/path/memory.db"
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_MissingMemorySection(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `[llm]
default_provider = "openrouter"
`
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := resolveDBPath(cfgPath)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".denkeeper", "data", "memory.db")
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(`not valid toml {{{{`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := resolveDBPath(cfgPath)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".denkeeper", "data", "memory.db")
	if got != want {
		t.Errorf("resolveDBPath = %q, want default %q", got, want)
	}
}
