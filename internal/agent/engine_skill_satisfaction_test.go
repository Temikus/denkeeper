package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
	"github.com/Temikus/denkeeper/internal/tool"
)

// satisfactionToolArgs is the (empty) argument struct of the stand-in tools
// registered below — these tools exist to occupy a name in the registry, not
// to be called.
type satisfactionToolArgs struct{}

// registerSatisfactionTools connects an in-process MCP server exposing the
// named tools and registers it on mgr, the test-side equivalent of an MCP
// server coming online. Safe to call more than once with different server
// names, which is how the reactivation test makes a tool appear mid-session.
func registerSatisfactionTools(t *testing.T, mgr *tool.Manager, serverName string, toolNames ...string) {
	t.Helper()

	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: "v1"}, nil)
	for _, name := range toolNames {
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: name},
			func(_ context.Context, _ *mcp.CallToolRequest, _ satisfactionToolArgs) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
				}, nil, nil
			})
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("connecting MCP server %q: %v", serverName, err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "denkeeper-test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connecting MCP client for %q: %v", serverName, err)
	}
	if err := mgr.RegisterSession(context.Background(), serverName, session); err != nil {
		t.Fatalf("registering MCP session %q: %v", serverName, err)
	}
}

// newSatisfactionEngine builds an autonomous engine holding skills and the
// given tool manager (nil is passed through, so the "no capability
// information" path is reachable).
func newSatisfactionEngine(t *testing.T, mgr *tool.Manager, skills []skill.Skill, provider llm.Provider) *Engine {
	t.Helper()

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	if provider != nil {
		router.RegisterProvider(provider)
	}

	return NewEngine("default", router, store, nil, permissions, nil, "Test.",
		skills, mgr, nil, testLogger())
}

// newSatisfactionToolManager returns a manager exposing toolNames, or an empty
// one when called with none.
func newSatisfactionToolManager(t *testing.T, toolNames ...string) *tool.Manager {
	t.Helper()

	mgr := tool.NewManager(testLogger())
	t.Cleanup(func() { _ = mgr.Close() })
	if len(toolNames) > 0 {
		registerSatisfactionTools(t, mgr, "satisfaction-tools", toolNames...)
	}
	return mgr
}

// captureLogs redirects the engine's logger into a buffer so state-change
// logging can be counted.
func captureLogs(e *Engine) *bytes.Buffer {
	var buf bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &buf
}

func satisfactionMessage(sessionID, text string) adapter.IncomingMessage {
	return adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: sessionID,
		UserID:     "user-1",
		Text:       text,
		Timestamp:  time.Now(),
	}
}

func TestFilterUnsatisfiedSkills_MissingToolExcludes(t *testing.T) {
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"SKILL-BODY-MARKER", []string{"gmail_list"})
	e := newSatisfactionEngine(t, newSatisfactionToolManager(t, "kv_get"), []skill.Skill{s}, nil)

	msg := satisfactionMessage("sat-missing", "anything")
	if got, _ := e.filterUnsatisfiedSkills([]skill.Skill{s}, msg); len(got) != 0 {
		t.Fatalf("filtered set = %d skills, want 0 (gmail_list is not registered)", len(got))
	}

	result := e.buildSystemPrompt(nil, msg, nil)
	if strings.Contains(result.prompt, "SKILL-BODY-MARKER") {
		t.Error("system prompt contains the body of a skill whose required tool is unavailable")
	}
	if len(result.matchedSkills) != 0 {
		t.Errorf("matchedSkills = %d, want 0 — downstream consumers must see the filtered set", len(result.matchedSkills))
	}
}

func TestFilterUnsatisfiedSkills_AllPresentIncludes(t *testing.T) {
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"SKILL-BODY-MARKER", []string{"kv_get", "kv_set"})
	e := newSatisfactionEngine(t, newSatisfactionToolManager(t, "kv_get", "kv_set", "kv_list"), []skill.Skill{s}, nil)

	msg := satisfactionMessage("sat-present", "anything")
	got, _ := e.filterUnsatisfiedSkills([]skill.Skill{s}, msg)
	if len(got) != 1 || got[0].Name != "inbox-triage" {
		t.Fatalf("filtered set = %+v, want the skill kept (every declared tool is registered)", got)
	}

	result := e.buildSystemPrompt(nil, msg, nil)
	if !strings.Contains(result.prompt, "SKILL-BODY-MARKER") {
		t.Error("system prompt should contain the body of a satisfied skill")
	}
}

func TestFilterUnsatisfiedSkills_NilManagerNoFilter(t *testing.T) {
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"SKILL-BODY-MARKER", []string{"gmail_list"})
	e := newSatisfactionEngine(t, nil, []skill.Skill{s}, nil)

	msg := satisfactionMessage("sat-nil-mgr", "anything")
	got, _ := e.filterUnsatisfiedSkills([]skill.Skill{s}, msg)
	if len(got) != 1 {
		t.Fatalf("filtered set = %d skills, want 1 — a nil manager carries no capability information", len(got))
	}
	if result := e.buildSystemPrompt(nil, msg, nil); !strings.Contains(result.prompt, "SKILL-BODY-MARKER") {
		t.Error("system prompt should keep the skill body when the engine has no tool manager")
	}
}

// The item-defining race: the skill's server is absent for message 1 and
// registered before message 2. No restart, no reload, no event plumbing —
// satisfaction is re-derived from the live registry on every message.
func TestFilterUnsatisfiedSkills_ReactivatesAcrossMessages(t *testing.T) {
	provider := &capturingSequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "First.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
			{Content: "Second.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	mgr := newSatisfactionToolManager(t)
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"SKILL-BODY-MARKER", []string{"gmail_list"})
	e := newSatisfactionEngine(t, mgr, []skill.Skill{s}, provider)

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("sat-reactivate", "first"), nil); err != nil {
		t.Fatalf("first chat: %v", err)
	}

	// The server comes back; nothing tells the engine about it.
	registerSatisfactionTools(t, mgr, "gmail", "gmail_list")

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("sat-reactivate", "second"), nil); err != nil {
		t.Fatalf("second chat: %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("provider saw %d requests, want 2", len(provider.requests))
	}
	if got := systemPromptOf(t, provider.requests[0]); strings.Contains(got, "SKILL-BODY-MARKER") {
		t.Error("message 1: skill body present, want it excluded while gmail_list is unregistered")
	}
	if got := systemPromptOf(t, provider.requests[1]); !strings.Contains(got, "SKILL-BODY-MARKER") {
		t.Error("message 2: skill body absent, want it reactivated now that gmail_list is registered")
	}
}

func systemPromptOf(t *testing.T, req llm.ChatRequest) string {
	t.Helper()
	for _, m := range req.Messages {
		if m.Role == "system" {
			return m.Content
		}
	}
	t.Fatal("request carried no system message")
	return ""
}

func TestFilterUnsatisfiedSkills_ScheduledSkillExcludedWarns(t *testing.T) {
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "Nothing to do.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	s := skilltest.NewWithRequiresTools("nightly", "Nightly run", []string{"schedule:daily"},
		"SKILL-BODY-MARKER", []string{"gmail_list"})
	e := newSatisfactionEngine(t, newSatisfactionToolManager(t, "kv_get"), []skill.Skill{s}, provider)
	logs := captureLogs(e)

	msg := satisfactionMessage("sat-scheduled", "Run the nightly job")
	msg.SkillName = "nightly"
	msg.IsScheduled = true

	result, err := e.ChatWithEvents(context.Background(), msg, nil)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if result != "[engine: nightly skipped — required tools unavailable: gmail_list]" {
		t.Errorf("result = %q, want the skip notice", result)
	}
	if provider.callIndex != 0 {
		t.Errorf("provider called %d times, want 0 — a dropped scheduled skill must not spend an LLM round", provider.callIndex)
	}

	out := logs.String()
	if !strings.Contains(out, "scheduled skill deactivated") {
		t.Errorf("logs = %q, want a Warn naming the deactivated scheduled skill", out)
	}
	if !strings.Contains(out, "missing=gmail_list") {
		t.Errorf("logs = %q, want the missing tool name in the Warn", out)
	}
	// "not found" would be wrong here: the skill exists, its tools do not.
	if strings.Contains(out, "scheduled skill not found") {
		t.Errorf("logs = %q, want no not-found Warn for a skill that matched", out)
	}
	if strings.Contains(e.buildSystemPrompt(nil, msg, nil).prompt, "SKILL-BODY-MARKER") {
		t.Error("system prompt contains the scheduled skill's body despite its missing tool")
	}
}

func TestFilterUnsatisfiedSkills_LogsOncePerStateChange(t *testing.T) {
	mgr := newSatisfactionToolManager(t)
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"SKILL-BODY-MARKER", []string{"gmail_list"})
	e := newSatisfactionEngine(t, mgr, []skill.Skill{s}, nil)
	logs := captureLogs(e)

	msg := satisfactionMessage("sat-log-once", "anything")
	for i := 0; i < 3; i++ {
		e.buildSystemPrompt(nil, msg, nil)
	}
	if got := strings.Count(logs.String(), "skill deactivated"); got != 1 {
		t.Errorf("deactivation logged %d times across 3 messages, want 1 — an ambient skill matches every message", got)
	}

	registerSatisfactionTools(t, mgr, "gmail", "gmail_list")
	for i := 0; i < 3; i++ {
		e.buildSystemPrompt(nil, msg, nil)
	}
	if got := strings.Count(logs.String(), "skill reactivated"); got != 1 {
		t.Errorf("reactivation logged %d times across 3 messages, want 1", got)
	}
	if got := strings.Count(logs.String(), "skill deactivated"); got != 1 {
		t.Errorf("deactivation logged %d times in total, want 1", got)
	}
}

// A deactivated skill is inactive everywhere, not just in the prompt: it cannot
// drive the turn's round budget and is not recorded as used.
func TestFilterUnsatisfiedSkills_ExcludedSkillDoesNotDriveTurn(t *testing.T) {
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "Done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
			{Content: "Done again.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	mgr := newSatisfactionToolManager(t, "kv_get")
	s := skilltest.NewWithRequiresTools("triage", "Triage", []string{"command:triage"},
		"SKILL-BODY-MARKER", []string{"gmail_list"})
	s.MaxToolRounds = 2
	e := newSatisfactionEngine(t, mgr, []skill.Skill{s}, provider)

	msg := satisfactionMessage("sat-no-driver", "/triage now")
	result := e.buildSystemPrompt(nil, msg, nil)

	budget := e.resolveToolBudget(context.Background(), result.matchedSkills, msg)
	if budget.maxRounds != defaultMaxToolRounds || budget.skillName != "" {
		t.Errorf("budget = %+v, want the engine cap (%d) and no skill — an inactive skill must not cap rounds",
			budget, defaultMaxToolRounds)
	}

	if _, err := e.ChatWithEvents(context.Background(), msg, nil); err != nil {
		t.Fatalf("chat: %v", err)
	}
	store, ok := e.memory.(TelemetryStore)
	if !ok {
		t.Fatal("in-memory store does not implement TelemetryStore")
	}
	const convID = "default:test:sat-no-driver"
	usages, err := store.GetSkillUsages(context.Background(), convID)
	if err != nil {
		t.Fatalf("GetSkillUsages: %v", err)
	}
	if len(usages) != 0 {
		t.Errorf("recorded %d skill usages (%+v), want 0 for a deactivated skill", len(usages), usages)
	}

	// Control: the same command with the tool available records the usage and
	// binds the cap — so the assertions above measure the filter, not a wrong
	// conversation ID or a skill that never matched.
	registerSatisfactionTools(t, mgr, "gmail", "gmail_list")
	active := e.buildSystemPrompt(nil, msg, nil)
	if budget := e.resolveToolBudget(context.Background(), active.matchedSkills, msg); budget.skillName != "triage" {
		t.Errorf("budget = %+v, want the skill cap once its tool is available", budget)
	}
	if _, err := e.ChatWithEvents(context.Background(), msg, nil); err != nil {
		t.Fatalf("second chat: %v", err)
	}
	usages, err = store.GetSkillUsages(context.Background(), convID)
	if err != nil {
		t.Fatalf("GetSkillUsages: %v", err)
	}
	if len(usages) != 1 || usages[0].SkillName != "triage" {
		t.Errorf("recorded %+v, want one usage of triage after its tool returned", usages)
	}
}

// A dropped scheduled skill answers with a notice on the schedule's channel,
// audits it, and persists the turn like any other.
func TestScheduledSkillDropped_NoticeAuditedAndPersisted(t *testing.T) {
	provider := &sequentialProvider{}
	s := skilltest.NewWithRequiresTools("heartbeat", "Heartbeat", []string{"schedule:heartbeat"},
		"SKILL-BODY-MARKER", []string{"find-tasks", "find-projects"})
	e := newSatisfactionEngine(t, newSatisfactionToolManager(t, "kv_get"), []skill.Skill{s}, provider)
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	msg := satisfactionMessage("sat-dropped", "[Scheduled: heartbeat | 2026-09-05T08:30:51+10:00 Australia/Sydney | 2026-W36]")
	msg.SkillName = "heartbeat"
	msg.ScheduleName = "heartbeat"
	msg.IsScheduled = true

	result, err := e.ChatWithEvents(context.Background(), msg, nil)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !strings.HasPrefix(result, "[engine: heartbeat skipped — required tools unavailable: ") {
		t.Fatalf("result = %q, want the skip notice", result)
	}
	if !strings.Contains(result, "find-tasks") || !strings.Contains(result, "find-projects") {
		t.Errorf("result = %q, want both missing tools named", result)
	}

	var found *audit.Event
	for i := range auditor.events {
		ev := auditor.events[i]
		if ev.Category == audit.CategorySkill && ev.Action == "deactivated" {
			found = &ev
			break
		}
	}
	if found == nil {
		t.Fatalf("no skill/deactivated audit event among %d events", len(auditor.events))
	}
	if found.Status != audit.StatusError {
		t.Errorf("audit status = %q, want %q", found.Status, audit.StatusError)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(found.Detail), &detail); err != nil {
		t.Fatalf("audit detail is not JSON: %v (%q)", err, found.Detail)
	}
	if detail["skill"] != "heartbeat" || detail["schedule"] != "heartbeat" {
		t.Errorf("audit detail = %v, want skill and schedule named", detail)
	}

	msgs, err := e.memory.GetMessages(context.Background(), "default:test:sat-dropped", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[1].Role != "assistant" || msgs[1].Content != result {
		t.Errorf("stored messages = %+v, want user + assistant notice", msgs)
	}
}

// A live command turn keeps the old behaviour: the skill body is withheld but
// the model still answers.
func TestScheduledSkillDropped_LiveTurnStillRuns(t *testing.T) {
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "Answered anyway.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	s := skilltest.NewWithRequiresTools("nightly", "Nightly run", []string{"command:nightly"},
		"SKILL-BODY-MARKER", []string{"gmail_list"})
	e := newSatisfactionEngine(t, newSatisfactionToolManager(t, "kv_get"), []skill.Skill{s}, provider)

	msg := satisfactionMessage("sat-live", "/nightly")
	result, err := e.ChatWithEvents(context.Background(), msg, nil)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if result != "Answered anyway." {
		t.Errorf("result = %q, want the model's answer", result)
	}
}
