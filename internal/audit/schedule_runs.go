package audit

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ScheduledRunCount is how many scheduled turns an agent started for one
// schedule, and when the first and last of them started.
type ScheduledRunCount struct {
	Schedule    string
	Runs        int
	First, Last time.Time
}

// ScheduledRuns counts the agent's scheduled turns in [from, to), per
// schedule, from the engine's session/trigger events (source "scheduler",
// detail.schedule_name). The agent match is exact, so dry-run and eval turns
// ("<agent>#dryrun") are left out. Rows whose detail is not JSON are skipped.
// An empty agent is an error: there is no all-agents form.
func (s *SQLiteStore) ScheduledRuns(ctx context.Context, agent string, from, to time.Time) ([]ScheduledRunCount, error) {
	if agent == "" {
		return nil, errors.New("audit scheduled runs: agent is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT json_extract(detail, '$.schedule_name') AS schedule, COUNT(*), MIN(timestamp), MAX(timestamp)
		FROM audit_events
		WHERE agent = ? AND category = ? AND action = 'trigger' AND source = 'scheduler'
		  AND timestamp >= ? AND timestamp < ? AND json_valid(detail)
		  AND json_extract(detail, '$.schedule_name') IS NOT NULL
		GROUP BY schedule
		ORDER BY schedule`,
		agent, CategorySession, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("audit scheduled runs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := []ScheduledRunCount{}
	for rows.Next() {
		var r ScheduledRunCount
		var first, last string
		if err := rows.Scan(&r.Schedule, &r.Runs, &first, &last); err != nil {
			return nil, fmt.Errorf("audit scheduled runs: %w", err)
		}
		r.First, _ = time.Parse(time.RFC3339Nano, first)
		r.Last, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit scheduled runs: %w", err)
	}
	return out, nil
}
