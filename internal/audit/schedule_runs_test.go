package audit

import (
	"context"
	"testing"
	"time"
)

var runsFrom = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)

func runsStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// scheduledTrigger is the event the engine emits at the start of a scheduled turn.
func scheduledTrigger(agent, schedule string, at time.Time) Event {
	return Event{
		Timestamp: at, Category: CategorySession, Action: "trigger", Agent: agent,
		Summary: "trigger", Status: StatusOK, Source: "scheduler",
		Detail: `{"trigger_type":"schedule","schedule_name":"` + schedule + `"}`,
	}
}

func insertEvents(t *testing.T, s *SQLiteStore, events ...Event) {
	t.Helper()
	for _, e := range events {
		if err := s.Insert(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
}

func scheduledRuns(t *testing.T, s *SQLiteStore, agent string) map[string]ScheduledRunCount {
	t.Helper()
	got, err := s.ScheduledRuns(context.Background(), agent, runsFrom, runsFrom.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("ScheduledRuns: %v", err)
	}
	out := map[string]ScheduledRunCount{}
	for _, r := range got {
		out[r.Schedule] = r
	}
	return out
}

func TestScheduledRuns_CountsPerSchedule(t *testing.T) {
	s := runsStore(t)
	h := time.Hour
	insertEvents(t, s,
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(h)),
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(5*h)),
		scheduledTrigger("pamela", "digest", runsFrom.Add(3*h)),
	)
	got := scheduledRuns(t, s, "pamela")
	hb := got["heartbeat"]
	if hb.Runs != 2 || !hb.First.Equal(runsFrom.Add(h)) || !hb.Last.Equal(runsFrom.Add(5*h)) {
		t.Fatalf("heartbeat = %+v", hb)
	}
	if d := got["digest"]; d.Runs != 1 || !d.First.Equal(runsFrom.Add(3*h)) {
		t.Fatalf("digest = %+v", d)
	}
	if len(got) != 2 {
		t.Fatalf("got %d schedules, want 2", len(got))
	}
}

func TestScheduledRuns_OnlyThisAgentInsideTheWindow(t *testing.T) {
	s := runsStore(t)
	insertEvents(t, s,
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(time.Hour)),
		scheduledTrigger("argus", "heartbeat", runsFrom.Add(time.Hour)),
		scheduledTrigger("pamela#dryrun", "heartbeat", runsFrom.Add(time.Hour)),
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(-time.Minute)),
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(7*24*time.Hour)),
	)
	if hb := scheduledRuns(t, s, "pamela")["heartbeat"]; hb.Runs != 1 {
		t.Fatalf("heartbeat runs = %d, want 1", hb.Runs)
	}
}

// Timestamps are stored as RFC3339Nano, which drops trailing zeros, so as
// text ".5Z" sorts before "Z". The window must compare instants.
func TestScheduledRuns_SubSecondBoundaries(t *testing.T) {
	s := runsStore(t)
	half := 500 * time.Millisecond
	insertEvents(t, s,
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(half)),
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(7*24*time.Hour+half)),
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(-half)),
	)
	hb := scheduledRuns(t, s, "pamela")["heartbeat"]
	if hb.Runs != 1 || !hb.First.Equal(runsFrom.Add(half)) {
		t.Fatalf("heartbeat = %+v, want only the from+500ms run", hb)
	}
}

func TestScheduledRuns_FirstAndLastWithinOneSecond(t *testing.T) {
	s := runsStore(t)
	at := runsFrom.Add(time.Hour)
	insertEvents(t, s,
		scheduledTrigger("pamela", "heartbeat", at.Add(500*time.Millisecond)),
		scheduledTrigger("pamela", "heartbeat", at),
	)
	hb := scheduledRuns(t, s, "pamela")["heartbeat"]
	if hb.Runs != 2 || !hb.First.Equal(at) || !hb.Last.Equal(at.Add(500*time.Millisecond)) {
		t.Fatalf("heartbeat = %+v, want first %v and last %v", hb, at, at.Add(500*time.Millisecond))
	}
}

// Insert keeps the event's own offset, so the window must not compare text
// across offsets.
func TestScheduledRuns_NonUTCTimestamps(t *testing.T) {
	s := runsStore(t)
	sydney := time.FixedZone("AEST", 10*60*60)
	insertEvents(t, s,
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(-30*time.Minute).In(sydney)),
		scheduledTrigger("pamela", "heartbeat", runsFrom.Add(time.Hour).In(sydney)),
	)
	hb := scheduledRuns(t, s, "pamela")["heartbeat"]
	if hb.Runs != 1 || !hb.First.Equal(runsFrom.Add(time.Hour)) {
		t.Fatalf("heartbeat = %+v, want only the in-window run", hb)
	}
}

func TestScheduledRuns_IgnoresUserTriggersAndFireEvents(t *testing.T) {
	s := runsStore(t)
	user := scheduledTrigger("pamela", "heartbeat", runsFrom.Add(time.Hour))
	user.Source = "telegram"
	fire := scheduledTrigger("pamela", "heartbeat", runsFrom.Add(time.Hour))
	fire.Category, fire.Action = CategorySchedule, "fire"
	insertEvents(t, s, user, fire)
	if got := scheduledRuns(t, s, "pamela"); len(got) != 0 {
		t.Fatalf("got %+v, want nothing", got)
	}
}

func TestScheduledRuns_SkipsNonJSONDetail(t *testing.T) {
	s := runsStore(t)
	bad := scheduledTrigger("pamela", "heartbeat", runsFrom.Add(time.Hour))
	bad.Detail = "not json"
	insertEvents(t, s, bad, scheduledTrigger("pamela", "heartbeat", runsFrom.Add(2*time.Hour)))
	if hb := scheduledRuns(t, s, "pamela")["heartbeat"]; hb.Runs != 1 {
		t.Fatalf("heartbeat runs = %d, want 1", hb.Runs)
	}
}

func TestScheduledRuns_EmptyAgentIsError(t *testing.T) {
	if _, err := runsStore(t).ScheduledRuns(context.Background(), "", runsFrom, runsFrom.Add(time.Hour)); err == nil {
		t.Fatal("expected an error for an empty agent")
	}
}
