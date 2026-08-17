package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
)

// PendingItem is one entry of the judge's queue. It carries just enough to pick
// work — never the responses, which come from GetBlindedItem.
type PendingItem struct {
	ItemID   int64  `db:"item_id"  json:"item_id"`
	PairID   int64  `db:"pair_id"  json:"pair_id"`
	RunID    int64  `db:"run_id"   json:"run_id"`
	TaskID   int64  `db:"task_id"  json:"task_id"`
	Category string `db:"category" json:"category"`
	// Prompt is the task's own text, which is identical for both sides and so
	// leaks nothing.
	Prompt string `db:"prompt" json:"prompt"`
}

// BlindedToolCall is one tool call as the judge sees it. Built field by field
// from agent.ToolCallRecord rather than embedding it, so a field added to the
// record cannot leak into a judge payload by default.
type BlindedToolCall struct {
	Round     int    `json:"round"`
	Name      string `json:"tool_name"`
	Server    string `json:"server_name,omitempty"`
	Outcome   string `json:"outcome"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

// BlindedResponse is one side of a pair. Deliberately absent: variant name,
// model, provider, token usage, cost, latency, and the sample's conversation id
// (which names the variant). Duration is dropped too — a consistently slower
// side is an identity hint.
type BlindedResponse struct {
	Response   string            `json:"response"`
	Rounds     int               `json:"rounds"`
	StopReason string            `json:"stop_reason,omitempty"`
	ToolCalls  []BlindedToolCall `json:"tool_calls"`
}

// BlindedItem is the judge-visible payload for one judgment item.
type BlindedItem struct {
	ItemID   int64  `json:"item_id"`
	RunID    int64  `json:"run_id"`
	TaskID   int64  `json:"task_id"`
	Prompt   string `json:"prompt"`
	Category string `json:"category"`
	// Notes is the task's free-text "what good looks like". Judge context, not
	// an assertion — nothing parses it.
	Notes string `json:"notes,omitempty"`
	// PinnedHistory is the context the turn ran against, so a verdict can tell
	// a non-sequitur from a correct follow-up.
	PinnedHistory json.RawMessage `json:"pinned_history,omitempty"`
	Status        string          `json:"status"`
	ResponseA     BlindedResponse `json:"response_a"`
	ResponseB     BlindedResponse `json:"response_b"`
}

// GetItem returns one judgment item by id.
func (s *Store) GetItem(ctx context.Context, itemID int64) (*JudgmentItem, error) {
	var item JudgmentItem
	err := s.db.GetContext(ctx, &item,
		`SELECT id, pair_id, presentation_order, status, created_at
		 FROM eval_judgment_items WHERE id = ?`, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("judgment item %d: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting judgment item %d: %w", itemID, err)
	}
	return &item, nil
}

// ListItems returns every judgment item of a run, in creation order.
func (s *Store) ListItems(ctx context.Context, runID int64) ([]JudgmentItem, error) {
	out := []JudgmentItem{}
	if err := s.db.SelectContext(ctx, &out,
		`SELECT i.id, i.pair_id, i.presentation_order, i.status, i.created_at
		 FROM eval_judgment_items i
		 JOIN eval_pairs p ON p.id = i.pair_id
		 WHERE p.run_id = ? ORDER BY i.id`, runID); err != nil {
		return nil, fmt.Errorf("listing judgment items of run %d: %w", runID, err)
	}
	return out, nil
}

// ListPending returns pending judgment items, optionally scoped to one run
// (0 = every run). sampleN > 0 draws a random subset instead of the head of the
// queue: the interactive calibration pass judges ~20 items, and taking the
// first 20 would calibrate against whichever tasks happen to sort first.
func (s *Store) ListPending(ctx context.Context, runID int64, limit, sampleN int) ([]PendingItem, error) {
	q := `SELECT i.id AS item_id, i.pair_id, p.run_id, p.task_id, t.category, t.prompt
	      FROM eval_judgment_items i
	      JOIN eval_pairs p ON p.id = i.pair_id
	      JOIN eval_tasks t ON t.id = p.task_id
	      WHERE i.status = ?`
	args := []any{ItemPending}
	if runID > 0 {
		q += ` AND p.run_id = ?`
		args = append(args, runID)
	}
	q += ` ORDER BY i.id`

	out := []PendingItem{}
	if err := s.db.SelectContext(ctx, &out, q, args...); err != nil {
		return nil, fmt.Errorf("listing pending judgment items: %w", err)
	}
	if sampleN > 0 && sampleN < len(out) {
		// Fisher-Yates over the same crypto/rand source the blinding coin uses.
		for i := len(out) - 1; i > 0; i-- {
			j := randIndex(i + 1)
			out[i], out[j] = out[j], out[i]
		}
		out = out[:sampleN]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// GetBlindedItem builds the judge-visible payload for one item.
//
// The payload is constructed from scratch rather than filtered out of the
// stored rows: blinding that works by removing fields fails open the day a
// column is added, and the whole judge path depends on it failing closed.
func (s *Store) GetBlindedItem(ctx context.Context, itemID int64) (*BlindedItem, error) {
	item, err := s.GetItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	pair, err := s.GetPair(ctx, item.PairID)
	if err != nil {
		return nil, err
	}
	task, err := s.getTaskByID(ctx, pair.TaskID)
	if err != nil {
		return nil, err
	}
	first, err := s.GetSample(ctx, pair.SampleA)
	if err != nil {
		return nil, err
	}
	second, err := s.GetSample(ctx, pair.SampleB)
	if err != nil {
		return nil, err
	}
	// The item's order is applied here and nowhere else: everything downstream
	// of this call speaks in presented letters.
	if item.PresentationOrder == OrderBA {
		first, second = second, first
	}

	out := &BlindedItem{
		ItemID:    item.ID,
		RunID:     pair.RunID,
		TaskID:    task.ID,
		Prompt:    task.Prompt,
		Category:  task.Category,
		Notes:     task.Notes,
		Status:    item.Status,
		ResponseA: blindSample(first),
		ResponseB: blindSample(second),
	}
	if task.PinnedHistory != "" && task.PinnedHistory != "[]" {
		out.PinnedHistory = json.RawMessage(task.PinnedHistory)
	}
	return out, nil
}

// blindSample projects a sample onto the judge-visible shape.
func blindSample(smp *Sample) BlindedResponse {
	out := BlindedResponse{
		Response:   smp.Response,
		Rounds:     smp.Rounds,
		StopReason: smp.StopReason,
		ToolCalls:  []BlindedToolCall{},
	}
	var records []agent.ToolCallRecord
	if err := json.Unmarshal([]byte(smp.Trace), &records); err != nil {
		// An unreadable trace costs the judge tool context, not the comparison:
		// the responses are still the thing being judged.
		return out
	}
	for _, rec := range records {
		out.ToolCalls = append(out.ToolCalls, BlindedToolCall{
			Round:     rec.Round,
			Name:      rec.ToolName,
			Server:    rec.ServerName,
			Outcome:   rec.Outcome,
			Arguments: rec.Arguments,
			Result:    rec.Result,
			Error:     rec.ErrorMsg,
		})
	}
	return out
}

// GetSample returns one sample by id.
func (s *Store) GetSample(ctx context.Context, id int64) (*Sample, error) {
	var smp Sample
	err := s.db.GetContext(ctx, &smp,
		`SELECT id, run_id, variant_id, task_id, k_index, status, error, response, trace,
		        rounds, stop_reason, outcome_ok, outcome_rejected, outcome_failed,
		        outcome_denied, outcome_cached, outcome_suppressed, tokens_prompt,
		        tokens_completion, cost, latency_ms, created_at
		 FROM eval_samples WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sample %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting sample %d: %w", id, err)
	}
	return &smp, nil
}

// getTaskByID reads a task without the set scoping GetTask enforces — a pair
// already proves which task it belongs to.
func (s *Store) getTaskByID(ctx context.Context, id int64) (*Task, error) {
	var t Task
	err := s.db.GetContext(ctx, &t,
		`SELECT id, set_id, prompt, category, COALESCE(pinned_history, '') AS pinned_history,
		        source_conversation_id, source_message_id, tags, notes, created_at
		 FROM eval_tasks WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting task %d: %w", id, err)
	}
	return &t, nil
}

// RecordVerdict writes one judge's call on one item and marks the item judged.
//
// The operator's calibration marks deliberately do *not* flip the status: they
// are recorded against an item the judge has already worked, and an item that
// only the operator has seen is still outstanding judge work.
func (s *Store) RecordVerdict(ctx context.Context, v Verdict) (*Verdict, error) {
	if !ValidWinner(v.Winner) {
		return nil, fmt.Errorf("invalid winner %q", v.Winner)
	}
	if _, err := s.GetItem(ctx, v.ItemID); err != nil {
		return nil, err
	}
	if v.Dimensions == "" {
		v.Dimensions = "{}"
	}
	v.CreatedAt = time.Now().UTC()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("recording verdict: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO eval_verdicts (item_id, winner, dimensions, notes, judge_ident, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (item_id, judge_ident) DO UPDATE SET
		     winner = excluded.winner, dimensions = excluded.dimensions,
		     notes = excluded.notes, created_at = excluded.created_at`,
		v.ItemID, v.Winner, v.Dimensions, v.Notes, v.JudgeIdent, v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("recording verdict for item %d: %w", v.ItemID, err)
	}
	if v.ID, err = res.LastInsertId(); err != nil {
		return nil, fmt.Errorf("recording verdict for item %d: %w", v.ItemID, err)
	}
	if v.JudgeIdent != JudgeOperator {
		if _, err := tx.ExecContext(ctx,
			`UPDATE eval_judgment_items SET status = ? WHERE id = ?`, ItemJudged, v.ItemID); err != nil {
			return nil, fmt.Errorf("marking item %d judged: %w", v.ItemID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("recording verdict: %w", err)
	}
	return &v, nil
}

// ListVerdicts returns every verdict recorded against a run's items.
func (s *Store) ListVerdicts(ctx context.Context, runID int64) ([]Verdict, error) {
	out := []Verdict{}
	if err := s.db.SelectContext(ctx, &out,
		`SELECT v.id, v.item_id, v.winner, v.dimensions, v.notes, v.judge_ident, v.created_at
		 FROM eval_verdicts v
		 JOIN eval_judgment_items i ON i.id = v.item_id
		 JOIN eval_pairs p ON p.id = i.pair_id
		 WHERE p.run_id = ? ORDER BY v.id`, runID); err != nil {
		return nil, fmt.Errorf("listing verdicts of run %d: %w", runID, err)
	}
	return out, nil
}
