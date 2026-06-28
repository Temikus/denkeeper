package configmcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
)

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func newTestLogger(_ *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func newTestServer(t *testing.T, overrides func(*configmcp.Deps)) (*mcp.ClientSession, *configmcp.Deps) {
	t.Helper()
	dir := t.TempDir()

	var mu sync.RWMutex
	var skills []skill.Skill

	deps := &configmcp.Deps{
		AgentName:      "test-agent",
		AgentSkillsDir: filepath.Join(dir, "skills"),
		GetSkills: func() []skill.Skill {
			mu.RLock()
			defer mu.RUnlock()
			return skills
		},
		AppendSkill: func(s skill.Skill) {
			mu.Lock()
			defer mu.Unlock()
			skills = append(skills, s)
		},
		Sched: scheduler.New(newTestLogger(t), nil),
		HandleMessage: func(_ context.Context, _ adapter.IncomingMessage) error {
			return nil
		},
		PermissionTier: func() string { return "autonomous" },
		Logger:         newTestLogger(t),
	}

	if overrides != nil {
		overrides(deps)
	}

	srv := configmcp.New(*deps)
	session, err := srv.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, deps
}

// callTool invokes a named tool and returns the first text content and isError.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshaling args: %v", err)
	}
	var argsMap map[string]any
	if err := json.Unmarshal(argsJSON, &argsMap); err != nil {
		t.Fatalf("unmarshaling args: %v", err)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: argsMap,
	})
	if err != nil {
		t.Fatalf("CallTool %q: %v", name, err)
	}

	var text string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = tc.Text
			break
		}
	}
	return text, result.IsError
}

// --------------------------------------------------------------------------
// Tests: tool discovery
// --------------------------------------------------------------------------

func TestServer_ListTools(t *testing.T) {
	session, _ := newTestServer(t, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := map[string]bool{
		"skill_create":  false,
		"skill_list":    false,
		"schedule_add":  false,
		"schedule_list": false,
	}
	for _, tool := range result.Tools {
		want[tool.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q not listed", name)
		}
	}
}

// --------------------------------------------------------------------------
// Tests: skill_list
// --------------------------------------------------------------------------

func TestSkillList_Empty(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "skill_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if text != "[]" {
		t.Errorf("expected empty array, got %q", text)
	}
}

// --------------------------------------------------------------------------
// Tests: skill_create (autonomous)
// --------------------------------------------------------------------------

func TestSkillCreate_Autonomous_Success(t *testing.T) {
	session, deps := newTestServer(t, nil)

	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name": "test-skill",
		"body": "# Test\n\nDo the thing.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if text == "" {
		t.Fatal("expected non-empty success message")
	}

	// Skill file should exist on disk.
	skillFile := filepath.Join(deps.AgentSkillsDir, "test-skill.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Errorf("skill file not created: %v", err)
	}

	// Skill should appear in skill_list.
	listText, isErr := callTool(t, session, "skill_list", map[string]any{})
	if isErr {
		t.Fatalf("skill_list error: %s", listText)
	}
	var listed []map[string]any
	if err := json.Unmarshal([]byte(listText), &listed); err != nil {
		t.Fatalf("parsing skill_list result: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("expected 1 skill, got %d", len(listed))
	}
	if listed[0]["name"] != "test-skill" {
		t.Errorf("unexpected skill name: %v", listed[0]["name"])
	}
}

func TestSkillCreate_MissingName(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"body": "some body",
	})
	if !isErr {
		t.Fatalf("expected error for missing name, got: %s", text)
	}
}

func TestSkillCreate_MissingBody(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name": "no-body-skill",
	})
	if !isErr {
		t.Fatalf("expected error for missing body, got: %s", text)
	}
}

func TestSkillCreate_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name": "blocked",
		"body": "should not be created",
	})
	if !isErr {
		t.Fatalf("expected error in restricted mode, got: %s", text)
	}
}

func TestSkillCreate_NoSkillsDir(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.AgentSkillsDir = ""
	})
	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name": "orphan",
		"body": "no directory configured",
	})
	if !isErr {
		t.Fatalf("expected error when skills dir is empty, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: skill_create (supervised)
// --------------------------------------------------------------------------

// TestSkillCreate_Supervised_ExecutesDirectly verifies that Config MCP
// actions execute directly without a second approval round — the Engine
// already handles tool-call approval in supervised mode.
func TestSkillCreate_Supervised_ExecutesDirectly(t *testing.T) {
	var skillsDir string
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		skillsDir = d.AgentSkillsDir
		d.PermissionTier = func() string { return "supervised" }
	})

	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name": "supervised-skill",
		"body": "# Supervised\n\nCreated directly.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "Done:") {
		t.Errorf("expected done message, got: %s", text)
	}

	// The skill file should exist (action executed directly).
	skillFile := filepath.Join(skillsDir, "supervised-skill.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Errorf("skill file should exist: %v", err)
	}
}

// --------------------------------------------------------------------------
// Tests: schedule_add

func TestScheduleAdd_Supervised_ExecutesDirectly(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "supervised" }
	})

	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "supervised-sched",
		"schedule": "@every 1h",
		"channel":  "telegram:12345",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "Done:") {
		t.Errorf("expected done message, got: %s", text)
	}
}

// --------------------------------------------------------------------------

func TestScheduleAdd_Autonomous_Success(t *testing.T) {
	session, _ := newTestServer(t, nil)

	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "test-sched",
		"schedule": "@every 1h",
		"channel":  "telegram:12345",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if text == "" {
		t.Fatal("expected success message")
	}
}

func TestScheduleAdd_InvalidExpression(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "bad-sched",
		"schedule": "not-a-cron",
		"channel":  "telegram:12345",
	})
	if !isErr {
		t.Fatalf("expected error for invalid expression, got: %s", text)
	}
}

func TestScheduleAdd_InvalidChannelFormat(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "bad-channel",
		"schedule": "@daily",
		"channel":  "notachannel",
	})
	if !isErr {
		t.Fatalf("expected error for bad channel format, got: %s", text)
	}
}

func TestScheduleAdd_NoScheduler(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.Sched = nil
		d.HandleMessage = nil
	})
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "no-sched",
		"schedule": "@daily",
		"channel":  "telegram:12345",
	})
	if !isErr {
		t.Fatalf("expected error when scheduler is nil, got: %s", text)
	}
}

func TestScheduleAdd_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "blocked",
		"schedule": "@daily",
		"channel":  "telegram:99",
	})
	if !isErr {
		t.Fatalf("expected error in restricted mode, got: %s", text)
	}
}

func TestScheduleAdd_MissingSkill(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(_ string) (skill.Skill, bool) {
			return skill.Skill{}, false
		}
	})
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "bad-skill-sched",
		"schedule": "@daily",
		"channel":  "telegram:123",
		"skill":    "nonexistent",
	})
	if !isErr {
		t.Fatalf("expected error for missing skill, got: %s", text)
	}
	if !strings.Contains(text, "nonexistent") {
		t.Errorf("expected error to mention skill name; got: %s", text)
	}
}

func TestScheduleAdd_ValidSkill(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "greet" {
				return skill.Skill{Name: "greet"}, true
			}
			return skill.Skill{}, false
		}
	})
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "good-skill-sched",
		"schedule": "@daily",
		"channel":  "telegram:123",
		"skill":    "greet",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: schedule_list
// --------------------------------------------------------------------------

func TestScheduleList_Empty(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "schedule_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if text != "[]" {
		t.Errorf("expected empty array, got %q", text)
	}
}

func TestScheduleList_AfterAdd(t *testing.T) {
	session, _ := newTestServer(t, nil)

	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "listed-sched",
		"schedule": "@daily",
		"channel":  "telegram:99",
	})
	if isErr {
		t.Fatal("unexpected error adding schedule")
	}

	listText, isErr := callTool(t, session, "schedule_list", map[string]any{})
	if isErr {
		t.Fatalf("schedule_list error: %s", listText)
	}

	var listed []map[string]any
	if err := json.Unmarshal([]byte(listText), &listed); err != nil {
		t.Fatalf("parsing schedule_list: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("expected 1 schedule, got %d", len(listed))
	}
	if listed[0]["name"] != "listed-sched" {
		t.Errorf("unexpected schedule name: %v", listed[0]["name"])
	}
}

// registerForeignSchedule registers an agent-type schedule owned by another
// agent directly on the shared scheduler, bypassing the per-agent tools.
func registerForeignSchedule(t *testing.T, deps *configmcp.Deps, name, owner string) {
	t.Helper()
	err := deps.Sched.Register(scheduler.Config{
		Name:     name,
		Type:     string(scheduler.ScheduleTypeAgent),
		Agent:    owner,
		Schedule: "@daily",
		Enabled:  true,
	}, func(scheduler.Entry) {})
	if err != nil {
		t.Fatalf("registering foreign schedule: %v", err)
	}
}

func TestScheduleList_ExcludesOtherAgents(t *testing.T) {
	session, deps := newTestServer(t, nil)

	registerForeignSchedule(t, deps, "other-sched", "other-agent")

	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "mine",
		"schedule": "@daily",
		"channel":  "telegram:99",
	})
	if isErr {
		t.Fatal("unexpected error adding own schedule")
	}

	listText, isErr := callTool(t, session, "schedule_list", map[string]any{})
	if isErr {
		t.Fatalf("schedule_list error: %s", listText)
	}

	var listed []map[string]any
	if err := json.Unmarshal([]byte(listText), &listed); err != nil {
		t.Fatalf("parsing schedule_list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected only own schedule, got %d: %s", len(listed), listText)
	}
	if listed[0]["name"] != "mine" {
		t.Errorf("expected own schedule, got %v", listed[0]["name"])
	}
}

func TestScheduleDelete_OtherAgentNotFound(t *testing.T) {
	session, deps := newTestServer(t, nil)
	registerForeignSchedule(t, deps, "other-sched", "other-agent")

	text, isErr := callTool(t, session, "schedule_delete", map[string]any{
		"name": "other-sched",
	})
	if !isErr {
		t.Fatalf("expected not-found error deleting another agent's schedule, got: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected %q to mention not found", text)
	}

	// The foreign schedule must remain registered.
	if _, ok := deps.Sched.GetEntry("other-sched"); !ok {
		t.Error("foreign schedule was deleted across agent boundary")
	}
}

func TestScheduleUpdate_OtherAgentNotFound(t *testing.T) {
	session, deps := newTestServer(t, nil)
	registerForeignSchedule(t, deps, "other-sched", "other-agent")

	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":     "other-sched",
		"schedule": "@hourly",
	})
	if !isErr {
		t.Fatalf("expected not-found error updating another agent's schedule, got: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected %q to mention not found", text)
	}
}

// --------------------------------------------------------------------------
// Tests: tool discovery includes new tools
// --------------------------------------------------------------------------

func TestServer_ListTools_IncludesToolAndPluginTools(t *testing.T) {
	session, _ := newTestServer(t, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	expected := map[string]bool{
		"tool_list":     false,
		"tool_add":      false,
		"tool_remove":   false,
		"plugin_list":   false,
		"plugin_add":    false,
		"plugin_remove": false,
	}
	for _, tool := range result.Tools {
		if _, ok := expected[tool.Name]; ok {
			expected[tool.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("tool %q not listed", name)
		}
	}
}

// --------------------------------------------------------------------------
// Tests: tool_list
// --------------------------------------------------------------------------

func TestToolList_Empty(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "tool_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	// With no lifecycle manager, returns empty array.
	if text != "[]" {
		t.Errorf("expected empty array, got %q", text)
	}
}

// --------------------------------------------------------------------------
// Tests: tool_add (restricted tier)
// --------------------------------------------------------------------------

func TestToolAdd_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	text, isErr := callTool(t, session, "tool_add", map[string]any{
		"name":    "blocked-tool",
		"command": "/usr/bin/blocked",
	})
	if !isErr {
		t.Fatalf("expected error in restricted mode, got: %s", text)
	}
}

func TestToolAdd_MissingName(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "tool_add", map[string]any{
		"command": "/usr/bin/test",
	})
	if !isErr {
		t.Fatalf("expected error for missing name, got: %s", text)
	}
}

func TestToolAdd_MissingCommand(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "tool_add", map[string]any{
		"name": "no-cmd",
	})
	if !isErr {
		t.Fatalf("expected error for missing command, got: %s", text)
	}
}

func TestToolAdd_NoLifecycleManager(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.LifecycleMgr = nil
	})
	text, isErr := callTool(t, session, "tool_add", map[string]any{
		"name":    "test",
		"command": "/bin/test",
	})
	if !isErr {
		t.Fatalf("expected error when lifecycle manager is nil, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: tool_remove (restricted tier)
// --------------------------------------------------------------------------

func TestToolRemove_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	text, isErr := callTool(t, session, "tool_remove", map[string]any{
		"name": "some-tool",
	})
	if !isErr {
		t.Fatalf("expected error in restricted mode, got: %s", text)
	}
}

func TestToolRemove_MissingName(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "tool_remove", map[string]any{})
	if !isErr {
		t.Fatalf("expected error for missing name, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: tool_restart
// --------------------------------------------------------------------------

func TestToolRestart_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	text, isErr := callTool(t, session, "tool_restart", map[string]any{
		"name": "some-tool",
	})
	if !isErr {
		t.Fatalf("expected error in restricted mode, got: %s", text)
	}
}

func TestToolRestart_MissingName(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "tool_restart", map[string]any{})
	if !isErr {
		t.Fatalf("expected error for missing name, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: plugin_list
// --------------------------------------------------------------------------

func TestPluginList_Empty(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "plugin_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if text != "[]" {
		t.Errorf("expected empty array, got %q", text)
	}
}

// --------------------------------------------------------------------------
// Tests: plugin_add (restricted tier)
// --------------------------------------------------------------------------

func TestPluginAdd_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	text, isErr := callTool(t, session, "plugin_add", map[string]any{
		"name": "blocked-plugin",
		"type": "subprocess",
	})
	if !isErr {
		t.Fatalf("expected error in restricted mode, got: %s", text)
	}
}

func TestPluginAdd_InvalidType(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "plugin_add", map[string]any{
		"name": "bad-type",
		"type": "invalid",
	})
	if !isErr {
		t.Fatalf("expected error for invalid type, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: plugin_remove (restricted tier)
// --------------------------------------------------------------------------

func TestPluginRemove_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	text, isErr := callTool(t, session, "plugin_remove", map[string]any{
		"name": "some-plugin",
	})
	if !isErr {
		t.Fatalf("expected error in restricted mode, got: %s", text)
	}
}

func TestPluginRemove_MissingName(t *testing.T) {
	session, _ := newTestServer(t, nil)
	text, isErr := callTool(t, session, "plugin_remove", map[string]any{})
	if !isErr {
		t.Fatalf("expected error for missing name, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Helpers: KV store
// --------------------------------------------------------------------------

func newTestServerWithKV(t *testing.T, overrides func(*configmcp.Deps)) (*mcp.ClientSession, *configmcp.Deps) {
	t.Helper()
	kvStore, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = kvStore.Close() })

	return newTestServer(t, func(d *configmcp.Deps) {
		d.KVStore = kvStore
		if overrides != nil {
			overrides(d)
		}
	})
}

// --------------------------------------------------------------------------
// Tests: KV tool discovery
// --------------------------------------------------------------------------

func TestServer_ListTools_IncludesKVTools(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	expected := map[string]bool{
		"kv_get":    false,
		"kv_set":    false,
		"kv_delete": false,
		"kv_list":   false,
		"kv_set_nx": false,
	}
	for _, tool := range result.Tools {
		if _, ok := expected[tool.Name]; ok {
			expected[tool.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("kv tool %q not listed", name)
		}
	}
}

func TestServer_ListTools_NoKVToolsWithoutStore(t *testing.T) {
	session, _ := newTestServer(t, nil) // no KV store

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	for _, tool := range result.Tools {
		if strings.HasPrefix(tool.Name, "kv_") {
			t.Errorf("kv tool %q should not be listed without KV store", tool.Name)
		}
	}
}

func TestServer_KVToolDescriptions_MentionNamespaces(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	// kv_get / kv_set surface namespace conventions; kv_list shows a prefix example.
	wantInDesc := map[string][]string{
		"kv_get":  {"`cache:*`", "`log:*`", "`pref:*`", "`state:*`"},
		"kv_set":  {"`prefix:subkey`"},
		"kv_list": {"`log:heartbeat:`"},
	}
	for _, tool := range result.Tools {
		needles, ok := wantInDesc[tool.Name]
		if !ok {
			continue
		}
		for _, needle := range needles {
			if !strings.Contains(tool.Description, needle) {
				t.Errorf("%s description missing %q; got: %s", tool.Name, needle, tool.Description)
			}
		}
	}
}

// --------------------------------------------------------------------------
// Tests: kv_get / kv_set
// --------------------------------------------------------------------------

func TestKVSetAndGet(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	text, isErr := callTool(t, session, "kv_set", map[string]any{
		"key":   "greeting",
		"value": "hello",
	})
	if isErr {
		t.Fatalf("kv_set error: %s", text)
	}

	text, isErr = callTool(t, session, "kv_get", map[string]any{
		"key": "greeting",
	})
	if isErr {
		t.Fatalf("kv_get error: %s", text)
	}

	var result map[string]string
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing kv_get result: %v", err)
	}
	if result["value"] != "hello" {
		t.Errorf("kv_get value = %q, want %q", result["value"], "hello")
	}
}

func TestKVGet_NotFound(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	text, isErr := callTool(t, session, "kv_get", map[string]any{
		"key": "missing",
	})
	if isErr {
		t.Fatalf("kv_get error: %s", text)
	}
	if !strings.Contains(text, "null") {
		t.Errorf("expected null value for missing key, got %q", text)
	}
}

func TestKVGet_MissingKey(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	_, isErr := callTool(t, session, "kv_get", map[string]any{})
	if !isErr {
		t.Fatal("expected error for missing key argument")
	}
}

// --------------------------------------------------------------------------
// Tests: kv_set with TTL
// --------------------------------------------------------------------------

func TestKVSet_WithTTL(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	text, isErr := callTool(t, session, "kv_set", map[string]any{
		"key":   "temp",
		"value": "ephemeral",
		"ttl":   "1h",
	})
	if isErr {
		t.Fatalf("kv_set with TTL error: %s", text)
	}

	text, isErr = callTool(t, session, "kv_get", map[string]any{
		"key": "temp",
	})
	if isErr {
		t.Fatalf("kv_get error: %s", text)
	}

	var result map[string]string
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if result["value"] != "ephemeral" {
		t.Errorf("value = %q, want %q", result["value"], "ephemeral")
	}
}

func TestKVSet_InvalidTTL(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	_, isErr := callTool(t, session, "kv_set", map[string]any{
		"key":   "k",
		"value": "v",
		"ttl":   "not-a-duration",
	})
	if !isErr {
		t.Fatal("expected error for invalid TTL")
	}
}

func TestKVSet_NegativeTTL(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	_, isErr := callTool(t, session, "kv_set", map[string]any{
		"key":   "k",
		"value": "v",
		"ttl":   "-5m",
	})
	if !isErr {
		t.Fatal("expected error for negative TTL")
	}
}

func TestKVSetNX_NegativeTTL(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	_, isErr := callTool(t, session, "kv_set_nx", map[string]any{
		"key":   "k",
		"value": "v",
		"ttl":   "-1s",
	})
	if !isErr {
		t.Fatal("expected error for negative TTL")
	}
}

func TestKVSet_EmptyKey(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	_, isErr := callTool(t, session, "kv_set", map[string]any{
		"key":   "",
		"value": "v",
	})
	if !isErr {
		t.Fatal("expected error for empty key")
	}
}

func TestKVDelete_EmptyKey(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	_, isErr := callTool(t, session, "kv_delete", map[string]any{
		"key": "",
	})
	if !isErr {
		t.Fatal("expected error for empty key")
	}
}

// --------------------------------------------------------------------------
// Tests: kv_delete
// --------------------------------------------------------------------------

func TestKVDelete(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	callTool(t, session, "kv_set", map[string]any{"key": "k", "value": "v"})

	text, isErr := callTool(t, session, "kv_delete", map[string]any{"key": "k"})
	if isErr {
		t.Fatalf("kv_delete error: %s", text)
	}

	text, _ = callTool(t, session, "kv_get", map[string]any{"key": "k"})
	if !strings.Contains(text, "null") {
		t.Errorf("expected null after delete, got %q", text)
	}
}

// --------------------------------------------------------------------------
// Tests: kv_list
// --------------------------------------------------------------------------

func TestKVList_Empty(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	text, isErr := callTool(t, session, "kv_list", map[string]any{})
	if isErr {
		t.Fatalf("kv_list error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	entries, _ := result["entries"].([]any)
	if len(entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(entries))
	}
}

func TestKVList_WithPrefix(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	callTool(t, session, "kv_set", map[string]any{"key": "lock:a", "value": "1"})
	callTool(t, session, "kv_set", map[string]any{"key": "lock:b", "value": "2"})
	callTool(t, session, "kv_set", map[string]any{"key": "cache:x", "value": "3"})

	text, isErr := callTool(t, session, "kv_list", map[string]any{"prefix": "lock:"})
	if isErr {
		t.Fatalf("kv_list error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	entries, _ := result["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries with prefix 'lock:', got %d", len(entries))
	}
}

// --------------------------------------------------------------------------
// Tests: kv_set_nx
// --------------------------------------------------------------------------

func TestKVSetNX_Success(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	text, isErr := callTool(t, session, "kv_set_nx", map[string]any{
		"key":   "lock:job",
		"value": "owner1",
		"ttl":   "5m",
	})
	if isErr {
		t.Fatalf("kv_set_nx error: %s", text)
	}

	var result map[string]bool
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if !result["acquired"] {
		t.Error("expected acquired=true on new key")
	}
}

func TestKVSetNX_AlreadyExists(t *testing.T) {
	session, _ := newTestServerWithKV(t, nil)

	callTool(t, session, "kv_set_nx", map[string]any{
		"key": "lock:job", "value": "owner1",
	})

	text, isErr := callTool(t, session, "kv_set_nx", map[string]any{
		"key": "lock:job", "value": "owner2",
	})
	if isErr {
		t.Fatalf("kv_set_nx error: %s", text)
	}

	var result map[string]bool
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if result["acquired"] {
		t.Error("expected acquired=false for existing key")
	}
}

// --------------------------------------------------------------------------
// Tests: KV permission tiers
// --------------------------------------------------------------------------

func TestKVSet_RestrictedTier(t *testing.T) {
	session, _ := newTestServerWithKV(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})

	_, isErr := callTool(t, session, "kv_set", map[string]any{
		"key": "k", "value": "v",
	})
	if !isErr {
		t.Fatal("expected error in restricted mode")
	}
}

func TestKVDelete_RestrictedTier(t *testing.T) {
	session, _ := newTestServerWithKV(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})

	_, isErr := callTool(t, session, "kv_delete", map[string]any{"key": "k"})
	if !isErr {
		t.Fatal("expected error in restricted mode")
	}
}

func TestKVSetNX_RestrictedTier(t *testing.T) {
	session, _ := newTestServerWithKV(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})

	_, isErr := callTool(t, session, "kv_set_nx", map[string]any{
		"key": "k", "value": "v",
	})
	if !isErr {
		t.Fatal("expected error in restricted mode")
	}
}

func TestKVGet_AllowedInRestrictedTier(t *testing.T) {
	session, _ := newTestServerWithKV(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})

	_, isErr := callTool(t, session, "kv_get", map[string]any{"key": "k"})
	if isErr {
		t.Fatal("kv_get should be allowed in restricted mode")
	}
}

func TestKVList_AllowedInRestrictedTier(t *testing.T) {
	session, _ := newTestServerWithKV(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})

	_, isErr := callTool(t, session, "kv_list", map[string]any{})
	if isErr {
		t.Fatal("kv_list should be allowed in restricted mode")
	}
}

// --------------------------------------------------------------------------
// Tests: schedule_update
// --------------------------------------------------------------------------

func TestScheduleUpdate_Success(t *testing.T) {
	session, _ := newTestServer(t, nil)

	// First, add a schedule.
	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "updatable",
		"schedule": "@every 1h",
		"channel":  "telegram:12345",
		"skill":    "old-skill",
	})
	if isErr {
		t.Fatal("schedule_add failed")
	}

	// Update only the skill and schedule expression.
	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":     "updatable",
		"schedule": "@daily",
		"skill":    "new-skill",
	})
	if isErr {
		t.Fatalf("schedule_update error: %s", text)
	}
	if !strings.Contains(text, "Update schedule") {
		t.Errorf("expected update confirmation, got: %s", text)
	}

	// Verify the schedule was updated in the list.
	listText, _ := callTool(t, session, "schedule_list", map[string]any{})
	var listed []map[string]any
	if err := json.Unmarshal([]byte(listText), &listed); err != nil {
		t.Fatalf("parsing schedule_list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(listed))
	}
	if listed[0]["skill"] != "new-skill" {
		t.Errorf("skill = %v, want new-skill", listed[0]["skill"])
	}
	if listed[0]["schedule"] != "@daily" {
		t.Errorf("schedule = %v, want @daily", listed[0]["schedule"])
	}
	// Channel should be preserved from original.
	if listed[0]["channel"] != "telegram:12345" {
		t.Errorf("channel = %v, want telegram:12345", listed[0]["channel"])
	}
}

func TestScheduleUpdate_NotFound(t *testing.T) {
	session, _ := newTestServer(t, nil)

	_, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":     "nonexistent",
		"schedule": "@daily",
	})
	if !isErr {
		t.Fatal("expected error for nonexistent schedule")
	}
}

func TestScheduleUpdate_MissingSkill(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "greet" {
				return skill.Skill{Name: "greet"}, true
			}
			return skill.Skill{}, false
		}
	})

	// Create a schedule with a valid skill.
	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name": "skill-update-test", "schedule": "@daily", "channel": "telegram:1", "skill": "greet",
	})
	if isErr {
		t.Fatal("setup: schedule_add failed")
	}

	// Update to a nonexistent skill.
	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":  "skill-update-test",
		"skill": "nonexistent",
	})
	if !isErr {
		t.Fatalf("expected error for missing skill, got: %s", text)
	}
	if !strings.Contains(text, "nonexistent") {
		t.Errorf("expected error to mention skill name; got: %s", text)
	}
}

func TestScheduleUpdate_InvalidExpression(t *testing.T) {
	session, _ := newTestServer(t, nil)

	callTool(t, session, "schedule_add", map[string]any{
		"name": "expr-test", "schedule": "@daily", "channel": "telegram:1",
	})

	_, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":     "expr-test",
		"schedule": "bad-expr",
	})
	if !isErr {
		t.Fatal("expected error for invalid expression")
	}
}

func TestScheduleUpdate_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})
	_, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name": "blocked",
	})
	if !isErr {
		t.Fatal("expected error in restricted mode")
	}
}

func TestScheduleUpdate_NoScheduler(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.Sched = nil
		d.HandleMessage = nil
	})
	_, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name": "test",
	})
	if !isErr {
		t.Fatal("expected error when scheduler is nil")
	}
}

func TestScheduleUpdate_EnableDisable(t *testing.T) {
	session, _ := newTestServer(t, nil)

	callTool(t, session, "schedule_add", map[string]any{
		"name": "toggle", "schedule": "@daily", "channel": "telegram:1", "enabled": true,
	})

	// Disable the schedule.
	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":    "toggle",
		"enabled": false,
	})
	if isErr {
		t.Fatalf("schedule_update error: %s", text)
	}

	listText, _ := callTool(t, session, "schedule_list", map[string]any{})
	var listed []map[string]any
	_ = json.Unmarshal([]byte(listText), &listed)
	if len(listed) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(listed))
	}
	if listed[0]["enabled"] != false {
		t.Errorf("enabled = %v, want false", listed[0]["enabled"])
	}
}

// --------------------------------------------------------------------------
// Tests: set_fallback
// --------------------------------------------------------------------------

func TestSetFallback_Success(t *testing.T) {
	var captured []configmcp.FallbackRuleInput
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SetFallbacks = func(rules []configmcp.FallbackRuleInput) {
			captured = rules
		}
	})

	text, isErr := callTool(t, session, "set_fallback", map[string]any{
		"rules": []map[string]any{
			{"trigger": "rate_limit", "action": "wait_and_retry", "max_retries": 3, "backoff": "exponential"},
			{"trigger": "error", "action": "switch_provider", "provider": "ollama"},
		},
	})
	if isErr {
		t.Fatalf("set_fallback error: %s", text)
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 rules captured, got %d", len(captured))
	}
	if captured[0].Trigger != "rate_limit" {
		t.Errorf("rule[0].Trigger = %q, want rate_limit", captured[0].Trigger)
	}
	if captured[1].Provider != "ollama" {
		t.Errorf("rule[1].Provider = %q, want ollama", captured[1].Provider)
	}
}

func TestSetFallback_ClearRules(t *testing.T) {
	var captured []configmcp.FallbackRuleInput
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SetFallbacks = func(rules []configmcp.FallbackRuleInput) {
			captured = rules
		}
	})

	text, isErr := callTool(t, session, "set_fallback", map[string]any{
		"rules": []map[string]any{},
	})
	if isErr {
		t.Fatalf("set_fallback error: %s", text)
	}
	if len(captured) != 0 {
		t.Errorf("expected 0 rules, got %d", len(captured))
	}
	if !strings.Contains(text, "0 fallback") {
		t.Errorf("unexpected message: %s", text)
	}
}

func TestSetFallback_InvalidTrigger(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SetFallbacks = func(rules []configmcp.FallbackRuleInput) {}
	})

	_, isErr := callTool(t, session, "set_fallback", map[string]any{
		"rules": []map[string]any{
			{"trigger": "invalid", "action": "wait_and_retry"},
		},
	})
	if !isErr {
		t.Fatal("expected error for invalid trigger")
	}
}

func TestSetFallback_InvalidAction(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SetFallbacks = func(rules []configmcp.FallbackRuleInput) {}
	})

	_, isErr := callTool(t, session, "set_fallback", map[string]any{
		"rules": []map[string]any{
			{"trigger": "error", "action": "explode"},
		},
	})
	if !isErr {
		t.Fatal("expected error for invalid action")
	}
}

func TestSetFallback_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SetFallbacks = func(rules []configmcp.FallbackRuleInput) {}
		d.PermissionTier = func() string { return "restricted" }
	})

	_, isErr := callTool(t, session, "set_fallback", map[string]any{
		"rules": []map[string]any{},
	})
	if !isErr {
		t.Fatal("expected error in restricted mode")
	}
}

func TestSetFallback_NotAvailable(t *testing.T) {
	session, _ := newTestServer(t, nil) // no SetFallbacks

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "set_fallback" {
			t.Error("set_fallback should not be listed when SetFallbacks is nil")
		}
	}
}

// --------------------------------------------------------------------------
// Tests: get_cost_summary
// --------------------------------------------------------------------------

func TestGetCostSummary_Success(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{
				GlobalCost:    1.23,
				MaxPerSession: 5.0,
				SessionCosts:  map[string]float64{"sess-1": 0.45, "sess-2": 0.78},
			}
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if result["global_cost"] != 1.23 {
		t.Errorf("global_cost = %v, want 1.23", result["global_cost"])
	}
	if result["max_per_session"] != 5.0 {
		t.Errorf("max_per_session = %v, want 5", result["max_per_session"])
	}
	sessions, ok := result["session_costs"].(map[string]any)
	if !ok {
		t.Fatalf("session_costs not a map: %T", result["session_costs"])
	}
	if sessions["sess-1"] != 0.45 {
		t.Errorf("sess-1 cost = %v, want 0.45", sessions["sess-1"])
	}
}

func TestGetCostSummary_NotAvailable(t *testing.T) {
	session, _ := newTestServer(t, nil) // no CostSummary

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "get_cost_summary" {
			t.Error("get_cost_summary should not be listed when CostSummary is nil")
		}
	}
}

func TestGetCostSummary_EmptySessions(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{
				GlobalCost:    0,
				MaxPerSession: 10.0,
				SessionCosts:  map[string]float64{},
			}
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary error: %s", text)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if result["global_cost"] != 0.0 {
		t.Errorf("global_cost = %v, want 0", result["global_cost"])
	}
}

func TestGetCostSummary_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{GlobalCost: 0.5}
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary should be allowed in restricted mode, got error: %s", text)
	}
	if !strings.Contains(text, "0.5") {
		t.Errorf("expected cost data, got: %s", text)
	}
}

func TestGetCostSummary_WithTelemetry(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{GlobalCost: 1.23}
		}
		d.TelemetrySummary = func(_ context.Context, since *time.Time) (*agent.TelemetrySummary, error) {
			if since != nil {
				t.Errorf("since = %v, want nil when days is absent", since)
			}
			return &agent.TelemetrySummary{
				ByTool: []agent.ToolUsageSummary{
					{ToolName: "web_search", ServerName: "web", CallCount: 42, ErrorCount: 3, AvgDuration: 120.5},
				},
				BySkill: []agent.SkillUsageSummary{
					{SkillName: "self-audit", MatchCount: 7, MatchTypes: "command"},
				},
			}, nil
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary error: %s", text)
	}

	var result configmcp.CostSummaryData
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if len(result.ByTool) != 1 || result.ByTool[0].ToolName != "web_search" || result.ByTool[0].ErrorCount != 3 {
		t.Errorf("ByTool = %+v, want web_search with 3 errors", result.ByTool)
	}
	if len(result.BySkill) != 1 || result.BySkill[0].SkillName != "self-audit" {
		t.Errorf("BySkill = %+v, want self-audit", result.BySkill)
	}
	if result.TelemetryError != "" {
		t.Errorf("TelemetryError = %q, want empty", result.TelemetryError)
	}
}

func TestGetCostSummary_SurfacesLifetimeCost(t *testing.T) {
	// Regression: the persistent lifetime spend (sum of ByModel costs) must be
	// surfaced separately from the transient in-memory GlobalCost. The bug was
	// that the handler fetched ByModel but discarded it, so the agent only saw
	// the since-restart number.
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{GlobalCost: 0.49}
		}
		d.TelemetrySummary = func(_ context.Context, _ *time.Time) (*agent.TelemetrySummary, error) {
			return &agent.TelemetrySummary{
				ByModel: []agent.ModelCostSummary{
					{Model: "claude-opus", Provider: "anthropic", TotalCost: 4.00},
					{Model: "claude-haiku", Provider: "anthropic", TotalCost: 1.28},
				},
			}, nil
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary error: %s", text)
	}

	var result configmcp.CostSummaryData
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if result.LifetimeCost != 5.28 {
		t.Errorf("LifetimeCost = %v, want 5.28", result.LifetimeCost)
	}
	if result.LifetimeCost == result.GlobalCost {
		t.Errorf("LifetimeCost (%v) must differ from transient GlobalCost (%v)",
			result.LifetimeCost, result.GlobalCost)
	}
	if result.GlobalCost != 0.49 {
		t.Errorf("GlobalCost = %v, want 0.49 (live budget number preserved)", result.GlobalCost)
	}
}

func TestGetCostSummary_IncludesByModel(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{GlobalCost: 0.1}
		}
		d.TelemetrySummary = func(_ context.Context, _ *time.Time) (*agent.TelemetrySummary, error) {
			return &agent.TelemetrySummary{
				ByModel: []agent.ModelCostSummary{
					{Model: "gpt-4o", Provider: "openai", TotalCost: 2.5, MessageCount: 11, TotalPrompt: 100},
				},
			}, nil
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary error: %s", text)
	}

	var result configmcp.CostSummaryData
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if len(result.ByModel) != 1 {
		t.Fatalf("ByModel len = %d, want 1", len(result.ByModel))
	}
	got := result.ByModel[0]
	if got.Model != "gpt-4o" || got.Provider != "openai" || got.TotalCost != 2.5 || got.MessageCount != 11 {
		t.Errorf("ByModel[0] = %+v, want gpt-4o/openai/2.5/11", got)
	}
}

func TestGetCostSummary_DaysFilter(t *testing.T) {
	var gotSince *time.Time
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{}
		}
		d.TelemetrySummary = func(_ context.Context, since *time.Time) (*agent.TelemetrySummary, error) {
			gotSince = since
			return &agent.TelemetrySummary{}, nil
		}
	})

	_, isErr := callTool(t, session, "get_cost_summary", map[string]any{"days": 7})
	if isErr {
		t.Fatal("get_cost_summary error")
	}

	if gotSince == nil {
		t.Fatal("since = nil, want ~7 days ago")
	}
	want := time.Now().AddDate(0, 0, -7)
	if diff := gotSince.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("since = %v, want within a minute of %v", gotSince, want)
	}
}

func TestGetCostSummary_TelemetryErrorIsNonFatal(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{GlobalCost: 0.5}
		}
		d.TelemetrySummary = func(_ context.Context, _ *time.Time) (*agent.TelemetrySummary, error) {
			return nil, context.DeadlineExceeded
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary should not fail on telemetry error: %s", text)
	}

	var result configmcp.CostSummaryData
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if result.GlobalCost != 0.5 {
		t.Errorf("GlobalCost = %v, want 0.5 (cost data must survive telemetry failure)", result.GlobalCost)
	}
	if result.TelemetryError == "" {
		t.Error("TelemetryError empty, want the lookup error")
	}
	if result.LifetimeCost != 0 {
		t.Errorf("LifetimeCost = %v, want 0 on telemetry failure", result.LifetimeCost)
	}
	if result.ByModel != nil {
		t.Errorf("ByModel = %+v, want nil on telemetry failure", result.ByModel)
	}
}

func TestGetCostSummary_NoTelemetryDep(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.CostSummary = func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{GlobalCost: 1.0}
		}
	})

	text, isErr := callTool(t, session, "get_cost_summary", map[string]any{})
	if isErr {
		t.Fatalf("get_cost_summary error: %s", text)
	}
	if strings.Contains(text, "by_tool") || strings.Contains(text, "telemetry_error") {
		t.Errorf("expected no telemetry keys without TelemetrySummary dep, got: %s", text)
	}
	if strings.Contains(text, "lifetime_cost") || strings.Contains(text, "by_model") {
		t.Errorf("expected lifetime_cost/by_model omitted without TelemetrySummary dep, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: supervised tier approvals for new tools
// --------------------------------------------------------------------------

func TestSetFallback_Supervised_ExecutesDirectly(t *testing.T) {
	var applied bool
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "supervised" }
		d.SetFallbacks = func(rules []configmcp.FallbackRuleInput) {
			applied = true
		}
	})

	text, isErr := callTool(t, session, "set_fallback", map[string]any{
		"rules": []map[string]any{
			{"trigger": "error", "action": "switch_model", "model": "gpt-4"},
		},
	})
	if isErr {
		t.Fatalf("set_fallback error: %s", text)
	}
	if !strings.Contains(text, "Done:") {
		t.Errorf("expected done message, got: %s", text)
	}
	if !applied {
		t.Error("fallback rules should be applied directly")
	}
}

func TestScheduleUpdate_InvalidChannel(t *testing.T) {
	session, _ := newTestServer(t, nil)

	callTool(t, session, "schedule_add", map[string]any{
		"name": "chan-test", "schedule": "@daily", "channel": "telegram:1",
	})

	_, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":    "chan-test",
		"channel": "bad-no-colon",
	})
	if !isErr {
		t.Fatal("expected error for invalid channel format in update")
	}
}

func TestScheduleUpdate_MissingName(t *testing.T) {
	session, _ := newTestServer(t, nil)

	_, isErr := callTool(t, session, "schedule_update", map[string]any{
		"schedule": "@daily",
	})
	if !isErr {
		t.Fatal("expected error for missing name")
	}
}

func TestScheduleUpdate_CrossAgentReassign(t *testing.T) {
	otherHandlerCalled := false
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.ResolveAgentHandler = func(name string) func(context.Context, adapter.IncomingMessage) error {
			if name == "other-agent" {
				return func(_ context.Context, _ adapter.IncomingMessage) error {
					otherHandlerCalled = true
					return nil
				}
			}
			return nil
		}
	})

	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name": "reassign-me", "schedule": "@daily", "channel": "telegram:1",
	})
	if isErr {
		t.Fatal("setup: schedule_add failed")
	}

	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":  "reassign-me",
		"agent": "other-agent",
	})
	if isErr {
		t.Fatalf("schedule_update error: %s", text)
	}

	// After handing the schedule off to another agent it is owned by that agent
	// and must no longer appear in this agent's owner-scoped listing.
	listText, _ := callTool(t, session, "schedule_list", map[string]any{})
	if strings.Contains(listText, "reassign-me") {
		t.Errorf("reassigned schedule should leave this agent's list, got: %s", listText)
	}
	_ = otherHandlerCalled
}

func TestScheduleUpdate_CrossAgentUnknown(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.ResolveAgentHandler = func(name string) func(context.Context, adapter.IncomingMessage) error {
			return nil
		}
	})

	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name": "unknown-agent-test", "schedule": "@daily", "channel": "telegram:1",
	})
	if isErr {
		t.Fatal("setup: schedule_add failed")
	}

	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":  "unknown-agent-test",
		"agent": "nonexistent",
	})
	if !isErr {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(text, "nonexistent") {
		t.Errorf("expected error to mention agent name; got: %s", text)
	}
}

func TestScheduleUpdate_CrossAgentNoResolver(t *testing.T) {
	session, _ := newTestServer(t, nil) // ResolveAgentHandler is nil

	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name": "no-resolver-test", "schedule": "@daily", "channel": "telegram:1",
	})
	if isErr {
		t.Fatal("setup: schedule_add failed")
	}

	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":  "no-resolver-test",
		"agent": "some-other-agent",
	})
	if !isErr {
		t.Fatal("expected error when ResolveAgentHandler is nil")
	}
	if !strings.Contains(text, "not supported") {
		t.Errorf("expected 'not supported' error; got: %s", text)
	}
}

func TestScheduleUpdate_SameAgentNoResolver(t *testing.T) {
	session, _ := newTestServer(t, nil) // ResolveAgentHandler is nil

	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name": "same-agent-test", "schedule": "@daily", "channel": "telegram:1",
	})
	if isErr {
		t.Fatal("setup: schedule_add failed")
	}

	// Updating to the same agent should succeed even without a resolver.
	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":  "same-agent-test",
		"agent": "test-agent",
	})
	if isErr {
		t.Fatalf("expected success for same-agent update; got error: %s", text)
	}
}

func TestScheduleUpdate_EmptyAgentTreatedAsSelf(t *testing.T) {
	session, _ := newTestServer(t, nil) // ResolveAgentHandler is nil

	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name": "empty-agent-test", "schedule": "@daily", "channel": "telegram:1",
	})
	if isErr {
		t.Fatal("setup: schedule_add failed")
	}

	// Empty string agent should be treated as "self", not trigger cross-agent resolution.
	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":  "empty-agent-test",
		"agent": "",
	})
	if isErr {
		t.Fatalf("expected success for empty-agent update; got error: %s", text)
	}

	listText, _ := callTool(t, session, "schedule_list", map[string]any{})
	if !strings.Contains(listText, "empty-agent-test") {
		t.Errorf("schedule should still be listed; got: %s", listText)
	}
}

// --------------------------------------------------------------------------
// Tests: tool discovery includes new tools
// --------------------------------------------------------------------------

func TestServer_ListTools_IncludesNewTools(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SetFallbacks = func(rules []configmcp.FallbackRuleInput) {}
		d.CostSummary = func() configmcp.CostSummaryData { return configmcp.CostSummaryData{} }
	})

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	expected := map[string]bool{
		"schedule_update":  false,
		"set_fallback":     false,
		"get_cost_summary": false,
		"tool_restart":     false,
	}
	for _, tool := range result.Tools {
		if _, ok := expected[tool.Name]; ok {
			expected[tool.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("tool %q not listed", name)
		}
	}
}

// --------------------------------------------------------------------------
// Tests: skill_get
// --------------------------------------------------------------------------

func TestSkillGet_Success(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "greet" {
				return skill.Skill{
					Name:        "greet",
					Description: "Greeting skill",
					Version:     "1.0",
					Triggers:    []string{"command:hello"},
					Body:        "# Hello\nSay hello!",
				}, true
			}
			return skill.Skill{}, false
		}
	})

	text, isErr := callTool(t, session, "skill_get", map[string]string{"name": "greet"})
	if isErr {
		t.Fatalf("skill_get returned error: %s", text)
	}
	if !strings.Contains(text, "greet") {
		t.Errorf("response missing skill name: %s", text)
	}
	if !strings.Contains(text, "Hello") {
		t.Errorf("response missing body content: %s", text)
	}
}

func TestSkillGet_NotFound(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(_ string) (skill.Skill, bool) {
			return skill.Skill{}, false
		}
	})

	text, isErr := callTool(t, session, "skill_get", map[string]string{"name": "nonexistent"})
	if !isErr {
		t.Errorf("expected error for nonexistent skill, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: skill_update
// --------------------------------------------------------------------------

func TestSkillUpdate_Success(t *testing.T) {
	var mu sync.RWMutex
	var updatedName string
	var updatedSkill skill.Skill

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		// Ensure skills dir exists.
		if err := os.MkdirAll(d.AgentSkillsDir, 0755); err != nil {
			t.Fatalf("creating skills dir: %v", err)
		}
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			if name == "greet" {
				return skill.Skill{
					Name:        "greet",
					Description: "Greeting skill",
					Version:     "1.0",
					Triggers:    []string{"command:hello"},
					Body:        "# Hello\nSay hello!",
				}, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			updatedName = name
			updatedSkill = s
			return true
		}
		d.RemoveSkill = func(_ string) bool { return true }
		d.PermissionTier = func() string { return "autonomous" }
	})

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name":    "greet",
		"version": "2.0",
		"body":    "# Updated\nNew content",
	})
	if isErr {
		t.Fatalf("skill_update returned error: %s", text)
	}

	mu.RLock()
	defer mu.RUnlock()
	if updatedName != "greet" {
		t.Errorf("UpdateSkill called with name %q, want greet", updatedName)
	}
	if updatedSkill.Version != "2.0" {
		t.Errorf("updated version = %q, want 2.0", updatedSkill.Version)
	}
}

func TestSkillUpdate_Restricted(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "greet" {
				return skill.Skill{Name: "greet", Body: "# Test"}, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(_ string, _ skill.Skill) bool { return true }
		d.PermissionTier = func() string { return "restricted" }
	})

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name": "greet",
		"body": "# Modified",
	})
	if !isErr {
		t.Errorf("expected error for restricted tier, got: %s", text)
	}
	if !strings.Contains(strings.ToLower(text), "denied") && !strings.Contains(strings.ToLower(text), "restricted") {
		t.Errorf("error should mention denied/restricted: %s", text)
	}
}

func TestSkillUpdate_Supervised_ExecutesDirectly(t *testing.T) {
	var updated bool
	var skillsDir string
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		skillsDir = d.AgentSkillsDir
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "greet" {
				return skill.Skill{Name: "greet", Body: "# Original"}, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(_ string, _ skill.Skill) bool { updated = true; return true }
		d.PermissionTier = func() string { return "supervised" }
	})

	// Pre-create the skills dir and existing file so ApplySkillUpdate can write.
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "greet.md"), []byte("# Original"), 0o644); err != nil {
		t.Fatal(err)
	}

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name": "greet",
		"body": "# Updated",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "Done:") {
		t.Errorf("expected done message, got: %s", text)
	}
	if !updated {
		t.Error("skill should have been updated directly")
	}
}

// --------------------------------------------------------------------------
// Tests: skill_update with rename (new_name)
// --------------------------------------------------------------------------

func TestSkillUpdate_Rename_Success(t *testing.T) {
	var mu sync.RWMutex
	skills := map[string]skill.Skill{
		"old-skill": {
			Name:        "old-skill",
			Description: "Original",
			Version:     "1.0",
			Body:        "# Old content",
		},
	}

	session, deps := newTestServer(t, func(d *configmcp.Deps) {
		if err := os.MkdirAll(d.AgentSkillsDir, 0755); err != nil {
			t.Fatalf("creating skills dir: %v", err)
		}
		// Write the old skill file to disk so rename can remove it.
		oldPayload := configmcp.BuildSkillPayload("old-skill", "Original", "1.0", nil, "# Old content")
		if err := os.WriteFile(filepath.Join(d.AgentSkillsDir, "old-skill.md"), []byte(oldPayload+"\n"), 0600); err != nil {
			t.Fatalf("writing old skill file: %v", err)
		}

		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			s, ok := skills[name]
			return s, ok
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := skills[name]; !ok {
				return false
			}
			skills[name] = s
			return true
		}
		d.RemoveSkill = func(name string) bool {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := skills[name]; !ok {
				return false
			}
			delete(skills, name)
			return true
		}
		d.AppendSkill = func(s skill.Skill) {
			mu.Lock()
			defer mu.Unlock()
			skills[s.Name] = s
		}
		d.PermissionTier = func() string { return "autonomous" }
	})

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name":     "old-skill",
		"new_name": "new-skill",
		"version":  "2.0",
	})
	if isErr {
		t.Fatalf("skill_update rename error: %s", text)
	}

	// Old skill should be gone from in-memory map.
	mu.RLock()
	_, oldExists := skills["old-skill"]
	newSkill, newExists := skills["new-skill"]
	mu.RUnlock()

	if oldExists {
		t.Error("old skill should be removed after rename")
	}
	if !newExists {
		t.Fatal("new skill should exist after rename")
	}
	if newSkill.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", newSkill.Version)
	}

	// New file should exist on disk.
	newFile := filepath.Join(deps.AgentSkillsDir, "new-skill.md")
	if _, err := os.Stat(newFile); err != nil {
		t.Errorf("new skill file should exist: %v", err)
	}

	// Old file should be removed.
	oldFile := filepath.Join(deps.AgentSkillsDir, "old-skill.md")
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old skill file should be removed, err = %v", err)
	}
}

func TestSkillUpdate_Rename_Conflict(t *testing.T) {
	skills := map[string]skill.Skill{
		"skill-a": {Name: "skill-a", Body: "# A"},
		"skill-b": {Name: "skill-b", Body: "# B"},
	}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		if err := os.MkdirAll(d.AgentSkillsDir, 0755); err != nil {
			t.Fatalf("creating skills dir: %v", err)
		}
		d.GetSkill = func(name string) (skill.Skill, bool) {
			s, ok := skills[name]
			return s, ok
		}
		d.UpdateSkill = func(_ string, _ skill.Skill) bool { return true }
		d.RemoveSkill = func(_ string) bool { return true }
		d.PermissionTier = func() string { return "autonomous" }
	})

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name":     "skill-a",
		"new_name": "skill-b",
	})
	if !isErr {
		t.Fatalf("expected error for conflicting rename, got: %s", text)
	}
	if !strings.Contains(text, "already exists") {
		t.Errorf("error should mention 'already exists': %s", text)
	}
}

// --------------------------------------------------------------------------
// Schedule TOML persistence
// --------------------------------------------------------------------------

func TestScheduleAdd_PersistsToConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("# empty config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.ConfigPath = cfgPath
	})

	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "persisted-add",
		"schedule": "@daily",
		"channel":  "telegram:99",
		"skill":    "greet",
	})
	if isErr {
		t.Fatalf("schedule_add error: %s", text)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "persisted-add") {
		t.Errorf("config file should contain schedule name, got:\n%s", content)
	}
	if !strings.Contains(content, "@daily") {
		t.Errorf("config file should contain schedule expression, got:\n%s", content)
	}
}

func TestScheduleUpdate_PersistsToConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("# empty config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.ConfigPath = cfgPath
	})

	// Add a schedule first (this also persists).
	_, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "update-me",
		"schedule": "@every 1h",
		"channel":  "telegram:42",
		"skill":    "old-skill",
	})
	if isErr {
		t.Fatal("schedule_add failed")
	}

	// Update the schedule expression.
	text, isErr := callTool(t, session, "schedule_update", map[string]any{
		"name":     "update-me",
		"schedule": "@daily",
		"skill":    "new-skill",
	})
	if isErr {
		t.Fatalf("schedule_update error: %s", text)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "@daily") {
		t.Errorf("config should contain updated expression, got:\n%s", content)
	}
	if strings.Contains(content, "@every 1h") {
		t.Errorf("config should NOT contain old expression, got:\n%s", content)
	}
	if !strings.Contains(content, "new-skill") {
		t.Errorf("config should contain updated skill, got:\n%s", content)
	}
}

func TestScheduleDelete_Success(t *testing.T) {
	session, _ := newTestServer(t, nil)

	// First add a schedule.
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "to-delete",
		"schedule": "@daily",
		"channel":  "telegram:12345",
	})
	if isErr {
		t.Fatalf("schedule_add error: %s", text)
	}

	// Delete it.
	text, isErr = callTool(t, session, "schedule_delete", map[string]any{
		"name": "to-delete",
	})
	if isErr {
		t.Fatalf("schedule_delete error: %s", text)
	}
	if !strings.Contains(text, "to-delete") {
		t.Errorf("response should mention schedule name, got: %s", text)
	}
}

func TestScheduleDelete_NotFound(t *testing.T) {
	session, _ := newTestServer(t, nil)

	text, isErr := callTool(t, session, "schedule_delete", map[string]any{
		"name": "nonexistent",
	})
	if !isErr {
		t.Fatalf("expected error for nonexistent schedule, got: %s", text)
	}
}

func TestScheduleDelete_NoScheduler(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.Sched = nil
		d.HandleMessage = nil
	})

	text, isErr := callTool(t, session, "schedule_delete", map[string]any{
		"name": "any",
	})
	if !isErr {
		t.Fatalf("expected error when scheduler is nil, got: %s", text)
	}
}

func TestScheduleDelete_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "restricted" }
	})

	text, isErr := callTool(t, session, "schedule_delete", map[string]any{
		"name": "any",
	})
	if !isErr {
		t.Fatalf("expected error for restricted tier, got: %s", text)
	}
}

func TestScheduleDelete_MissingName(t *testing.T) {
	session, _ := newTestServer(t, nil)

	text, isErr := callTool(t, session, "schedule_delete", map[string]any{
		"name": "",
	})
	if !isErr {
		t.Fatalf("expected error for empty name, got: %s", text)
	}
}

func TestScheduleDelete_PersistsToConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.ConfigPath = configPath
	})

	// Add a schedule (persists to config).
	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "persist-delete",
		"schedule": "@daily",
		"channel":  "telegram:1",
	})
	if isErr {
		t.Fatalf("schedule_add error: %s", text)
	}

	// Verify it was written.
	content, _ := os.ReadFile(configPath)
	if !strings.Contains(string(content), "persist-delete") {
		t.Fatal("config should contain schedule before deletion")
	}

	// Delete it.
	text, isErr = callTool(t, session, "schedule_delete", map[string]any{
		"name": "persist-delete",
	})
	if isErr {
		t.Fatalf("schedule_delete error: %s", text)
	}

	// Verify it was removed from config.
	content, _ = os.ReadFile(configPath)
	if strings.Contains(string(content), "persist-delete") {
		t.Errorf("config should not contain deleted schedule, got:\n%s", content)
	}
}

func TestSkillDelete_Success(t *testing.T) {
	var removedName string
	session, deps := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "doomed" {
				return skill.Skill{Name: "doomed", Body: "# Doomed"}, true
			}
			return skill.Skill{}, false
		}
		d.RemoveSkill = func(name string) bool {
			removedName = name
			return true
		}
	})

	// Create the skill file on disk so deletion can remove it.
	if err := os.MkdirAll(deps.AgentSkillsDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps.AgentSkillsDir, "doomed.md"), []byte("# Doomed"), 0600); err != nil {
		t.Fatal(err)
	}

	text, isErr := callTool(t, session, "skill_delete", map[string]any{
		"name": "doomed",
	})
	if isErr {
		t.Fatalf("skill_delete error: %s", text)
	}
	if removedName != "doomed" {
		t.Errorf("RemoveSkill called with %q, want doomed", removedName)
	}

	// Verify file is gone.
	if _, err := os.Stat(filepath.Join(deps.AgentSkillsDir, "doomed.md")); !os.IsNotExist(err) {
		t.Error("skill file should have been deleted")
	}
}

func TestSkillDelete_NotFound(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(_ string) (skill.Skill, bool) { return skill.Skill{}, false }
		d.RemoveSkill = func(_ string) bool { return false }
	})

	text, isErr := callTool(t, session, "skill_delete", map[string]any{
		"name": "nonexistent",
	})
	if !isErr {
		t.Fatalf("expected error for nonexistent skill, got: %s", text)
	}
}

func TestSkillDelete_RestrictedTier(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(_ string) (skill.Skill, bool) { return skill.Skill{}, false }
		d.RemoveSkill = func(_ string) bool { return true }
		d.PermissionTier = func() string { return "restricted" }
	})

	text, isErr := callTool(t, session, "skill_delete", map[string]any{
		"name": "any",
	})
	if !isErr {
		t.Fatalf("expected error for restricted tier, got: %s", text)
	}
}

func TestSkillDelete_MissingName(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(_ string) (skill.Skill, bool) { return skill.Skill{}, false }
		d.RemoveSkill = func(_ string) bool { return true }
	})

	text, isErr := callTool(t, session, "skill_delete", map[string]any{
		"name": "",
	})
	if !isErr {
		t.Fatalf("expected error for empty name, got: %s", text)
	}
}

func TestScheduleAdd_NoConfigPath_NoPersistence(t *testing.T) {
	session, _ := newTestServer(t, nil) // no ConfigPath set

	text, isErr := callTool(t, session, "schedule_add", map[string]any{
		"name":     "ephemeral",
		"schedule": "@daily",
		"channel":  "telegram:1",
	})
	if isErr {
		t.Fatalf("schedule_add error: %s", text)
	}
	// No assertion on disk — just verify it doesn't panic or error without ConfigPath.
}

// --------------------------------------------------------------------------
// Tests: skill_patch
// --------------------------------------------------------------------------

func newSkillPatchServer(t *testing.T, tier string, body string, pinned bool) (*mcp.ClientSession, *skill.Skill) {
	t.Helper()
	sk := &skill.Skill{
		Name:        "greet",
		Description: "Greets users",
		Body:        body,
	}

	var mu sync.RWMutex
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		if err := os.MkdirAll(d.AgentSkillsDir, 0o755); err != nil {
			t.Fatalf("creating skills dir: %v", err)
		}
		d.PermissionTier = func() string { return tier }
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			if name == sk.Name {
				return *sk, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			if name == sk.Name {
				*sk = s
				return true
			}
			return false
		}
		d.IsSkillPinned = func(name string) (bool, error) {
			if name == sk.Name {
				return pinned, nil
			}
			return false, nil
		}
	})
	return session, sk
}

func TestSkillPatch_HappyPath(t *testing.T) {
	session, sk := newSkillPatchServer(t, "autonomous", "Hello world, welcome!", false)

	text, isErr := callTool(t, session, "skill_patch", map[string]string{
		"name":       "greet",
		"old_string": "Hello world",
		"new_string": "Hi there",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(sk.Body, "Hi there") {
		t.Errorf("expected body to contain 'Hi there', got %q", sk.Body)
	}
	if strings.Contains(sk.Body, "Hello world") {
		t.Errorf("expected body to NOT contain 'Hello world', got %q", sk.Body)
	}
}

func TestSkillPatch_NoMatchErrors(t *testing.T) {
	session, _ := newSkillPatchServer(t, "autonomous", "Hello world", false)

	text, isErr := callTool(t, session, "skill_patch", map[string]string{
		"name":       "greet",
		"old_string": "not found text",
		"new_string": "replacement",
	})
	if !isErr {
		t.Fatal("expected error for no match")
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' error, got %q", text)
	}
}

func TestSkillPatch_MultipleMatchErrors(t *testing.T) {
	session, _ := newSkillPatchServer(t, "autonomous", "foo bar foo baz", false)

	text, isErr := callTool(t, session, "skill_patch", map[string]string{
		"name":       "greet",
		"old_string": "foo",
		"new_string": "qux",
	})
	if !isErr {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(text, "2 times") {
		t.Errorf("expected match count error, got %q", text)
	}
}

func TestSkillPatch_PinnedRefuses(t *testing.T) {
	session, _ := newSkillPatchServer(t, "autonomous", "Hello world", true)

	text, isErr := callTool(t, session, "skill_patch", map[string]string{
		"name":       "greet",
		"old_string": "Hello",
		"new_string": "Hi",
	})
	if !isErr {
		t.Fatal("expected error for pinned skill")
	}
	if !strings.Contains(text, "pinned") {
		t.Errorf("expected 'pinned' error, got %q", text)
	}
}

func TestSkillPatch_EmptyNewStringDeletes(t *testing.T) {
	session, sk := newSkillPatchServer(t, "autonomous", "Keep this. Remove this part.", false)

	text, isErr := callTool(t, session, "skill_patch", map[string]string{
		"name":       "greet",
		"old_string": " Remove this part.",
		"new_string": "",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if sk.Body != "Keep this." {
		t.Errorf("expected body 'Keep this.', got %q", sk.Body)
	}
}

// --------------------------------------------------------------------------
// Tests: skill_read_file / skill_write_file
// --------------------------------------------------------------------------

func newSkillFileServer(t *testing.T, tier string) (*mcp.ClientSession, *skill.Skill, string) {
	t.Helper()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "research")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "api.md"), []byte("# API Reference\nSome content"), 0o644); err != nil {
		t.Fatal(err)
	}

	sk := &skill.Skill{
		Name:         "research",
		Description:  "Research skill",
		Body:         "Do research.",
		Dir:          skillDir,
		SubFileNames: []string{"references/api.md"},
	}

	var mu sync.RWMutex
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.AgentSkillsDir = filepath.Join(dir, "skills")
		d.PermissionTier = func() string { return tier }
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			if name == sk.Name {
				return *sk, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			if name == sk.Name {
				*sk = s
				return true
			}
			return false
		}
	})
	return session, sk, skillDir
}

func TestSkillReadFile_HappyPath(t *testing.T) {
	session, _, _ := newSkillFileServer(t, "autonomous")

	text, isErr := callTool(t, session, "skill_read_file", map[string]string{
		"skill":     "research",
		"file_path": "references/api.md",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "API Reference") {
		t.Errorf("expected file content, got %q", text)
	}
}

func TestSkillReadFile_NotFound(t *testing.T) {
	session, _, _ := newSkillFileServer(t, "autonomous")

	text, isErr := callTool(t, session, "skill_read_file", map[string]string{
		"skill":     "research",
		"file_path": "references/nonexistent.md",
	})
	if !isErr {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' error, got %q", text)
	}
}

func TestSkillReadFile_FlatSkillRejects(t *testing.T) {
	// Create a server with a flat skill (no Dir)
	flatSk := &skill.Skill{Name: "flat", Body: "simple"}
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "flat" {
				return *flatSk, true
			}
			return skill.Skill{}, false
		}
	})

	text, isErr := callTool(t, session, "skill_read_file", map[string]string{
		"skill":     "flat",
		"file_path": "references/test.md",
	})
	if !isErr {
		t.Fatal("expected error for flat skill")
	}
	if !strings.Contains(text, "flat-file") {
		t.Errorf("expected flat-file error, got %q", text)
	}
}

func TestSkillWriteFile_CreatesNewReference(t *testing.T) {
	session, sk, _ := newSkillFileServer(t, "autonomous")

	text, isErr := callTool(t, session, "skill_write_file", map[string]string{
		"skill":     "research",
		"file_path": "references/new-doc.md",
		"content":   "# New Doc\nNew content",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	if !strings.Contains(text, `"ok":true`) && !strings.Contains(text, `"ok": true`) {
		t.Errorf("expected ok response, got %q", text)
	}

	// Verify sub_files updated
	found := false
	for _, f := range sk.SubFileNames {
		if f == "references/new-doc.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SubFileNames should include new-doc.md, got %v", sk.SubFileNames)
	}
}

func TestSkillWriteFile_OverwritesExisting(t *testing.T) {
	session, _, skillDir := newSkillFileServer(t, "autonomous")

	text, isErr := callTool(t, session, "skill_write_file", map[string]string{
		"skill":     "research",
		"file_path": "references/api.md",
		"content":   "# Updated API\nNew content",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	data, err := os.ReadFile(filepath.Join(skillDir, "references", "api.md"))
	if err != nil {
		t.Fatalf("reading overwritten file: %v", err)
	}
	if !strings.Contains(string(data), "Updated API") {
		t.Errorf("file should be overwritten, got %q", string(data))
	}
}

func TestSkillWriteFile_PinnedRefuses(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "pinned-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.AgentSkillsDir = filepath.Join(dir, "skills")
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "pinned-skill" {
				return skill.Skill{Name: "pinned-skill", Dir: skillDir}, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(_ string, _ skill.Skill) bool { return true }
		d.IsSkillPinned = func(name string) (bool, error) {
			return name == "pinned-skill", nil
		}
	})

	text, isErr := callTool(t, session, "skill_write_file", map[string]string{
		"skill":     "pinned-skill",
		"file_path": "references/test.md",
		"content":   "test",
	})
	if !isErr {
		t.Fatal("expected error for pinned skill")
	}
	if !strings.Contains(text, "pinned") {
		t.Errorf("expected 'pinned' error, got %q", text)
	}
}

func TestSkillWriteFile_AutoConvertsFlat(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a flat skill file
	flatContent := `+++
name = "flat-skill"
description = "A flat skill"
+++

Body content.`
	if err := os.WriteFile(filepath.Join(skillsDir, "flat-skill.md"), []byte(flatContent), 0o644); err != nil {
		t.Fatal(err)
	}

	sk := &skill.Skill{
		Name:        "flat-skill",
		Description: "A flat skill",
		Body:        "Body content.",
		Source:      filepath.Join(skillsDir, "flat-skill.md"),
	}

	var mu sync.RWMutex
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.AgentSkillsDir = skillsDir
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			if name == "flat-skill" {
				return *sk, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			if name == "flat-skill" {
				*sk = s
				return true
			}
			return false
		}
	})

	text, isErr := callTool(t, session, "skill_write_file", map[string]string{
		"skill":     "flat-skill",
		"file_path": "references/notes.md",
		"content":   "Some reference notes.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	// Verify conversion happened
	if sk.Dir == "" {
		t.Error("skill Dir should be set after auto-conversion")
	}

	// Verify SKILL.md moved
	if _, err := os.Stat(filepath.Join(skillsDir, "flat-skill", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md should exist in subdirectory: %v", err)
	}

	// Verify old flat file removed
	if _, err := os.Stat(filepath.Join(skillsDir, "flat-skill.md")); err == nil {
		t.Error("old flat file should be removed after conversion")
	}

	// Verify the new file exists
	data, err := os.ReadFile(filepath.Join(skillsDir, "flat-skill", "references", "notes.md"))
	if err != nil {
		t.Fatalf("reading new reference file: %v", err)
	}
	if string(data) != "Some reference notes." {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

// --------------------------------------------------------------------------
// Tests: session_search
// --------------------------------------------------------------------------

func TestSessionSearch_BasicHit(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SearchMessages = func(_ context.Context, query string, limit int, agentFilter string) ([]agent.MessageSearchHit, error) {
			return []agent.MessageSearchHit{
				{
					ID:             1,
					ConversationID: "chan:test",
					Role:           "user",
					CreatedAt:      time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
					Snippet:        "discussing <b>quantum</b> computing",
				},
			}, nil
		}
	})

	text, isErr := callTool(t, session, "session_search", map[string]any{
		"query": "quantum",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "1 result") {
		t.Errorf("expected '1 result' in output, got: %s", text)
	}
	if !strings.Contains(text, "chan:test") {
		t.Errorf("expected conversation ID in output, got: %s", text)
	}
	if !strings.Contains(text, "quantum") {
		t.Errorf("expected snippet content in output, got: %s", text)
	}
}

func TestSessionSearch_EmptyQuery(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SearchMessages = func(_ context.Context, _ string, _ int, _ string) ([]agent.MessageSearchHit, error) {
			t.Fatal("SearchMessages should not be called with empty query")
			return nil, nil
		}
	})

	_, isErr := callTool(t, session, "session_search", map[string]any{
		"query": "",
	})
	if !isErr {
		t.Error("expected error for empty query")
	}
}

func TestSessionSearch_NilDep(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SearchMessages = nil
	})

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "session_search" {
			t.Error("session_search should not be registered when SearchMessages dep is nil")
		}
	}
}
