package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/llm"
)

func seedTrace(t *testing.T, store *eval.Store, tr agent.TurnTrace) {
	t.Helper()
	if err := store.SaveTrace(context.Background(), tr); err != nil {
		t.Fatalf("SaveTrace: %v", err)
	}
}

func liveTrace() agent.TurnTrace {
	return agent.TurnTrace{
		Agent:          "default",
		ConversationID: "chan:main",
		Source:         agent.TraceSourceLive,
		Model:          "claude-opus-5",
		Provider:       "anthropic",
		Rounds:         1,
		Tokens:         llm.TokenUsage{Prompt: 90, Completion: 10, Total: 100},
		CostUSD:        0.004,
		LatencyMs:      2500,
		CreatedAt:      time.Now().UTC(),
		Payload: agent.TracePayload{
			SystemPrompt: "you are the default agent",
			History:      []agent.TraceMessage{{Role: "user", Content: "earlier turn"}},
			Prompt:       "what did you do",
			Response:     "I looked it up",
			Rounds: []agent.TraceRound{{Round: 1, ToolCalls: []agent.TraceToolCall{
				{Tool: "web_fetch", Outcome: "ok", Arguments: `{"url":"https://example.test"}`, Result: "200 OK", DurationMs: 90},
				{Tool: "kv_set", Outcome: "suppressed"},
			}}},
		},
	}
}

func TestTraces_ListReturnsHeadersWithoutPayloads(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTrace(t, store, liveTrace())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out traceListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Traces) != 1 || out.Total != 1 {
		t.Fatalf("traces = %d, total = %d, want 1/1", len(out.Traces), out.Total)
	}
	row := out.Traces[0]
	if row.Agent != "default" || row.Model != "claude-opus-5" || row.TokensTotal != 100 {
		t.Errorf("header row = %+v", row)
	}
	// The list is a header view: the prompt must not ride along in it.
	if strings.Contains(rec.Body.String(), "you are the default agent") {
		t.Error("list response carried the system prompt; the payload belongs to the detail read only")
	}
}

// The empty state has to distinguish "nothing recorded yet" from "recording is
// off", so the switch travels with the list.
func TestTraces_ListReportsTheCaptureSwitch(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out traceListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Capture {
		t.Error("capture reported as on with the default config — live capture must be opt-in")
	}
	if len(out.Traces) != 0 {
		t.Errorf("traces = %+v, want none", out.Traces)
	}
}

func TestTraces_ListReportsCaptureOnWhenConfigured(t *testing.T) {
	srv, _ := evalTestServer(t)
	srv.deps.Config.Eval.Capture = true
	srv.deps.Config.Eval.RetentionDays = 14
	srv.deps.Config.Eval.MaxTraceBytes = 65536

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces", "", "dk-test-key")
	var out traceListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !out.Capture || out.RetentionDays != 14 || out.MaxTraceBytes != 65536 {
		t.Errorf("capture settings = %+v", out)
	}
}

func TestTraces_ListFiltersByAgentSourceAndConversation(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTrace(t, store, liveTrace())
	evalOne := liveTrace()
	evalOne.Agent = "pamela"
	evalOne.Source = agent.TraceSourceEval
	evalOne.ConversationID = "eval:1:2:0:3"
	seedTrace(t, store, evalOne)

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"?agent=default", 1},
		{"?agent=pamela", 1},
		{"?source=eval", 1},
		{"?source=live", 1},
		{"?conversation_id=chan:main", 1},
		{"?agent=nobody", 0},
	} {
		rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces"+tc.query, "", "dk-test-key")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.query, rec.Code)
		}
		var out traceListResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: decoding: %v", tc.query, err)
		}
		if len(out.Traces) != tc.want {
			t.Errorf("%s returned %d traces, want %d", tc.query, len(out.Traces), tc.want)
		}
	}
}

// The page's "load older turns" reads total: an unfiltered total beside a
// filtered page would keep offering a page that can never arrive.
func TestTraces_ListTotalCountsTheFilteredSet(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTrace(t, store, liveTrace())
	other := liveTrace()
	other.Agent = "pamela"
	other.Source = agent.TraceSourceEval
	seedTrace(t, store, other)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces?agent=pamela", "", "dk-test-key")
	var out traceListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Total != 1 || len(out.Traces) != 1 {
		t.Errorf("filtered listing returned %d rows with total %d, want 1/1", len(out.Traces), out.Total)
	}
}

// A caller paging on the echoed limit walks past rows when the echo is the
// value it asked for rather than the one the store applied.
func TestTraces_ListEchoesTheEffectiveLimit(t *testing.T) {
	srv, _ := evalTestServer(t)

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 50},
		{"?limit=5", 5},
		{"?limit=5000", 200},
	} {
		rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces"+tc.query, "", "dk-test-key")
		var out traceListResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: decoding: %v", tc.query, err)
		}
		if out.Limit != tc.want {
			t.Errorf("%s: limit echoed as %d, want %d", tc.query, out.Limit, tc.want)
		}
	}
}

func TestTraces_ListRejectsMalformedQuery(t *testing.T) {
	srv, _ := evalTestServer(t)
	for _, q := range []string{"?since=yesterday", "?until=soon", "?limit=0", "?limit=abc", "?offset=-1"} {
		rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces"+q, "", "dk-test-key")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestTraces_GetReturnsPromptHistoryAndToolPayloads(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTrace(t, store, liveTrace())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces/1", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out traceDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.SystemPrompt != "you are the default agent" {
		t.Errorf("system prompt = %q", out.SystemPrompt)
	}
	if len(out.History) != 1 || out.History[0].Content != "earlier turn" {
		t.Errorf("history = %+v", out.History)
	}
	if out.Prompt != "what did you do" || out.Response != "I looked it up" {
		t.Errorf("prompt/response = %q / %q", out.Prompt, out.Response)
	}
	if len(out.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want 2 flattened out of their round", out.ToolCalls)
	}
	if out.ToolCalls[0].Round != 1 || out.ToolCalls[0].Arguments == "" || out.ToolCalls[0].Result != "200 OK" {
		t.Errorf("first call = %+v", out.ToolCalls[0])
	}
	if !out.ToolCalls[1].Suppressed || out.SuppressedN != 1 {
		t.Errorf("suppressed call not marked: %+v (count %d)", out.ToolCalls[1], out.SuppressedN)
	}
	// The renderer reads duration_ms; latency_ms is the column name.
	if out.DurationMs != 2500 {
		t.Errorf("duration_ms = %d, want the trace's latency", out.DurationMs)
	}
}

func TestTraces_GetReportsWhatTruncationRemoved(t *testing.T) {
	srv, store := evalTestServer(t)
	tr := liveTrace()
	tr.Truncated = true
	tr.Payload.Truncation = &agent.TraceTruncation{DroppedRounds: 3, Note: "trace exceeded 4096 bytes: 3 oldest round(s) dropped"}
	seedTrace(t, store, tr)

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces/1", "", "dk-test-key")
	var out traceDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !out.Truncated {
		t.Error("truncated flag lost")
	}
	if out.Truncation == nil || out.Truncation.DroppedRounds != 3 {
		t.Fatalf("truncation detail = %+v", out.Truncation)
	}
}

func TestTraces_GetUnknownIsNotFound(t *testing.T) {
	srv, _ := evalTestServer(t)
	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces/42", "", "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestTraces_GetRejectsBadID(t *testing.T) {
	srv, _ := evalTestServer(t)
	for _, id := range []string{"abc", "0", "-3"} {
		rec := evalRequest(t, srv, http.MethodGet, "/api/v1/traces/"+id, "", "dk-test-key")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: status = %d, want 400", id, rec.Code)
		}
	}
}

// The eval scopes belong to a judge, which must never be able to read live
// prompts; traces are turn content and follow sessions:read.
func TestTraces_EvalReadScopeIsNotEnough(t *testing.T) {
	srv, store := evalTestServer(t, evalReadOnlyKey())
	seedTrace(t, store, liveTrace())

	for _, path := range []string{"/api/v1/traces", "/api/v1/traces/1"} {
		rec := evalRequest(t, srv, http.MethodGet, path, "", "dk-eval-readonly")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with eval:read only: status = %d, want 403", path, rec.Code)
		}
	}
}

func TestTraces_UnconfiguredStoreIs503(t *testing.T) {
	deps := testDeps()
	deps.Config.Eval = config.EvalConfig{}
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	for _, path := range []string{"/api/v1/traces", "/api/v1/traces/1"} {
		rec := evalRequest(t, srv, http.MethodGet, path, "", "dk-test-key")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, rec.Code)
		}
	}
}
