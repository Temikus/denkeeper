package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
)

const outputCapSkillBody = "Do the nightly work."

func nightlyOutputCapSkill(maxOutputTokens int) skill.Skill {
	return skilltest.NewWithOutputCap("nightly", "Nightly run", []string{"schedule:daily"}, outputCapSkillBody, maxOutputTokens)
}

func nightlyMsg(externalID string) adapter.IncomingMessage {
	return adapter.IncomingMessage{
		Adapter:     "test",
		ExternalID:  externalID,
		UserID:      "user-1",
		Text:        "Run the nightly job",
		SkillName:   "nightly",
		IsScheduled: true,
		Timestamp:   time.Now(),
	}
}

func stopResponse(text string) *llm.ChatResponse {
	return &llm.ChatResponse{Content: text, TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"}
}

// outputCapTurn runs one chat turn on an engine with the given agent cap and
// returns every request the provider received.
func outputCapTurn(t *testing.T, agentCap int, skills []skill.Skill, msg adapter.IncomingMessage, responses ...*llm.ChatResponse) []llm.ChatRequest {
	t.Helper()
	provider := &capturingSequentialProvider{responses: responses}
	e := newRoundCapTestEngine(t, provider, skills)
	e.SetMaxOutputTokens(agentCap)
	if _, err := e.ChatWithEvents(context.Background(), msg, nil); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(provider.requests) == 0 {
		t.Fatal("provider received no requests")
	}
	return provider.requests
}

func assertMaxTokens(t *testing.T, reqs []llm.ChatRequest, want int) {
	t.Helper()
	for i, r := range reqs {
		if r.MaxTokens != want {
			t.Errorf("request %d MaxTokens = %d, want %d", i, r.MaxTokens, want)
		}
	}
}

// --- resolveOutputCap unit tests ---

func TestResolveOutputCap_NoAgentCap_SkillValueApplies(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	got := e.resolveOutputCap(context.Background(), []skill.Skill{nightlyOutputCapSkill(1200)}, nightlyMsg("x"))
	if got != 1200 {
		t.Errorf("cap = %d, want 1200", got)
	}
}

func TestResolveOutputCap_SkillBelowAgent_SkillApplies(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	e.SetMaxOutputTokens(8000)
	got := e.resolveOutputCap(context.Background(), []skill.Skill{nightlyOutputCapSkill(1200)}, nightlyMsg("x"))
	if got != 1200 {
		t.Errorf("cap = %d, want 1200", got)
	}
}

// Zero means "no override": the agent's own cap stays in force.
func TestResolveOutputCap_SkillAboveAgent_NoOverride(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	e.SetMaxOutputTokens(1000)
	got := e.resolveOutputCap(context.Background(), []skill.Skill{nightlyOutputCapSkill(5000)}, nightlyMsg("x"))
	if got != 0 {
		t.Errorf("cap = %d, want 0 (the agent cap binds)", got)
	}
}

func TestResolveOutputCap_SkillWithoutCap_NoOverride(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	e.SetMaxOutputTokens(8000)
	got := e.resolveOutputCap(context.Background(), []skill.Skill{nightlyOutputCapSkill(0)}, nightlyMsg("x"))
	if got != 0 {
		t.Errorf("cap = %d, want 0", got)
	}
}

func TestResolveOutputCap_AmbientOnly_NoOverride(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{skilltest.NewWithOutputCap("ambient", "Always on", nil, "", 1200)}
	got := e.resolveOutputCap(context.Background(), matched, adapter.IncomingMessage{Text: "hello"})
	if got != 0 {
		t.Errorf("cap = %d, want 0 (an ambient match does not cap)", got)
	}
}

func TestResolveOutputCap_AmbiguousCommands_NoOverride(t *testing.T) {
	e := newRoundCapTestEngine(t, nil, nil)
	matched := []skill.Skill{
		skilltest.NewWithOutputCap("first", "First", []string{"command:go"}, "", 1200),
		skilltest.NewWithOutputCap("second", "Second", []string{"command:go"}, "", 900),
	}
	got := e.resolveOutputCap(context.Background(), matched, adapter.IncomingMessage{Text: "/go"})
	if got != 0 {
		t.Errorf("cap = %d, want 0 (two command matches do not cap)", got)
	}
}

// --- what reaches the provider ---

func TestEngine_SkillOutputCap_ScheduledTurnWithNoAgentCapSendsSkillValue(t *testing.T) {
	reqs := outputCapTurn(t, 0, []skill.Skill{nightlyOutputCapSkill(1200)}, nightlyMsg("oc-noagent"), stopResponse("Done."))
	assertMaxTokens(t, reqs, 1200)
}

func TestEngine_SkillOutputCap_SkillBelowAgentSendsSkillValue(t *testing.T) {
	reqs := outputCapTurn(t, 8000, []skill.Skill{nightlyOutputCapSkill(1200)}, nightlyMsg("oc-below"), stopResponse("Done."))
	assertMaxTokens(t, reqs, 1200)
}

func TestEngine_SkillOutputCap_SkillAboveAgentSendsAgentValue(t *testing.T) {
	reqs := outputCapTurn(t, 1000, []skill.Skill{nightlyOutputCapSkill(5000)}, nightlyMsg("oc-above"), stopResponse("Done."))
	assertMaxTokens(t, reqs, 1000)
}

func TestEngine_SkillOutputCap_AmbientMatchDoesNotCap(t *testing.T) {
	ambient := skilltest.NewWithOutputCap("ambient", "Always on", nil, "Be nice.", 1200)
	msg := adapter.IncomingMessage{Adapter: "test", ExternalID: "oc-ambient", UserID: "user-1", Text: "hello", Timestamp: time.Now()}
	reqs := outputCapTurn(t, 8000, []skill.Skill{ambient}, msg, stopResponse("Hi."))
	assertMaxTokens(t, reqs, 8000)
}

func TestEngine_SkillOutputCap_TwoCommandMatchesDoNotCap(t *testing.T) {
	skills := []skill.Skill{
		skilltest.NewWithOutputCap("first", "First", []string{"command:go"}, "First body.", 1200),
		skilltest.NewWithOutputCap("second", "Second", []string{"command:go"}, "Second body.", 900),
	}
	msg := adapter.IncomingMessage{Adapter: "test", ExternalID: "oc-two", UserID: "user-1", Text: "/go", Timestamp: time.Now()}
	reqs := outputCapTurn(t, 8000, skills, msg, stopResponse("Went."))
	assertMaxTokens(t, reqs, 8000)
}

// A round cap of 1 forces the wrap-up: the first round runs, the second
// completion asks for another call, and the loop stops into CompleteFinal.
func TestEngine_SkillOutputCap_HoldsOnSecondRoundAndWrapUp(t *testing.T) {
	s := nightlyOutputCapSkill(1200)
	s.MaxToolRounds = 1
	toolRound := func(id string) *llm.ChatResponse {
		return &llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: id, Type: "function", Function: llm.FunctionCall{Name: "tool_a", Arguments: `{}`}}},
			TokensUsed:   llm.TokenUsage{Total: 10},
			FinishReason: "tool_calls",
		}
	}
	reqs := outputCapTurn(t, 8000, []skill.Skill{s}, nightlyMsg("oc-rounds"),
		toolRound("c1"), toolRound("c2"), stopResponse("Wrapped up."))

	if len(reqs) != 3 {
		t.Fatalf("got %d requests, want 3 (first round, second round, wrap-up)", len(reqs))
	}
	if reqs[2].Tools != nil {
		t.Errorf("request 3 carries %d tools, want none (the wrap-up)", len(reqs[2].Tools))
	}
	assertMaxTokens(t, reqs, 1200)
}

func TestDryRun_SkillOutputCap_KeepsModelOverride(t *testing.T) {
	provider := &capturingSequentialProvider{responses: []*llm.ChatResponse{stopResponse("Done.")}}
	e := newRoundCapTestEngine(t, provider, []skill.Skill{nightlyOutputCapSkill(1200)})
	e.SetMaxOutputTokens(8000)

	policy := dryRunPolicy()
	policy.Model = "candidate/model"
	if _, err := e.DryRun(context.Background(), nightlyMsg("oc-dryrun"), policy); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(provider.requests) == 0 {
		t.Fatal("provider received no requests")
	}
	for i, r := range provider.requests {
		if r.Model != "candidate/model" || r.MaxTokens != 1200 {
			t.Errorf("request %d model/cap = %q/%d, want candidate/model/1200", i, r.Model, r.MaxTokens)
		}
	}
	if got := e.MaxOutputTokens(); got != 8000 {
		t.Errorf("engine cap after dry run = %d, want 8000", got)
	}
}

func TestEngine_SkillOutputCap_UncappedTurnSendsAgentValue(t *testing.T) {
	reqs := outputCapTurn(t, 8000, []skill.Skill{nightlyOutputCapSkill(0)}, nightlyMsg("oc-plain"), stopResponse("Done."))
	assertMaxTokens(t, reqs, 8000)
}

func TestEngine_SkillOutputCap_LeavesTheEngineCapUnchanged(t *testing.T) {
	provider := &capturingSequentialProvider{responses: []*llm.ChatResponse{stopResponse("Done.")}}
	e := newRoundCapTestEngine(t, provider, []skill.Skill{nightlyOutputCapSkill(1200)})
	e.SetMaxOutputTokens(8000)
	if _, err := e.ChatWithEvents(context.Background(), nightlyMsg("oc-unchanged"), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if got := e.MaxOutputTokens(); got != 8000 {
		t.Errorf("engine cap after a capped turn = %d, want 8000", got)
	}
}

// lockedProvider answers every request with the same reply, safe for
// concurrent turns.
type lockedProvider struct {
	mu       sync.Mutex
	requests []llm.ChatRequest
}

func (p *lockedProvider) Name() string { return "mock" }
func (p *lockedProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	return stopResponse("Done."), nil
}
func (p *lockedProvider) HealthCheck(_ context.Context) error { return nil }

// Dry runs, because an in-memory store gives each pooled connection its own
// database. They resolve the turn's router the same way a live turn does.
func TestEngine_SkillOutputCap_ConcurrentUncappedTurnUnaffected(t *testing.T) {
	provider := &lockedProvider{}
	e := newRoundCapTestEngine(t, provider, []skill.Skill{nightlyOutputCapSkill(1200)})
	e.SetMaxOutputTokens(8000)

	const turns = 8
	var wg sync.WaitGroup
	errs := make(chan error, 2*turns)
	for i := range turns {
		wg.Add(2)
		go func() {
			defer wg.Done()
			policy := dryRunPolicy()
			policy.ConvID = fmt.Sprintf("dryrun:capped-%d", i)
			_, err := e.DryRun(context.Background(), nightlyMsg("oc-capped"), policy)
			errs <- err
		}()
		go func() {
			defer wg.Done()
			policy := dryRunPolicy()
			policy.ConvID = fmt.Sprintf("dryrun:free-%d", i)
			_, err := e.DryRun(context.Background(), adapter.IncomingMessage{
				Adapter: "test", ExternalID: "oc-free", UserID: "user-1", Text: "hello", Timestamp: time.Now(),
			}, policy)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
	}

	var capped, free int
	for _, r := range provider.requests {
		if strings.Contains(r.Messages[0].Content, outputCapSkillBody) {
			capped++
			if r.MaxTokens != 1200 {
				t.Errorf("capped turn MaxTokens = %d, want 1200", r.MaxTokens)
			}
			continue
		}
		free++
		if r.MaxTokens != 8000 {
			t.Errorf("uncapped turn MaxTokens = %d, want 8000", r.MaxTokens)
		}
	}
	if capped != turns || free != turns {
		t.Errorf("saw %d capped and %d uncapped requests, want %d each", capped, free, turns)
	}
}
