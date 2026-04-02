package browser

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrProfileNotFound is returned when a requested agent profile directory
// does not exist.
var ErrProfileNotFound = errors.New("browser: profile not found")

// ProfileInfo describes a single agent's browser profile on disk.
type ProfileInfo struct {
	Agent       string    `json:"agent"`
	SizeBytes   int64     `json:"size_bytes"`
	DomainCount int       `json:"domain_count"`
	Domains     []string  `json:"domains,omitempty"`
	LastUsed    time.Time `json:"last_used"`
}

// ProfileService provides read and mutation operations on per-agent browser
// profile directories stored under a shared base directory.
type ProfileService struct {
	baseDir string
	logger  *slog.Logger
}

// NewProfileService creates a ProfileService rooted at baseDir.
// baseDir must be an absolute path.
func NewProfileService(baseDir string, logger *slog.Logger) *ProfileService {
	return &ProfileService{baseDir: baseDir, logger: logger}
}

// BaseDir returns the base directory containing all agent profiles.
func (ps *ProfileService) BaseDir() string { return ps.baseDir }

// List returns profile info for every agent that has a profile directory.
func (ps *ProfileService) List(_ context.Context) ([]ProfileInfo, error) {
	entries, err := os.ReadDir(ps.baseDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []ProfileInfo{}, nil
		}
		return nil, fmt.Errorf("listing browser profiles: %w", err)
	}

	var profiles []ProfileInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, infoErr := ps.infoForDir(e.Name())
		if infoErr != nil {
			ps.logger.Warn("skipping browser profile", "agent", e.Name(), "error", infoErr)
			continue
		}
		profiles = append(profiles, *info)
	}
	if profiles == nil {
		profiles = []ProfileInfo{}
	}
	return profiles, nil
}

// Info returns detailed information about a specific agent's browser profile.
func (ps *ProfileService) Info(_ context.Context, agent string) (*ProfileInfo, error) {
	if err := validateAgentName(agent); err != nil {
		return nil, err
	}
	return ps.infoForDir(agent)
}

// Clear removes all files inside an agent's profile directory but keeps the
// directory itself so the browser container can immediately reuse it.
func (ps *ProfileService) Clear(_ context.Context, agent string) error {
	if err := validateAgentName(agent); err != nil {
		return err
	}
	agentDir := filepath.Join(ps.baseDir, agent)
	if _, err := os.Stat(agentDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrProfileNotFound
		}
		return fmt.Errorf("checking browser profile dir: %w", err)
	}
	if err := os.RemoveAll(agentDir); err != nil {
		return fmt.Errorf("clearing browser profile: %w", err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("recreating browser profile dir: %w", err)
	}
	ps.logger.Info("browser profile cleared", "agent", agent)
	return nil
}

// Delete removes an agent's profile directory entirely.
func (ps *ProfileService) Delete(_ context.Context, agent string) error {
	if err := validateAgentName(agent); err != nil {
		return err
	}
	agentDir := filepath.Join(ps.baseDir, agent)
	if _, err := os.Stat(agentDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrProfileNotFound
		}
		return fmt.Errorf("checking browser profile dir: %w", err)
	}
	if err := os.RemoveAll(agentDir); err != nil {
		return fmt.Errorf("deleting browser profile: %w", err)
	}
	ps.logger.Info("browser profile deleted", "agent", agent)
	return nil
}

// infoForDir computes size, last-used time, and cookie domains for an agent dir.
func (ps *ProfileService) infoForDir(agent string) (*ProfileInfo, error) {
	agentDir := filepath.Join(ps.baseDir, agent)
	fi, err := os.Stat(agentDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("stat browser profile: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("browser profile %q is not a directory", agent)
	}

	var totalSize int64
	var lastMod time.Time

	walkErr := filepath.WalkDir(agentDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		totalSize += info.Size()
		if info.ModTime().After(lastMod) {
			lastMod = info.ModTime()
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking browser profile %q: %w", agent, walkErr)
	}
	if lastMod.IsZero() {
		lastMod = fi.ModTime()
	}

	domains := ps.extractCookieDomains(agentDir)

	return &ProfileInfo{
		Agent:       agent,
		SizeBytes:   totalSize,
		DomainCount: len(domains),
		Domains:     domains,
		LastUsed:    lastMod,
	}, nil
}

// extractCookieDomains reads unique domains from a Chromium Cookies SQLite DB.
// Returns nil on any error (missing DB, locked, etc.).
func (ps *ProfileService) extractCookieDomains(agentDir string) []string {
	// Chromium stores cookies at Default/Cookies or directly at Cookies.
	candidates := []string{
		filepath.Join(agentDir, "Default", "Cookies"),
		filepath.Join(agentDir, "Cookies"),
	}

	var dbPath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			dbPath = c
			break
		}
	}
	if dbPath == "" {
		return nil
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_busy_timeout=1000")
	if err != nil {
		ps.logger.Debug("cannot open cookies db", "path", dbPath, "error", err)
		return nil
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query("SELECT DISTINCT host_key FROM cookies ORDER BY host_key")
	if err != nil {
		ps.logger.Debug("cannot query cookies", "path", dbPath, "error", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			continue
		}
		domains = append(domains, d)
	}
	return domains
}

// validateAgentName rejects names that could cause path traversal.
func validateAgentName(name string) error {
	if name == "" {
		return errors.New("browser: agent name is required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return errors.New("browser: invalid agent name")
	}
	return nil
}
