package browser

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestProfileService(t *testing.T) (*ProfileService, string) {
	t.Helper()
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewProfileService(dir, logger), dir
}

func TestProfileService_List_Empty(t *testing.T) {
	ps, _ := newTestProfileService(t)
	profiles, err := ps.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestProfileService_List_WithProfiles(t *testing.T) {
	ps, dir := newTestProfileService(t)

	// Create two agent profile dirs with dummy files.
	for _, agent := range []string{"alice", "bob"} {
		agentDir := filepath.Join(dir, agent)
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "data.bin"), make([]byte, 1024), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	profiles, err := ps.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}

	// Verify sizes are at least 1024 bytes each.
	for _, p := range profiles {
		if p.SizeBytes < 1024 {
			t.Errorf("agent %q: expected size >= 1024, got %d", p.Agent, p.SizeBytes)
		}
	}
}

func TestProfileService_List_NonexistentBaseDir(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ps := NewProfileService(filepath.Join(t.TempDir(), "nonexistent"), logger)

	profiles, err := ps.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestProfileService_Info_NoCookiesDB(t *testing.T) {
	ps, dir := newTestProfileService(t)
	agentDir := filepath.Join(dir, "myagent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := ps.Info(context.Background(), "myagent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Agent != "myagent" {
		t.Errorf("expected agent 'myagent', got %q", info.Agent)
	}
	if info.SizeBytes < 2 {
		t.Errorf("expected positive size, got %d", info.SizeBytes)
	}
	if info.DomainCount != 0 {
		t.Errorf("expected 0 domains, got %d", info.DomainCount)
	}
	if len(info.Domains) != 0 {
		t.Errorf("expected empty domains, got %v", info.Domains)
	}
}

func TestProfileService_Info_WithCookies(t *testing.T) {
	ps, dir := newTestProfileService(t)
	agentDir := filepath.Join(dir, "cookieagent", "Default")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Create a minimal Chromium Cookies SQLite DB.
	dbPath := filepath.Join(agentDir, "Cookies")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE cookies (
			host_key TEXT NOT NULL,
			name TEXT NOT NULL,
			value TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO cookies (host_key, name, value) VALUES
			('.github.com', 'session', 'abc'),
			('.github.com', 'token', 'xyz'),
			('.google.com', 'pref', '123')
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	info, err := ps.Info(context.Background(), "cookieagent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.DomainCount != 2 {
		t.Errorf("expected 2 domains, got %d", info.DomainCount)
	}
	// Domains should be sorted: .github.com, .google.com.
	if len(info.Domains) >= 2 {
		if info.Domains[0] != ".github.com" || info.Domains[1] != ".google.com" {
			t.Errorf("unexpected domains: %v", info.Domains)
		}
	}
}

func TestProfileService_Info_WithCookiesAtRoot(t *testing.T) {
	ps, dir := newTestProfileService(t)
	// Place Cookies DB directly in the agent dir (no Default/ subdirectory).
	agentDir := filepath.Join(dir, "rootcookies")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(agentDir, "Cookies")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE cookies (host_key TEXT NOT NULL, name TEXT, value TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO cookies (host_key, name, value) VALUES ('.example.com', 'sid', 'v')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	info, err := ps.Info(context.Background(), "rootcookies")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.DomainCount != 1 {
		t.Errorf("expected 1 domain, got %d", info.DomainCount)
	}
	if len(info.Domains) != 1 || info.Domains[0] != ".example.com" {
		t.Errorf("unexpected domains: %v", info.Domains)
	}
}

func TestProfileService_Info_NotFound(t *testing.T) {
	ps, _ := newTestProfileService(t)
	_, err := ps.Info(context.Background(), "nonexistent")
	if err != ErrProfileNotFound {
		t.Fatalf("expected ErrProfileNotFound, got %v", err)
	}
}

func TestProfileService_Clear(t *testing.T) {
	ps, dir := newTestProfileService(t)
	agentDir := filepath.Join(dir, "clearme")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "data.bin"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ps.Clear(context.Background(), "clearme"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Directory should exist but be empty.
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		t.Fatalf("directory should still exist: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty directory, got %d entries", len(entries))
	}
}

func TestProfileService_Clear_NotFound(t *testing.T) {
	ps, _ := newTestProfileService(t)
	err := ps.Clear(context.Background(), "ghost")
	if err != ErrProfileNotFound {
		t.Fatalf("expected ErrProfileNotFound, got %v", err)
	}
}

func TestProfileService_Delete(t *testing.T) {
	ps, dir := newTestProfileService(t)
	agentDir := filepath.Join(dir, "deleteme")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "data.bin"), []byte("bye"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ps.Delete(context.Background(), "deleteme"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(agentDir); !os.IsNotExist(err) {
		t.Errorf("expected directory to be removed, got err=%v", err)
	}
}

func TestProfileService_Delete_NotFound(t *testing.T) {
	ps, _ := newTestProfileService(t)
	err := ps.Delete(context.Background(), "ghost")
	if err != ErrProfileNotFound {
		t.Fatalf("expected ErrProfileNotFound, got %v", err)
	}
}

func TestProfileService_PathTraversal(t *testing.T) {
	ps, _ := newTestProfileService(t)
	ctx := context.Background()

	for _, bad := range []string{"../etc", "foo/bar", "", "a\\b"} {
		if _, err := ps.Info(ctx, bad); err == nil {
			t.Errorf("expected error for agent name %q", bad)
		}
		if err := ps.Clear(ctx, bad); err == nil {
			t.Errorf("expected error for agent name %q", bad)
		}
		if err := ps.Delete(ctx, bad); err == nil {
			t.Errorf("expected error for agent name %q", bad)
		}
	}
}

func TestProfileService_LastUsed(t *testing.T) {
	ps, dir := newTestProfileService(t)
	agentDir := filepath.Join(dir, "timeagent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fpath := filepath.Join(agentDir, "recent.txt")
	if err := os.WriteFile(fpath, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Set a known mod time.
	known := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(fpath, known, known); err != nil {
		t.Fatal(err)
	}

	info, err := ps.Info(context.Background(), "timeagent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.LastUsed.Equal(known) {
		t.Errorf("expected LastUsed %v, got %v", known, info.LastUsed)
	}
}
