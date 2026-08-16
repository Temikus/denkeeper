package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// JSONLTask is one line of a task-set export. It is the portable shape: the
// row's identity and provenance ids are written for reference but ignored on
// import, so a set exported from one instance imports cleanly into another.
type JSONLTask struct {
	Prompt               string          `json:"prompt"`
	Category             string          `json:"category"`
	PinnedHistory        json.RawMessage `json:"pinned_history,omitempty"`
	Tags                 json.RawMessage `json:"tags,omitempty"`
	Notes                string          `json:"notes,omitempty"`
	SourceConversationID string          `json:"source_conversation_id,omitempty"`
	SourceMessageID      *int64          `json:"source_message_id,omitempty"`
}

// ExportJSONL writes one task per line.
func (s *Store) ExportJSONL(ctx context.Context, setID int64, w io.Writer) error {
	tasks, err := s.ListTasks(ctx, setID)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	for _, t := range tasks {
		line := JSONLTask{
			Prompt:               t.Prompt,
			Category:             t.Category,
			Notes:                t.Notes,
			SourceConversationID: t.SourceConversationID,
			SourceMessageID:      t.SourceMessageID,
		}
		if t.PinnedHistory != "" {
			line.PinnedHistory = json.RawMessage(t.PinnedHistory)
		}
		if t.Tags != "" {
			line.Tags = json.RawMessage(t.Tags)
		}
		if err := enc.Encode(line); err != nil {
			return fmt.Errorf("exporting task %d: %w", t.ID, err)
		}
	}
	return nil
}

// ImportError names the offending line so an operator hand-editing a JSONL
// file is told where to look, not just that something was wrong.
type ImportError struct {
	Line int
	Err  error
}

func (e *ImportError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e *ImportError) Unwrap() error { return e.Err }

// ImportJSONL appends every line of r to a set. It is all-or-none: every line
// is parsed and validated before anything is written, so a typo halfway down a
// hand-edited file leaves the set exactly as it was rather than half-imported.
func (s *Store) ImportJSONL(ctx context.Context, setID int64, r io.Reader) (int, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLLineBytes)

	var parsed []Task
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var line JSONLTask
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			return 0, &ImportError{Line: lineNo, Err: fmt.Errorf("invalid JSON: %w", err)}
		}
		if strings.TrimSpace(line.Prompt) == "" {
			return 0, &ImportError{Line: lineNo, Err: fmt.Errorf("prompt is required")}
		}
		if line.Category == "" {
			line.Category = CategoryChat
		}
		if !ValidCategory(line.Category) {
			return 0, &ImportError{Line: lineNo, Err: fmt.Errorf(
				"invalid category %q: want one of %s", line.Category, strings.Join(Categories(), ", "))}
		}
		t := Task{
			Prompt:   line.Prompt,
			Category: line.Category,
			Notes:    line.Notes,
			Tags:     "[]",
		}
		if len(line.PinnedHistory) > 0 {
			t.PinnedHistory = string(line.PinnedHistory)
		}
		if len(line.Tags) > 0 {
			t.Tags = string(line.Tags)
		}
		parsed = append(parsed, t)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("reading JSONL: %w", err)
	}

	if err := s.addTasks(ctx, setID, parsed); err != nil {
		return 0, err
	}
	return len(parsed), nil
}

// addTasks inserts a batch under one transaction, so a store error partway
// through leaves nothing behind — the write half of the all-or-none contract.
func (s *Store) addTasks(ctx context.Context, setID int64, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("importing tasks: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	for _, t := range tasks {
		var pinned any
		if t.PinnedHistory != "" {
			pinned = t.PinnedHistory
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO eval_tasks
			   (set_id, prompt, category, pinned_history, source_conversation_id,
			    source_message_id, tags, notes, created_at)
			 VALUES (?, ?, ?, ?, '', NULL, ?, ?, ?)`,
			setID, t.Prompt, t.Category, pinned, t.Tags, t.Notes, now); err != nil {
			return fmt.Errorf("importing tasks: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("importing tasks: %w", err)
	}
	return nil
}

// maxJSONLLineBytes bounds one import line. A pinned history plus prompt is
// well under this; the cap exists so a malformed file cannot be read into
// memory without limit.
const maxJSONLLineBytes = 1 << 20
