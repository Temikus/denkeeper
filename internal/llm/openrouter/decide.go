package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/llm"
)

var _ llm.DecisionProvider = (*Client)(nil)

// Decide implements llm.DecisionProvider via OpenRouter's System One
// endpoint, which serves non-generative decision models such as
// typesafe/jev-1.13. See https://openrouter.ai/docs/guides/community/typesafe-sdk
func (c *Client) Decide(ctx context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	ctx, span := tracer.Start(ctx, "llm.provider.decide", trace.WithAttributes(
		attribute.String("gen_ai.system", c.Name()),
		attribute.String("gen_ai.request.model", req.Model),
	))
	defer span.End()

	resp, err := c.decide(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.String("gen_ai.response.model", resp.Model))
	return resp, nil
}

func (c *Client) decide(ctx context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	body := decisionRequest{
		Model:     req.Model,
		State:     req.State,
		Questions: make(map[string]decisionQuestion, len(req.Questions)),
	}
	for id, q := range req.Questions {
		body.Questions[id] = encodeQuestion(q)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling decision request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/systemone", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating decision request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/Temikus/denkeeper")
	httpReq.Header.Set("X-Title", "Denkeeper")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending decision request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading decision response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: decisionErrorMessage(resp.StatusCode, respBody)}
	}

	var out decisionResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("parsing decision response: %w", err)
	}
	return out.toLLM()
}

// maxDecisionErrorLen caps the provider message kept on a failed decision.
const maxDecisionErrorLen = 200

// decisionErrorMessage keeps only OpenRouter's short error.message, capped,
// falling back to the status text. The error reaches spans and audit detail,
// and the raw body could echo the request state (tool arguments).
func decisionErrorMessage(status int, body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := ""
	if json.Unmarshal(body, &e) == nil {
		msg = strings.TrimSpace(e.Error.Message)
	}
	if msg == "" {
		return http.StatusText(status)
	}
	if len(msg) > maxDecisionErrorLen {
		msg = msg[:maxDecisionErrorLen] + "..."
	}
	return msg
}

// encodeQuestion maps a Question onto the wire shape, where "criteria" is an
// object for choice and noul but an ordered array for score.
func encodeQuestion(q llm.Question) decisionQuestion {
	out := decisionQuestion{Type: string(q.Type), Instructions: q.Instructions}
	switch q.Type {
	case llm.QuestionScore:
		if len(q.Levels) > 0 {
			out.Criteria = q.Levels
		}
	default:
		if len(q.Choices) > 0 {
			out.Criteria = q.Choices
		}
	}
	return out
}

type decisionRequest struct {
	Model     string                      `json:"model"`
	State     any                         `json:"state"`
	Questions map[string]decisionQuestion `json:"questions"`
}

type decisionQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type decisionResponse struct {
	ID      string                    `json:"id"`
	Model   string                    `json:"model"`
	Answers map[string]decisionAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Cost         float64 `json:"cost"`
	} `json:"usage"`
}

type decisionAnswer struct {
	Type          string          `json:"type"`
	Choice        string          `json:"choice"`
	Probabilities json.RawMessage `json:"probabilities"`
	Confidence    float64         `json:"confidence"`
	Noul          *float64        `json:"noul"`
	Score         float64         `json:"score"`
}

func (r *decisionResponse) toLLM() (*llm.DecisionResponse, error) {
	out := &llm.DecisionResponse{
		ID:    r.ID,
		Model: r.Model,
		Usage: llm.TokenUsage{
			Prompt:     r.Usage.InputTokens,
			Completion: r.Usage.OutputTokens,
			Total:      r.Usage.InputTokens + r.Usage.OutputTokens,
		},
		CostUSD: r.Usage.Cost,
		Answers: make(map[string]llm.Answer, len(r.Answers)),
	}
	for id, a := range r.Answers {
		ans := llm.Answer{
			Type:       llm.QuestionType(a.Type),
			Choice:     a.Choice,
			Confidence: a.Confidence,
			Score:      a.Score,
		}
		if ans.Type == llm.QuestionNoul {
			// A missing noul must not read as p=0, which would look like a
			// confident "false".
			if a.Noul == nil {
				return nil, fmt.Errorf("decision answer %q: noul probability missing", id)
			}
			ans.Noul = *a.Noul
		}
		probs, err := decodeProbabilities(a.Probabilities)
		if err != nil {
			return nil, fmt.Errorf("decision answer %q: %w", id, err)
		}
		ans.Probabilities = probs
		out.Answers[id] = ans
	}
	return out, nil
}

// decodeProbabilities accepts an object keyed by option or level, or an array
// indexed by level (keyed "0", "1", ...). The alpha API documents the former
// for choice but only "the probability of each level" for score.
func decodeProbabilities(raw json.RawMessage) (map[string]float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]float64
	if err := json.Unmarshal(raw, &m); err == nil {
		return m, nil
	}
	var arr []float64
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("probabilities: want object or array, got %s", raw)
	}
	m = make(map[string]float64, len(arr))
	for i, p := range arr {
		m[strconv.Itoa(i)] = p
	}
	return m, nil
}
