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
	WriteActionRename = "rename" // skill_update with a new_name
)

// WriteTarget is what a write tool call would create, replace or delete.
type WriteTarget struct {
	Kind          string     `json:"kind"`   // WriteTargetSkill or WriteTargetKVKey
	Name          string     `json:"name"`   // skill name or KV key, as the model chose it
	Action        string     `json:"action"` // one of the WriteAction constants
	Exists        bool       `json:"exists"`
	LastWrittenBy string     `json:"last_written_by,omitempty"` // KV only; "" = not recorded
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`      // KV only
	NewName       string     `json:"new_name,omitempty"`        // rename only: the destination name
	NewNameExists bool       `json:"new_name_exists,omitempty"` // rename only: a skill already has NewName
}

// KVTargetStore is the KV state a write target needs, kept as an interface so
// agent does not import kv. StampKVWriter is called only by the engine after a
// successful kv_set: no other path may set the writer, or an agent could forge
// "last written by <trusted skill>". It stamps only while the key still holds
// value, so a newer concurrent write is never attributed to this one.
type KVTargetStore interface {
	KVTarget(ctx context.Context, agent, key string) (exists bool, lastWrittenBy string, updatedAt time.Time, err error)
	StampKVWriter(ctx context.Context, agent, key, value, skill string) error
}

// SetSkillFileCheck wires the on-disk existence check the skill_create
// handler uses, so a create it will refuse is not shown as a fresh one. Nil
// means loaded skills only.
func (e *Engine) SetSkillFileCheck(fn func(name string) (bool, error)) {
	e.kvTargetsMu.Lock()
	defer e.kvTargetsMu.Unlock()
	e.skillFileExists = fn
}

// skillExists answers the way the skill_create handler does: a loaded skill,
// or a file for it in the agent's skills directory.
func (e *Engine) skillExists(name string) (bool, error) {
	if _, ok := e.GetSkill(name); ok {
		return true, nil
	}
	e.kvTargetsMu.RLock()
	fn := e.skillFileExists
	e.kvTargetsMu.RUnlock()
	if fn == nil {
		return false, nil
	}
	return fn(name)
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
	argKey string // the argument the handler reads the target from
}

// writeTools is keyed by the server-local name of an in-process tool. An
// external server's kv_set is somebody else's store, so it gets no target.
// argKey must match the handler's field: skill_write_file reads "skill" and
// ignores "name", so reading "name" there lets a decoy hide an overwrite.
var writeTools = map[string]writeToolSpec{
	"skill_create":     {WriteTargetSkill, WriteActionCreate, "name"},
	"skill_update":     {WriteTargetSkill, WriteActionUpdate, "name"},
	"skill_patch":      {WriteTargetSkill, WriteActionUpdate, "name"},
	"skill_write_file": {WriteTargetSkill, WriteActionUpdate, "skill"},
	"skill_delete":     {WriteTargetSkill, WriteActionDelete, "name"},
	"kv_set":           {WriteTargetKVKey, "", "key"},
	"kv_delete":        {WriteTargetKVKey, WriteActionDelete, "key"},
}

// The argument structs carry the config MCP handlers' JSON tags, so key case
// and duplicate keys resolve the way the handler will resolve them. Each
// holds one field, so no other argument can be read as the target.
type skillNameArgs struct {
	Name string `json:"name"`
}

type skillFileArgs struct {
	Skill string `json:"skill"`
}

type kvTargetArgs struct {
	Key string `json:"key"`
}

// skillRenameArgs reads skill_update's new_name, the handler's rename field.
type skillRenameArgs struct {
	NewName *string `json:"new_name"`
}

// kvWriteArgs reads what kv_set and kv_set_nx wrote, for the writer stamp.
type kvWriteArgs struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// targetArg decodes only the argument named by argKey. "" means the
// arguments do not parse or the field is empty.
func targetArg(argKey, arguments string) string {
	switch argKey {
	case "name":
		var a skillNameArgs
		if json.Unmarshal([]byte(arguments), &a) != nil {
			return ""
		}
		return a.Name
	case "skill":
		var a skillFileArgs
		if json.Unmarshal([]byte(arguments), &a) != nil {
			return ""
		}
		return a.Skill
	case "key":
		var a kvTargetArgs
		if json.Unmarshal([]byte(arguments), &a) != nil {
			return ""
		}
		return a.Key
	default:
		return ""
	}
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
	name := targetArg(spec.argKey, arguments)
	if name == "" {
		return nil
	}
	if spec.kind == WriteTargetSkill {
		return e.resolveSkillTarget(local, spec, name, arguments)
	}
	return e.resolveKVTarget(ctx, spec, name)
}

func (e *Engine) resolveSkillTarget(local string, spec writeToolSpec, name, arguments string) *WriteTarget {
	exists, err := e.skillExists(name)
	if err != nil {
		e.logger.Warn("supervisor: skill target lookup failed", "skill", name, "error", err)
		return nil
	}
	t := &WriteTarget{Kind: spec.kind, Name: name, Action: spec.action, Exists: exists}
	if local != "skill_update" {
		return t
	}
	var r skillRenameArgs
	if json.Unmarshal([]byte(arguments), &r) != nil || r.NewName == nil {
		return t
	}
	newName := strings.TrimSpace(*r.NewName)
	if newName == "" || newName == name {
		return t
	}
	newExists, err := e.skillExists(newName)
	if err != nil {
		e.logger.Warn("supervisor: skill target lookup failed", "skill", newName, "error", err)
		return nil
	}
	t.Action, t.NewName, t.NewNameExists = WriteActionRename, newName, newExists
	return t
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
		case WriteActionRename:
			dest := "does not exist"
			if t.NewNameExists {
				dest = "already exists"
			}
			return fmt.Sprintf("**Target**: skill %q already exists. This call renames it to %q, which %s.", t.Name, t.NewName, dest)
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

// stampKVWriter records the turn's scheduled skill as the writer of a key the
// call just set. It runs only after a successful call; dry-run and eval turns
// never get here, since their writes are suppressed before execution. The
// stamp applies only while the key still holds the value this call wrote. A
// failed stamp is logged and never fails the call.
func (e *Engine) stampKVWriter(ctx context.Context, tc llm.ToolCall, result string) {
	sk := agentctx.SkillContext(ctx)
	// Scheduled only: the writer is documented as "which scheduled skill's
	// turn made the write", and only schedule-driven turns name their skill.
	if sk == nil || sk.Name == "" || !sk.IsScheduled {
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
	var args kvWriteArgs
	if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil || args.Key == "" {
		return
	}
	if err := store.StampKVWriter(ctx, e.name, args.Key, args.Value, sk.Name); err != nil {
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
