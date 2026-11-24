package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// ReviewMark implements Store.
func (s *SQLiteStore) ReviewMark(ctx context.Context, agent, decider string) (ReviewMark, bool, error) {
	var (
		ns              int64
		by, updatedText string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT reviewed_up_to_ns, reviewed_by, updated_at FROM decider_review_marks WHERE agent = ? AND decider = ?`,
		agent, decider).Scan(&ns, &by, &updatedText)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewMark{}, false, nil
	}
	if err != nil {
		return ReviewMark{}, false, fmt.Errorf("reading review mark: %w", err)
	}
	updated, _ := time.Parse(time.RFC3339Nano, updatedText)
	return ReviewMark{
		Agent: agent, Decider: decider, ReviewedUpTo: time.Unix(0, ns).UTC(),
		ReviewedBy: by, UpdatedAt: updated,
	}, true, nil
}

// ErrReviewMarkOutOfRange is returned for a mark time UnixNano cannot hold.
var ErrReviewMarkOutOfRange = errors.New("review mark time out of range")

// Marks are stored as Unix nanoseconds, which hold 1677-09-21 to 2262-04-11.
var (
	minReviewMark = time.Unix(0, math.MinInt64)
	maxReviewMark = time.Unix(0, math.MaxInt64)
)

// ReviewMarkInRange reports whether t can be stored as a mark. Outside the
// range UnixNano wraps around, so 1000 AD would store as about 2169 and the
// forward-only upsert would then refuse every real mark.
func ReviewMarkInRange(t time.Time) bool {
	return !t.Before(minReviewMark) && !t.After(maxReviewMark)
}

// SetReviewMark implements Store. The upsert only ever moves the mark forward.
func (s *SQLiteStore) SetReviewMark(ctx context.Context, m ReviewMark) (ReviewMark, error) {
	if !ReviewMarkInRange(m.ReviewedUpTo) {
		return ReviewMark{}, fmt.Errorf("storing review mark at %s: %w", m.ReviewedUpTo.Format(time.RFC3339), ErrReviewMarkOutOfRange)
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO decider_review_marks (agent, decider, reviewed_up_to_ns, reviewed_by, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent, decider) DO UPDATE SET
			reviewed_up_to_ns = excluded.reviewed_up_to_ns,
			reviewed_by = excluded.reviewed_by,
			updated_at = excluded.updated_at
		WHERE excluded.reviewed_up_to_ns > decider_review_marks.reviewed_up_to_ns`,
		m.Agent, m.Decider, m.ReviewedUpTo.UnixNano(), m.ReviewedBy, m.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ReviewMark{}, fmt.Errorf("storing review mark: %w", err)
	}
	stored, _, err := s.ReviewMark(ctx, m.Agent, m.Decider)
	return stored, err
}
