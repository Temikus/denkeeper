package scheduler

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// discardLogger returns a no-op logger suitable for tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---------------------------------------------------------------------------
// Expression parsing tests
// ---------------------------------------------------------------------------

func TestValidateExpr_Valid(t *testing.T) {
	cases := []string{
		"@hourly",
		"@daily",
		"@midnight",
		"@weekly",
		"@monthly",
		"@yearly",
		"@annually",
		"@HOURLY", // case-insensitive
		"@every 1h",
		"@every 30m",
		"@every 1h30m",
		"@every 45s",
		"0 8 * * *",      // daily at 08:00
		"0 8 * * 1-5",    // weekdays at 08:00
		"0 18 * * 5",     // every Friday at 18:00
		"*/15 * * * *",   // every 15 minutes
		"0 0 1 1 *",      // yearly
		"0 0 1,15 * *",   // 1st and 15th of month
		"0 8-18 * * 1-5", // hourly during business hours on weekdays
		"0 0 * * 7",      // Sunday (using 7 as alias)
	}
	for _, expr := range cases {
		if err := ValidateExpr(expr); err != nil {
			t.Errorf("ValidateExpr(%q) unexpected error: %v", expr, err)
		}
	}
}

func TestValidateExpr_Invalid(t *testing.T) {
	cases := []struct {
		expr    string
		wantErr string
	}{
		{"", "must not be empty"},
		{"@every", "shorthand"},     // missing duration — falls through to unknown shorthand
		{"@every -1h", "positive"},  // negative duration
		{"@every 0s", "positive"},   // zero duration
		{"@every abc", "invalid"},   // bad duration string
		{"@unknown", "unknown"},     // unrecognised shorthand
		{"* * * *", "5 fields"},     // too few fields
		{"* * * * * *", "5 fields"}, // too many fields
		{"60 * * * *", "out of"},    // minute > 59
		{"* 24 * * *", "out of"},    // hour > 23
		{"* * 0 * *", "out of"},     // dom < 1
		{"* * 32 * *", "out of"},    // dom > 31
		{"* * * 0 *", "out of"},     // month < 1
		{"* * * 13 *", "out of"},    // month > 12
		{"* * * * 8", "out of"},     // dow > 7
		{"* * * * abc", "invalid"},  // non-integer dow
		{"a * * * *", "invalid"},    // non-integer minute
	}
	for _, tc := range cases {
		err := ValidateExpr(tc.expr)
		if err == nil {
			t.Errorf("ValidateExpr(%q): expected error containing %q, got nil", tc.expr, tc.wantErr)
			continue
		}
		if tc.wantErr != "" && !containsStr(err.Error(), tc.wantErr) {
			t.Errorf("ValidateExpr(%q): error %q does not contain %q", tc.expr, err.Error(), tc.wantErr)
		}
	}
}

// ---------------------------------------------------------------------------
// cronSpec matching tests
// ---------------------------------------------------------------------------

func TestCronSpec_Matches(t *testing.T) {
	spec, err := parseCronSpec("0 8 * * 1-5")
	if err != nil {
		t.Fatalf("parseCronSpec: %v", err)
	}

	// Monday 08:00 UTC — should match.
	mon8 := time.Date(2024, 1, 8, 8, 0, 0, 0, time.UTC) // Jan 8 2024 is a Monday
	if !spec.matches(mon8, time.UTC) {
		t.Error("expected match for Monday 08:00")
	}

	// Monday 08:01 — should not match (minute ≠ 0).
	if spec.matches(mon8.Add(time.Minute), time.UTC) {
		t.Error("unexpected match for Monday 08:01")
	}

	// Saturday 08:00 — should not match (weekend).
	sat8 := time.Date(2024, 1, 13, 8, 0, 0, 0, time.UTC) // Jan 13 2024 is a Saturday
	if spec.matches(sat8, time.UTC) {
		t.Error("unexpected match for Saturday 08:00")
	}
}

func TestCronSpec_Next(t *testing.T) {
	spec, err := parseCronSpec("0 9 * * *")
	if err != nil {
		t.Fatalf("parseCronSpec: %v", err)
	}
	// Next after 08:00 should be 09:00 the same day.
	after := time.Date(2024, 6, 1, 8, 0, 0, 0, time.UTC)
	want := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	got := spec.next(after, time.UTC)
	if !got.Equal(want) {
		t.Errorf("next = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Scheduler tests
// ---------------------------------------------------------------------------

func TestScheduler_Register(t *testing.T) {
	s := New(discardLogger(), nil)

	err := s.Register(Config{
		Name:     "job1",
		Type:     "system",
		Schedule: "@every 1h",
		Enabled:  true,
		Tags:     []string{"system", "health"},
	}, func(Entry) {})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	entries := s.Entries()
	if len(entries) != 1 {
		t.Fatalf("Entries() len = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Name != "job1" {
		t.Errorf("Name = %q, want job1", e.Name)
	}
	if e.Type != ScheduleTypeSystem {
		t.Errorf("Type = %q, want %q", e.Type, ScheduleTypeSystem)
	}
	if len(e.Tags) != 2 || e.Tags[0] != "system" {
		t.Errorf("Tags = %v, want [system health]", e.Tags)
	}
	if e.NextRun.IsZero() {
		t.Error("NextRun should be set for an enabled entry")
	}
}

func TestScheduler_Register_Duplicate(t *testing.T) {
	s := New(discardLogger(), nil)

	cfg := Config{Name: "dup", Type: "agent", Schedule: "@daily", Enabled: true}
	if err := s.Register(cfg, func(Entry) {}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := s.Register(cfg, func(Entry) {}); err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestScheduler_Register_InvalidExpr(t *testing.T) {
	s := New(discardLogger(), nil)

	err := s.Register(Config{
		Name:     "bad",
		Type:     "agent",
		Schedule: "@every notaduration",
		Enabled:  true,
	}, func(Entry) {})
	if err == nil {
		t.Fatal("expected error for invalid expression, got nil")
	}
}

func TestScheduler_DisabledEntry_NotFired(t *testing.T) {
	s := New(discardLogger(), nil)

	fired := make(chan struct{}, 1)
	if err := s.Register(Config{
		Name:     "disabled",
		Type:     "agent",
		Schedule: "@every 10ms",
		Enabled:  false,
	}, func(Entry) { fired <- struct{}{} }); err != nil {
		t.Fatalf("Register: %v", err)
	}

	s.Start()
	defer s.Stop()

	select {
	case <-fired:
		t.Fatal("disabled schedule should not fire")
	case <-time.After(100 * time.Millisecond):
		// expected: no fire
	}

	entries := s.Entries()
	if !entries[0].NextRun.IsZero() {
		t.Error("disabled entry should have zero NextRun")
	}
}

func TestScheduler_IntervalFires(t *testing.T) {
	s := New(discardLogger(), nil)

	fired := make(chan Entry, 1)
	if err := s.Register(Config{
		Name:     "ticker",
		Type:     "system",
		Schedule: "@every 20ms",
		Enabled:  true,
		Tags:     []string{"test"},
	}, func(e Entry) {
		select {
		case fired <- e:
		default:
		}
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	s.Start()
	defer s.Stop()

	select {
	case e := <-fired:
		if e.Name != "ticker" {
			t.Errorf("Name = %q, want ticker", e.Name)
		}
		if e.Type != ScheduleTypeSystem {
			t.Errorf("Type = %q, want system", e.Type)
		}
		if len(e.Tags) != 1 || e.Tags[0] != "test" {
			t.Errorf("Tags = %v, want [test]", e.Tags)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("interval schedule did not fire within 500ms")
	}
}

func TestScheduler_EntriesByType(t *testing.T) {
	s := New(discardLogger(), nil)

	_ = s.Register(Config{Name: "sys1", Type: "system", Schedule: "@hourly", Enabled: true}, func(Entry) {})
	_ = s.Register(Config{Name: "sys2", Type: "system", Schedule: "@daily", Enabled: false}, func(Entry) {})
	_ = s.Register(Config{Name: "agent1", Type: "agent", Schedule: "@weekly", Enabled: true}, func(Entry) {})

	sysEntries := s.SystemEntries()
	if len(sysEntries) != 2 {
		t.Errorf("SystemEntries() = %d, want 2", len(sysEntries))
	}

	agentEntries := s.AgentEntries()
	if len(agentEntries) != 1 {
		t.Errorf("AgentEntries() = %d, want 1", len(agentEntries))
	}
	if agentEntries[0].Name != "agent1" {
		t.Errorf("AgentEntries()[0].Name = %q, want agent1", agentEntries[0].Name)
	}
}

func TestScheduler_EntriesByTag(t *testing.T) {
	s := New(discardLogger(), nil)

	_ = s.Register(Config{Name: "j1", Type: "system", Schedule: "@hourly", Enabled: true, Tags: []string{"morning", "briefing"}}, func(Entry) {})
	_ = s.Register(Config{Name: "j2", Type: "agent", Schedule: "@daily", Enabled: true, Tags: []string{"morning"}}, func(Entry) {})
	_ = s.Register(Config{Name: "j3", Type: "agent", Schedule: "@weekly", Enabled: true, Tags: []string{"weekend"}}, func(Entry) {})

	morning := s.EntriesByTag("morning")
	if len(morning) != 2 {
		t.Errorf("EntriesByTag(morning) = %d, want 2", len(morning))
	}

	weekend := s.EntriesByTag("weekend")
	if len(weekend) != 1 || weekend[0].Name != "j3" {
		t.Errorf("EntriesByTag(weekend) = %v, want [{Name:j3}]", weekend)
	}

	if got := s.EntriesByTag("nonexistent"); len(got) != 0 {
		t.Errorf("EntriesByTag(nonexistent) = %v, want []", got)
	}
}

func TestScheduler_MultipleFiresUpdateLastRun(t *testing.T) {
	s := New(discardLogger(), nil)

	count := make(chan struct{}, 5)
	if err := s.Register(Config{
		Name:     "fast",
		Type:     "agent",
		Schedule: "@every 15ms",
		Enabled:  true,
	}, func(Entry) { count <- struct{}{} }); err != nil {
		t.Fatalf("Register: %v", err)
	}

	s.Start()
	defer s.Stop()

	// Wait for at least 2 fires.
	for i := 0; i < 2; i++ {
		select {
		case <-count:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("fire %d did not arrive within timeout", i+1)
		}
	}

	entries := s.Entries()
	if entries[0].LastRun.IsZero() {
		t.Error("LastRun should be non-zero after firing")
	}
}

// ---------------------------------------------------------------------------
// RegisterAndStart tests
// ---------------------------------------------------------------------------

func TestScheduler_RegisterAndStart_Fires(t *testing.T) {
	s := New(discardLogger(), nil)
	s.Start()
	defer s.Stop()

	fired := make(chan Entry, 1)
	err := s.RegisterAndStart(Config{
		Name:     "hot-add",
		Type:     "agent",
		Schedule: "@every 20ms",
		Enabled:  true,
	}, func(e Entry) {
		select {
		case fired <- e:
		default:
		}
	})
	if err != nil {
		t.Fatalf("RegisterAndStart: %v", err)
	}

	select {
	case e := <-fired:
		if e.Name != "hot-add" {
			t.Errorf("Name = %q, want hot-add", e.Name)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RegisterAndStart entry did not fire within 500ms")
	}
}

func TestScheduler_RegisterAndStart_Disabled(t *testing.T) {
	s := New(discardLogger(), nil)
	s.Start()
	defer s.Stop()

	fired := make(chan struct{}, 1)
	err := s.RegisterAndStart(Config{
		Name:     "disabled-hot",
		Type:     "agent",
		Schedule: "@every 10ms",
		Enabled:  false,
	}, func(Entry) { fired <- struct{}{} })
	if err != nil {
		t.Fatalf("RegisterAndStart: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("disabled entry should not fire")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

func TestScheduler_RegisterAndStart_Duplicate(t *testing.T) {
	s := New(discardLogger(), nil)
	s.Start()
	defer s.Stop()

	_ = s.RegisterAndStart(Config{Name: "dup", Type: "agent", Schedule: "@daily", Enabled: true}, func(Entry) {})
	err := s.RegisterAndStart(Config{Name: "dup", Type: "agent", Schedule: "@daily", Enabled: true}, func(Entry) {})
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

// ---------------------------------------------------------------------------
// Unregister / GetEntry tests
// ---------------------------------------------------------------------------

func TestScheduler_GetEntry(t *testing.T) {
	s := New(discardLogger(), nil)

	_ = s.Register(Config{Name: "j1", Type: "agent", Schedule: "@daily", Enabled: true, Skill: "greet"}, func(Entry) {})

	e, ok := s.GetEntry("j1")
	if !ok {
		t.Fatal("GetEntry returned false for existing entry")
	}
	if e.Name != "j1" || e.Skill != "greet" {
		t.Errorf("GetEntry = %+v, want name=j1, skill=greet", e)
	}

	_, ok = s.GetEntry("nonexistent")
	if ok {
		t.Error("GetEntry returned true for nonexistent entry")
	}
}

func TestScheduler_Unregister(t *testing.T) {
	s := New(discardLogger(), nil)

	_ = s.Register(Config{Name: "removable", Type: "agent", Schedule: "@every 1h", Enabled: true}, func(Entry) {})

	if err := s.Unregister("removable"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if len(s.Entries()) != 0 {
		t.Error("expected 0 entries after Unregister")
	}
}

func TestScheduler_Unregister_NotFound(t *testing.T) {
	s := New(discardLogger(), nil)

	if err := s.Unregister("ghost"); err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestScheduler_Unregister_StopsRunningEntry(t *testing.T) {
	s := New(discardLogger(), nil)

	fired := make(chan struct{}, 10)
	_ = s.Register(Config{Name: "fast", Type: "agent", Schedule: "@every 15ms", Enabled: true}, func(Entry) {
		fired <- struct{}{}
	})

	s.Start()
	defer s.Stop()

	// Wait for at least one fire.
	select {
	case <-fired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("schedule did not fire")
	}

	// Unregister — the goroutine should stop.
	if err := s.Unregister("fast"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	// Drain any pending fires.
	time.Sleep(50 * time.Millisecond)
	for len(fired) > 0 {
		<-fired
	}

	// No more fires should arrive.
	select {
	case <-fired:
		t.Error("schedule fired after Unregister")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsStr(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
