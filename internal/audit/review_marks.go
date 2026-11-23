package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// SetReviewMark implements Store. The upsert only ever moves the mark forward.
func (s *SQLiteStore) SetReviewMark(ctx context.Context, m ReviewMark) (ReviewMark, error) {
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
