package eval

import (
	"context"
	"errors"
	"math"
	"testing"
)

// --- Hand-written lookups ---

// stubStats answers the history basis from a fixed map. A conversation absent
// from the map returns (nil, nil), which is how the real store reports a
// conversation that retention pruned.
type stubStats struct {
	rows map[string]ConvStats
	err  error
	// calls counts lookups so the per-conversation memoisation can be asserted.
	calls int
}

func (s *stubStats) ConversationStats(_ context.Context, convID string) (*ConvStats, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	row, ok := s.rows[convID]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

// stubPrices answers the list-price basis, keyed by model id alone — the
// provider is carried through but the estimator never keys on it.
type stubPrices map[string]ModelPrice

func (p stubPrices) ModelPrice(_ context.Context, _, model string) (ModelPrice, bool) {
	price, ok := p[model]
	return price, ok
}

// --- Helpers ---

func incumbentOnly() []EstimateVariant {
	return []EstimateVariant{{Name: "current"}}
}

// nearly compares money to the estimator's own rounding granularity.
func nearly(got, want float64) bool { return math.Abs(got-want) < 1e-6 }

// --- History basis ---

func TestEstimate_HistoryBasisIsThePerExchangeAverage(t *testing.T) {
	est := Estimator{Stats: &stubStats{rows: map[string]ConvStats{
		// $1.00 over 4 messages = 2 exchanges = $0.50 a turn.
		"tg:1": {TotalCost: 1.0, TotalMessages: 4},
	}}}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  incumbentOnly(),
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Basis != BasisHistory {
		t.Fatalf("basis = %q, want %q", got.Basis, BasisHistory)
	}
	if !nearly(got.Low, 0.25) || !nearly(got.High, 1.0) {
		t.Errorf("range = %v..%v, want 0.25..1.00 (the 0.5x/2x spread on $0.50)", got.Low, got.High)
	}
	if got.Note != "" {
		t.Errorf("note = %q, want empty — nothing about this figure is partial", got.Note)
	}
}

func TestEstimate_HistoryScalesToACandidateByListPriceRatio(t *testing.T) {
	est := Estimator{
		Stats: &stubStats{rows: map[string]ConvStats{"tg:1": {TotalCost: 1.0, TotalMessages: 4}}},
		Prices: stubPrices{
			"base-model": {InputPerMTok: 1, OutputPerMTok: 1},
			"candidate":  {InputPerMTok: 2, OutputPerMTok: 2},
		},
	}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  []EstimateVariant{{Name: "candidate", Model: "candidate"}},
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Basis != BasisHistory {
		t.Fatalf("basis = %q, want %q — both models are priced, so the measurement scales", got.Basis, BasisHistory)
	}
	// $0.50 a turn on the base model, doubled by the 2x price ratio.
	if !nearly(got.Low, 0.5) || !nearly(got.High, 2.0) {
		t.Errorf("range = %v..%v, want 0.50..2.00", got.Low, got.High)
	}
}

func TestEstimate_UnpricedCandidateCannotScaleHistory(t *testing.T) {
	// The candidate's price is unknown, so the incumbent's measured cost
	// cannot be honestly rescaled and there is no list price to fall back to.
	est := Estimator{
		Stats:  &stubStats{rows: map[string]ConvStats{"tg:1": {TotalCost: 1.0, TotalMessages: 4}}},
		Prices: stubPrices{"base-model": {InputPerMTok: 1, OutputPerMTok: 1}},
	}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  []EstimateVariant{{Name: "candidate", Model: "mystery-model"}},
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Basis != BasisUnknown {
		t.Fatalf("basis = %q, want %q — a scaled figure would be invented", got.Basis, BasisUnknown)
	}
	if got.Low != 0 || got.High != 0 {
		t.Errorf("range = %v..%v, want 0..0", got.Low, got.High)
	}
}

func TestEstimate_HistoryReadsEachConversationOnce(t *testing.T) {
	stats := &stubStats{rows: map[string]ConvStats{"tg:1": {TotalCost: 1.0, TotalMessages: 4}}}
	est := Estimator{Stats: stats}

	if _, err := est.Estimate(context.Background(), EstimateInput{
		Tasks: []Task{
			{Prompt: "a", SourceConversationID: "tg:1"},
			{Prompt: "b", SourceConversationID: "tg:1"},
			{Prompt: "c", SourceConversationID: "tg:1"},
		},
		Variants:  incumbentOnly(),
		K:         1,
		BaseModel: "base-model",
	}); err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if stats.calls != 1 {
		t.Errorf("stats lookups = %d, want 1 — three tasks share one conversation", stats.calls)
	}
}

func TestEstimate_StatsErrorFailsTheEstimate(t *testing.T) {
	boom := errors.New("db is on fire")
	est := Estimator{Stats: &stubStats{err: boom}}

	_, err := est.Estimate(context.Background(), EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  incumbentOnly(),
		K:         1,
		BaseModel: "base-model",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the store error", err)
	}
}

// --- List-price basis ---

func TestEstimate_ListPriceBasisWhenATaskHasNoHistory(t *testing.T) {
	est := Estimator{Prices: stubPrices{"base-model": {InputPerMTok: 3, OutputPerMTok: 15}}}

	got, err := est.Estimate(context.Background(), EstimateInput{
		// "hello" is 5 chars = 1 token, so the prompt is 2001 tokens.
		Tasks:     []Task{{Prompt: "hello"}},
		Variants:  incumbentOnly(),
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Basis != BasisListPrice {
		t.Fatalf("basis = %q, want %q", got.Basis, BasisListPrice)
	}
	// Low is prompt only: 2001 tokens at $3/Mtok.
	if !nearly(got.Low, 0.006) {
		t.Errorf("low = %v, want 0.006 (prompt only)", got.Low)
	}
	// High adds the 600-token output allowance at $15/Mtok.
	if !nearly(got.High, 0.015) {
		t.Errorf("high = %v, want 0.015 (prompt + output allowance)", got.High)
	}
}

func TestEstimate_ListPriceCountsPinnedHistoryInThePrompt(t *testing.T) {
	est := Estimator{Prices: stubPrices{"base-model": {InputPerMTok: 1000, OutputPerMTok: 0}}}
	in := EstimateInput{Variants: incumbentOnly(), K: 1, BaseModel: "base-model"}

	in.Tasks = []Task{{Prompt: "hello"}}
	bare, err := est.Estimate(context.Background(), in)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	// 400 chars of pinned history = 100 tokens = $0.10 at $1000/Mtok.
	in.Tasks = []Task{{Prompt: "hello", PinnedHistory: string(make([]byte, 400))}}
	pinned, err := est.Estimate(context.Background(), in)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if !nearly(pinned.High-bare.High, 0.1) {
		t.Errorf("pinned history added %v, want 0.10", pinned.High-bare.High)
	}
}

// --- Unknown basis ---

func TestEstimate_UnknownBasisWithNeitherLookup(t *testing.T) {
	got, err := Estimator{}.Estimate(context.Background(), EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  incumbentOnly(),
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Basis != BasisUnknown {
		t.Fatalf("basis = %q, want %q", got.Basis, BasisUnknown)
	}
	if got.Low != 0 || got.High != 0 {
		t.Errorf("range = %v..%v, want 0..0 — a fabricated number is worse than none", got.Low, got.High)
	}
	if got.PerVariant[0].Basis != BasisUnknown {
		t.Errorf("per_variant basis = %q, want %q", got.PerVariant[0].Basis, BasisUnknown)
	}
}

func TestEstimate_PartiallyPricedSetNamesTheGapInTheNote(t *testing.T) {
	// Only the history basis is available, so the task without a source
	// conversation cannot be priced at all.
	est := Estimator{Stats: &stubStats{rows: map[string]ConvStats{"tg:1": {TotalCost: 1.0, TotalMessages: 4}}}}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks: []Task{
			{Prompt: "a", SourceConversationID: "tg:1"},
			{Prompt: "b"},
		},
		Variants:  incumbentOnly(),
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Basis != BasisHistory {
		t.Errorf("basis = %q, want %q — a partial figure is not mislabelled unknown", got.Basis, BasisHistory)
	}
	if !nearly(got.High, 1.0) {
		t.Errorf("high = %v, want 1.00 — only the priced task contributes", got.High)
	}
	if got.Note == "" {
		t.Error("note is empty; an unpriced task must be declared")
	}
}

// --- Per-variant breakdown ---

func TestEstimate_PerVariantBreakdownSumsToTheTotal(t *testing.T) {
	est := Estimator{
		Stats: &stubStats{rows: map[string]ConvStats{"tg:1": {TotalCost: 1.0, TotalMessages: 4}}},
		Prices: stubPrices{
			"base-model": {InputPerMTok: 1, OutputPerMTok: 1},
			"candidate":  {InputPerMTok: 3, OutputPerMTok: 3},
		},
	}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  []EstimateVariant{{Name: "current"}, {Name: "candidate", Model: "candidate"}},
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if len(got.PerVariant) != 2 {
		t.Fatalf("per_variant has %d entries, want 2", len(got.PerVariant))
	}
	if got.PerVariant[0].Name != "current" || got.PerVariant[1].Name != "candidate" {
		t.Errorf("per_variant names = %q, %q — request order must be preserved",
			got.PerVariant[0].Name, got.PerVariant[1].Name)
	}
	// The incumbent runs the base model verbatim; the candidate is 3x.
	if !nearly(got.PerVariant[0].High, 1.0) {
		t.Errorf("incumbent high = %v, want 1.00", got.PerVariant[0].High)
	}
	if !nearly(got.PerVariant[1].High, 3.0) {
		t.Errorf("candidate high = %v, want 3.00", got.PerVariant[1].High)
	}
	if !nearly(got.Low, got.PerVariant[0].Low+got.PerVariant[1].Low) ||
		!nearly(got.High, got.PerVariant[0].High+got.PerVariant[1].High) {
		t.Errorf("total %v..%v is not the sum of the per-variant figures", got.Low, got.High)
	}
}

func TestEstimate_VariantBasisIsReportedPerVariant(t *testing.T) {
	// The incumbent has a measurement; the candidate is priced but its history
	// cannot be scaled because the base model has no list price.
	est := Estimator{
		Stats:  &stubStats{rows: map[string]ConvStats{"tg:1": {TotalCost: 1.0, TotalMessages: 4}}},
		Prices: stubPrices{"candidate": {InputPerMTok: 3, OutputPerMTok: 15}},
	}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  []EstimateVariant{{Name: "current"}, {Name: "candidate", Model: "candidate"}},
		K:         1,
		BaseModel: "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.PerVariant[0].Basis != BasisHistory {
		t.Errorf("incumbent basis = %q, want %q", got.PerVariant[0].Basis, BasisHistory)
	}
	if got.PerVariant[1].Basis != BasisListPrice {
		t.Errorf("candidate basis = %q, want %q", got.PerVariant[1].Basis, BasisListPrice)
	}
	if got.Basis != BasisListPrice {
		t.Errorf("roll-up basis = %q, want %q — the weakest contributing basis wins", got.Basis, BasisListPrice)
	}
}

// --- Subset and k scaling ---

func TestEstimate_SampleTasksScalesByTheMeanPerTask(t *testing.T) {
	// Four tasks costing $2, $4, $6 and $8 a turn (one exchange each): mean
	// $5.00, so a drawn subset of two is $10.00 before the spread.
	est := Estimator{Stats: &stubStats{rows: map[string]ConvStats{
		"c1": {TotalCost: 2, TotalMessages: 2},
		"c2": {TotalCost: 4, TotalMessages: 2},
		"c3": {TotalCost: 6, TotalMessages: 2},
		"c4": {TotalCost: 8, TotalMessages: 2},
	}}}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks: []Task{
			{Prompt: "a", SourceConversationID: "c1"},
			{Prompt: "b", SourceConversationID: "c2"},
			{Prompt: "c", SourceConversationID: "c3"},
			{Prompt: "d", SourceConversationID: "c4"},
		},
		Variants:    incumbentOnly(),
		K:           1,
		SampleTasks: 2,
		BaseModel:   "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Tasks != 2 {
		t.Errorf("tasks = %d, want 2 — the drawn size, not the set size", got.Tasks)
	}
	if !nearly(got.High, 20.0) {
		t.Errorf("high = %v, want 20.00 (2 tasks x $5.00 mean x the 2x spread)", got.High)
	}
	if got.Note == "" {
		t.Error("note is empty; the subset is drawn at run time and the figure is a mean")
	}
}

func TestEstimate_SampleTasksAtOrAboveTheSetSizePricesEverything(t *testing.T) {
	est := Estimator{Stats: &stubStats{rows: map[string]ConvStats{
		"c1": {TotalCost: 2, TotalMessages: 2},
		"c2": {TotalCost: 4, TotalMessages: 2},
	}}}

	got, err := est.Estimate(context.Background(), EstimateInput{
		Tasks: []Task{
			{Prompt: "a", SourceConversationID: "c1"},
			{Prompt: "b", SourceConversationID: "c2"},
		},
		Variants:    incumbentOnly(),
		K:           1,
		SampleTasks: 50,
		BaseModel:   "base-model",
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Tasks != 2 {
		t.Errorf("tasks = %d, want 2", got.Tasks)
	}
	if !nearly(got.High, 12.0) {
		t.Errorf("high = %v, want 12.00 ((2+4) x the 2x spread)", got.High)
	}
	if got.Note != "" {
		t.Errorf("note = %q, want empty — nothing is sampled", got.Note)
	}
}

func TestEstimate_KMultipliesTheRange(t *testing.T) {
	est := Estimator{Stats: &stubStats{rows: map[string]ConvStats{"tg:1": {TotalCost: 1.0, TotalMessages: 4}}}}
	in := EstimateInput{
		Tasks:     []Task{{Prompt: "hello", SourceConversationID: "tg:1"}},
		Variants:  incumbentOnly(),
		K:         1,
		BaseModel: "base-model",
	}

	one, err := est.Estimate(context.Background(), in)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	in.K = 3
	three, err := est.Estimate(context.Background(), in)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if three.K != 3 {
		t.Errorf("k = %d, want 3 echoed back", three.K)
	}
	if !nearly(three.Low, one.Low*3) || !nearly(three.High, one.High*3) {
		t.Errorf("k=3 range %v..%v is not 3x the k=1 range %v..%v",
			three.Low, three.High, one.Low, one.High)
	}
}

func TestEstimate_CurrencyIsAlwaysUSD(t *testing.T) {
	got, err := Estimator{}.Estimate(context.Background(), EstimateInput{
		Tasks: []Task{{Prompt: "hello"}}, Variants: incumbentOnly(), K: 1,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if got.Currency != "USD" {
		t.Errorf("currency = %q, want USD", got.Currency)
	}
}
