package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
)

func TestReplaySupervisorDecider_RendersLiveStateAndQuestions(t *testing.T) {
	prov := &fakeDecisionProvider{p: 0.9, cost: 0.0002}
	d := llm.NewDecider(llm.DeciderConfig{Name: "jev", Model: "typesafe/jev-1.13", MaxInputTokens: 30000}, prov, nil)

	resp, err := ReplaySupervisorDecider(context.Background(), d, "decide-replay:default", DeciderReplayInput{
		Agent:     "default",
		Tool:      "send_email",
		Arguments: `{"to":"a@example.com"}`,
		Recent: []StoredMessage{
			{Role: "user", Content: "email Alice the notes"},
			{Role: "assistant", Content: "On it."},
		},
	})
	if err != nil {
		t.Fatalf("ReplaySupervisorDecider: %v", err)
	}
	if resp.CostUSD != 0.0002 || len(resp.Answers) != 3 {
		t.Errorf("resp cost/answers = %v/%d, want 0.0002/3", resp.CostUSD, len(resp.Answers))
	}

	b, err := json.Marshal(prov.last.State)
	if err != nil {
		t.Fatalf("marshaling state: %v", err)
	}
	var state struct {
		Agent string `json:"agent"`
		Tool  struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		} `json:"tool"`
		UserRequest    string           `json:"user_request"`
		RecentMessages []deciderMessage `json:"recent_messages"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatalf("state is not the live shape: %v (%s)", err, b)
	}
	if state.Agent != "default" || state.Tool.Name != "send_email" || state.Tool.Arguments["to"] != "a@example.com" {
		t.Errorf("state tool = %+v", state)
	}
	if state.UserRequest != "email Alice the notes" || len(state.RecentMessages) != 2 {
		t.Errorf("user_request = %q, recent = %d", state.UserRequest, len(state.RecentMessages))
	}

	var ids []string
	for id := range prov.last.Questions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	want := slices.Sorted(slices.Values(deciderQuestionOrder))
	if !slices.Equal(ids, want) {
		t.Errorf("question ids = %v, want the live stage's %v", ids, want)
	}
}

func TestReplaySupervisorDecider_ErrorCauseMatchesAudit(t *testing.T) {
	d := llm.NewDecider(llm.DeciderConfig{Name: "jev", Model: "m", MaxInputTokens: 5}, &fakeDecisionProvider{p: 0.9}, nil)
	_, err := ReplaySupervisorDecider(context.Background(), d, "s", DeciderReplayInput{Tool: "t", Arguments: `{"a":"long enough to exceed five tokens"}`})
	if !errors.Is(err, llm.ErrDecisionTooLarge) {
		t.Fatalf("err = %v, want ErrDecisionTooLarge", err)
	}
	if got := SupervisorErrorCause(err); got != "too_large" {
		t.Errorf("cause = %q, want too_large", got)
	}
}

func TestSupervisorDeciderVerdict_ThresholdsAreInclusive(t *testing.T) {
	answers := func(p float64) map[string]llm.Answer {
		return map[string]llm.Answer{
			deciderQAligned:  {Noul: 0.99},
			deciderQSafeArgs: {Noul: p},
			deciderQScoped:   {Noul: 0.99},
		}
	}
	if v, _ := SupervisorDeciderVerdict(answers(0.95), 0.95, 0.05); v != "APPROVE" {
		t.Errorf("min p at approve_at = %s, want APPROVE", v)
	}
	if v, _ := SupervisorDeciderVerdict(answers(0.05), 0.95, 0.05); v != "DENY" {
		t.Errorf("min p at deny_at = %s, want DENY", v)
	}
	if v, _ := SupervisorDeciderVerdict(answers(0.5), 0.95, 0.05); v != "ESCALATE" {
		t.Errorf("min p between thresholds = %s, want ESCALATE", v)
	}
}

func TestMemoryStore_GetMessagesBefore(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	convID, err := store.GetOrCreateConversation(ctx, "telegram", "1")
	if err != nil {
		t.Fatal(err)
	}

	// Stamp each message explicitly: CURRENT_TIMESTAMP would put all four in
	// the same second.
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for i, content := range []string{"first", "second", "third", "later"} {
		id, err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: content})
		if err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute).Format(messageTimeLayout)
		if _, err := store.db.ExecContext(ctx, `UPDATE messages SET created_at = ? WHERE id = ?`, at, id); err != nil {
			t.Fatal(err)
		}
	}

	// A non-UTC cutoff: the comparison must be made in UTC.
	cutoff := base.Add(2 * time.Minute).In(time.FixedZone("AEST", 10*3600))
	got, err := store.GetMessagesBefore(ctx, convID, cutoff, 2)
	if err != nil {
		t.Fatalf("GetMessagesBefore: %v", err)
	}
	if len(got) != 2 || got[0].Content != "second" || got[1].Content != "third" {
		t.Errorf("messages = %+v, want [second third]", got)
	}
}
