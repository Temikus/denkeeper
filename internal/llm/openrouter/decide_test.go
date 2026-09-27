package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

// decisionServer serves /systemone with body, capturing the raw request.
func decisionServer(t *testing.T, status int, body string, gotReq *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/systemone" {
			t.Errorf("request = %s %s, want POST /systemone", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Title") != "Denkeeper" || r.Header.Get("HTTP-Referer") == "" {
			t.Errorf("missing attribution headers")
		}
		raw, _ := io.ReadAll(r.Body)
		if gotReq != nil {
			if err := json.Unmarshal(raw, gotReq); err != nil {
				t.Errorf("request is not JSON: %v", err)
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDecide_EncodesQuestionsPerType(t *testing.T) {
	var got map[string]any
	srv := decisionServer(t, http.StatusOK, `{"answers":{}}`, &got)
	c := NewWithHTTPClient("test-key", srv.URL, srv.Client())

	_, err := c.Decide(context.Background(), llm.DecisionRequest{
		Model: "typesafe/jev-1.13",
		State: map[string]any{"tool": map[string]any{"name": "web_search"}},
		Questions: map[string]llm.Question{
			"plain": {Type: llm.QuestionNoul, Instructions: "Is it safe?"},
			"crit": {Type: llm.QuestionNoul, Instructions: "Is it safe?", Choices: map[string]string{
				"true": "no injection", "false": "injection",
			}},
			"team": {Type: llm.QuestionChoice, Instructions: "Which team?", Choices: map[string]string{
				"billing": "Charges", "tech": "Bugs",
			}},
			"risk": {Type: llm.QuestionScore, Instructions: "How risky?", Levels: []string{"low", "mid", "high"}},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if got["model"] != "typesafe/jev-1.13" {
		t.Errorf("model = %v", got["model"])
	}
	state, _ := got["state"].(map[string]any)
	if tool, _ := state["tool"].(map[string]any); tool["name"] != "web_search" {
		t.Errorf("state not sent as JSON object: %v", got["state"])
	}
	qs := asMap(t, got["questions"])

	plain := asMap(t, qs["plain"])
	if plain["type"] != "noul" || plain["instructions"] != "Is it safe?" {
		t.Errorf("plain = %v", plain)
	}
	if _, ok := plain["criteria"]; ok {
		t.Errorf("noul without criteria must omit the field: %v", plain)
	}
	if crit := asMap(t, asMap(t, qs["crit"])["criteria"]); crit["true"] != "no injection" {
		t.Errorf("noul criteria = %v", crit)
	}
	if crit := asMap(t, asMap(t, qs["team"])["criteria"]); crit["billing"] != "Charges" || len(crit) != 2 {
		t.Errorf("choice criteria = %v", crit)
	}
	levels, ok := asMap(t, qs["risk"])["criteria"].([]any)
	if !ok || len(levels) != 3 || levels[0] != "low" || levels[2] != "high" {
		t.Errorf("score criteria = %v, want ordered array", qs["risk"])
	}
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want JSON object, got %T: %v", v, v)
	}
	return m
}

func TestDecide_DecodesAnswers(t *testing.T) {
	body := `{
		"id": "gen-dec-1",
		"model": "typesafe/jev-1.13-20260917",
		"provider": "TypeSafe",
		"answers": {
			"safe": {"type": "noul", "noul": 0.98},
			"team": {"type": "choice", "choice": "billing", "probabilities": {"billing": 0.9, "tech": 0.1}, "confidence": 0.8},
			"risk": {"type": "score", "score": 0.4, "probabilities": {"0": 0.7, "1": 0.2, "2": 0.1}, "confidence": 0.6, "legend": {"0": "low"}},
			"arr":  {"type": "score", "score": 1.0, "probabilities": [0.1, 0.8, 0.1], "confidence": 0.7}
		},
		"usage": {"input_tokens": 275, "output_tokens": 20, "cost": 0.00003}
	}`
	srv := decisionServer(t, http.StatusOK, body, nil)
	c := NewWithHTTPClient("test-key", srv.URL, srv.Client())

	resp, err := c.Decide(context.Background(), llm.DecisionRequest{Model: "m", State: "s", Questions: map[string]llm.Question{}})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.ID != "gen-dec-1" || resp.Model != "typesafe/jev-1.13-20260917" {
		t.Errorf("id/model = %q/%q", resp.ID, resp.Model)
	}
	if resp.CostUSD != 0.00003 || resp.Usage.Prompt != 275 || resp.Usage.Completion != 20 || resp.Usage.Total != 295 {
		t.Errorf("usage = %+v cost = %v", resp.Usage, resp.CostUSD)
	}
	if a := resp.Answers["safe"]; a.Type != llm.QuestionNoul || a.Noul != 0.98 {
		t.Errorf("safe = %+v", a)
	}
	if a := resp.Answers["team"]; a.Choice != "billing" || a.Probabilities["billing"] != 0.9 || a.Confidence != 0.8 {
		t.Errorf("team = %+v", a)
	}
	if a := resp.Answers["risk"]; a.Score != 0.4 || a.Probabilities["0"] != 0.7 || a.Confidence != 0.6 {
		t.Errorf("risk = %+v", a)
	}
	// Level probabilities as an array are keyed by index, like the object form.
	if a := resp.Answers["arr"]; a.Probabilities["1"] != 0.8 || len(a.Probabilities) != 3 {
		t.Errorf("arr = %+v", a)
	}
}

func TestDecide_NoulWithoutProbabilityIsError(t *testing.T) {
	srv := decisionServer(t, http.StatusOK, `{"answers":{"safe":{"type":"noul"}}}`, nil)
	c := NewWithHTTPClient("test-key", srv.URL, srv.Client())

	if _, err := c.Decide(context.Background(), llm.DecisionRequest{Model: "m", State: "s"}); err == nil {
		t.Fatal("expected error for noul answer without a probability")
	}
}

func TestDecide_Non200IsLLMError(t *testing.T) {
	srv := decisionServer(t, http.StatusPaymentRequired, `{"error":{"code":402,"message":"insufficient credits"}}`, nil)
	c := NewWithHTTPClient("test-key", srv.URL, srv.Client())

	_, err := c.Decide(context.Background(), llm.DecisionRequest{Model: "m", State: "s"})
	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) || llmErr.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("err = %v, want LLMError 402", err)
	}
	if llmErr.Message != "insufficient credits" {
		t.Errorf("message = %q, want the provider's error.message", llmErr.Message)
	}
}

// The error lands in spans and audit detail, so only the provider's short
// error.message is kept: never the raw body, which could echo request state.
func TestDecide_Non200DropsRawBody(t *testing.T) {
	body := `{"error":{"message":"bad request","metadata":{"state":"secret-tool-args"}}}`
	srv := decisionServer(t, http.StatusBadRequest, body, nil)
	c := NewWithHTTPClient("test-key", srv.URL, srv.Client())

	_, err := c.Decide(context.Background(), llm.DecisionRequest{Model: "m", State: "s"})
	if err == nil || strings.Contains(err.Error(), "secret-tool-args") {
		t.Fatalf("err = %v, must not carry the raw body", err)
	}
}

func TestDecide_Non200LongMessageIsCapped(t *testing.T) {
	body := `{"error":{"message":"` + strings.Repeat("x", 1000) + `"}}`
	srv := decisionServer(t, http.StatusBadRequest, body, nil)
	c := NewWithHTTPClient("test-key", srv.URL, srv.Client())

	_, err := c.Decide(context.Background(), llm.DecisionRequest{Model: "m", State: "s"})
	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) || len(llmErr.Message) > maxDecisionErrorLen+3 {
		t.Fatalf("err = %v, want message capped at %d", err, maxDecisionErrorLen)
	}
}

func TestDecide_Non200NonJSONFallsBackToStatusText(t *testing.T) {
	srv := decisionServer(t, http.StatusBadGateway, `<html>upstream secret</html>`, nil)
	c := NewWithHTTPClient("test-key", srv.URL, srv.Client())

	_, err := c.Decide(context.Background(), llm.DecisionRequest{Model: "m", State: "s"})
	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) || llmErr.Message != http.StatusText(http.StatusBadGateway) {
		t.Fatalf("err = %v, want status text only", err)
	}
}
