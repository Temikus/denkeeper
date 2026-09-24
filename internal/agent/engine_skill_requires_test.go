package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
	"github.com/Temikus/denkeeper/internal/tool"
)

// newExposureEngine builds an autonomous engine whose router advertises mgr's
// tools, which is what makes the assembled request payload observable. The
// satisfaction tests deliberately leave the router tool-less; exposure gating
// is only visible with a live tool source.
func newExposureEngine(t *testing.T, mgr *tool.Manager, skills []skill.Skill, provider llm.Provider) *Engine {
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

	router := llm.NewRouter("mock", "test-model", llm.NewCostTracker(llm.SessionLimits{}, nil))
	router.RegisterProvider(provider)
	router.SetTools(mgr.ToolDefs)

	return NewEngine("default", router, store, nil, permissions, nil, "Test.",
		skills, mgr, nil, testLogger())
}

// advertisedTools returns the tool names on the request the provider saw.
func advertisedTools(t *testing.T, provider *capturingSequentialProvider, i int) []string {
	t.Helper()
	if len(provider.requests) <= i {
		t.Fatalf("provider saw %d requests, want more than %d", len(provider.requests), i)
	}
	names := make([]string, 0, len(provider.requests[i].Tools))
	for _, td := range provider.requests[i].Tools {
		names = append(names, td.Function.Name)
	}
	slices.Sort(names)
	return names
}

func exposureProvider() *capturingSequentialProvider {
	return &capturingSequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "Done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
}

func TestToolExposure_DeclaringSkillNarrowsPayload(t *testing.T) {
	mgr := newSatisfactionToolManager(t, "gmail_list", "kv_get", "kv_set")
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"BODY", []string{"gmail_list"})
	provider := exposureProvider()
	e := newExposureEngine(t, mgr, []skill.Skill{s}, provider)

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("exp-narrow", "go"), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	got := advertisedTools(t, provider, 0)
	if !slices.Equal(got, []string{"gmail_list"}) {
		t.Errorf("advertised tools = %v, want only the declared gmail_list", got)
	}
}

func TestToolExposure_UnionAcrossActiveSkills(t *testing.T) {
	mgr := newSatisfactionToolManager(t, "gmail_list", "kv_get", "kv_set")
	declaring := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"BODY", []string{"gmail_list"})
	other := skilltest.NewWithRequiresTools("notes", "Keep notes", nil,
		"BODY", []string{"kv_set"})
	provider := exposureProvider()
	e := newExposureEngine(t, mgr, []skill.Skill{declaring, other}, provider)

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("exp-union", "go"), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	got := advertisedTools(t, provider, 0)
	if !slices.Equal(got, []string{"gmail_list", "kv_set"}) {
		t.Errorf("advertised tools = %v, want the union of both skills' declarations", got)
	}
}

func TestToolExposure_NoDeclarationsAdvertisesEverything(t *testing.T) {
	mgr := newSatisfactionToolManager(t, "gmail_list", "kv_get", "kv_set")
	s := skilltest.New("chatty", "Just talk", nil, "BODY")
	provider := exposureProvider()
	e := newExposureEngine(t, mgr, []skill.Skill{s}, provider)

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("exp-open", "go"), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	got := advertisedTools(t, provider, 0)
	if !slices.Equal(got, []string{"gmail_list", "kv_get", "kv_set"}) {
		t.Errorf("advertised tools = %v, want every registered tool (nothing declared, so the gate fails open)", got)
	}
}

// A declaration naming a tool that does not exist deactivates the skill (the
// inclusion half), which leaves nothing declared — so the turn must fall back
// to advertising everything rather than to advertising nothing.
func TestToolExposure_UnknownDeclaredToolDoesNotEmptyPayload(t *testing.T) {
	mgr := newSatisfactionToolManager(t, "gmail_list", "kv_get", "kv_set")
	s := skilltest.NewWithRequiresTools("typo", "Mistyped requirement", nil,
		"BODY", []string{"gmial_list"})
	provider := exposureProvider()
	e := newExposureEngine(t, mgr, []skill.Skill{s}, provider)

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("exp-typo", "go"), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	got := advertisedTools(t, provider, 0)
	if !slices.Equal(got, []string{"gmail_list", "kv_get", "kv_set"}) {
		t.Errorf("advertised tools = %v, want every registered tool — a typo must not blind the agent", got)
	}
}

// The agent's self-management surface is exempt by server identity: a focused
// skill can hide task tools but never the tools the agent patches itself with.
func TestToolExposure_AlwaysAdvertisedServerSurvivesGate(t *testing.T) {
	mgr := newSatisfactionToolManager(t, "gmail_list", "kv_get")
	registerSatisfactionTools(t, mgr, "config-default", "skill_get", "skill_update")
	mgr.MarkAlwaysAdvertised("config-default")

	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"BODY", []string{"gmail_list"})
	provider := exposureProvider()
	e := newExposureEngine(t, mgr, []skill.Skill{s}, provider)

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("exp-exempt", "go"), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	got := advertisedTools(t, provider, 0)
	if !slices.Equal(got, []string{"gmail_list", "skill_get", "skill_update"}) {
		t.Errorf("advertised tools = %v, want the declared tool plus the exempt config server", got)
	}
}

// Every completion in the turn is gated, not just the first: a tool round's
// follow-up call must not quietly re-advertise the full surface.
func TestToolExposure_AppliesToFollowUpRounds(t *testing.T) {
	mgr := newSatisfactionToolManager(t, "gmail_list", "kv_get", "kv_set")
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"BODY", []string{"gmail_list"})
	provider := &capturingSequentialProvider{
		responses: []*llm.ChatResponse{
			{
				FinishReason: "tool_calls",
				TokensUsed:   llm.TokenUsage{Total: 5},
				ToolCalls: []llm.ToolCall{{
					ID:       "call-1",
					Type:     "function",
					Function: llm.FunctionCall{Name: "gmail_list", Arguments: "{}"},
				}},
			},
			{Content: "Done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	e := newExposureEngine(t, mgr, []skill.Skill{s}, provider)

	if _, err := e.ChatWithEvents(context.Background(), satisfactionMessage("exp-rounds", "go"), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}

	got := advertisedTools(t, provider, 1)
	if !slices.Equal(got, []string{"gmail_list"}) {
		t.Errorf("round-2 advertised tools = %v, want the same narrowed set as round 1", got)
	}
}

func TestResolveToolExposure_NilManagerAdvertisesEverything(t *testing.T) {
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil,
		"BODY", []string{"gmail_list"})
	e := newSatisfactionEngine(t, nil, []skill.Skill{s}, nil)

	exposure := e.resolveToolExposure(context.Background(), []skill.Skill{s}, satisfactionMessage("exp-nil", "go"))
	if len(exposure.allowed) != 0 {
		t.Errorf("allowed = %v, want empty — a nil manager carries no capability information", exposure.allowed)
	}
	if exposure.filter(nil) != nil {
		t.Error("filter should be nil when there is nothing to gate")
	}
}
