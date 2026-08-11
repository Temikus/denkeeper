package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDBPath_NoConfigFile(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")

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
	t.Setenv("DENKEEPER_DATA_DIR", "")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")

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

// writeCfg writes a config file into a temp dir and returns its path.
func writeCfg(t *testing.T, content string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfgPath
}

func TestResolveDBPath_DBPathWinsOverDataDir(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")

	cfgPath := writeCfg(t, `data_dir = "/srv/denkeeper"

[memory]
db_path = "/custom/path/memory.db"
`)

	got := resolveDBPath(cfgPath)
	want := "/custom/path/memory.db"
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_DataDirUsedWhenDBPathAbsent(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")

	cfgPath := writeCfg(t, `data_dir = "/srv/denkeeper"

[llm]
default_provider = "openrouter"
`)

	got := resolveDBPath(cfgPath)
	want := filepath.Join("/srv/denkeeper", "data", "memory.db")
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_EnvDataDirWinsOverConfigDataDir(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("DENKEEPER_DATA_DIR", envDir)
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")

	cfgPath := writeCfg(t, `data_dir = "/srv/denkeeper"

[llm]
default_provider = "openrouter"
`)

	got := resolveDBPath(cfgPath)
	want := filepath.Join(envDir, "data", "memory.db")
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_EnvDBPathWinsOverAll(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", t.TempDir())
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "/env/memory.db")

	cfgPath := writeCfg(t, `data_dir = "/srv/denkeeper"

[memory]
db_path = "/custom/path/memory.db"
`)

	got := resolveDBPath(cfgPath)
	want := "/env/memory.db"
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_DefaultWhenNeitherSet(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")

	cfgPath := writeCfg(t, `[llm]
default_provider = "openrouter"
`)

	got := resolveDBPath(cfgPath)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".denkeeper", "data", "memory.db")
	if got != want {
		t.Errorf("resolveDBPath = %q, want %q", got, want)
	}
}

func TestResolveDBPath_MalformedTOML(t *testing.T) {
	t.Setenv("DENKEEPER_DATA_DIR", "")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")

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
