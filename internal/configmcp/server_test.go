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
