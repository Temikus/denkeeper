package task

import (
	"context"
	"database/sql"
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

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    title          TEXT     NOT NULL,
    body           TEXT     NOT NULL DEFAULT '',
    status         TEXT     NOT NULL DEFAULT 'todo',
    assignee       TEXT     NOT NULL DEFAULT '',
    created_by     TEXT     NOT NULL,
    reviewer       TEXT     NOT NULL DEFAULT '',
    parent_id      INTEGER  REFERENCES tasks(id),
    depth          INTEGER  NOT NULL DEFAULT 0,
    holder         TEXT     NOT NULL DEFAULT '',
    held_at        DATETIME,
    blocked_reason TEXT     NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    updated_at     DATETIME NOT NULL,
    closed_at      DATETIME
);
CREATE INDEX IF NOT EXISTS idx_tasks_status   ON tasks (status);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks (assignee);
CREATE INDEX IF NOT EXISTS idx_tasks_parent   ON tasks (parent_id);
CREATE TABLE IF NOT EXISTS task_events (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER  NOT NULL REFERENCES tasks(id),
    at      DATETIME NOT NULL,
    actor   TEXT     NOT NULL,
    kind    TEXT     NOT NULL,
    body    TEXT     NOT NULL DEFAULT '',
    data    TEXT     NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events (task_id, id);
`

const taskCols = `t.id, t.title, t.body, t.status, t.assignee, t.created_by, t.reviewer,
	t.parent_id, t.depth, t.holder, t.held_at, t.blocked_reason, t.created_at, t.updated_at, t.closed_at,
	(SELECT COUNT(*) FROM tasks c WHERE c.parent_id = t.id) AS children_total,
	(SELECT COUNT(*) FROM tasks c WHERE c.parent_id = t.id AND c.status = 'done') AS children_done`

// Store persists tasks and their threads. Every mutation runs in a
// BEGIN IMMEDIATE transaction, so a rule check and the write it guards see
// the same row even when two members race.
type Store struct {
	db          *sqlx.DB
	maxDepth    int
	maxChildren int
}

// Option configures a Store.
type Option func(*Store)

// WithMaxDepth caps subtask nesting. A top-level task has depth 0.
func WithMaxDepth(n int) Option { return func(s *Store) { s.maxDepth = n } }

// WithMaxChildren caps the number of subtasks under one parent.
func WithMaxChildren(n int) Option { return func(s *Store) { s.maxChildren = n } }

// NewSQLiteStore opens the task tables in the database at dbPath.
func NewSQLiteStore(dbPath string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}
	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening task database: %w", err)
	}
	return newStore(db, opts)
}

// NewInMemoryStore creates a private in-memory store for tests.
func NewInMemoryStore(opts ...Option) (*Store, error) {
	db, err := sqlx.Open("sqlite", ":memory:?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory task database: %w", err)
	}
	// Each connection to ":memory:" is a separate database.
	db.SetMaxOpenConns(1)
	return newStore(db, opts)
}

func newStore(db *sqlx.DB, opts []Option) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing task schema: %w", err)
	}
	s := &Store{db: db, maxDepth: DefaultMaxDepth, maxChildren: DefaultMaxChildren}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Close closes the database handle.
func (s *Store) Close() error { return s.db.Close() }

// CreateInput describes a new task. Member fields hold member IDs.
type CreateInput struct {
	Actor    string
	Title    string
	Body     string
	Assignee string
	Reviewer string
	ParentID *int64
}

// Create inserts a task and its "created" event. A subtask also adds a
// "subtask" event to its parent's thread.
func (s *Store) Create(ctx context.Context, in CreateInput) (*Task, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sqlx.Tx) error {
		depth, err := s.checkParent(ctx, tx, in.ParentID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		res, err := tx.ExecContext(ctx, `INSERT INTO tasks
			(title, body, assignee, created_by, reviewer, parent_id, depth, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Title, in.Body, in.Assignee, in.Actor, in.Reviewer, in.ParentID, depth, now, now)
		if err != nil {
			return fmt.Errorf("inserting task: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("reading task id: %w", err)
		}
		if err := addEvent(ctx, tx, id, in.Actor, EventCreated, "", map[string]any{"assignee": in.Assignee, "reviewer": in.Reviewer}); err != nil {
			return err
		}
		if in.ParentID != nil {
			return addEvent(ctx, tx, *in.ParentID, in.Actor, EventSubtask, in.Title, map[string]any{"child_id": id, "assignee": in.Assignee})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// checkParent returns the depth a child of parentID gets, or a RuleError
// when the parent is closed, too deep or full.
func (s *Store) checkParent(ctx context.Context, tx *sqlx.Tx, parentID *int64) (int, error) {
	if parentID == nil {
		return 0, nil
	}
	parent, err := getTask(ctx, tx, *parentID)
	if err != nil {
		return 0, err
	}
	if parent.Closed() {
		return 0, ruleErr("%s is %s, so it can't take new subtasks. File a new top-level task instead.", parent.Key(), parent.Status)
	}
	if parent.Depth+1 > s.maxDepth {
		return 0, ruleErr("%s is already %d levels deep. Finish it yourself, or comment on its parent to ask for a different split.", parent.Key(), parent.Depth)
	}
	if parent.ChildrenTotal >= s.maxChildren {
		return 0, ruleErr("%s already has %d subtasks, the limit. Finish some before filing more.", parent.Key(), parent.ChildrenTotal)
	}
	return parent.Depth + 1, nil
}

// Get returns one task with its subtask counts.
func (s *Store) Get(ctx context.Context, id int64) (*Task, error) {
	return getTask(ctx, s.db, id)
}

// Detail returns a task with its thread, subtasks and parent.
func (s *Store) Detail(ctx context.Context, id int64) (*Detail, error) {
	t, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &Detail{Task: *t, Events: []Event{}, Children: []Task{}}
	if err := s.db.SelectContext(ctx, &d.Events,
		`SELECT id, task_id, at, actor, kind, body, data FROM task_events WHERE task_id = ? ORDER BY id`, id); err != nil {
		return nil, fmt.Errorf("listing events for %s: %w", t.Key(), err)
	}
	if err := s.db.SelectContext(ctx, &d.Children,
		`SELECT `+taskCols+` FROM tasks t WHERE t.parent_id = ? ORDER BY t.id`, id); err != nil {
		return nil, fmt.Errorf("listing subtasks of %s: %w", t.Key(), err)
	}
	if t.ParentID != nil {
		p, err := s.Get(ctx, *t.ParentID)
		if err != nil {
			return nil, err
		}
		d.Parent = p
	}
	return d, nil
}

// Filter narrows List. Zero values mean "no filter", except ClosedSince:
// closed tasks are returned only when they closed at or after it.
type Filter struct {
	Statuses    []string
	Assignee    string
	Unassigned  bool // only tasks nobody is assigned to; ignored when Assignee is set
	ParentID    *int64
	TopLevel    bool
	ClosedSince time.Time
	Limit       int
}

// List returns tasks newest first.
func (s *Store) List(ctx context.Context, f Filter) ([]Task, error) {
	where := []string{"(t.closed_at IS NULL OR t.closed_at >= ?)"}
	args := []any{f.ClosedSince.UTC()}
	if len(f.Statuses) > 0 {
		where = append(where, "t.status IN (?"+strings.Repeat(", ?", len(f.Statuses)-1)+")")
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.Assignee != "" {
		where = append(where, "t.assignee = ?")
		args = append(args, f.Assignee)
	} else if f.Unassigned {
		where = append(where, "t.assignee = ''")
	}
	if f.ParentID != nil {
		where = append(where, "t.parent_id = ?")
		args = append(args, *f.ParentID)
	} else if f.TopLevel {
		where = append(where, "t.parent_id IS NULL")
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	args = append(args, limit)
	out := []Task{}
	q := `SELECT ` + taskCols + ` FROM tasks t WHERE ` + strings.Join(where, " AND ") + ` ORDER BY t.id DESC LIMIT ?`
	if err := s.db.SelectContext(ctx, &out, q, args...); err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	return out, nil
}

// Checkout claims a task for actor. It succeeds when the task is open, not
// held by someone else, and unassigned or assigned to actor; an unassigned
// task becomes actor's. Claiming a task you already hold is a no-op.
func (s *Store) Checkout(ctx context.Context, actor string, id int64) (*Task, error) {
	err := s.inTx(ctx, func(tx *sqlx.Tx) error {
		t, err := getTask(ctx, tx, id)
		if err != nil {
			return err
		}
		if t.Holder == actor && t.Status == StatusInProgress {
			return nil
		}
		now := time.Now().UTC()
		res, err := tx.ExecContext(ctx, `UPDATE tasks SET
				holder = ?, held_at = ?, status = 'in_progress', blocked_reason = '',
				assignee = CASE WHEN assignee = '' THEN ? ELSE assignee END, updated_at = ?
			WHERE id = ? AND (holder = '' OR holder = ?)
				AND status IN ('todo', 'in_progress', 'blocked')
				AND (assignee = '' OR assignee = ?)`,
			actor, now, actor, now, id, actor, actor)
		if err != nil {
			return fmt.Errorf("claiming %s: %w", t.Key(), err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return checkoutConflict(t, actor)
		}
		return addEvent(ctx, tx, id, actor, EventClaimed, "", nil)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// checkoutConflict explains why a claim found no row to update.
func checkoutConflict(t *Task, actor string) error {
	c := &ConflictError{TaskID: t.ID, Holder: t.Holder}
	switch {
	case t.Holder != "" && t.Holder != actor:
		since := ""
		if t.HeldAt != nil {
			since = " since " + t.HeldAt.UTC().Format("15:04 MST")
		}
		c.Message = fmt.Sprintf("%s is held by %s%s. Don't retry. Pick another task, or comment on this one.",
			t.Key(), MemberName(t.Holder), since)
	case t.Assignee != "" && t.Assignee != actor:
		c.Message = fmt.Sprintf("%s is assigned to %s. Don't retry. Comment on it if you think it should move to you.",
			t.Key(), MemberName(t.Assignee))
	default:
		c.Message = fmt.Sprintf("%s is %s and can't be claimed. Don't retry.", t.Key(), t.Status)
	}
	return c
}

// Release drops the hold on a task and returns it to To do. Only the holder
// and the operator may release.
func (s *Store) Release(ctx context.Context, actor string, id int64) (*Task, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx *sqlx.Tx, t *Task) error {
		if t.Holder == "" {
			return ruleErr("%s isn't held by anyone.", t.Key())
		}
		if actor != Human && actor != t.Holder {
			return ruleErr("%s is held by %s. Only they or the operator can release it.", t.Key(), MemberName(t.Holder))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET holder = '', held_at = NULL,
				status = CASE WHEN status = 'in_progress' THEN 'todo' ELSE status END, updated_at = ?
			WHERE id = ?`, time.Now().UTC(), id); err != nil {
			return fmt.Errorf("releasing %s: %w", t.Key(), err)
		}
		return addEvent(ctx, tx, id, actor, EventReleased, "", map[string]any{"holder": t.Holder})
	})
}

// SetStatus moves a task to status. reason is required for blocked.
func (s *Store) SetStatus(ctx context.Context, actor string, id int64, status, reason string) (*Task, error) {
	if !ValidStatus(status) {
		return nil, ruleErr("unknown status %q", status)
	}
	return s.mutate(ctx, id, func(ctx context.Context, tx *sqlx.Tx, t *Task) error {
		if t.Status == status {
			return nil
		}
		if err := checkStatusRule(actor, t, status, reason); err != nil {
			return err
		}
		now := time.Now().UTC()
		var closedAt *time.Time
		if IsClosed(status) {
			closedAt = &now
		}
		blocked := ""
		if status == StatusBlocked {
			blocked = reason
		}
		// Leaving in_progress drops the hold; only checkout sets one.
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = ?, blocked_reason = ?, closed_at = ?,
				holder = CASE WHEN ? = 'in_progress' THEN holder ELSE '' END,
				held_at = CASE WHEN ? = 'in_progress' THEN held_at ELSE NULL END,
				updated_at = ?
			WHERE id = ?`, status, blocked, closedAt, status, status, now, id); err != nil {
			return fmt.Errorf("updating %s: %w", t.Key(), err)
		}
		return addEvent(ctx, tx, id, actor, EventStatus, reason, map[string]any{"from": t.Status, "to": status})
	})
}

// Assign hands a task to assignee (a member ID, or "" to unassign). It drops
// any hold and sends in-progress work back to To do.
func (s *Store) Assign(ctx context.Context, actor string, id int64, assignee string) (*Task, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx *sqlx.Tx, t *Task) error {
		if t.Assignee == assignee {
			return nil
		}
		if t.Closed() {
			return ruleErr("%s is %s. Reopen it before reassigning.", t.Key(), t.Status)
		}
		if actor != Human && actor != t.CreatedBy && actor != t.Assignee {
			return ruleErr("only the operator, the filer (%s) or the assignee can reassign %s. Comment instead.", MemberName(t.CreatedBy), t.Key())
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET assignee = ?, holder = '', held_at = NULL,
				status = CASE WHEN status = 'in_progress' THEN 'todo' ELSE status END, updated_at = ?
			WHERE id = ?`, assignee, time.Now().UTC(), id); err != nil {
			return fmt.Errorf("reassigning %s: %w", t.Key(), err)
		}
		return addEvent(ctx, tx, id, actor, EventAssigned, "", map[string]any{"from": t.Assignee, "to": assignee})
	})
}

// Edit changes a task's title and body. Only the operator and the filer may edit.
func (s *Store) Edit(ctx context.Context, actor string, id int64, title, body string) (*Task, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx *sqlx.Tx, t *Task) error {
		if t.Title == title && t.Body == body {
			return nil
		}
		if actor != Human && actor != t.CreatedBy {
			return ruleErr("only the operator or the filer (%s) can edit %s. Comment instead.", MemberName(t.CreatedBy), t.Key())
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ?, body = ?, updated_at = ? WHERE id = ?`,
			title, body, time.Now().UTC(), id); err != nil {
			return fmt.Errorf("editing %s: %w", t.Key(), err)
		}
		return addEvent(ctx, tx, id, actor, EventEdited, "", nil)
	})
}

// Comment adds a comment to a task's thread. Closed tasks take comments too.
func (s *Store) Comment(ctx context.Context, actor string, id int64, body string) (*Event, error) {
	var ev Event
	err := s.inTx(ctx, func(tx *sqlx.Tx) error {
		if _, err := getTask(ctx, tx, id); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, id, actor, EventComment, body, nil); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET updated_at = ? WHERE id = ?`, time.Now().UTC(), id); err != nil {
			return fmt.Errorf("touching %s: %w", Key(id), err)
		}
		return tx.GetContext(ctx, &ev, `SELECT id, task_id, at, actor, kind, body, data FROM task_events
			WHERE task_id = ? ORDER BY id DESC LIMIT 1`, id)
	})
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// mutate loads a task inside a transaction, runs fn, and returns the task as
// it is after the commit.
func (s *Store) mutate(ctx context.Context, id int64, fn func(context.Context, *sqlx.Tx, *Task) error) (*Task, error) {
	err := s.inTx(ctx, func(tx *sqlx.Tx) error {
		t, err := getTask(ctx, tx, id)
		if err != nil {
			return err
		}
		return fn(ctx, tx, t)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Store) inTx(ctx context.Context, fn func(*sqlx.Tx) error) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning task transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing task transaction: %w", err)
	}
	return nil
}

func getTask(ctx context.Context, q sqlx.QueryerContext, id int64) (*Task, error) {
	var t Task
	err := sqlx.GetContext(ctx, q, &t, `SELECT `+taskCols+` FROM tasks t WHERE t.id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s: %w", Key(id), ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", Key(id), err)
	}
	return &t, nil
}

func addEvent(ctx context.Context, tx *sqlx.Tx, taskID int64, actor, kind, body string, data map[string]any) error {
	encoded := ""
	if len(data) > 0 {
		b, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("encoding %s event: %w", kind, err)
		}
		encoded = string(b)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_events (task_id, at, actor, kind, body, data) VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, time.Now().UTC(), actor, kind, body, encoded); err != nil {
		return fmt.Errorf("recording %s event on %s: %w", kind, Key(taskID), err)
	}
	return nil
}
