package eval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
)

// L1 turn traces (design/eval-subsystem.md §4.2). The table lives with the
// eval schema because `[eval] capture` gates it and eval samples are its other
// producer, but the rows are ordinary turns: the inspector's whole job is to
// answer "why did it do that" for a live turn, which the audit log cannot,
// having rounds and outcomes but never the prompt or the payloads.
//
// The blob is the payload (system prompt, history, per-round tool calls,
// response); everything a list view or a retention sweep needs is a column, so
// neither has to decode JSON.

// traceSchema is applied by initEvalDB alongside the eval tables. New table,
// so it needs no evalMigrations entry — CREATE TABLE IF NOT EXISTS is the
// migration.
const traceSchema = `
CREATE TABLE IF NOT EXISTS turn_traces (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    agent             TEXT     NOT NULL DEFAULT '',
    conversation_id   TEXT     NOT NULL DEFAULT '',
    source            TEXT     NOT NULL DEFAULT 'live',
    model             TEXT     NOT NULL DEFAULT '',
    provider          TEXT     NOT NULL DEFAULT '',
    requested_model   TEXT     NOT NULL DEFAULT '',
    upstream          TEXT     NOT NULL DEFAULT '',
    rounds            INTEGER  NOT NULL DEFAULT 0,
    stop_reason       TEXT     NOT NULL DEFAULT '',
    tokens_prompt     INTEGER  NOT NULL DEFAULT 0,
    tokens_completion INTEGER  NOT NULL DEFAULT 0,
    tokens_cached     INTEGER  NOT NULL DEFAULT 0,
    tokens_total      INTEGER  NOT NULL DEFAULT 0,
    cost              REAL     NOT NULL DEFAULT 0,
    latency_ms        INTEGER  NOT NULL DEFAULT 0,
    truncated         INTEGER  NOT NULL DEFAULT 0,
    bytes             INTEGER  NOT NULL DEFAULT 0,
    payload           TEXT     NOT NULL DEFAULT '{}',
    started_at        DATETIME NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_turn_traces_created ON turn_traces (created_at);
CREATE INDEX IF NOT EXISTS idx_turn_traces_agent   ON turn_traces (agent);
CREATE INDEX IF NOT EXISTS idx_turn_traces_conv    ON turn_traces (conversation_id);
CREATE INDEX IF NOT EXISTS idx_turn_traces_source  ON turn_traces (source);
`

// TraceRow is one stored trace. Payload is the raw JSON blob; a list read
// leaves it empty rather than hauling a quarter of a megabyte per row into a
// page that only renders the header line.
type TraceRow struct {
	ID               int64     `db:"id"                json:"id"`
	Agent            string    `db:"agent"             json:"agent"`
	ConversationID   string    `db:"conversation_id"   json:"conversation_id"`
	Source           string    `db:"source"            json:"source"`
	Model            string    `db:"model"             json:"model,omitempty"`
	Provider         string    `db:"provider"          json:"provider,omitempty"`
	RequestedModel   string    `db:"requested_model"   json:"requested_model,omitempty"`
	Upstream         string    `db:"upstream"          json:"upstream,omitempty"`
	Rounds           int       `db:"rounds"            json:"rounds"`
	StopReason       string    `db:"stop_reason"       json:"stop_reason,omitempty"`
	TokensPrompt     int       `db:"tokens_prompt"     json:"tokens_prompt"`
	TokensCompletion int       `db:"tokens_completion" json:"tokens_completion"`
	TokensCached     int       `db:"tokens_cached"     json:"tokens_cached"`
	TokensTotal      int       `db:"tokens_total"      json:"tokens_total"`
	Cost             float64   `db:"cost"              json:"cost_usd"`
	LatencyMs        int64     `db:"latency_ms"        json:"latency_ms"`
	Truncated        bool      `db:"truncated"         json:"truncated"`
	Bytes            int       `db:"bytes"             json:"bytes"`
	Payload          string    `db:"payload"           json:"-"`
	StartedAt        time.Time `db:"started_at"        json:"started_at"`
	CreatedAt        time.Time `db:"created_at"        json:"created_at"`
}

// TraceFilter narrows a trace listing. A zero value lists the newest traces.
type TraceFilter struct {
	Agent          string
	ConversationID string
	Source         string
	Since          time.Time
	Until          time.Time
	Limit          int
	Offset         int
}

// traceListLimit bounds one page. Traces are large even without their payload,
// and the inspector pages rather than scrolling a whole retention window.
const (
	traceDefaultLimit = 50
	traceMaxLimit     = 200
)

// traceColumns is the shared select list. Payload is added only by GetTrace.
const traceColumns = `id, agent, conversation_id, source, model, provider, requested_model,
	        upstream, rounds, stop_reason, tokens_prompt, tokens_completion, tokens_cached,
	        tokens_total, cost, latency_ms, truncated, bytes, started_at, created_at`

// SaveTrace persists one captured turn. It is the agent.TraceSink
// implementation, so the engine can record a live turn without importing this
// package.
//
// An unencodable payload is an error rather than an empty row: a trace whose
// blob silently vanished is worse than no trace, since the inspector would
// show a turn with no prompt and no reason why.
func (s *Store) SaveTrace(ctx context.Context, t agent.TurnTrace) error {
	blob, err := t.EncodePayload()
	if err != nil {
		return err
	}
	if t.Source == "" {
		t.Source = agent.TraceSourceLive
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.StartedAt.IsZero() {
		t.StartedAt = t.CreatedAt
	}
	if t.Bytes == 0 {
		t.Bytes = len(blob)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO turn_traces
		   (agent, conversation_id, source, model, provider, requested_model, upstream,
		    rounds, stop_reason, tokens_prompt, tokens_completion, tokens_cached, tokens_total,
		    cost, latency_ms, truncated, bytes, payload, started_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Agent, t.ConversationID, t.Source, t.Model, t.Provider, t.RequestedModel, t.Upstream,
		t.Rounds, t.StopReason, t.Tokens.Prompt, t.Tokens.Completion, t.Tokens.CachedPrompt,
		t.Tokens.Total, t.CostUSD, t.LatencyMs, t.Truncated, t.Bytes, blob,
		t.StartedAt.UTC(), t.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("saving turn trace for %q: %w", t.ConversationID, err)
	}
	return nil
}

// ListTraces returns trace headers newest first, without payloads.
func (s *Store) ListTraces(ctx context.Context, f TraceFilter) ([]TraceRow, error) {
	where, args := traceWhere(f)
	q := `SELECT ` + traceColumns + ` FROM turn_traces` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, BoundTraceLimit(f.Limit), max(f.Offset, 0))

	rows := []TraceRow{}
	if err := s.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, fmt.Errorf("listing turn traces: %w", err)
	}
	return rows, nil
}

// CountTraces returns how many traces match the filter, so a list view can say
// whether there is more behind the page it is showing. It takes the same
// filter as ListTraces deliberately: an unfiltered count beside a filtered
// page makes "load more" ask for rows that do not exist and never terminate.
func (s *Store) CountTraces(ctx context.Context, f TraceFilter) (int, error) {
	where, args := traceWhere(f)
	var n int
	if err := s.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM turn_traces`+where, args...); err != nil {
		return 0, fmt.Errorf("counting turn traces: %w", err)
	}
	return n, nil
}

// traceWhere builds the predicate shared by listing and counting. Paging and
// ordering stay out of it: only the row-selecting half is shared.
func traceWhere(f TraceFilter) (string, []any) {
	q := ` WHERE 1 = 1`
	var args []any
	if f.Agent != "" {
		q += ` AND agent = ?`
		args = append(args, f.Agent)
	}
	if f.ConversationID != "" {
		q += ` AND conversation_id = ?`
		args = append(args, f.ConversationID)
	}
	if f.Source != "" {
		q += ` AND source = ?`
		args = append(args, f.Source)
	}
	if !f.Since.IsZero() {
		q += ` AND created_at >= ?`
		args = append(args, f.Since.UTC())
	}
	if !f.Until.IsZero() {
		q += ` AND created_at <= ?`
		args = append(args, f.Until.UTC())
	}
	return q, args
}

// BoundTraceLimit resolves a requested page size to the one a listing will
// actually use. Exported so a caller can echo the effective limit rather than
// the one it asked for: a pager that trusts its own request walks past rows
// when the store clamped it.
func BoundTraceLimit(n int) int {
	if n <= 0 {
		return traceDefaultLimit
	}
	return min(n, traceMaxLimit)
}

// GetTrace returns one trace with its payload decoded.
func (s *Store) GetTrace(ctx context.Context, id int64) (*TraceRow, agent.TracePayload, error) {
	var row TraceRow
	err := s.db.GetContext(ctx, &row,
		`SELECT `+traceColumns+`, payload FROM turn_traces WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, agent.TracePayload{}, fmt.Errorf("turn trace %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, agent.TracePayload{}, fmt.Errorf("getting turn trace %d: %w", id, err)
	}
	payload, err := agent.DecodeTracePayload(row.Payload)
	if err != nil {
		return nil, agent.TracePayload{}, fmt.Errorf("turn trace %d: %w", id, err)
	}
	return &row, payload, nil
}

// PruneTracesBefore deletes traces created before the cutoff and reports how
// many went. Traces carry their own [eval] retention_days (30 by default,
// matching audit) because they are the most sensitive rows in the database and
// keeping them for the telemetry window would be a different decision than the
// one the operator made when they turned capture on.
func (s *Store) PruneTracesBefore(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM turn_traces WHERE created_at < ?`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("pruning turn traces: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("pruning turn traces: %w", err)
	}
	return int(n), nil
}
