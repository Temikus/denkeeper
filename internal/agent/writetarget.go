package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Temikus/denkeeper/internal/agentctx"
	"github.com/Temikus/denkeeper/internal/llm"
)

// Write target kinds and actions.
const (
	WriteTargetSkill = "skill"
	WriteTargetKVKey = "kv_key"

	WriteActionCreate = "create"
	WriteActionUpdate = "update"
	WriteActionDelete = "delete"
)

// WriteTarget is what a write tool call would create, replace or delete.
type WriteTarget struct {
	Kind          string     `json:"kind"`   // WriteTargetSkill or WriteTargetKVKey
	Name          string     `json:"name"`   // skill name or KV key, as the model chose it
	Action        string     `json:"action"` // WriteActionCreate, WriteActionUpdate or WriteActionDelete
	Exists        bool       `json:"exists"`
	LastWrittenBy string     `json:"last_written_by,omitempty"` // KV only; "" = not recorded
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`      // KV only
}

// KVTargetStore is the KV state a write target needs, kept as an interface so
// agent does not import kv. StampKVWriter is called only by the engine after a
// successful kv_set: no other path may set the writer, or an agent could forge
// "last written by <trusted skill>".
type KVTargetStore interface {
	KVTarget(ctx context.Context, agent, key string) (exists bool, lastWrittenBy string, updatedAt time.Time, err error)
	StampKVWriter(ctx context.Context, agent, key, skill string) error
}

// SetKVTargets wires the KV store. Nil (KV off) means KV calls get no target
// and no writer stamp.
func (e *Engine) SetKVTargets(s KVTargetStore) {
	e.kvTargetsMu.Lock()
	defer e.kvTargetsMu.Unlock()
	e.kvTargets = s
}

func (e *Engine) kvTargetStore() KVTargetStore {
	e.kvTargetsMu.RLock()
	defer e.kvTargetsMu.RUnlock()
	return e.kvTargets
}

type writeToolSpec struct {
	kind   string
	action string // "" for kv_set: create or update, depending on the key
}

// writeTools is keyed by the server-local name of an in-process tool. An
// external server's kv_set is somebody else's store, so it gets no target.
var writeTools = map[string]writeToolSpec{
	"skill_create":     {WriteTargetSkill, WriteActionCreate},
	"skill_update":     {WriteTargetSkill, WriteActionUpdate},
	"skill_patch":      {WriteTargetSkill, WriteActionUpdate},
	"skill_write_file": {WriteTargetSkill, WriteActionUpdate},
	"skill_delete":     {WriteTargetSkill, WriteActionDelete},
	"kv_set":           {WriteTargetKVKey, ""},
	"kv_delete":        {WriteTargetKVKey, WriteActionDelete},
}

// The argument structs carry the config MCP handlers' JSON tags, so key case
// and duplicate keys resolve the way the handler will resolve them.
type skillTargetArgs struct {
	Name string `json:"name"`
}

type kvTargetArgs struct {
	Key string `json:"key"`
}

// builtinLocal returns toolName's local name when an in-process server owns it.
func (e *Engine) builtinLocal(toolName string) (string, bool) {
	if e.tools == nil {
		return "", false
	}
	return e.tools.BuiltinLocalName(toolName)
}

// ResolveWriteTarget returns nil when the call is not one of the write tools
// above, the tool is not one of ours, the arguments do not parse, or the
// lookup fails. A failed lookup is logged and never blocks a review.
func (e *Engine) ResolveWriteTarget(ctx context.Context, toolName, arguments string) *WriteTarget {
	local, ok := e.builtinLocal(toolName)
	if !ok {
		return nil
	}
	spec, ok := writeTools[local]
	if !ok {
		return nil
	}
	if spec.kind == WriteTargetSkill {
		var args skillTargetArgs
		if json.Unmarshal([]byte(arguments), &args) != nil || args.Name == "" {
			return nil
		}
		// skill_update with new_name still reports name; the rename is in the arguments.
		_, exists := e.GetSkill(args.Name)
		return &WriteTarget{Kind: spec.kind, Name: args.Name, Action: spec.action, Exists: exists}
	}
	var args kvTargetArgs
	if json.Unmarshal([]byte(arguments), &args) != nil || args.Key == "" {
		return nil
	}
	return e.resolveKVTarget(ctx, spec, args.Key)
}

func (e *Engine) resolveKVTarget(ctx context.Context, spec writeToolSpec, key string) *WriteTarget {
	store := e.kvTargetStore()
	if store == nil {
		return nil
	}
	exists, writer, updatedAt, err := store.KVTarget(ctx, e.name, key)
	if err != nil {
		e.logger.Warn("supervisor: KV target lookup failed", "key", key, "error", err)
		return nil
	}
	t := &WriteTarget{Kind: WriteTargetKVKey, Name: key, Action: spec.action, Exists: exists}
	if t.Action == "" {
		t.Action = WriteActionCreate
		if exists {
			t.Action = WriteActionUpdate
		}
	}
	if exists {
		t.LastWrittenBy = writer
		at := updatedAt.UTC()
		t.UpdatedAt = &at
	}
	return t
}

// markdownLine renders the supervisor's Target line. Names, keys and the
// writer are model-chosen text, so all go through %q.
func (t *WriteTarget) markdownLine() string {
	if !t.Exists {
		if t.Kind == WriteTargetSkill {
			return fmt.Sprintf("**Target**: skill %q does not exist.", t.Name)
		}
		return fmt.Sprintf("**Target**: KV key %q does not exist.", t.Name)
	}
	if t.Kind == WriteTargetSkill {
		switch t.Action {
		case WriteActionCreate:
			return fmt.Sprintf("**Target**: skill %q already exists. skill_create will be refused; the agent may be trying to overwrite it.", t.Name)
		case WriteActionDelete:
			return fmt.Sprintf("**Target**: skill %q already exists. This call deletes it.", t.Name)
		default:
			return fmt.Sprintf("**Target**: skill %q already exists. This call replaces it.", t.Name)
		}
	}
	verb := "replaces"
	if t.Action == WriteActionDelete {
		verb = "deletes"
	}
	return fmt.Sprintf("**Target**: KV key %q already exists (%s). This call %s it.", t.Name, t.lastWritten(), verb)
}

func (t *WriteTarget) lastWritten() string {
	date := "at an unknown time"
	if t.UpdatedAt != nil {
		date = t.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if t.LastWrittenBy == "" {
		return "last written " + date + "; the writing skill is not recorded"
	}
	return fmt.Sprintf("last written %s by skill %q", date, t.LastWrittenBy)
}

// addTargetDetail adds the write target to a review audit detail. arguments
// stays untouched: shadow and supervisor reviews are paired on it.
func addTargetDetail(detail map[string]any, in *supervisorReviewInput) {
	if in != nil && in.target != nil {
		detail["target"] = in.target
	}
}

// stampKVWriter records the turn's skill as the writer of a key the call just
// set. It runs only after a successful call; dry-run and eval turns never get
// here, since their writes are suppressed before execution. A failed stamp is
// logged and never fails the call.
func (e *Engine) stampKVWriter(ctx context.Context, tc llm.ToolCall, result string) {
	sk := agentctx.SkillContext(ctx)
	if sk == nil || sk.Name == "" {
		return
	}
	store := e.kvTargetStore()
	if store == nil {
		return
	}
	local, ok := e.builtinLocal(tc.Function.Name)
	if !ok || !kvCallSetKey(local, result) {
		return
	}
	var args kvTargetArgs
	if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil || args.Key == "" {
		return
	}
	if err := store.StampKVWriter(ctx, e.name, args.Key, sk.Name); err != nil {
		e.logger.Warn("failed to record KV writer", "key", args.Key, "skill", sk.Name, "error", err)
	}
}

// kvCallSetKey reports whether a successful call to the local tool wrote the
// key. kv_set_nx writes only when its result says it acquired the key.
func kvCallSetKey(local, result string) bool {
	switch local {
	case "kv_set":
		return true
	case "kv_set_nx":
		var r struct {
			Acquired bool `json:"acquired"`
		}
		return json.Unmarshal([]byte(strings.TrimSpace(result)), &r) == nil && r.Acquired
	default:
		return false
	}
}
