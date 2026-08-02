package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/security"
)

// The post-turn reviewer is an unattended engine steered by LLM output that is
// shaped by conversation content, including messages from external adapters.
// configmcp has no approval path of its own and the reviewer sends through a
// no-op, so it cannot be supervised — its only safety boundary is the set of
// dependencies it is handed. These tests pin that boundary.

// reviewerTestDeps builds the reviewer's Deps the way buildReviewerEngine does.
// Method values on a nil *agent.Engine are legal to create (they only panic if
// called), which is what makes the dependency shape testable without standing
// up a router and an LLM client.
func reviewerTestDeps(t *testing.T) configmcp.Deps {
	t.Helper()
	store, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return reviewerConfigDeps("test-agent", (*agent.Engine)(nil),
		func() string { return reviewerTier }, store, logger)
}

// TestReviewerConfigDeps_Shape asserts the exact set of populated fields.
// Deliberately reflection-based rather than a hand-written list of nil checks:
// a future refactor that adds a dependency back out of habit must fail here.
func TestReviewerConfigDeps_Shape(t *testing.T) {
	deps := reviewerTestDeps(t)

	want := []string{
		"AgentName",
		"AppendMemoryEntry",
		"BumpSkillView",
		"GetPersonaSection",
		"GetSkill",
		"GetSkills",
		"Logger",
		"PermissionTier",
	}

	v := reflect.ValueOf(deps)
	typ := v.Type()
	var got []string
	for i := range typ.NumField() {
		if !v.Field(i).IsZero() {
			got = append(got, typ.Field(i).Name)
		}
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("reviewer Deps populated fields =\n  %v\nwant\n  %v", got, want)
	}
}

// TestReviewerConfigDeps_ForbiddenDepsNil names the security-critical fields
// individually, so a regression reports the leaked capability rather than a
// diff of field names.
func TestReviewerConfigDeps_ForbiddenDepsNil(t *testing.T) {
	deps := reviewerTestDeps(t)

	if deps.UpdateSkill != nil {
		t.Error("UpdateSkill wired: reviewer could rewrite skill bodies, which are re-injected into the system prompt")
	}
	if deps.SavePersonaSection != nil {
		t.Error("SavePersonaSection wired: reviewer could rewrite soul/identity/user")
	}
	if deps.RemoveMemoryEntry != nil {
		t.Error("RemoveMemoryEntry wired: reviewer memory access must be append-only")
	}
	if deps.AgentSkillsDir != "" {
		t.Error("AgentSkillsDir set: reviewer could create or delete skill files")
	}
	if deps.AppendSkill != nil {
		t.Error("AppendSkill wired: reviewer could create skills")
	}
	if deps.RemoveSkill != nil {
		t.Error("RemoveSkill wired: reviewer could delete skills")
	}
	if deps.Sched != nil {
		t.Error("Sched wired: reviewer could change schedules")
	}
	if deps.HandleMessage != nil {
		t.Error("HandleMessage wired: reviewer could drive other agents")
	}
	if deps.LifecycleMgr != nil {
		t.Error("LifecycleMgr wired: reviewer could install tools or plugins")
	}
	if deps.KVStore != nil {
		t.Error("KVStore wired: reviewer write surface must stay limited to persona memory")
	}
	if deps.ConfigPath != "" {
		t.Error("ConfigPath set: reviewer could persist config changes")
	}
	if deps.SetFallbacks != nil {
		t.Error("SetFallbacks wired: reviewer could reroute the LLM router")
	}
	if deps.BrowserProfiles != nil {
		t.Error("BrowserProfiles wired: reviewer could touch browser state")
	}
}

// TestReviewerToolSet_Pinned is the end-to-end regression pin: it exercises the
// whole chain (Deps → registration gates → persona helper) and fails if any
// link starts advertising more capability.
func TestReviewerToolSet_Pinned(t *testing.T) {
	srv := configmcp.New(reviewerTestDeps(t))
	session, err := srv.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	var got []string
	for _, tl := range result.Tools {
		got = append(got, tl.Name)
	}
	sort.Strings(got)

	want := []string{
		"persona_get",
		"persona_memory_manage",
		"skill_get",
		"skill_list",
		"skill_read_file",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reviewer tool set =\n  %v\nwant\n  %v", got, want)
	}

	// persona_memory_manage must advertise append and nothing else.
	for _, tl := range result.Tools {
		if tl.Name != "persona_memory_manage" {
			continue
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("marshaling schema: %v", err)
		}
		var schema struct {
			Properties struct {
				Operation struct {
					Enum []string `json:"enum"`
				} `json:"operation"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshaling schema: %v", err)
		}
		if !reflect.DeepEqual(schema.Properties.Operation.Enum, []string{"append"}) {
			t.Errorf("persona_memory_manage operations = %v, want [append]", schema.Properties.Operation.Enum)
		}
	}
}

// TestReviewerTier documents, in executable form, why the reviewer is not
// built at the restricted tier: restricted omits use_tools, which the engine
// checks before any tool round, so the reviewer could not reach even its
// append-only memory tool.
func TestReviewerTier(t *testing.T) {
	perms, err := security.NewPermissionEngine(reviewerTier)
	if err != nil {
		t.Fatalf("NewPermissionEngine(%q): %v", reviewerTier, err)
	}
	if !perms.CanExecute("use_tools") {
		t.Fatalf("tier %q cannot use tools; the reviewer would be unable to append to memory", reviewerTier)
	}
	for _, action := range []string{"create_skill", "modify_schedule", "execute_shell", "access_filesystem"} {
		if perms.CanExecute(action) {
			t.Errorf("tier %q grants %q; the reviewer should not hold it", reviewerTier, action)
		}
	}

	restricted, err := security.NewPermissionEngine("restricted")
	if err != nil {
		t.Fatalf("NewPermissionEngine(restricted): %v", err)
	}
	if restricted.CanExecute("use_tools") {
		t.Error("restricted now grants use_tools; reconsider using it for the reviewer")
	}
}
