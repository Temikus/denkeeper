// Package scheduler provides a cron-style task scheduler with support for
// named intervals (@daily, @every 5m), 5-field cron expressions, typed
// schedule categories, and freeform tags.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ScheduleType classifies the priority and security context of a schedule.
type ScheduleType string

const (
	// ScheduleTypeSystem is for core system tasks: agent heartbeats, memory
	// cleanup, cost resets, etc. System schedules run with elevated priority
	// and should not be modifiable by the agent without explicit permission.
	ScheduleTypeSystem ScheduleType = "system"

	// ScheduleTypeAgent is for user-configured scheduled agent skill runs.
	// These are created and managed through normal configuration.
	ScheduleTypeAgent ScheduleType = "agent"
)

// Config holds the configuration for a single schedule entry.
// It is populated from the application's main config and passed to Register.
type Config struct {
	// Name is a unique identifier for this schedule.
	Name string

	// Type classifies the schedule as "system" or "agent".
	Type string

	// Schedule is the timing expression. Supported formats:
	//   @hourly, @daily, @midnight, @weekly, @monthly, @yearly, @annually
	//   @every <duration>  (e.g. @every 5m, @every 1h30m)
	//   5-field cron:      "0 8 * * 1-5"
	Schedule string

	// Skill is the name of the skill to invoke when this schedule fires.
	Skill string

	// SessionTier is the permission tier for the session spawned on each run.
	SessionTier string

	// Channel is the adapter channel to deliver results to (e.g. "telegram:123456").
	Channel string

	// SessionMode controls which conversation context is used on each run.
	// "shared": reuses the channel's existing conversation history (default).
	// "isolated": creates a fresh conversation for each run.
	SessionMode string

	// Tags are freeform labels for organizing and filtering schedules.
	Tags []string

	// Enabled controls whether this schedule runs. Disabled schedules are
	// registered but their goroutines are never started.
	Enabled bool
}

// Entry is an immutable snapshot of a schedule entry and its runtime state.
// It is passed to JobFunc and returned by the Entries methods.
type Entry struct {
	Name        string
	Type        ScheduleType
	Expr        string // original schedule expression string
	Skill       string
	SessionTier string
	SessionMode string
	Channel     string
	Tags        []string
	Enabled     bool
	LastRun     time.Time // zero if never fired
	NextRun     time.Time // zero if disabled or indeterminate
}

// JobFunc is the function called each time a schedule fires.
// It receives a read-only snapshot of the triggering entry.
type JobFunc func(entry Entry)

// Scheduler runs registered schedules concurrently.
// System and agent schedules are tracked separately to allow priority
// enforcement and selective querying.
type Scheduler struct {
	mu      sync.RWMutex
	entries map[string]*internalEntry
	logger  *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type internalEntry struct {
	Entry
	job    JobFunc
	expr   *parsedExpr
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a Scheduler. All schedule times are interpreted in UTC.
func New(logger *slog.Logger) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		entries: make(map[string]*internalEntry),
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Register adds a schedule entry with its associated job function.
//
// The schedule expression is validated immediately; an error is returned for
// invalid expressions or duplicate names. Disabled entries are stored but
// their goroutines are not started.
//
// Register may be called before or after Start. Entries added after Start
// are not automatically activated — Stop and re-Start, or register before
// the first Start.
func (s *Scheduler) Register(cfg Config, job JobFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.entries[cfg.Name]; exists {
		return fmt.Errorf("scheduler: duplicate schedule name %q", cfg.Name)
	}

	expr, err := parseScheduleExpr(cfg.Schedule)
	if err != nil {
		return fmt.Errorf("scheduler: schedule %q: %w", cfg.Name, err)
	}

	entryCtx, entryCancel := context.WithCancel(s.ctx) // #nosec G118 -- cancel is stored in entry.cancel and called in Unregister/Stop
	e := &internalEntry{
		Entry: Entry{
			Name:        cfg.Name,
			Type:        ScheduleType(cfg.Type),
			Expr:        cfg.Schedule,
			Skill:       cfg.Skill,
			SessionTier: cfg.SessionTier,
			SessionMode: cfg.SessionMode,
			Channel:     cfg.Channel,
			Tags:        cfg.Tags,
			Enabled:     cfg.Enabled,
		},
		job:    job,
		expr:   expr,
		ctx:    entryCtx,
		cancel: entryCancel,
	}

	if cfg.Enabled {
		now := time.Now().UTC()
		switch expr.kind {
		case kindInterval:
			e.NextRun = now.Add(expr.interval)
		case kindCron:
			e.NextRun = expr.cron.next(now)
		}
	}

	s.entries[cfg.Name] = e
	return nil
}

// RegisterAndStart registers a new schedule entry and immediately starts its
// goroutine if the entry is enabled. Unlike Register, this is safe to call
// after Start has been invoked — the goroutine is spawned inline rather than
// waiting for the next Start call.
func (s *Scheduler) RegisterAndStart(cfg Config, job JobFunc) error {
	s.mu.Lock()

	if _, exists := s.entries[cfg.Name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: duplicate schedule name %q", cfg.Name)
	}

	expr, err := parseScheduleExpr(cfg.Schedule)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: schedule %q: %w", cfg.Name, err)
	}

	entryCtx, entryCancel := context.WithCancel(s.ctx) // #nosec G118 -- cancel is stored in entry.cancel and called in Unregister/Stop
	e := &internalEntry{
		Entry: Entry{
			Name:        cfg.Name,
			Type:        ScheduleType(cfg.Type),
			Expr:        cfg.Schedule,
			Skill:       cfg.Skill,
			SessionTier: cfg.SessionTier,
			SessionMode: cfg.SessionMode,
			Channel:     cfg.Channel,
			Tags:        cfg.Tags,
			Enabled:     cfg.Enabled,
		},
		job:    job,
		expr:   expr,
		ctx:    entryCtx,
		cancel: entryCancel,
	}

	if cfg.Enabled {
		now := time.Now().UTC()
		switch expr.kind {
		case kindInterval:
			e.NextRun = now.Add(expr.interval)
		case kindCron:
			e.NextRun = expr.cron.next(now)
		}
	}

	s.entries[cfg.Name] = e
	s.mu.Unlock()

	if cfg.Enabled {
		s.wg.Add(1)
		go s.runEntry(e)
		s.logger.Info("schedule registered and started", "name", cfg.Name)
	} else {
		s.logger.Info("schedule registered (disabled)", "name", cfg.Name)
	}

	return nil
}

// Start launches goroutines for all enabled entries.
// Calling Start on an already-running Scheduler is safe but will spawn
// duplicate goroutines for already-active entries.
func (s *Scheduler) Start() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	active := 0
	for _, e := range s.entries {
		if !e.Enabled {
			continue
		}
		s.wg.Add(1)
		go s.runEntry(e)
		active++
	}
	s.logger.Info("scheduler started", "total_entries", len(s.entries), "active_entries", active)
}

// Stop signals all schedule goroutines to exit and blocks until they finish.
func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
	s.logger.Info("scheduler stopped")
}

// Entries returns a snapshot of all registered entries (enabled and disabled).
func (s *Scheduler) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e.Entry)
	}
	return out
}

// SystemEntries returns a snapshot of entries with Type == ScheduleTypeSystem.
func (s *Scheduler) SystemEntries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Entry
	for _, e := range s.entries {
		if e.Type == ScheduleTypeSystem {
			out = append(out, e.Entry)
		}
	}
	return out
}

// AgentEntries returns a snapshot of entries with Type == ScheduleTypeAgent.
func (s *Scheduler) AgentEntries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Entry
	for _, e := range s.entries {
		if e.Type == ScheduleTypeAgent {
			out = append(out, e.Entry)
		}
	}
	return out
}

// EntriesByTag returns entries that carry the given tag.
func (s *Scheduler) EntriesByTag(tag string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Entry
	for _, e := range s.entries {
		for _, t := range e.Tags {
			if t == tag {
				out = append(out, e.Entry)
				break
			}
		}
	}
	return out
}

// GetEntry returns a snapshot of the named entry and true, or a zero value and
// false if not found.
func (s *Scheduler) GetEntry(name string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[name]
	if !ok {
		return Entry{}, false
	}
	return e.Entry, true
}

// Unregister stops and removes a named schedule entry. If the entry is running,
// its goroutine is cancelled asynchronously. Returns an error if the name is
// not found.
func (s *Scheduler) Unregister(name string) error {
	s.mu.Lock()
	e, exists := s.entries[name]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: schedule %q not found", name)
	}
	if e.cancel != nil {
		e.cancel()
	}
	delete(s.entries, name)
	s.mu.Unlock()
	s.logger.Info("schedule unregistered", "name", name)
	return nil
}

// ---------------------------------------------------------------------------
// Internal run loops
// ---------------------------------------------------------------------------

func (s *Scheduler) runEntry(e *internalEntry) {
	defer s.wg.Done()
	switch e.expr.kind {
	case kindInterval:
		s.runInterval(e)
	case kindCron:
		s.runCron(e)
	}
}

func (s *Scheduler) runInterval(e *internalEntry) {
	ticker := time.NewTicker(e.expr.interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case t := <-ticker.C:
			now := t.UTC()
			s.mu.Lock()
			e.LastRun = now
			e.NextRun = now.Add(e.expr.interval)
			snapshot := e.Entry
			job := e.job
			s.mu.Unlock()

			s.logFire(snapshot)
			job(snapshot)
		}
	}
}

func (s *Scheduler) runCron(e *internalEntry) {
	// Cron expressions have minute-level granularity. We tick every minute
	// and check whether the current (truncated) minute matches the spec.
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	var lastFiredMinute time.Time

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			minuteStart := now.Truncate(time.Minute)

			if minuteStart.Equal(lastFiredMinute) {
				continue // already fired in this minute
			}
			if !e.expr.cron.matches(minuteStart) {
				continue
			}

			lastFiredMinute = minuteStart

			s.mu.Lock()
			e.LastRun = minuteStart
			e.NextRun = e.expr.cron.next(minuteStart)
			snapshot := e.Entry
			job := e.job
			s.mu.Unlock()

			s.logFire(snapshot)
			job(snapshot)
		}
	}
}

func (s *Scheduler) logFire(e Entry) {
	s.logger.Info("schedule fired",
		"name", e.Name,
		"type", e.Type,
		"skill", e.Skill,
		"tags", e.Tags,
	)
}
