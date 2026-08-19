package eval

import (
	"context"
	"fmt"
	"math"
)

// Estimate bases, weakest last. A figure is only ever labelled with a basis
// that actually contributed to it.
const (
	// BasisHistory: the task's source conversation has real telemetry, so the
	// per-turn figure is a measurement rather than a model.
	BasisHistory = "history"
	// BasisListPrice: no usable history, so the figure is the variant's list
	// price times a nominal token budget.
	BasisListPrice = "list_price"
	// BasisUnknown: neither was available. The caller shows the hard cap alone
	// rather than a fabricated number.
	BasisUnknown = "unknown"
)

// Estimator constants. Deliberately in one place: they are the whole model
// behind a list-price figure, and a reader comparing an estimate against a
// finished run needs to see what was assumed.
const (
	// nominalSystemPromptTokens stands in for the assembled system prompt
	// (persona + skills + date injection). The Engine exposes no accessor for
	// the built prompt and building one costs a full turn setup, so a fixed
	// stand-in is used instead — it dominates a short task prompt, which is
	// why it is modelled at all. Roughly a medium persona plus two skills.
	nominalSystemPromptTokens = 2000
	// nominalOutputTokens is the per-turn completion allowance. It is what
	// separates a variant's low (prompt only) from its high (prompt + output).
	nominalOutputTokens = 600
	// charsPerToken is the usual English rule of thumb, applied to the task
	// prompt and its pinned history.
	charsPerToken = 4
	// historySpreadLow and historySpreadHigh bracket a history-derived
	// per-turn figure. A saved exchange average measures a different turn on
	// the same agent, so the range is wide on purpose.
	historySpreadLow  = 0.5
	historySpreadHigh = 2.0
	// tokensPerMTok converts a per-million-token list price to per-token.
	tokensPerMTok = 1_000_000
)

// ConvStats is the slice of conversation telemetry the history basis reads.
type ConvStats struct {
	TotalCost     float64
	TotalMessages int
}

// StatsLookup resolves a task's source conversation to its aggregate
// telemetry. A nil result means the conversation carries no stats — pruned by
// retention, cleared, or never real — and is not an error.
type StatsLookup interface {
	ConversationStats(ctx context.Context, convID string) (*ConvStats, error)
}

// ModelPrice is a model's list price in USD per million tokens.
type ModelPrice struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// PriceLookup resolves a (provider, model) pair to its list price. An empty
// provider means the base agent's own provider. ok=false means the price is
// unknown, which is a valid answer, not a failure.
type PriceLookup interface {
	ModelPrice(ctx context.Context, provider, model string) (ModelPrice, bool)
}

// EstimateVariant is one side of the comparison being priced. An empty Model
// is the incumbent overlay: it runs the base agent's live model.
type EstimateVariant struct {
	Name     string
	Model    string
	Provider string
}

// EstimateInput is everything the estimator needs that it cannot look up.
type EstimateInput struct {
	Tasks    []Task
	Variants []EstimateVariant
	K        int
	// SampleTasks, when set below len(Tasks), is the size of the stratified
	// subset a Quick check draws. The drawn set is not known at estimate time,
	// so the figure scales the mean per-task cost instead.
	SampleTasks int
	// BaseModel and BaseProvider are the agent's live config — the model the
	// history basis actually measured, and what an empty overlay runs.
	BaseModel    string
	BaseProvider string
}

// VariantEstimate is one variant's share of the range.
type VariantEstimate struct {
	Name  string  `json:"name"`
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	Basis string  `json:"basis"`
}

// Estimate is a pre-run cost range in USD.
type Estimate struct {
	Low      float64 `json:"low"`
	High     float64 `json:"high"`
	Currency string  `json:"currency"`
	Basis    string  `json:"basis"`
	// Tasks is how many tasks the figure covers — the drawn subset size when
	// sample_tasks narrows the run, otherwise the whole set.
	Tasks      int               `json:"tasks"`
	K          int               `json:"k"`
	PerVariant []VariantEstimate `json:"per_variant"`
	// Note names anything that makes the figure less than a straight sum:
	// a sampled subset, or tasks that could not be priced at all.
	Note string `json:"note,omitempty"`
}

// Estimator computes a pre-run cost estimate. Both lookups are optional: a nil
// Stats removes the history basis, a nil Prices removes the list-price basis,
// and with neither the estimate is honestly unknown.
type Estimator struct {
	Stats  StatsLookup
	Prices PriceLookup
}

// taskCost is one (task, variant) per-turn figure and the basis behind it.
// outputCost is the completion allowance inside perTurn on the list-price
// basis, carried here so spread cannot drift from listPricePerTurn.
type taskCost struct {
	perTurn    float64
	outputCost float64
	basis      string
}

// Estimate prices a run before it is created.
//
// Per (task, variant) the basis order is history → list price → unknown.
// History measures the incumbent; a variant running a different model only
// keeps it when both models are priced and the figure can be scaled by their
// list-price ratio, otherwise that variant falls to list price outright.
func (e Estimator) Estimate(ctx context.Context, in EstimateInput) (*Estimate, error) {
	k := in.K
	if k < 1 {
		k = 1
	}
	drawn := len(in.Tasks)
	sampled := in.SampleTasks > 0 && in.SampleTasks < drawn
	if sampled {
		drawn = in.SampleTasks
	}

	perTurnHistory, err := e.historyPerTurn(ctx, in.Tasks)
	if err != nil {
		return nil, err
	}
	basePrice, basePriced := e.price(ctx, in.BaseProvider, in.BaseModel)

	out := &Estimate{Currency: "USD", Tasks: drawn, K: k, Basis: BasisUnknown}
	unpriced := 0
	for _, v := range in.Variants {
		ve, miss := e.variantEstimate(ctx, v, in, perTurnHistory, basePrice, basePriced, drawn, k)
		if miss > unpriced {
			unpriced = miss
		}
		out.PerVariant = append(out.PerVariant, ve)
		out.Low += ve.Low
		out.High += ve.High
	}
	out.Basis = rollUpBasis(out.PerVariant)
	out.Note = estimateNote(sampled, drawn, len(in.Tasks), unpriced)
	out.Low = roundCents(out.Low)
	out.High = roundCents(out.High)
	return out, nil
}

// variantEstimate prices one variant across the task set, returning its range
// and how many tasks it could not price at all.
func (e Estimator) variantEstimate(ctx context.Context, v EstimateVariant, in EstimateInput,
	perTurnHistory []float64, basePrice ModelPrice, basePriced bool, drawn, k int) (VariantEstimate, int) {

	model, provider := v.Model, v.Provider
	if model == "" {
		model = in.BaseModel
	}
	if provider == "" {
		provider = in.BaseProvider
	}
	price, priced := e.price(ctx, provider, model)

	// A history figure measures the base model. Keep it for a different model
	// only when both are priced, so the scale is a real ratio rather than an
	// assumption that two models cost the same.
	ratio, scalable := 1.0, true
	if model != in.BaseModel {
		ratio, scalable = priceRatio(basePrice, price, basePriced && priced)
	}

	var sum, sumLow, sumHigh float64
	var priceable int
	usedHistory, usedList := false, false
	for i, task := range in.Tasks {
		tc := taskCost{basis: BasisUnknown}
		switch {
		case scalable && perTurnHistory[i] > 0:
			tc = taskCost{perTurn: perTurnHistory[i] * ratio, basis: BasisHistory}
		case priced:
			total, output := listPricePerTurn(task, price)
			tc = taskCost{perTurn: total, outputCost: output, basis: BasisListPrice}
		}
		low, high := spread(tc)
		switch tc.basis {
		case BasisHistory:
			usedHistory = true
		case BasisListPrice:
			usedList = true
		default:
			continue
		}
		priceable++
		sum += tc.perTurn
		sumLow += low
		sumHigh += high
	}

	ve := VariantEstimate{Name: v.Name, Basis: variantBasis(usedHistory, usedList)}
	if priceable == 0 {
		return ve, len(in.Tasks)
	}
	// A sampled run draws its subset server-side at creation, so the mean is
	// the only honest per-task figure available here.
	if drawn != len(in.Tasks) {
		scale := float64(drawn) / float64(priceable)
		sumLow, sumHigh = sumLow*scale, sumHigh*scale
	}
	ve.Low = roundCents(sumLow * float64(k))
	ve.High = roundCents(sumHigh * float64(k))
	return ve, len(in.Tasks) - priceable
}

// spread turns a per-turn figure into a low/high pair. The history basis
// brackets a measurement; the list-price basis brackets prompt-only against
// prompt plus the output allowance, which is where its own low already sits.
func spread(tc taskCost) (low, high float64) {
	switch tc.basis {
	case BasisHistory:
		return tc.perTurn * historySpreadLow, tc.perTurn * historySpreadHigh
	case BasisListPrice:
		// perTurn is the prompt+output figure; the low strips the allowance
		// back out rather than recomputing it.
		return tc.perTurn - tc.outputCost, tc.perTurn
	default:
		return 0, 0
	}
}

// listPricePerTurn prices one turn at list: the nominal system prompt plus the
// task's own text and pinned history, plus the output allowance. The second
// return is the output share of the first.
func listPricePerTurn(task Task, price ModelPrice) (total, output float64) {
	promptTokens := nominalSystemPromptTokens +
		len(task.Prompt)/charsPerToken +
		len(task.PinnedHistory)/charsPerToken
	output = nominalOutputTokens * price.OutputPerMTok / tokensPerMTok
	return float64(promptTokens)*price.InputPerMTok/tokensPerMTok + output, output
}

// priceRatio blends the input and output list prices of two models by the
// nominal token budget, so a model that is cheap on input but dear on output
// is not treated as uniformly cheap.
func priceRatio(base, candidate ModelPrice, bothPriced bool) (float64, bool) {
	if !bothPriced {
		return 0, false
	}
	denom := base.InputPerMTok*nominalSystemPromptTokens + base.OutputPerMTok*nominalOutputTokens
	if denom <= 0 {
		return 0, false
	}
	num := candidate.InputPerMTok*nominalSystemPromptTokens + candidate.OutputPerMTok*nominalOutputTokens
	return num / denom, true
}

// historyPerTurn returns each task's per-exchange cost from its source
// conversation, or 0 where there is none. conversation_stats counts both sides
// of an exchange, hence the halving.
func (e Estimator) historyPerTurn(ctx context.Context, tasks []Task) ([]float64, error) {
	out := make([]float64, len(tasks))
	if e.Stats == nil {
		return out, nil
	}
	seen := make(map[string]float64, len(tasks))
	for i, t := range tasks {
		if t.SourceConversationID == "" {
			continue
		}
		if cached, ok := seen[t.SourceConversationID]; ok {
			out[i] = cached
			continue
		}
		stats, err := e.Stats.ConversationStats(ctx, t.SourceConversationID)
		if err != nil {
			return nil, fmt.Errorf("reading conversation stats for %q: %w", t.SourceConversationID, err)
		}
		var perTurn float64
		if stats != nil && stats.TotalMessages > 0 && stats.TotalCost > 0 {
			exchanges := math.Ceil(float64(stats.TotalMessages) / 2)
			perTurn = stats.TotalCost / exchanges
		}
		seen[t.SourceConversationID] = perTurn
		out[i] = perTurn
	}
	return out, nil
}

// price resolves a model's list price, tolerating a nil lookup.
func (e Estimator) price(ctx context.Context, provider, model string) (ModelPrice, bool) {
	if e.Prices == nil || model == "" {
		return ModelPrice{}, false
	}
	p, ok := e.Prices.ModelPrice(ctx, provider, model)
	if !ok || (p.InputPerMTok <= 0 && p.OutputPerMTok <= 0) {
		return ModelPrice{}, false
	}
	return p, true
}

// variantBasis labels a variant by the weakest basis that contributed. Tasks
// that could not be priced at all are reported through the note instead, so a
// partial figure is not mislabelled "unknown".
func variantBasis(usedHistory, usedList bool) string {
	switch {
	case usedList:
		return BasisListPrice
	case usedHistory:
		return BasisHistory
	default:
		return BasisUnknown
	}
}

// rollUpBasis applies the same weakest-wins rule across variants.
func rollUpBasis(vs []VariantEstimate) string {
	basis := BasisUnknown
	for _, v := range vs {
		switch v.Basis {
		case BasisListPrice:
			return BasisListPrice
		case BasisHistory:
			basis = BasisHistory
		}
	}
	return basis
}

// estimateNote states anything that makes the figure less than a straight sum.
func estimateNote(sampled bool, drawn, total, unpriced int) string {
	switch {
	case sampled && unpriced > 0:
		return fmt.Sprintf("%d of %d tasks are drawn at run time, so the figure scales the mean per-task cost; %d task(s) could not be priced and are excluded from that mean",
			drawn, total, unpriced)
	case sampled:
		return fmt.Sprintf("%d of %d tasks are drawn at run time, so the figure scales the mean per-task cost", drawn, total)
	case unpriced > 0:
		return fmt.Sprintf("%d of %d tasks could not be priced and are not represented in this figure", unpriced, total)
	}
	return ""
}

// roundCents trims the float noise a chain of ratios accumulates. Sub-cent
// precision is kept because a Quick check can genuinely cost a few cents.
func roundCents(v float64) float64 {
	return math.Round(v*10000) / 10000
}
