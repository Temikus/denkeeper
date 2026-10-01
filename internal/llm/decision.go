package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// QuestionType is the answer shape a decision model is asked for.
type QuestionType string

const (
	QuestionChoice QuestionType = "choice" // one of 2-255 named options
	QuestionNoul   QuestionType = "noul"   // probability the statement is true
	QuestionScore  QuestionType = "score"  // 2-10 ordered levels
)

const (
	maxChoiceOptions = 255
	minScoreLevels   = 2
	maxScoreLevels   = 10
)

// Question is one typed question put to a decision model.
type Question struct {
	Type         QuestionType
	Instructions string
	Choices      map[string]string // choice options; for noul, optional "true"/"false" descriptions
	Levels       []string          // score levels, worst -> best
}

// Answer is a decision model's answer to one Question. Which fields are set
// depends on Type: Choice/Probabilities/Confidence for choice, Noul for noul,
// Score/Probabilities/Confidence for score (probabilities keyed by level index).
type Answer struct {
	Type          QuestionType       `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Score         float64            `json:"score,omitempty"`
}

// DecisionRequest asks a decision model a set of questions about State, which
// may be a string or any JSON-marshalable value.
type DecisionRequest struct {
	Model     string
	State     any
	Questions map[string]Question
}

// DecisionResponse carries the answers keyed by question id.
type DecisionResponse struct {
	ID, Model string
	Answers   map[string]Answer
	Usage     TokenUsage
	CostUSD   float64
}

// DecisionProvider is implemented by provider clients that expose a decisions
// API. Deliberately not part of Provider: a decider cannot chat, so it must
// never be selectable as an agent model, and it has no tool channel.
type DecisionProvider interface {
	Decide(ctx context.Context, req DecisionRequest) (*DecisionResponse, error)
}

// ErrDecisionTooLarge is returned when the estimated input exceeds the
// decider's max_input_tokens. The input is never truncated: a truncated view
// can hide the payload that matters.
var ErrDecisionTooLarge = errors.New("decision input exceeds max_input_tokens")

// ValidateQuestions checks question shapes against the decisions API limits so
// a malformed question fails before any call is made.
func ValidateQuestions(qs map[string]Question) error {
	if len(qs) == 0 {
		return errors.New("at least one question is required")
	}
	for _, id := range sortedQuestionIDs(qs) {
		if err := validateQuestion(id, qs[id]); err != nil {
			return err
		}
	}
	return nil
}

func validateQuestion(id string, q Question) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("question id must not be empty")
	}
	if strings.TrimSpace(q.Instructions) == "" {
		return fmt.Errorf("question %q: instructions must not be empty", id)
	}
	return validateQuestionShape(id, q)
}

// validateQuestionShape checks the criteria fields against the question type.
func validateQuestionShape(id string, q Question) error {
	switch q.Type {
	case QuestionChoice:
		if len(q.Levels) > 0 {
			return fmt.Errorf("question %q: choice questions take choices, not levels", id)
		}
		if n := len(q.Choices); n < 2 || n > maxChoiceOptions {
			return fmt.Errorf("question %q: choice needs 2-%d options, got %d", id, maxChoiceOptions, n)
		}
	case QuestionScore:
		if len(q.Choices) > 0 {
			return fmt.Errorf("question %q: score questions take levels, not choices", id)
		}
		if n := len(q.Levels); n < minScoreLevels || n > maxScoreLevels {
			return fmt.Errorf("question %q: score needs %d-%d levels, got %d", id, minScoreLevels, maxScoreLevels, n)
		}
	case QuestionNoul:
		if len(q.Levels) > 0 {
			return fmt.Errorf("question %q: noul questions take no levels", id)
		}
		for k := range q.Choices {
			if k != "true" && k != "false" {
				return fmt.Errorf("question %q: noul criteria keys must be \"true\" or \"false\", got %q", id, k)
			}
		}
	default:
		return fmt.Errorf("question %q: unknown type %q (want choice, noul, or score)", id, q.Type)
	}
	return nil
}

func sortedQuestionIDs(qs map[string]Question) []string {
	ids := make([]string, 0, len(qs))
	for id := range qs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// EstimateDecisionTokens estimates the input size of a decision call at one
// token per 3 bytes of JSON: conservative for punctuation-heavy JSON, so an
// estimate under the limit is unlikely to be rejected upstream.
func EstimateDecisionTokens(state any, qs map[string]Question) (int, error) {
	b, err := json.Marshal(struct {
		State     any                 `json:"state"`
		Questions map[string]Question `json:"questions"`
	}{state, qs})
	if err != nil {
		return 0, fmt.Errorf("marshaling decision input: %w", err)
	}
	return (len(b) + 2) / 3, nil
}

// DeciderConfig binds a named decider to a provider instance and model.
type DeciderConfig struct {
	Name           string
	Provider       string // provider instance name, for cost attribution
	Model          string
	Timeout        time.Duration // 0 = caller's context only
	MaxInputTokens int           // 0 = no pre-flight size check
}

// Decider is the decision-side counterpart of Router: it binds a
// DecisionProvider, a model and the cost tracker, and enforces the size,
// budget and timeout guards around each call.
type Decider struct {
	cfg      DeciderConfig
	provider DecisionProvider
	costs    *CostTracker
	tracer   trace.Tracer
}

// NewDecider creates a Decider. costs may be nil (no budget guard or
// recording).
func NewDecider(cfg DeciderConfig, provider DecisionProvider, costs *CostTracker) *Decider {
	return &Decider{
		cfg:      cfg,
		provider: provider,
		costs:    costs,
		tracer:   otel.Tracer("denkeeper.llm"),
	}
}

// Name returns the decider's configured name.
func (d *Decider) Name() string { return d.cfg.Name }

// Model returns the decision model the decider calls.
func (d *Decider) Model() string { return d.cfg.Model }

// CostTracker returns the tracker calls are billed to (may be nil).
func (d *Decider) CostTracker() *CostTracker { return d.costs }

// Decide asks the configured model the questions about state, billing the
// call to sessionID. It returns ErrDecisionTooLarge or ErrHardLimitExceeded
// without calling the provider, and an error if any asked question is left
// unanswered.
func (d *Decider) Decide(ctx context.Context, sessionID string, state any, qs map[string]Question) (*DecisionResponse, error) {
	ctx, span := d.tracer.Start(ctx, "llm.decide", trace.WithAttributes(
		attribute.String("decider", d.cfg.Name),
		attribute.String("gen_ai.request.model", d.cfg.Model),
		attribute.Int("decision.questions", len(qs)),
	))
	defer span.End()

	resp, err := d.decide(ctx, sessionID, state, qs)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.Float64("decision.cost_usd", resp.CostUSD))
	return resp, nil
}

func (d *Decider) decide(ctx context.Context, sessionID string, state any, qs map[string]Question) (*DecisionResponse, error) {
	if err := ValidateQuestions(qs); err != nil {
		return nil, fmt.Errorf("decider %q: %w", d.cfg.Name, err)
	}
	if d.cfg.MaxInputTokens > 0 {
		est, err := EstimateDecisionTokens(state, qs)
		if err != nil {
			return nil, fmt.Errorf("decider %q: %w", d.cfg.Name, err)
		}
		if est > d.cfg.MaxInputTokens {
			return nil, fmt.Errorf("decider %q: ~%d tokens > %d: %w", d.cfg.Name, est, d.cfg.MaxInputTokens, ErrDecisionTooLarge)
		}
	}
	if d.costs != nil && d.costs.ExceedsHardLimit(sessionID) {
		return nil, fmt.Errorf("session %q exceeded hard cost limit: %w", sessionID, ErrHardLimitExceeded)
	}

	if d.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.cfg.Timeout)
		defer cancel()
	}

	resp, err := d.provider.Decide(ctx, DecisionRequest{Model: d.cfg.Model, State: state, Questions: qs})
	if err != nil {
		return nil, fmt.Errorf("decider %q: %w", d.cfg.Name, err)
	}
	// Record before checking answers: an unusable response was still billed.
	if d.costs != nil {
		d.costs.RecordWithProvider(sessionID, d.cfg.Provider, resp.CostUSD, resp.Usage, "provider")
	}
	for _, id := range sortedQuestionIDs(qs) {
		if _, ok := resp.Answers[id]; !ok {
			return nil, fmt.Errorf("decider %q: no answer for question %q", d.cfg.Name, id)
		}
	}
	return resp, nil
}
