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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
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
		Sched: scheduler.New(newTestLogger(t)),
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

func TestSkillCreate_Supervised_SubmitsApproval(t *testing.T) {
	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	approvalMgr := approval.NewManager(store, newTestLogger(t))

	var skillsDir string
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		skillsDir = d.AgentSkillsDir
		d.PermissionTier = func() string { return "supervised" }
		d.Approvals = approvalMgr
	})

	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name": "supervised-skill",
		"body": "# Supervised\n\nPending approval.",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "pproval") && !strings.Contains(text, "ubmit") {
		t.Errorf("expected approval-related message, got: %s", text)
	}

	// The skill file should NOT exist yet (approval pending).
	skillFile := filepath.Join(skillsDir, "supervised-skill.md")
	if _, err := os.Stat(skillFile); err == nil {
		t.Error("skill file should not exist before approval")
	}

	// A pending approval should be recorded.
	requests, err := approvalMgr.List(context.Background(), approval.StatusPending)
	if err != nil {
		t.Fatalf("listing approvals: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(requests))
	}
	if requests[0].Kind != approval.ActionKindCreateSkill {
		t.Errorf("unexpected approval kind: %v", requests[0].Kind)
	}
}

// --------------------------------------------------------------------------
// Tests: schedule_add
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

// --------------------------------------------------------------------------
// Tests: supervised tier approvals for new tools
// --------------------------------------------------------------------------

func TestSetFallback_Supervised_SubmitsApproval(t *testing.T) {
	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	approvalMgr := approval.NewManager(store, newTestLogger(t))

	var applied bool
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.PermissionTier = func() string { return "supervised" }
		d.Approvals = approvalMgr
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
	if !strings.Contains(text, "pproval") && !strings.Contains(text, "ubmit") {
		t.Errorf("expected approval submission message, got: %s", text)
	}

	// Should NOT have been applied yet.
	if applied {
		t.Error("fallback rules should not be applied before approval")
	}

	// A pending approval should exist.
	requests, err := approvalMgr.List(context.Background(), approval.StatusPending)
	if err != nil {
		t.Fatalf("listing approvals: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(requests))
	}
	if requests[0].Kind != approval.ActionKindModifyConfig {
		t.Errorf("approval kind = %q, want modify_config", requests[0].Kind)
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
