// Package eval stores eval task sets and runs comparison evals against a base
// agent's live engine under the no-side-effects execution policy
// (agent.ExecPolicy). Stages B and C of design/eval-subsystem.md: L2 task sets,
// the L3 runner and its objective metrics, then the blinded pairing, judging
// and decision rule layered on top.
package eval

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// ErrNotFound is the sentinel every lookup wraps when the addressed row does
// not exist, so REST handlers can classify a 404 with errors.Is rather than by
// inspecting the message (the tool.ErrToolNotFound convention).
var ErrNotFound = errors.New("eval: not found")

// ErrTaskSetInUse is returned by DeleteTaskSet when runs still reference the
// set. Deleting would either orphan those runs or cascade away results the
// operator may still be reading, so the delete is refused and the caller maps
// it to 409.
var ErrTaskSetInUse = errors.New("eval: task set is referenced by runs")

// ErrNameTaken is returned when a task-set name collides with an existing one.
var ErrNameTaken = errors.New("eval: task set name already exists")

// Task categories. The curation axis from design/eval-subsystem.md §4.3 —
// validated in Go rather than by a SQL CHECK constraint, matching the house
// style (there are no CHECK constraints anywhere in the schema).
const (
	CategoryChat         = "chat"
	CategorySkillCommand = "skill_command"
	CategoryScheduled    = "scheduled"
	CategoryToolHeavy    = "tool_heavy"
)

// Run statuses.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusCapped  = "capped"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// Sample statuses. A sample is the unit of failure tolerance: a provider
// hiccup fails the sample, never the run.
const (
	SampleOK     = "ok"
	SampleFailed = "failed"
)

// Judgment item statuses.
const (
	ItemPending = "pending"
	ItemJudged  = "judged"
)

// Presentation orders. An item's order says which pair letter is shown to the
// judge first: "ab" shows the pair's A sample as Response A, "ba" swaps them.
// Two items per pair, one of each, is what makes position-bias control
// structural rather than a matter of judge discipline.
const (
	OrderAB = "ab"
	OrderBA = "ba"
)

// Verdict winners, in terms of the *presented* responses — the judge never
// learns the pair letters, only "Response A" and "Response B" as shown to it.
const (
	WinnerA   = "a"
	WinnerB   = "b"
	WinnerTie = "tie"
)

// JudgeOperator is the judge identity reserved for the operator's calibration
// marks. Stored as ordinary verdicts (no schema of their own) and excluded from
// the win rate; they only feed the operator–judge agreement figure.
const JudgeOperator = "operator"

// Judgment dimensions, in the order the rubric lists them.
const (
	DimTaskSuccess = "task_success"
	DimToolPath    = "tool_path"
	DimPersonaFit  = "persona_fit"
	DimLength      = "length"
)

// ValidWinner reports whether w is one of the three verdict outcomes.
func ValidWinner(w string) bool {
	switch w {
	case WinnerA, WinnerB, WinnerTie:
		return true
	}
	return false
}

// Dimensions returns the four judgment dimensions, in rubric order.
func Dimensions() []string {
	return []string{DimTaskSuccess, DimToolPath, DimPersonaFit, DimLength}
}

// ValidCategory reports whether c is one of the four curation categories.
func ValidCategory(c string) bool {
	switch c {
	case CategoryChat, CategorySkillCommand, CategoryScheduled, CategoryToolHeavy:
		return true
	}
	return false
}

// Categories returns the four valid task categories, in the order the docs
// list them.
func Categories() []string {
	return []string{CategoryChat, CategorySkillCommand, CategoryScheduled, CategoryToolHeavy}
}

// IsTerminal reports whether a run status is final — nothing further will be
// dispatched and the row will not change again.
func IsTerminal(status string) bool {
	switch status {
	case StatusDone, StatusCapped, StatusStopped, StatusFailed:
		return true
	}
	return false
}

// schema holds the five Stage B tables plus the three Stage C judging tables.
// turn_traces (Stage E, L1) lives beside them in traceSchema.
//
// eval_samples.trace and turn_traces answer different questions and both stay:
// the sample's inline trace is the tool path the judge reads, the turn trace is
// the whole turn including the system prompt and the history window.
const schema = `
CREATE TABLE IF NOT EXISTS eval_task_sets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT     NOT NULL UNIQUE,
    description TEXT     NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS eval_tasks (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    set_id                 INTEGER NOT NULL REFERENCES eval_task_sets(id),
    prompt                 TEXT    NOT NULL,
    category               TEXT    NOT NULL DEFAULT 'chat',
    pinned_history         TEXT,
    source_conversation_id TEXT    NOT NULL DEFAULT '',
    source_message_id      INTEGER,
    tags                   TEXT    NOT NULL DEFAULT '[]',
    notes                  TEXT    NOT NULL DEFAULT '',
    created_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS eval_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_set_id INTEGER  NOT NULL REFERENCES eval_task_sets(id),
    base_agent  TEXT     NOT NULL,
    status      TEXT     NOT NULL,
    k           INTEGER  NOT NULL,
    cost_cap    REAL     NOT NULL,
    cost_spent  REAL     NOT NULL DEFAULT 0,
    as_of       DATETIME NOT NULL,
    error       TEXT     NOT NULL DEFAULT '',
    task_ids    TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE TABLE IF NOT EXISTS eval_variants (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id  INTEGER NOT NULL REFERENCES eval_runs(id),
    name    TEXT    NOT NULL,
    overlay TEXT    NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS eval_samples (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id             INTEGER NOT NULL REFERENCES eval_runs(id),
    variant_id         INTEGER NOT NULL REFERENCES eval_variants(id),
    task_id            INTEGER NOT NULL REFERENCES eval_tasks(id),
    k_index            INTEGER NOT NULL,
    status             TEXT    NOT NULL,
    error              TEXT    NOT NULL DEFAULT '',
    response           TEXT    NOT NULL DEFAULT '',
    trace              TEXT    NOT NULL DEFAULT '[]',
    rounds             INTEGER NOT NULL DEFAULT 0,
    stop_reason        TEXT    NOT NULL DEFAULT '',
    upstream           TEXT    NOT NULL DEFAULT '',
    outcome_ok         INTEGER NOT NULL DEFAULT 0,
    outcome_rejected   INTEGER NOT NULL DEFAULT 0,
    outcome_failed     INTEGER NOT NULL DEFAULT 0,
    outcome_denied     INTEGER NOT NULL DEFAULT 0,
    outcome_cached     INTEGER NOT NULL DEFAULT 0,
    outcome_suppressed INTEGER NOT NULL DEFAULT 0,
    tokens_prompt      INTEGER NOT NULL DEFAULT 0,
    tokens_completion  INTEGER NOT NULL DEFAULT 0,
    cost               REAL    NOT NULL DEFAULT 0,
    latency_ms         INTEGER NOT NULL DEFAULT 0,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS eval_pairs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     INTEGER NOT NULL REFERENCES eval_runs(id),
    task_id    INTEGER NOT NULL REFERENCES eval_tasks(id),
    k_index    INTEGER NOT NULL,
    sample_a   INTEGER NOT NULL REFERENCES eval_samples(id),
    sample_b   INTEGER NOT NULL REFERENCES eval_samples(id),
    assignment TEXT    NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS eval_judgment_items (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    pair_id            INTEGER NOT NULL REFERENCES eval_pairs(id),
    presentation_order TEXT    NOT NULL,
    status             TEXT    NOT NULL DEFAULT 'pending',
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS eval_verdicts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id        INTEGER NOT NULL REFERENCES eval_judgment_items(id),
    winner         TEXT    NOT NULL,
    dimensions     TEXT    NOT NULL DEFAULT '{}',
    notes          TEXT    NOT NULL DEFAULT '',
    judge_ident    TEXT    NOT NULL DEFAULT '',
    rubric_version TEXT    NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_eval_tasks_set      ON eval_tasks (set_id);
CREATE INDEX IF NOT EXISTS idx_eval_runs_task_set  ON eval_runs (task_set_id);
CREATE INDEX IF NOT EXISTS idx_eval_runs_status    ON eval_runs (status);
CREATE INDEX IF NOT EXISTS idx_eval_variants_run   ON eval_variants (run_id);
CREATE INDEX IF NOT EXISTS idx_eval_samples_run    ON eval_samples (run_id);
CREATE INDEX IF NOT EXISTS idx_eval_samples_pair   ON eval_samples (variant_id, task_id);
CREATE INDEX IF NOT EXISTS idx_eval_pairs_run      ON eval_pairs (run_id);
CREATE INDEX IF NOT EXISTS idx_eval_items_pair     ON eval_judgment_items (pair_id);
CREATE INDEX IF NOT EXISTS idx_eval_items_status   ON eval_judgment_items (status);

-- One verdict per (item, judge). A judge that re-runs the queue overwrites its
-- own earlier call rather than stacking duplicates, and the operator's
-- calibration mark sits alongside the judge's rather than replacing it.
CREATE UNIQUE INDEX IF NOT EXISTS idx_eval_verdicts_item_judge
    ON eval_verdicts (item_id, judge_ident);
`

// evalMigrations holds idempotent ALTER statements for schema changes after
// the initial release. Each is run once; a duplicate-column error on an
// already-migrated DB is swallowed (isDuplicateColumn).
var evalMigrations = []string{
	// upstream: OpenRouter's provider-reported serving upstream for the sample,
	// empty for providers without the concept.
	`ALTER TABLE eval_samples ADD COLUMN upstream TEXT NOT NULL DEFAULT ''`,
	// task_ids: the run's task list, pinned at creation. NULL means the whole
	// set, resolved at read time.
	`ALTER TABLE eval_runs ADD COLUMN task_ids TEXT`,
	// rubric_version: which revision of the judging rubric produced the verdict.
	// Empty on every pre-migration row, and on any judge that does not say.
	`ALTER TABLE eval_verdicts ADD COLUMN rubric_version TEXT NOT NULL DEFAULT ''`,
	// judge_cost: what the internal judge spent grading this run's pairs. Its
	// own column rather than cost_spent, because the two answer different
	// questions and share no cap: cost_spent is the sample budget the run was
	// created with, judging is a later decision to spend under
	// [eval] judge_max_cost_per_run. Folding them would make a judged run look
	// like it had blown its cap.
	`ALTER TABLE eval_runs ADD COLUMN judge_cost REAL NOT NULL DEFAULT 0`,
}

// TaskSet is a named collection of eval tasks.
type TaskSet struct {
	ID          int64     `db:"id"          json:"id"`
	Name        string    `db:"name"        json:"name"`
	Description string    `db:"description" json:"description"`
	CreatedAt   time.Time `db:"created_at"  json:"created_at"`
	// TaskCount is populated by ListTaskSets; it is not a column.
	TaskCount int `db:"task_count" json:"task_count"`
}

// Task is one saved test case.
type Task struct {
	ID       int64  `db:"id"       json:"id"`
	SetID    int64  `db:"set_id"   json:"set_id"`
	Prompt   string `db:"prompt"   json:"prompt"`
	Category string `db:"category" json:"category"`
	// PinnedHistory is a JSON array of {role, content} replayed verbatim as
	// the context preceding the turn. NULL/empty means a fresh turn.
	PinnedHistory        string    `db:"pinned_history"         json:"pinned_history,omitempty"`
	SourceConversationID string    `db:"source_conversation_id" json:"source_conversation_id,omitempty"`
	SourceMessageID      *int64    `db:"source_message_id"      json:"source_message_id,omitempty"`
	Tags                 string    `db:"tags"                   json:"tags"`
	Notes                string    `db:"notes"                  json:"notes"`
	CreatedAt            time.Time `db:"created_at"             json:"created_at"`
}

// TaskIDList is eval_runs.task_ids: a JSON array of task ids, or NULL for the
// whole set. It carries its own SQL codec so a run row round-trips without the
// callers ever handling the raw JSON.
type TaskIDList []int64

// Scan decodes the stored JSON. NULL and the empty string both mean "not
// pinned", which is the same thing a caller sees as a nil slice.
func (l *TaskIDList) Scan(src any) error {
	var raw string
	switch v := src.(type) {
	case nil:
		*l = nil
		return nil
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return fmt.Errorf("scanning task_ids: unsupported type %T", src)
	}
	if strings.TrimSpace(raw) == "" {
		*l = nil
		return nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return fmt.Errorf("scanning task_ids: %w", err)
	}
	*l = ids
	return nil
}

// Value encodes the pin, storing NULL rather than "[]" when absent: NULL is
// what every reader tests for, and an empty array would read as "run nothing".
func (l TaskIDList) Value() (driver.Value, error) {
	if len(l) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal([]int64(l))
	if err != nil {
		return nil, fmt.Errorf("encoding task_ids: %w", err)
	}
	return string(raw), nil
}

// Run is one comparison run.
type Run struct {
	ID        int64   `db:"id"          json:"id"`
	TaskSetID int64   `db:"task_set_id" json:"task_set_id"`
	BaseAgent string  `db:"base_agent"  json:"base_agent"`
	Status    string  `db:"status"      json:"status"`
	K         int     `db:"k"           json:"k"`
	CostCap   float64 `db:"cost_cap"    json:"cost_cap"`
	CostSpent float64 `db:"cost_spent"  json:"cost_spent"`
	// JudgeCost is what the internal judge has spent grading this run, kept
	// apart from CostSpent: it is a separate budget spent by a separate
	// decision, and adding it to the sample spend would read as a blown cap.
	JudgeCost float64   `db:"judge_cost"  json:"judge_cost"`
	AsOf      time.Time `db:"as_of"       json:"as_of"`
	// TaskIDs pins the run's task list at creation; nil means the whole set.
	// Pinning is what makes a sampled subset possible, and it also stops a task
	// added to the set later from retroactively inflating samples_expected and
	// flipping a finished run to inconclusive.
	TaskIDs TaskIDList `db:"task_ids" json:"task_ids,omitempty"`
	// TaskCount is how many tasks the run covers: the pinned count when pinned,
	// otherwise the set's current size. It is a display figure — the
	// authoritative dispatch count is len(RunTasks), which also drops a pinned
	// task deleted after the run was created.
	TaskCount  int        `db:"task_count"  json:"task_count"`
	Error      string     `db:"error"       json:"error,omitempty"`
	CreatedAt  time.Time  `db:"created_at"  json:"created_at"`
	FinishedAt *time.Time `db:"finished_at" json:"finished_at,omitempty"`
}

// resolveTaskCount narrows the set-wide count the SQL returns to the pin.
func (r *Run) resolveTaskCount() {
	if len(r.TaskIDs) > 0 {
		r.TaskCount = len(r.TaskIDs)
	}
}

// Variant is one side of a comparison: a named overlay on the base agent's
// live config. An empty overlay is the incumbent.
type Variant struct {
	ID      int64  `db:"id"      json:"id"`
	RunID   int64  `db:"run_id"  json:"run_id"`
	Name    string `db:"name"    json:"name"`
	Overlay string `db:"overlay" json:"overlay"`
}

// Overlay is the decoded eval_variants.overlay JSON.
type Overlay struct {
	Model    string `json:"llm_model,omitempty"`
	Provider string `json:"llm_provider,omitempty"`
}

// Sample is one (task, variant, k) execution.
type Sample struct {
	ID         int64  `db:"id"          json:"id"`
	RunID      int64  `db:"run_id"      json:"run_id"`
	VariantID  int64  `db:"variant_id"  json:"variant_id"`
	TaskID     int64  `db:"task_id"     json:"task_id"`
	KIndex     int    `db:"k_index"     json:"k_index"`
	Status     string `db:"status"      json:"status"`
	Error      string `db:"error"       json:"error,omitempty"`
	Response   string `db:"response"    json:"response"`
	Trace      string `db:"trace"       json:"trace"`
	Rounds     int    `db:"rounds"      json:"rounds"`
	StopReason string `db:"stop_reason" json:"stop_reason,omitempty"`
	// Upstream is the provider-reported serving upstream (OpenRouter's routed
	// provider), empty for providers without the concept.
	Upstream string `db:"upstream" json:"upstream,omitempty"`
	// Outcome counts are tool-call level, split exactly as
	// agent.ToolCallRecord.Outcome. Cached and suppressed are kept separate
	// from failed on purpose: folding either in would poison the failed-rate
	// gate, and an eval turn suppresses writes routinely.
	OutcomeOK         int       `db:"outcome_ok"         json:"outcome_ok"`
	OutcomeRejected   int       `db:"outcome_rejected"   json:"outcome_rejected"`
	OutcomeFailed     int       `db:"outcome_failed"     json:"outcome_failed"`
	OutcomeDenied     int       `db:"outcome_denied"     json:"outcome_denied"`
	OutcomeCached     int       `db:"outcome_cached"     json:"outcome_cached"`
	OutcomeSuppressed int       `db:"outcome_suppressed" json:"outcome_suppressed"`
	TokensPrompt      int       `db:"tokens_prompt"      json:"tokens_prompt"`
	TokensCompletion  int       `db:"tokens_completion"  json:"tokens_completion"`
	Cost              float64   `db:"cost"               json:"cost"`
	LatencyMs         int64     `db:"latency_ms"         json:"latency_ms"`
	CreatedAt         time.Time `db:"created_at"         json:"created_at"`
}

// Pair is one blinded comparison: a baseline sample and a candidate sample for
// the same (task, k), with a random A/B assignment that lives server-side only.
type Pair struct {
	ID      int64 `db:"id"        json:"id"`
	RunID   int64 `db:"run_id"    json:"run_id"`
	TaskID  int64 `db:"task_id"   json:"task_id"`
	KIndex  int   `db:"k_index"   json:"k_index"`
	SampleA int64 `db:"sample_a"  json:"sample_a"`
	SampleB int64 `db:"sample_b"  json:"sample_b"`
	// Assignment is the letter→variant map. It is the unblinding key and must
	// never reach a judge-visible payload.
	Assignment string    `db:"assignment" json:"assignment"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

// Assignment is the decoded eval_pairs.assignment JSON: which variant each
// presented letter really was.
type Assignment struct {
	A int64 `json:"a"`
	B int64 `json:"b"`
}

// JudgmentItem is one pass over a pair at a fixed presentation order. A pair
// yields two, one per order.
type JudgmentItem struct {
	ID                int64     `db:"id"                 json:"id"`
	PairID            int64     `db:"pair_id"            json:"pair_id"`
	PresentationOrder string    `db:"presentation_order" json:"presentation_order"`
	Status            string    `db:"status"             json:"status"`
	CreatedAt         time.Time `db:"created_at"         json:"created_at"`
}

// Verdict is one judge's call on one item, expressed in presented letters.
type Verdict struct {
	ID     int64  `db:"id"      json:"id"`
	ItemID int64  `db:"item_id" json:"item_id"`
	Winner string `db:"winner" json:"winner"`
	// Dimensions is a JSON object of dimension → winner (a/b/tie), the same
	// pairwise form as Winner rather than absolute scores: a judge comparing
	// two responses is reliable, a judge scoring one in isolation is not.
	Dimensions string `db:"dimensions" json:"dimensions"`
	Notes      string `db:"notes"      json:"notes"`
	// JudgeIdent names who judged — an API key name, or JudgeOperator for the
	// operator's calibration marks.
	JudgeIdent string `db:"judge_ident" json:"judge_ident"`
	// RubricVersion is the judging rubric revision this call was made under, as
	// the judge reported it (the `Rubric version:` line of the judge-eval
	// skill). Empty when the judge did not say, which is why the results view
	// reports the distinct set rather than assuming one.
	RubricVersion string    `db:"rubric_version" json:"rubric_version,omitempty"`
	CreatedAt     time.Time `db:"created_at"     json:"created_at"`
}

// Store persists eval task sets, runs and samples. It owns its own handle on
// the main database file, following the kv package: the eval tables live in
// the main DB (§5) because every run reads eval_tasks, and the write rate is
// far below anything that would justify a separate file.
type Store struct {
	db *sqlx.DB
}

// NewSQLiteStore opens or creates the database and applies the eval schema.
func NewSQLiteStore(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := initEvalDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// NewInMemoryStore creates an in-memory SQLite store for testing.
func NewInMemoryStore() (*Store, error) {
	db, err := sqlx.Open("sqlite", ":memory:?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory database: %w", err)
	}
	// An in-memory DB lives on its connection, so more than one would see a
	// different (empty) database.
	db.SetMaxOpenConns(1)
	if err := initEvalDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func initEvalDB(db *sqlx.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("initializing eval schema: %w", err)
	}
	if _, err := db.Exec(traceSchema); err != nil {
		return fmt.Errorf("initializing turn trace schema: %w", err)
	}
	for _, m := range evalMigrations {
		if _, err := db.Exec(m); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("migrating eval schema: %w", err)
		}
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "duplicate column name") ||
		strings.Contains(err.Error(), "already exists"))
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

// --- Task sets ---

// CreateTaskSet inserts a task set. A duplicate name returns ErrNameTaken.
func (s *Store) CreateTaskSet(ctx context.Context, name, description string) (*TaskSet, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO eval_task_sets (name, description, created_at) VALUES (?, ?, ?)`,
		name, description, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("creating task set %q: %w", name, ErrNameTaken)
		}
		return nil, fmt.Errorf("creating task set %q: %w", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("creating task set %q: %w", name, err)
	}
	return &TaskSet{ID: id, Name: name, Description: description, CreatedAt: now}, nil
}

// ListTaskSets returns every task set with its task count, ordered by name.
func (s *Store) ListTaskSets(ctx context.Context) ([]TaskSet, error) {
	sets := []TaskSet{}
	err := s.db.SelectContext(ctx, &sets,
		`SELECT ts.id, ts.name, ts.description, ts.created_at,
		        (SELECT COUNT(*) FROM eval_tasks t WHERE t.set_id = ts.id) AS task_count
		 FROM eval_task_sets ts ORDER BY ts.name`)
	if err != nil {
		return nil, fmt.Errorf("listing task sets: %w", err)
	}
	return sets, nil
}

// GetTaskSet returns a task set by name.
func (s *Store) GetTaskSet(ctx context.Context, name string) (*TaskSet, error) {
	var set TaskSet
	err := s.db.GetContext(ctx, &set,
		`SELECT ts.id, ts.name, ts.description, ts.created_at,
		        (SELECT COUNT(*) FROM eval_tasks t WHERE t.set_id = ts.id) AS task_count
		 FROM eval_task_sets ts WHERE ts.name = ?`, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task set %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting task set %q: %w", name, err)
	}
	return &set, nil
}

// GetTaskSetByID returns a task set by id.
func (s *Store) GetTaskSetByID(ctx context.Context, id int64) (*TaskSet, error) {
	var set TaskSet
	err := s.db.GetContext(ctx, &set,
		`SELECT id, name, description, created_at, 0 AS task_count
		 FROM eval_task_sets WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task set %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting task set %d: %w", id, err)
	}
	return &set, nil
}

// UpdateTaskSet renames a set and/or replaces its description. A nil field is
// left unchanged.
func (s *Store) UpdateTaskSet(ctx context.Context, name string, newName, description *string) (*TaskSet, error) {
	set, err := s.GetTaskSet(ctx, name)
	if err != nil {
		return nil, err
	}
	if newName != nil {
		set.Name = *newName
	}
	if description != nil {
		set.Description = *description
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE eval_task_sets SET name = ?, description = ? WHERE id = ?`,
		set.Name, set.Description, set.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("renaming task set %q: %w", name, ErrNameTaken)
		}
		return nil, fmt.Errorf("updating task set %q: %w", name, err)
	}
	return set, nil
}

// DeleteTaskSet removes a set and its tasks. It refuses with ErrTaskSetInUse
// when any run references the set: a run's samples are only interpretable
// against the tasks that produced them.
func (s *Store) DeleteTaskSet(ctx context.Context, name string) error {
	set, err := s.GetTaskSet(ctx, name)
	if err != nil {
		return err
	}
	var runs int
	if err := s.db.GetContext(ctx, &runs,
		`SELECT COUNT(*) FROM eval_runs WHERE task_set_id = ?`, set.ID); err != nil {
		return fmt.Errorf("checking runs for task set %q: %w", name, err)
	}
	if runs > 0 {
		return fmt.Errorf("task set %q has %d run(s): %w", name, runs, ErrTaskSetInUse)
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleting task set %q: %w", name, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM eval_tasks WHERE set_id = ?`, set.ID); err != nil {
		return fmt.Errorf("deleting tasks of set %q: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM eval_task_sets WHERE id = ?`, set.ID); err != nil {
		return fmt.Errorf("deleting task set %q: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("deleting task set %q: %w", name, err)
	}
	return nil
}

// --- Tasks ---

// AddTask appends a task to a set.
func (s *Store) AddTask(ctx context.Context, setID int64, t Task) (*Task, error) {
	if !ValidCategory(t.Category) {
		return nil, fmt.Errorf("invalid category %q", t.Category)
	}
	if t.Tags == "" {
		t.Tags = "[]"
	}
	now := time.Now().UTC()
	var pinned any
	if t.PinnedHistory != "" {
		pinned = t.PinnedHistory
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO eval_tasks
		   (set_id, prompt, category, pinned_history, source_conversation_id,
		    source_message_id, tags, notes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		setID, t.Prompt, t.Category, pinned, t.SourceConversationID,
		t.SourceMessageID, t.Tags, t.Notes, now)
	if err != nil {
		return nil, fmt.Errorf("adding task to set %d: %w", setID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("adding task to set %d: %w", setID, err)
	}
	t.ID = id
	t.SetID = setID
	t.CreatedAt = now
	return &t, nil
}

// ListTasks returns the tasks of a set, in creation order — the order the
// runner dispatches them and the order per-task deltas are baselined against.
func (s *Store) ListTasks(ctx context.Context, setID int64) ([]Task, error) {
	tasks := []Task{}
	err := s.db.SelectContext(ctx, &tasks,
		`SELECT id, set_id, prompt, category, COALESCE(pinned_history, '') AS pinned_history,
		        source_conversation_id, source_message_id, tags, notes, created_at
		 FROM eval_tasks WHERE set_id = ? ORDER BY id`, setID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks of set %d: %w", setID, err)
	}
	return tasks, nil
}

// SavedTaskSources returns the SourceKey of every turn already saved as a
// task, across all sets. The suggestion endpoint subtracts it so an accepted
// candidate does not come back next time. Tasks with no source message (hand
// written, imported) contribute nothing — there is no turn to suppress.
func (s *Store) SavedTaskSources(ctx context.Context) (map[string]struct{}, error) {
	var rows []struct {
		ConversationID string `db:"source_conversation_id"`
		MessageID      int64  `db:"source_message_id"`
	}
	err := s.db.SelectContext(ctx, &rows,
		`SELECT DISTINCT source_conversation_id, source_message_id
		 FROM eval_tasks
		 WHERE source_message_id IS NOT NULL AND source_conversation_id != ''`)
	if err != nil {
		return nil, fmt.Errorf("listing saved task sources: %w", err)
	}
	saved := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		saved[SourceKey(r.ConversationID, r.MessageID)] = struct{}{}
	}
	return saved, nil
}

// GetTask returns one task by id, scoped to a set so a caller cannot address
// another set's task through a set-scoped route.
func (s *Store) GetTask(ctx context.Context, setID, taskID int64) (*Task, error) {
	var t Task
	err := s.db.GetContext(ctx, &t,
		`SELECT id, set_id, prompt, category, COALESCE(pinned_history, '') AS pinned_history,
		        source_conversation_id, source_message_id, tags, notes, created_at
		 FROM eval_tasks WHERE id = ? AND set_id = ?`, taskID, setID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %d: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting task %d: %w", taskID, err)
	}
	return &t, nil
}

// TaskPatch carries the mutable task fields; a nil field is left unchanged.
type TaskPatch struct {
	Prompt        *string
	Category      *string
	PinnedHistory *string
	Tags          *string
	Notes         *string
}

// UpdateTask applies a patch to a task.
func (s *Store) UpdateTask(ctx context.Context, setID, taskID int64, patch TaskPatch) (*Task, error) {
	t, err := s.GetTask(ctx, setID, taskID)
	if err != nil {
		return nil, err
	}
	if patch.Prompt != nil {
		t.Prompt = *patch.Prompt
	}
	if patch.Category != nil {
		if !ValidCategory(*patch.Category) {
			return nil, fmt.Errorf("invalid category %q", *patch.Category)
		}
		t.Category = *patch.Category
	}
	if patch.PinnedHistory != nil {
		t.PinnedHistory = *patch.PinnedHistory
	}
	if patch.Tags != nil {
		t.Tags = *patch.Tags
	}
	if patch.Notes != nil {
		t.Notes = *patch.Notes
	}
	var pinned any
	if t.PinnedHistory != "" {
		pinned = t.PinnedHistory
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE eval_tasks SET prompt = ?, category = ?, pinned_history = ?, tags = ?, notes = ?
		 WHERE id = ?`, t.Prompt, t.Category, pinned, t.Tags, t.Notes, t.ID)
	if err != nil {
		return nil, fmt.Errorf("updating task %d: %w", taskID, err)
	}
	return t, nil
}

// DeleteTask removes one task from a set.
func (s *Store) DeleteTask(ctx context.Context, setID, taskID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM eval_tasks WHERE id = ? AND set_id = ?`, taskID, setID)
	if err != nil {
		return fmt.Errorf("deleting task %d: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("deleting task %d: %w", taskID, err)
	}
	if n == 0 {
		return fmt.Errorf("task %d: %w", taskID, ErrNotFound)
	}
	return nil
}

// --- Runs & variants ---

// runColumns is the shared select list for run reads. task_count comes back as
// the *set's* current size; resolveTaskCount narrows it to the pin, which is
// cheaper and clearer than counting a JSON array in SQL.
const runColumns = `id, task_set_id, base_agent, status, k, cost_cap, cost_spent, judge_cost, as_of,
	        error, task_ids, created_at, finished_at,
	        (SELECT COUNT(*) FROM eval_tasks t WHERE t.set_id = eval_runs.task_set_id) AS task_count`

// CreateRun inserts a run in the pending status together with its variants,
// in one transaction: a run without variants has nothing to compare and must
// never be visible.
func (s *Store) CreateRun(ctx context.Context, run Run, variants []Variant) (*Run, []Variant, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	run.Status = StatusPending
	run.CreatedAt = time.Now().UTC()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO eval_runs (task_set_id, base_agent, status, k, cost_cap, cost_spent, as_of, task_ids, created_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		run.TaskSetID, run.BaseAgent, run.Status, run.K, run.CostCap, run.AsOf,
		run.TaskIDs, run.CreatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("creating run: %w", err)
	}
	run.TaskCount = len(run.TaskIDs)
	if run.TaskCount == 0 {
		if err := tx.GetContext(ctx, &run.TaskCount,
			`SELECT COUNT(*) FROM eval_tasks WHERE set_id = ?`, run.TaskSetID); err != nil {
			return nil, nil, fmt.Errorf("counting tasks of set %d: %w", run.TaskSetID, err)
		}
	}
	run.ID, err = res.LastInsertId()
	if err != nil {
		return nil, nil, fmt.Errorf("creating run: %w", err)
	}

	out := make([]Variant, 0, len(variants))
	for _, v := range variants {
		if v.Overlay == "" {
			v.Overlay = "{}"
		}
		vres, err := tx.ExecContext(ctx,
			`INSERT INTO eval_variants (run_id, name, overlay) VALUES (?, ?, ?)`,
			run.ID, v.Name, v.Overlay)
		if err != nil {
			return nil, nil, fmt.Errorf("creating variant %q: %w", v.Name, err)
		}
		v.ID, err = vres.LastInsertId()
		if err != nil {
			return nil, nil, fmt.Errorf("creating variant %q: %w", v.Name, err)
		}
		v.RunID = run.ID
		out = append(out, v)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("creating run: %w", err)
	}
	return &run, out, nil
}

// GetRun returns a run by id.
func (s *Store) GetRun(ctx context.Context, id int64) (*Run, error) {
	var run Run
	err := s.db.GetContext(ctx, &run,
		`SELECT `+runColumns+` FROM eval_runs WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("run %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting run %d: %w", id, err)
	}
	run.resolveTaskCount()
	return &run, nil
}

// ListRuns returns runs newest first, optionally filtered by task set id
// (0 = any) and status ("" = any).
func (s *Store) ListRuns(ctx context.Context, taskSetID int64, status string) ([]Run, error) {
	q := `SELECT ` + runColumns + ` FROM eval_runs WHERE 1 = 1`
	var args []any
	if taskSetID > 0 {
		q += ` AND task_set_id = ?`
		args = append(args, taskSetID)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC`

	runs := []Run{}
	if err := s.db.SelectContext(ctx, &runs, q, args...); err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	for i := range runs {
		runs[i].resolveTaskCount()
	}
	return runs, nil
}

// RunTasks returns the tasks a run covers: its pinned list, or the whole set
// when it has none. Every reader that needs "what does this run run" goes
// through here — the runner, the progress figures and the summary's
// samples_expected — so the three can never disagree about the denominator.
//
// A pinned task deleted after the run was created is skipped rather than
// erroring: eval_tasks has no delete guard, the samples it already produced
// stay readable, and an expected count that no longer matches what the runner
// can dispatch would be the worse failure. Order is always creation order, the
// order the baseline convention and the per-task deltas are read in, whatever
// order the pin was written in.
func (s *Store) RunTasks(ctx context.Context, run *Run) ([]Task, error) {
	tasks, err := s.ListTasks(ctx, run.TaskSetID)
	if err != nil {
		return nil, err
	}
	if len(run.TaskIDs) == 0 {
		return tasks, nil
	}
	pinned := make(map[int64]struct{}, len(run.TaskIDs))
	for _, id := range run.TaskIDs {
		pinned[id] = struct{}{}
	}
	out := make([]Task, 0, len(run.TaskIDs))
	for _, t := range tasks {
		if _, ok := pinned[t.ID]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// ListVariants returns a run's variants in creation order. The first is the
// per-task delta baseline (convention: the incumbent is created first).
func (s *Store) ListVariants(ctx context.Context, runID int64) ([]Variant, error) {
	vs := []Variant{}
	if err := s.db.SelectContext(ctx, &vs,
		`SELECT id, run_id, name, overlay FROM eval_variants WHERE run_id = ? ORDER BY id`,
		runID); err != nil {
		return nil, fmt.Errorf("listing variants of run %d: %w", runID, err)
	}
	return vs, nil
}

// SetRunStatus updates a run's status and error text.
func (s *Store) SetRunStatus(ctx context.Context, runID int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE eval_runs SET status = ?, error = ? WHERE id = ?`, status, errMsg, runID)
	if err != nil {
		return fmt.Errorf("setting status of run %d: %w", runID, err)
	}
	return nil
}

// FinishRun writes the terminal status, error and finish time in one update.
func (s *Store) FinishRun(ctx context.Context, runID int64, status, errMsg string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE eval_runs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, now, runID)
	if err != nil {
		return fmt.Errorf("finishing run %d: %w", runID, err)
	}
	return nil
}

// AddRunCost accumulates spend on a run. Called after every sample so a
// crashed process still leaves an honest figure behind.
func (s *Store) AddRunCost(ctx context.Context, runID int64, cost float64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE eval_runs SET cost_spent = cost_spent + ? WHERE id = ?`, cost, runID)
	if err != nil {
		return fmt.Errorf("adding cost to run %d: %w", runID, err)
	}
	return nil
}

// AddJudgeCost accumulates internal-judge spend on a run. Written per item for
// the same reason sample cost is: a process that dies mid-pass still leaves an
// honest figure behind.
func (s *Store) AddJudgeCost(ctx context.Context, runID int64, cost float64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE eval_runs SET judge_cost = judge_cost + ? WHERE id = ?`, cost, runID)
	if err != nil {
		return fmt.Errorf("adding judge cost to run %d: %w", runID, err)
	}
	return nil
}

// --- Samples ---

// AddSample inserts one executed sample.
func (s *Store) AddSample(ctx context.Context, smp Sample) (*Sample, error) {
	if smp.Trace == "" {
		smp.Trace = "[]"
	}
	smp.CreatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO eval_samples
		   (run_id, variant_id, task_id, k_index, status, error, response, trace, rounds,
		    stop_reason, upstream, outcome_ok, outcome_rejected, outcome_failed, outcome_denied,
		    outcome_cached, outcome_suppressed, tokens_prompt, tokens_completion,
		    cost, latency_ms, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		smp.RunID, smp.VariantID, smp.TaskID, smp.KIndex, smp.Status, smp.Error,
		smp.Response, smp.Trace, smp.Rounds, smp.StopReason, smp.Upstream,
		smp.OutcomeOK, smp.OutcomeRejected, smp.OutcomeFailed, smp.OutcomeDenied,
		smp.OutcomeCached, smp.OutcomeSuppressed, smp.TokensPrompt, smp.TokensCompletion,
		smp.Cost, smp.LatencyMs, smp.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("adding sample for run %d: %w", smp.RunID, err)
	}
	smp.ID, err = res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("adding sample for run %d: %w", smp.RunID, err)
	}
	return &smp, nil
}

// ListSamples returns a run's samples in insertion order.
func (s *Store) ListSamples(ctx context.Context, runID int64) ([]Sample, error) {
	return s.listSamples(ctx, runID, 0)
}

// ListTaskSamples returns one task's samples within a run. The results view
// expands one test case at a time, and a full run's samples carry a trace each
// — fetching all of them to render one row is the whole reason this exists.
func (s *Store) ListTaskSamples(ctx context.Context, runID, taskID int64) ([]Sample, error) {
	return s.listSamples(ctx, runID, taskID)
}

// listSamples reads a run's samples, narrowed to one task when taskID > 0. The
// column list lives here once: a sample gained a column twice already, and two
// copies of it is how one reader silently stops carrying it.
func (s *Store) listSamples(ctx context.Context, runID, taskID int64) ([]Sample, error) {
	out := []Sample{}
	query := `SELECT id, run_id, variant_id, task_id, k_index, status, error, response, trace,
	                 rounds, stop_reason, upstream, outcome_ok, outcome_rejected, outcome_failed,
	                 outcome_denied, outcome_cached, outcome_suppressed, tokens_prompt,
	                 tokens_completion, cost, latency_ms, created_at
	          FROM eval_samples WHERE run_id = ?`
	args := []any{runID}
	if taskID > 0 {
		query += ` AND task_id = ?`
		args = append(args, taskID)
	}
	query += ` ORDER BY id`
	if err := s.db.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("listing samples of run %d: %w", runID, err)
	}
	return out, nil
}

// CountSamples returns how many samples a run has recorded so far.
func (s *Store) CountSamples(ctx context.Context, runID int64) (int, error) {
	var n int
	if err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM eval_samples WHERE run_id = ?`, runID); err != nil {
		return 0, fmt.Errorf("counting samples of run %d: %w", runID, err)
	}
	return n, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
