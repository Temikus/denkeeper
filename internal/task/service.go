package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
)

// Directory lists the den agents and external members tasks can go to. The
// operator (Human) is always a member and is added by the Service.
type Directory interface {
	Members(ctx context.Context) ([]Member, error)
}

// DirectoryFunc adapts a function to Directory.
type DirectoryFunc func(ctx context.Context) ([]Member, error)

// Members implements Directory.
func (f DirectoryFunc) Members(ctx context.Context) ([]Member, error) { return f(ctx) }

// Change describes a mutation, for live-update fan-out.
type Change struct {
	TaskID int64
	Kind   string
	Actor  string
}

// Service is the one entry point for task reads and writes. REST and both
// MCP surfaces call it, so rules, audit and notifications can't drift apart.
type Service struct {
	store    *Store
	dir      Directory
	auditor  audit.Emitter
	onChange atomic.Pointer[func(Change)]
}

// NewService wires a store to a member directory. auditor may be nil.
func NewService(store *Store, dir Directory, auditor audit.Emitter) *Service {
	return &Service{store: store, dir: dir, auditor: auditor}
}

// SetOnChange registers the live-update hook. Safe to call while serving.
func (s *Service) SetOnChange(fn func(Change)) { s.onChange.Store(&fn) }

// Members returns everyone a task can be assigned to, the operator first.
func (s *Service) Members(ctx context.Context) ([]Member, error) {
	out := []Member{{ID: Human, Name: "you", Kind: "human"}}
	if s.dir == nil {
		return out, nil
	}
	ms, err := s.dir.Members(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing task members: %w", err)
	}
	return append(out, ms...), nil
}

// Resolve turns a name into a member ID. It accepts member IDs, bare agent or
// key names, "me" (the actor), and "you"/"human" (the operator). An empty name
// stays empty, meaning unassigned.
func (s *Service) Resolve(ctx context.Context, name, actor string) (string, error) {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "":
		return "", nil
	case "me":
		return actor, nil
	case "you", Human, "operator":
		return Human, nil
	}
	members, err := s.Members(ctx)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, m := range members {
		if m.ID == name {
			return m.ID, nil
		}
		if strings.EqualFold(m.Name, name) {
			matches = append(matches, m.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", ruleErr("no member named %q. Call task_list with members=true, or check the Tasks page, for valid names.", name)
	default:
		return "", ruleErr("%q matches %s. Use the full member ID.", name, strings.Join(matches, " and "))
	}
}

// CreateRequest is a new task as callers describe it. Assignee is a name
// Resolve understands. The filer reviews by default; NoReview skips review.
type CreateRequest struct {
	Title    string
	Body     string
	Assignee string
	ParentID *int64
	NoReview bool
}

// Create files a task for actor.
func (s *Service) Create(ctx context.Context, actor string, req CreateRequest) (*Task, error) {
	title, err := cleanText("title", req.Title, MaxTitleLen, true)
	if err != nil {
		return nil, err
	}
	body, err := cleanText("body", req.Body, MaxBodyLen, false)
	if err != nil {
		return nil, err
	}
	assignee, err := s.Resolve(ctx, req.Assignee, actor)
	if err != nil {
		return nil, err
	}
	reviewer := actor
	if req.NoReview || reviewer == assignee {
		reviewer = ""
	}
	t, err := s.store.Create(ctx, CreateInput{
		Actor: actor, Title: title, Body: body, Assignee: assignee, Reviewer: reviewer, ParentID: req.ParentID,
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, actor, t.ID, EventCreated, fmt.Sprintf("%s filed %s %q", MemberName(actor), t.Key(), title))
	if req.ParentID != nil {
		s.notify(Change{TaskID: *req.ParentID, Kind: EventSubtask, Actor: actor})
	}
	return t, nil
}

// Get returns one task.
func (s *Service) Get(ctx context.Context, id int64) (*Task, error) { return s.store.Get(ctx, id) }

// Detail returns a task with its thread, subtasks and parent.
func (s *Service) Detail(ctx context.Context, id int64) (*Detail, error) {
	return s.store.Detail(ctx, id)
}

// List returns tasks matching f.
func (s *Service) List(ctx context.Context, f Filter) ([]Task, error) { return s.store.List(ctx, f) }

// Checkout claims a task for actor.
func (s *Service) Checkout(ctx context.Context, actor string, id int64) (*Task, error) {
	t, err := s.store.Checkout(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	s.record(ctx, actor, id, EventClaimed, fmt.Sprintf("%s claimed %s", MemberName(actor), t.Key()))
	return t, nil
}

// Release drops the hold on a task.
func (s *Service) Release(ctx context.Context, actor string, id int64) (*Task, error) {
	t, err := s.store.Release(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	s.record(ctx, actor, id, EventReleased, fmt.Sprintf("%s released %s", MemberName(actor), t.Key()))
	return t, nil
}

// UpdateRequest changes any of a task's editable fields. Nil fields are left
// alone. Assignee is a name Resolve understands; "" unassigns.
type UpdateRequest struct {
	Title    *string
	Body     *string
	Assignee *string
	Status   *string
	Reason   string
}

// Update applies req in the order edit, reassign, status, so a handoff and a
// status change in one call land the way a reader expects. Inputs are all
// checked before the first write.
func (s *Service) Update(ctx context.Context, actor string, id int64, req UpdateRequest) (*Task, error) {
	cur, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	title, body, err := s.editFields(cur, req)
	if err != nil {
		return nil, err
	}
	reason, err := cleanText("reason", req.Reason, MaxReasonLen, false)
	if err != nil {
		return nil, err
	}
	var assignee string
	if req.Assignee != nil {
		if assignee, err = s.Resolve(ctx, *req.Assignee, actor); err != nil {
			return nil, err
		}
	}
	t := cur
	if title != cur.Title || body != cur.Body {
		if t, err = s.store.Edit(ctx, actor, id, title, body); err != nil {
			return nil, err
		}
		s.record(ctx, actor, id, EventEdited, fmt.Sprintf("%s edited %s", MemberName(actor), t.Key()))
	}
	if req.Assignee != nil && assignee != t.Assignee {
		if t, err = s.store.Assign(ctx, actor, id, assignee); err != nil {
			return nil, err
		}
		s.record(ctx, actor, id, EventAssigned, fmt.Sprintf("%s assigned %s to %s", MemberName(actor), t.Key(), memberOrNobody(assignee)))
	}
	if req.Status != nil && *req.Status != t.Status {
		if t, err = s.store.SetStatus(ctx, actor, id, *req.Status, reason); err != nil {
			return nil, err
		}
		s.record(ctx, actor, id, EventStatus, fmt.Sprintf("%s moved %s to %s", MemberName(actor), t.Key(), t.Status))
	}
	return t, nil
}

func (s *Service) editFields(cur *Task, req UpdateRequest) (string, string, error) {
	title, body := cur.Title, cur.Body
	var err error
	if req.Title != nil {
		if title, err = cleanText("title", *req.Title, MaxTitleLen, true); err != nil {
			return "", "", err
		}
	}
	if req.Body != nil {
		if body, err = cleanText("body", *req.Body, MaxBodyLen, false); err != nil {
			return "", "", err
		}
	}
	return title, body, nil
}

// Comment adds a comment to a task's thread.
func (s *Service) Comment(ctx context.Context, actor string, id int64, body string) (*Event, error) {
	text, err := cleanText("comment", body, MaxBodyLen, true)
	if err != nil {
		return nil, err
	}
	ev, err := s.store.Comment(ctx, actor, id, text)
	if err != nil {
		return nil, err
	}
	s.record(ctx, actor, id, EventComment, fmt.Sprintf("%s commented on %s", MemberName(actor), Key(id)))
	return ev, nil
}

// record writes the audit event and fans the change out.
func (s *Service) record(ctx context.Context, actor string, id int64, kind, summary string) {
	if s.auditor != nil {
		agent := ""
		if strings.HasPrefix(actor, AgentPrefix) {
			agent = MemberName(actor)
		}
		detail, _ := json.Marshal(map[string]any{"task_id": id, "actor": actor})
		s.auditor.Emit(ctx, audit.Event{
			Timestamp: time.Now().UTC(),
			Category:  audit.CategoryTask,
			Action:    kind,
			Agent:     agent,
			Summary:   summary,
			Detail:    string(detail),
			Status:    audit.StatusOK,
			Source:    "task",
		})
	}
	s.notify(Change{TaskID: id, Kind: kind, Actor: actor})
}

func (s *Service) notify(c Change) {
	if fn := s.onChange.Load(); fn != nil && *fn != nil {
		(*fn)(c)
	}
}

func memberOrNobody(id string) string {
	if id == "" {
		return "nobody"
	}
	return MemberName(id)
}

// cleanText trims s and enforces a length limit in runes.
func cleanText(field, s string, limit int, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if required && s == "" {
		return "", ruleErr("%s is required", field)
	}
	if n := len([]rune(s)); n > limit {
		return "", ruleErr("%s is %d characters; the limit is %d", field, n, limit)
	}
	return s, nil
}
