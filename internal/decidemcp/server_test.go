package decidemcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeProvider answers every noul with p and every choice with its first
// option, or fails with err.
type fakeProvider struct {
	p     float64
	cost  float64
	err   error
	delay time.Duration
	calls int
	last  llm.DecisionRequest
}

func (f *fakeProvider) Decide(ctx context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	f.calls++
	f.last = req
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	answers := make(map[string]llm.Answer, len(req.Questions))
	for id, q := range req.Questions {
		switch q.Type {
		case llm.QuestionChoice:
			probs := make(map[string]float64, len(q.Choices))
			var first string
			for opt := range q.Choices {
				probs[opt] = 0
				if first == "" || opt < first {
					first = opt
				}
			}
			probs[first] = 1
			answers[id] = llm.Answer{Type: q.Type, Choice: first, Probabilities: probs, Confidence: 1}
		default:
			answers[id] = llm.Answer{Type: q.Type, Noul: f.p}
		}
	}
	return &llm.DecisionResponse{Model: req.Model, Answers: answers, CostUSD: f.cost, Usage: llm.TokenUsage{Prompt: 10}}, nil
}

func newDecider(p *fakeProvider, costs *llm.CostTracker, cfg llm.DeciderConfig) *llm.Decider {
	if cfg.Name == "" {
		cfg.Name = "jev"
	}
	if cfg.Provider == "" {
		cfg.Provider = "or"
	}
	if cfg.Model == "" {
		cfg.Model = "typesafe/jev-1.13"
	}
	return llm.NewDecider(cfg, p, costs)
}

func newTestServer(t *testing.T, deps Deps) *mcp.ClientSession {
	t.Helper()
	srv := New(deps)
	session, err := srv.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callDecide(t *testing.T, session *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: ToolName, Arguments: args})
	if err != nil {
		t.Fatalf("call decide: %v", err)
	}
	return result
}

func extractText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func noulQuestion(instructions string) map[string]any {
	return map[string]any{"type": "noul", "instructions": instructions}
}

func TestDecide_ReturnsAnswersAndPassesStateVerbatim(t *testing.T) {
	p := &fakeProvider{p: 0.9, cost: 0.0001}
	session := newTestServer(t, Deps{Decider: newDecider(p, nil, llm.DeciderConfig{}), AgentName: "default"})

	state := map[string]any{"message": map[string]any{"subject": "Invoice overdue", "from": "billing@example.com"}}
	result := callDecide(t, session, map[string]any{
		"state": state,
		"questions": map[string]any{
			"urgent": noulQuestion("`message` needs a reply today"),
			"kind": map[string]any{
				"type":         "choice",
				"instructions": "What kind of message is `message`?",
				"choices":      map[string]string{"bill": "an invoice or payment request", "spam": "unsolicited marketing"},
			},
		},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}

	var out decideResult
	if err := json.Unmarshal([]byte(extractText(result)), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, extractText(result))
	}
	if out.Model != "typesafe/jev-1.13" || out.CostUSD != 0.0001 {
		t.Errorf("model/cost = %q/%v, want typesafe/jev-1.13/0.0001", out.Model, out.CostUSD)
	}
	if got := out.Answers["urgent"].Noul; got != 0.9 {
		t.Errorf("urgent.noul = %v, want 0.9", got)
	}
	if got := out.Answers["kind"]; got.Choice != "bill" || got.Probabilities["bill"] != 1 {
		t.Errorf("kind = %+v, want choice bill with p=1", got)
	}

	// The state reaches the provider as the agent sent it, not re-rendered.
	sent, err := json.Marshal(p.last.State)
	if err != nil {
		t.Fatalf("marshal sent state: %v", err)
	}
	want, _ := json.Marshal(state)
	if string(sent) != string(want) {
		t.Errorf("state sent = %s, want %s", sent, want)
	}
	if p.last.Questions["kind"].Choices["spam"] != "unsolicited marketing" {
		t.Errorf("choices not forwarded: %+v", p.last.Questions["kind"])
	}
}

func TestDecide_StringStateAccepted(t *testing.T) {
	p := &fakeProvider{p: 0.5}
	session := newTestServer(t, Deps{Decider: newDecider(p, nil, llm.DeciderConfig{}), AgentName: "default"})

	result := callDecide(t, session, map[string]any{
		"state":     "Please reset my password",
		"questions": map[string]any{"support": noulQuestion("The text is a support request")},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
	sent, _ := json.Marshal(p.last.State)
	if string(sent) != `"Please reset my password"` {
		t.Errorf("state sent = %s, want the quoted string", sent)
	}
}

func TestDecide_InvalidQuestionIsToolErrorWithoutCall(t *testing.T) {
	p := &fakeProvider{p: 0.5}
	session := newTestServer(t, Deps{Decider: newDecider(p, nil, llm.DeciderConfig{}), AgentName: "default"})

	result := callDecide(t, session, map[string]any{
		"state": "x",
		"questions": map[string]any{
			"kind": map[string]any{"type": "choice", "instructions": "Which?", "choices": map[string]string{"only": "one"}},
		},
	})
	if !result.IsError {
		t.Fatal("expected a tool error for a one-option choice")
	}
	if got := extractText(result); !strings.Contains(got, "choice needs 2-255 options") {
		t.Errorf("error = %q, want the shape message", got)
	}
	if p.calls != 0 {
		t.Errorf("provider called %d times, want 0", p.calls)
	}
}

func TestDecide_MissingStateIsToolError(t *testing.T) {
	p := &fakeProvider{p: 0.5}
	session := newTestServer(t, Deps{Decider: newDecider(p, nil, llm.DeciderConfig{}), AgentName: "default"})

	result := callDecide(t, session, map[string]any{
		"questions": map[string]any{"q": noulQuestion("anything")},
	})
	if !result.IsError || !strings.Contains(extractText(result), "state is required") {
		t.Fatalf("result = %v %q, want state-is-required error", result.IsError, extractText(result))
	}
}

func TestDecide_TooLargeIsRefusedWithoutCall(t *testing.T) {
	p := &fakeProvider{p: 0.5}
	session := newTestServer(t, Deps{Decider: newDecider(p, nil, llm.DeciderConfig{MaxInputTokens: 20}), AgentName: "default"})

	result := callDecide(t, session, map[string]any{
		"state":     strings.Repeat("payload ", 100),
		"questions": map[string]any{"q": noulQuestion("The text is long")},
	})
	if !result.IsError || !strings.Contains(extractText(result), "size cap") {
		t.Fatalf("result = %v %q, want size-cap refusal", result.IsError, extractText(result))
	}
	if p.calls != 0 {
		t.Errorf("provider called %d times, want 0", p.calls)
	}
}

func TestDecide_BillsTheAgentUnderADailyKey(t *testing.T) {
	p := &fakeProvider{p: 0.5, cost: 0.002}
	costs := llm.NewCostTracker(llm.SessionLimits{}, nil)
	now := time.Date(2026, 10, 3, 23, 59, 0, 0, time.UTC)
	session := newTestServer(t, Deps{
		Decider:   newDecider(p, costs, llm.DeciderConfig{}),
		AgentName: "default",
		Now:       func() time.Time { return now },
	})

	callDecide(t, session, map[string]any{"state": "x", "questions": map[string]any{"q": noulQuestion("anything")}})
	now = now.Add(2 * time.Minute) // next UTC day
	callDecide(t, session, map[string]any{"state": "x", "questions": map[string]any{"q": noulQuestion("anything")}})

	if got := costs.SessionCost("decide:jev:default:2026-10-03"); got != 0.002 {
		t.Errorf("day-1 cost = %v, want 0.002", got)
	}
	if got := costs.SessionCost("decide:jev:default:2026-10-04"); got != 0.002 {
		t.Errorf("day-2 cost = %v, want 0.002", got)
	}
	var agentCost float64
	for _, a := range costs.AgentCosts() {
		if a.Agent == "default" {
			agentCost = a.Cost
		}
	}
	if agentCost != 0.004 {
		t.Errorf("agent cost = %v, want 0.004 (both days billed to the agent)", agentCost)
	}
}

func TestDecide_CostLimitRefusesWithoutCall(t *testing.T) {
	p := &fakeProvider{p: 0.5, cost: 0.002}
	costs := llm.NewCostTracker(llm.SessionLimits{Hard: 0.001}, nil)
	session := newTestServer(t, Deps{Decider: newDecider(p, costs, llm.DeciderConfig{}), AgentName: "default"})

	args := map[string]any{"state": "x", "questions": map[string]any{"q": noulQuestion("anything")}}
	if r := callDecide(t, session, args); r.IsError {
		t.Fatalf("first call failed: %s", extractText(r))
	}
	r := callDecide(t, session, args)
	if !r.IsError || !strings.Contains(extractText(r), "cost limit") {
		t.Fatalf("second call = %v %q, want cost-limit refusal", r.IsError, extractText(r))
	}
	if p.calls != 1 {
		t.Errorf("provider called %d times, want 1", p.calls)
	}
}

func TestDecide_TimeoutIsToolError(t *testing.T) {
	p := &fakeProvider{p: 0.5, delay: time.Second}
	session := newTestServer(t, Deps{Decider: newDecider(p, nil, llm.DeciderConfig{Timeout: 10 * time.Millisecond}), AgentName: "default"})

	r := callDecide(t, session, map[string]any{"state": "x", "questions": map[string]any{"q": noulQuestion("anything")}})
	if !r.IsError || !strings.Contains(extractText(r), "timed out") {
		t.Fatalf("result = %v %q, want timeout error", r.IsError, extractText(r))
	}
}

func TestDecide_ProviderErrorIsToolError(t *testing.T) {
	p := &fakeProvider{err: errors.New("upstream down")}
	session := newTestServer(t, Deps{Decider: newDecider(p, nil, llm.DeciderConfig{}), AgentName: "default"})

	r := callDecide(t, session, map[string]any{"state": "x", "questions": map[string]any{"q": noulQuestion("anything")}})
	if !r.IsError || !strings.Contains(extractText(r), "upstream down") {
		t.Fatalf("result = %v %q, want provider error", r.IsError, extractText(r))
	}
}

func TestDecide_NoDeciderRegistersNoTool(t *testing.T) {
	session := newTestServer(t, Deps{AgentName: "default"})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 0 {
		t.Errorf("tools = %d, want 0 without a decider", len(tools.Tools))
	}
}

func TestDecide_DescriptionNamesTheModel(t *testing.T) {
	session := newTestServer(t, Deps{Decider: newDecider(&fakeProvider{}, nil, llm.DeciderConfig{}), AgentName: "default"})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != ToolName {
		t.Fatalf("tools = %+v, want one decide tool", tools.Tools)
	}
	if !strings.Contains(tools.Tools[0].Description, "typesafe/jev-1.13") {
		t.Errorf("description does not name the model: %q", tools.Tools[0].Description)
	}
}
