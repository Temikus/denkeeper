package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
	"github.com/Temikus/denkeeper/internal/tool"
)

// newRoundCapTestEngine builds an engine holding skills, with the engine-level
// round budget left at its default so tests can tell a skill cap apart from it.
func newRoundCapTestEngine(t *testing.T, provider llm.Provider, skills []skill.Skill) *Engine {
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
		skills, tool.NewManager(testLogger()), nil, testLogger())
}

// --- resolveToolBudget unit tests ---

func TestResolveToolBudget_ScheduledSkillCap(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.NewWithRoundCap("nightly", "Nightly run", []string{"schedule:daily"}, "", 8),
	}

	got := e.resolveToolBudget(context.Background(), matched, adapter.IncomingMessage{SkillName: "nightly"})
	if got.maxRounds != 8 || got.skillName != "nightly" {
		t.Errorf("budget = %+v, want {maxRounds:8 skillName:nightly}", got)
	}
}

// A command-invoked skill's cap applies even when ambient (trigger-less) skills
// co-match, since ambient skills are injected into every message.
func TestResolveToolBudget_CommandSkillCap(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.New("ambient", "Always on", nil, ""),
		skilltest.NewWithRoundCap("capped", "Command skill", []string{"command:capped"}, "", 8),
	}

	got := e.resolveToolBudget(context.Background(), matched, adapter.IncomingMessage{Text: "/capped go"})
	if got.maxRounds != 8 || got.skillName != "capped" {
		t.Errorf("budget = %+v, want {maxRounds:8 skillName:capped}", got)
	}
}

// The deliberate divergence from attributeSkill: a lone ambient skill owns the
// turn for telemetry, but must not throttle it. An ambient skill matches every
// message, so honoring its cap would silently shrink unrelated chat turns.
func TestResolveToolBudget_AmbientOnly_NoCap(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.NewWithRoundCap("ambient", "Always on", nil, "", 8),
	}

	got := e.resolveToolBudget(context.Background(), matched, adapter.IncomingMessage{Text: "hello"})
	if got.maxRounds != defaultMaxToolRounds || got.skillName != "" {
		t.Errorf("budget = %+v, want the engine cap (%d) and no skill name", got, defaultMaxToolRounds)
	}
}

func TestResolveToolBudget_AmbiguousCommands_NoCap(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.NewWithRoundCap("first", "First", []string{"command:go"}, "", 4),
		skilltest.NewWithRoundCap("second", "Second", []string{"command:go"}, "", 8),
	}

	got := e.resolveToolBudget(context.Background(), matched, adapter.IncomingMessage{Text: "/go"})
	if got.maxRounds != defaultMaxToolRounds || got.skillName != "" {
		t.Errorf("budget = %+v, want the engine cap (%d) and no skill name", got, defaultMaxToolRounds)
	}
}

// A skill can only tighten the operator's ceiling, never widen it — and when
// the engine cap is the binding constraint, skillName stays empty so the hint
// does not credit a skill for a number it did not set.
func TestResolveToolBudget_SkillCapAboveEngine_ClampsToEngine(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.NewWithRoundCap("greedy", "Wants more", []string{"schedule:daily"}, "", defaultMaxToolRounds+30),
	}

	got := e.resolveToolBudget(context.Background(), matched, adapter.IncomingMessage{SkillName: "greedy"})
	if got.maxRounds != defaultMaxToolRounds || got.skillName != "" {
		t.Errorf("budget = %+v, want the engine cap (%d) and no skill name", got, defaultMaxToolRounds)
	}
}

// The buildSystemPrompt warn path: a schedule names a skill that is not in the
// matched set, so no skill drives the turn.
func TestResolveToolBudget_ScheduledSkillNotFound_NoCap(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.NewWithRoundCap("present", "Present", []string{"schedule:daily"}, "", 4),
	}

	got := e.resolveToolBudget(context.Background(), matched, adapter.IncomingMessage{SkillName: "ghost"})
	if got.maxRounds != defaultMaxToolRounds || got.skillName != "" {
		t.Errorf("budget = %+v, want the engine cap (%d) and no skill name", got, defaultMaxToolRounds)
	}
}

func TestResolveToolBudget_SkillWithoutCap_UsesEngineCap(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.New("plain", "No cap", []string{"schedule:daily"}, ""),
	}

	got := e.resolveToolBudget(context.Background(), matched, adapter.IncomingMessage{SkillName: "plain"})
	if got.maxRounds != defaultMaxToolRounds || got.skillName != "" {
		t.Errorf("budget = %+v, want the engine cap (%d) and no skill name", got, defaultMaxToolRounds)
	}
}

// --- budget hint ---

func TestToolBudgetHint_SkillCapNamesSkill(t *testing.T) {
	budget := turnToolBudget{maxRounds: 8, skillName: "inbox-triage"}

	got := toolBudgetHint(budget, 5)
	want := "\n\n[engine: 3 of 8 tool-call rounds remaining this turn (skill cap: inbox-triage)]"
	if got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}

	got = toolBudgetHint(budget, 8)
	want = "\n\n[engine: 0 of 8 tool-call rounds remaining (skill cap: inbox-triage) — your next response must be the final answer, without tool calls]"
	if got != want {
		t.Errorf("final-round hint = %q, want %q", got, want)
	}

	// Regression guard: uncapped turns must render byte-identically to the
	// pre-skill-cap strings, so the common case sees no prompt churn.
	if got, want := toolBudgetHint(turnToolBudget{maxRounds: 8}, 5), "\n\n[engine: 3 of 8 tool-call rounds remaining this turn]"; got != want {
		t.Errorf("uncapped hint = %q, want %q", got, want)
	}
	if got, want := toolBudgetHint(turnToolBudget{maxRounds: 8}, 8), "\n\n[engine: 0 of 8 tool-call rounds remaining — your next response must be the final answer, without tool calls]"; got != want {
		t.Errorf("uncapped final-round hint = %q, want %q", got, want)
	}
}

// --- loop enforcement ---

// Mirror of TestEngine_ConfigurableMaxToolRounds, but the cap comes from the
// scheduled skill with the engine budget left at its default. Both runs consume
// the same four provider responses; only the capped one stops early, so the
// early-end marker is the discriminator.
func TestEngine_SkillMaxToolRounds_CapsLoop(t *testing.T) {
	toolRound := func(id, name string) *llm.ChatResponse {
		return &llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: `{}`}}},
			TokensUsed:   llm.TokenUsage{Total: 10},
			FinishReason: "tool_calls",
		}
	}
	responses := func() []*llm.ChatResponse {
		return []*llm.ChatResponse{
			toolRound("c1", "tool_a"),
			toolRound("c2", "tool_b"),
			toolRound("c3", "tool_c"),
			{Content: "Final.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		}
	}
	capped := skilltest.NewWithRoundCap("nightly", "Nightly run", []string{"schedule:daily"}, "Do the nightly work.", 2)

	countRounds := func(t *testing.T, skills []skill.Skill, sessionID string) (string, int, int) {
		t.Helper()
		provider := &sequentialProvider{responses: responses()}
		engine := newRoundCapTestEngine(t, provider, skills)

		var toolStarts int
		result, err := engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
			Adapter:     "test",
			ExternalID:  sessionID,
			UserID:      "user-1",
			Text:        "Run the nightly job",
			SkillName:   "nightly",
			IsScheduled: true,
			Timestamp:   time.Now(),
		}, func(ev ChatEvent) {
			if ev.Type == "tool_start" {
				toolStarts++
			}
		})
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
		return result, toolStarts, provider.callIndex
	}

	result, rounds, calls := countRounds(t, []skill.Skill{capped}, "skillcap-capped")
	if rounds != 2 {
		t.Errorf("executed %d tool rounds, want 2 (the skill cap)", rounds)
	}
	// Initial + 2 round completions + wrap-up.
	if calls != 4 {
		t.Errorf("provider called %d times, want 4", calls)
	}
	if !strings.Contains(result, "Final.") {
		t.Errorf("result = %q, want the wrap-up text 'Final.'", result)
	}
	if !strings.Contains(result, "[engine: turn ended early — tool-call round budget exhausted]") {
		t.Errorf("result = %q, want the early-end marker (stopMaxRounds is reused for skill caps)", result)
	}

	// Control: the same skill without a cap runs every round the model asks
	// for and finishes cleanly, proving the cap — not the response list — is
	// what ended the capped turn.
	uncapped := skilltest.New("nightly", "Nightly run", []string{"schedule:daily"}, "Do the nightly work.")
	result, rounds, calls = countRounds(t, []skill.Skill{uncapped}, "skillcap-uncapped")
	if rounds != 3 {
		t.Errorf("uncapped: executed %d tool rounds, want 3", rounds)
	}
	if calls != 4 {
		t.Errorf("uncapped: provider called %d times, want 4", calls)
	}
	if strings.Contains(result, "turn ended early") {
		t.Errorf("uncapped result = %q, want no early-end marker", result)
	}
}

// The hint the model actually reads must carry the skill-capped numbers, not
// the engine's, so stale "cap at N calls" prose cannot outrank the engine.
func TestEngine_SkillMaxToolRounds_HintUsesSkillCap(t *testing.T) {
	provider := &capturingSequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls:    []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "tool_a", Arguments: `{}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "Done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	capped := skilltest.NewWithRoundCap("nightly", "Nightly run", []string{"schedule:daily"}, "Body.", 3)
	engine := newRoundCapTestEngine(t, provider, []skill.Skill{capped})

	if _, err := engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:     "test",
		ExternalID:  "skillcap-hint",
		UserID:      "user-1",
		Text:        "Run the nightly job",
		SkillName:   "nightly",
		IsScheduled: true,
		Timestamp:   time.Now(),
	}, nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	if len(provider.requests) < 2 {
		t.Fatalf("got %d provider requests, want at least 2", len(provider.requests))
	}
	var hint string
	for _, m := range provider.requests[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "[engine:") {
			hint = m.Content
		}
	}
	if !strings.Contains(hint, "2 of 3 tool-call rounds remaining this turn (skill cap: nightly)") {
		t.Errorf("tool result hint = %q, want the skill-capped budget naming the skill", hint)
	}
}
