package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/eval"
)

// noEvalKey carries chat but neither eval scope.
func noEvalKey() config.APIKeyConfig {
	return config.APIKeyConfig{Name: "chat-only", Key: "dk-chat-only", Scopes: []string{"chat"}}
}

// seedSuggestTurn writes a real user turn, its assistant reply, and the reply's
// telemetry into the server's memory store, returning the user message id.
func seedSuggestTurn(t *testing.T, srv *Server, convID, agentName, prompt string, replyCost float64, calls []agent.ToolCallRecord, skills []agent.SkillUsageRecord) int64 {
	t.Helper()
	ctx := context.Background()
	mem := srv.deps.Memory
	tel, ok := mem.(agent.TelemetryStore)
	if !ok {
		t.Fatalf("memory store is not a TelemetryStore")
	}
	if err := mem.GetOrCreateConversationByID(ctx, convID, "test", convID); err != nil {
		t.Fatalf("creating conversation: %v", err)
	}
	userID, err := mem.AddMessage(ctx, convID, agent.StoredMessage{Role: "user", Content: prompt})
	if err != nil {
		t.Fatalf("adding user message: %v", err)
	}
	reply := agent.StoredMessage{Role: "assistant", Content: "ack", Cost: replyCost}
	replyID, err := mem.AddMessage(ctx, convID, reply)
	if err != nil {
		t.Fatalf("adding assistant message: %v", err)
	}
	if len(calls) > 0 {
		if err := tel.AddToolCalls(ctx, convID, replyID, calls); err != nil {
			t.Fatalf("adding tool calls: %v", err)
		}
	}
	if len(skills) > 0 {
		if err := tel.AddSkillUsages(ctx, convID, userID, skills); err != nil {
			t.Fatalf("adding skill usages: %v", err)
		}
	}
	if err := tel.UpdateConversationStats(ctx, convID, agentName, reply, len(calls), 0); err != nil {
		t.Fatalf("updating conversation stats: %v", err)
	}
	return userID
}

// seedOneTurnPerCategory writes four candidate turns — one per category — plus
// an unremarkable one that carries no signal at all. Returns the message ids
// keyed by the category each turn should land in.
func seedOneTurnPerCategory(t *testing.T, srv *Server, convID, agentName string) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	ids[eval.CategorySkillCommand] = seedSuggestTurn(t, srv, convID, agentName, "/report weekly", 0, nil,
		[]agent.SkillUsageRecord{{SkillName: "report", MatchType: "command"}})
	ids[eval.CategoryScheduled] = seedSuggestTurn(t, srv, convID, agentName,
		"[Scheduled: heartbeat | 2026-08-01T10:00:00Z UTC | 2026-W31]", 0,
		[]agent.ToolCallRecord{{ToolName: "kv_get", Round: 1, Outcome: "failed"}}, nil)
	ids[eval.CategoryToolHeavy] = seedSuggestTurn(t, srv, convID, agentName, "dig through the logs", 0,
		[]agent.ToolCallRecord{
			{ToolName: "web_fetch", Round: 1, Success: true, Outcome: "ok"},
			{ToolName: "web_fetch", Round: 2, Success: true, Outcome: "ok"},
			{ToolName: "kv_get", Round: 3, Success: true, Outcome: "ok"},
			{ToolName: "kv_set", Round: 4, Success: true, Outcome: "ok"},
		}, nil)
	// The priciest reply in the pool, so this one earns the cost signal alone.
	ids[eval.CategoryChat] = seedSuggestTurn(t, srv, convID, agentName, "write me a limerick", 5.0, nil, nil)
	// Unremarkable: no tools, no skills, no cost.
	seedSuggestTurn(t, srv, convID, agentName, "thanks", 0, nil, nil)
	return ids
}

func decodeSuggestions(t *testing.T, body []byte) evalSuggestResult {
	t.Helper()
	var got evalSuggestResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding response: %v (%s)", err, body)
	}
	return got
}

func TestEvalSuggest_ReturnsOnePerCategoryAndSkipsTheBoringTurn(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())
	ids := seedOneTurnPerCategory(t, srv, "chan:main", "default")

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeSuggestions(t, rec.Body.Bytes())
	if len(got.Candidates) != 4 {
		t.Fatalf("candidates = %d, want 4 (one per category, the boring turn excluded): %+v",
			len(got.Candidates), got.Candidates)
	}
	seen := map[string]int64{}
	for _, c := range got.Candidates {
		if _, dup := seen[c.Category]; dup {
			t.Errorf("category %q appeared twice", c.Category)
		}
		seen[c.Category] = c.MessageID
		if len(c.Signals) == 0 {
			t.Errorf("candidate %d has no signals; it should not have been offered", c.MessageID)
		}
		if c.ConversationID != "chan:main" {
			t.Errorf("ConversationID = %q, want %q", c.ConversationID, "chan:main")
		}
		if c.Prompt == "" || c.CreatedAt.IsZero() {
			t.Errorf("candidate %d is missing prompt or created_at: %+v", c.MessageID, c)
		}
	}
	for cat, wantID := range ids {
		if seen[cat] != wantID {
			t.Errorf("category %q resolved to message %d, want %d", cat, seen[cat], wantID)
		}
	}
}

func TestEvalSuggest_ExcludesTurnsAlreadySavedAsTasks(t *testing.T) {
	srv, store := evalTestServer(t, allScopesKey())
	ids := seedOneTurnPerCategory(t, srv, "chan:main", "default")

	set, err := store.CreateTaskSet(context.Background(), "golden", "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	saved := ids[eval.CategoryToolHeavy]
	if _, err := store.AddTask(context.Background(), set.ID, eval.Task{
		Prompt:               "dig through the logs",
		Category:             eval.CategoryToolHeavy,
		SourceConversationID: "chan:main",
		SourceMessageID:      &saved,
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeSuggestions(t, rec.Body.Bytes())
	if len(got.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3 — an accepted suggestion must not resurface: %+v",
			len(got.Candidates), got.Candidates)
	}
	for _, c := range got.Candidates {
		if c.MessageID == saved {
			t.Errorf("message %d is already saved as a task but was suggested again", saved)
		}
	}
}

func TestEvalSuggest_ScopesToAgent(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())
	seedOneTurnPerCategory(t, srv, "chan:main", "default")
	seedSuggestTurn(t, srv, "chan:other", "argus", "not mine", 0, nil,
		[]agent.SkillUsageRecord{{SkillName: "report", MatchType: "command"}})

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest?agent=default", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeSuggestions(t, rec.Body.Bytes())
	if len(got.Candidates) != 4 {
		t.Fatalf("candidates = %d, want 4: %+v", len(got.Candidates), got.Candidates)
	}
	for _, c := range got.Candidates {
		if c.ConversationID == "chan:other" {
			t.Errorf("another agent's turn leaked into the suggestions: %+v", c)
		}
	}
}

func TestEvalSuggest_DefaultLimitCapsTheResponse(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())
	for i := 0; i < suggestDefaultLimit+8; i++ {
		seedSuggestTurn(t, srv, "chan:main", "default", "/report", 0, nil,
			[]agent.SkillUsageRecord{{SkillName: "report", MatchType: "command"}})
	}

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeSuggestions(t, rec.Body.Bytes())
	if len(got.Candidates) != suggestDefaultLimit {
		t.Fatalf("candidates = %d, want the default limit of %d", len(got.Candidates), suggestDefaultLimit)
	}

	rec = evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest?limit=3", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := decodeSuggestions(t, rec.Body.Bytes()); len(got.Candidates) != 3 {
		t.Errorf("candidates = %d, want 3", len(got.Candidates))
	}
}

func TestEvalSuggest_RejectsMalformedParams(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest?limit=0", "", "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("limit=0 status = %d, want 400", rec.Code)
	}
	rec = evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest?since=yesterday", "", "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("since=yesterday status = %d, want 400", rec.Code)
	}
}

func TestEvalSuggest_NeedsEvalReadScope(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey(), evalReadOnlyKey(), noEvalKey())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest", "", "dk-chat-only")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 without eval:read", rec.Code)
	}
	rec = evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest", "", "dk-eval-readonly")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with eval:read — suggesting writes nothing", rec.Code)
	}
}

func TestEvalSuggest_UnwiredStoreReturns503(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDeps(), testLogger())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/suggest", "", "dk-test-key")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the eval subsystem is not wired", rec.Code)
	}
}
