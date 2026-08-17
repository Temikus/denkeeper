package eval

import "fmt"

// Decision-rule outcomes.
//
// The rule is asymmetric on purpose: the objective gates alone can declare a
// downgrade (a gate failed — no judge needed to reject a candidate) or report
// that nothing regressed, but they can never declare an upgrade. That needs the
// judge win-rate, so a candidate cannot be promoted on the strength of being
// cheap and quiet.
const (
	VerdictUpgrade       = "upgrade"
	VerdictDowngrade     = "downgrade"
	VerdictNoRegressions = "no_regressions"
	VerdictInconclusive  = "inconclusive"
)

// Gate names, as they appear in the gate table.
const (
	GateRejectedRate = "rejected_rate"
	GateMeanRounds   = "mean_rounds"
	GateCostPerTask  = "mean_cost_per_task"
)

// Gate is one row of the objective gate table. Every verdict surface shows this
// table, not just the label: a bare verdict banner with no visible criteria is
// the black box this subsystem exists to remove.
type Gate struct {
	Name     string  `json:"name"`
	Baseline float64 `json:"baseline"`
	Value    float64 `json:"value"`
	Delta    float64 `json:"delta"`
	// Threshold is the largest Delta that still passes, in Unit.
	Threshold float64 `json:"threshold"`
	// Unit is "pp" (percentage points, for rates) or "%" (relative change).
	Unit string `json:"unit"`
	Pass bool   `json:"pass"`
}

// Agreement is the operator–judge calibration figure: how often the operator's
// own call on a calibration item matched the judge's. Below roughly 80 % the
// rubric wants fixing before headless judging is trusted, since a drifted
// rubric silently devalues every later run.
type Agreement struct {
	Items  int     `json:"items"`
	Agreed int     `json:"agreed"`
	Rate   float64 `json:"rate"`
}

// Judgment is the blinded-pair tally for one candidate against the baseline.
type Judgment struct {
	Pairs int `json:"pairs"`
	// JudgedPairs counts pairs whose *both* presentation orders carry a judge
	// verdict. A half-judged pair is not evidence.
	JudgedPairs  int     `json:"judged_pairs"`
	Wins         int     `json:"wins"`
	Losses       int     `json:"losses"`
	Ties         int     `json:"ties"`
	WinRate      float64 `json:"win_rate"`
	WinThreshold float64 `json:"win_threshold"`
	// OperatorAgreement is nil until the operator marks a calibration item.
	OperatorAgreement *Agreement `json:"operator_agreement,omitempty"`
}

// CategoryResult breaks a candidate's performance down by task category. A
// rolled-up number hides bidirectional failures: a candidate winning big on
// chat while losing on tool-heavy still shows a comfortable overall win, and
// tool-heavy is usually what the operator actually cares about.
type CategoryResult struct {
	Category    string  `json:"category"`
	JudgedPairs int     `json:"judged_pairs"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	Ties        int     `json:"ties"`
	WinRate     float64 `json:"win_rate"`
	// Deltas mirror the three gates, restricted to this category's tasks.
	DeltaRejectedPP float64 `json:"delta_rejected_pp"`
	DeltaRoundsPct  float64 `json:"delta_rounds_pct"`
	DeltaCostPct    float64 `json:"delta_cost_pct"`
	// Regressed is true when this category alone would fail a gate or fall
	// below the win threshold, whatever the aggregate says.
	Regressed bool `json:"regressed"`
}

// VariantVerdict is the decision for one candidate variant against the run's
// baseline, with the work shown.
type VariantVerdict struct {
	VariantID int64  `json:"variant_id"`
	Variant   string `json:"variant"`
	Baseline  string `json:"baseline"`
	Verdict   string `json:"verdict"`
	// Reason is the one-line plain-language explanation, e.g. "downgrade: mean
	// rounds regressed +35% against a +20% threshold".
	Reason     string           `json:"reason"`
	Gates      []Gate           `json:"gates"`
	Judgment   Judgment         `json:"judgment"`
	Categories []CategoryResult `json:"categories"`
	// Divergence is set when the aggregate and a category disagree, e.g. "wins
	// overall; regresses on tool_heavy". v1 surfaces this prominently without
	// gating on it.
	Divergence string `json:"divergence,omitempty"`
}

// verdictInput is everything buildVerdicts needs, assembled by Summarize.
type verdictInput struct {
	opts     SummaryOpts
	variants []Variant
	metrics  []VariantMetrics
	tasks    []Task
	samples  []Sample
	pairs    []Pair
	items    []JudgmentItem
	verdicts []Verdict
	complete Completeness
}

// buildVerdicts produces one decision per non-baseline variant.
func buildVerdicts(in verdictInput) []VariantVerdict {
	if len(in.variants) < 2 {
		return nil
	}
	baseline := in.variants[0]
	byID := make(map[int64]VariantMetrics, len(in.metrics))
	for _, m := range in.metrics {
		byID[m.VariantID] = m
	}
	outcomes := resolvePairs(in)
	cats := categoryMetrics(in.tasks, in.samples)
	taskCat := make(map[int64]string, len(in.tasks))
	for _, t := range in.tasks {
		taskCat[t.ID] = t.Category
	}

	out := make([]VariantVerdict, 0, len(in.variants)-1)
	for _, cand := range in.variants[1:] {
		vv := VariantVerdict{
			VariantID:  cand.ID,
			Variant:    cand.Name,
			Baseline:   baseline.Name,
			Gates:      buildGates(in.opts, byID[baseline.ID], byID[cand.ID]),
			Judgment:   tallyJudgment(in.opts, outcomes, baseline.ID, cand.ID),
			Categories: buildCategories(in.opts, outcomes, taskCat, cats, baseline.ID, cand.ID),
		}
		vv.Verdict, vv.Reason = decide(in.opts, in.complete, vv)
		vv.Divergence = divergence(vv)
		out = append(out, vv)
	}
	return out
}

// --- Gates ---

func buildGates(opts SummaryOpts, base, cand VariantMetrics) []Gate {
	return []Gate{
		pointGate(GateRejectedRate, base.RejectedRate, cand.RejectedRate, opts.GateRejectedPP),
		percentGate(GateMeanRounds, base.MeanRounds, cand.MeanRounds, opts.GateRoundsPct),
		percentGate(GateCostPerTask, base.MeanCostPerTask, cand.MeanCostPerTask, opts.GateCostPct),
	}
}

// pointGate compares two rates in percentage points.
func pointGate(name string, base, value, threshold float64) Gate {
	delta := (value - base) * 100
	return Gate{
		Name: name, Baseline: base, Value: value,
		Delta: delta, Threshold: threshold, Unit: "pp",
		Pass: delta <= threshold,
	}
}

// percentGate compares two magnitudes relatively. A zero baseline has no ratio
// to take, so the gate passes and the table shows the raw values — an honest
// "cannot say" beats a fabricated infinity.
func percentGate(name string, base, value, threshold float64) Gate {
	g := Gate{Name: name, Baseline: base, Value: value, Threshold: threshold, Unit: "%", Pass: true}
	if base <= 0 {
		return g
	}
	g.Delta = (value - base) / base * 100
	g.Pass = g.Delta <= threshold
	return g
}

func failedGate(gates []Gate) (Gate, bool) {
	for _, g := range gates {
		if !g.Pass {
			return g, true
		}
	}
	return Gate{}, false
}

func gateLabel(name string) string {
	switch name {
	case GateRejectedRate:
		return "rejected tool-call rate"
	case GateMeanRounds:
		return "mean rounds"
	case GateCostPerTask:
		return "cost per task"
	}
	return name
}

// --- Judgment ---

// pairOutcome is one pair collapsed to a single winner.
type pairOutcome struct {
	taskID    int64
	candidate int64
	// winner is the variant id that took the pair, 0 for a tie.
	winner int64
	// decided is true once every item of the pair carries a judge verdict.
	decided bool
	// agreement counts the operator's calibration marks on this pair's items.
	agreeItems, agreed int
}

// resolvePairs collapses each pair's two presentation orders into one outcome.
//
// A win requires both orders to name the same variant. When they disagree the
// judge was tracking position rather than quality, and the pair records a tie —
// at aggregation time, never at write time, since a judge working the queue
// must not see the other order's verdict.
func resolvePairs(in verdictInput) []pairOutcome {
	itemsByPair := make(map[int64][]JudgmentItem, len(in.pairs))
	for _, it := range in.items {
		itemsByPair[it.PairID] = append(itemsByPair[it.PairID], it)
	}
	judge, operator := verdictsByItem(in.verdicts)

	out := make([]pairOutcome, 0, len(in.pairs))
	for _, p := range in.pairs {
		assign, err := DecodeAssignment(p.Assignment)
		if err != nil {
			continue
		}
		po := pairOutcome{taskID: p.TaskID, candidate: candidateOf(assign, in.variants[0].ID)}
		po.decided, po.winner = resolveOutcome(itemsByPair[p.ID], judge, assign)
		po.agreeItems, po.agreed = countAgreement(itemsByPair[p.ID], judge, operator)
		out = append(out, po)
	}
	return out
}

// resolveOutcome reads one pair's items and returns whether it is fully judged
// and, if so, which variant took it.
func resolveOutcome(items []JudgmentItem, judge map[int64]Verdict, assign Assignment) (bool, int64) {
	if len(items) < 2 {
		return false, 0
	}
	var winner int64
	for i, it := range items {
		v, ok := judge[it.ID]
		if !ok {
			return false, 0
		}
		w := VariantFor(assign, it.PresentationOrder, v.Winner)
		if i == 0 {
			winner = w
			continue
		}
		if w != winner {
			return true, 0
		}
	}
	return true, winner
}

// countAgreement tallies the operator's calibration marks against the judge's
// calls on the same items.
func countAgreement(items []JudgmentItem, judge, operator map[int64]Verdict) (int, int) {
	var total, agreed int
	for _, it := range items {
		op, ok := operator[it.ID]
		if !ok {
			continue
		}
		jv, ok := judge[it.ID]
		if !ok {
			continue
		}
		total++
		if op.Winner == jv.Winner {
			agreed++
		}
	}
	return total, agreed
}

// verdictsByItem splits stored verdicts into the judge's and the operator's,
// keeping the newest of each per item.
func verdictsByItem(verdicts []Verdict) (judge, operator map[int64]Verdict) {
	judge = make(map[int64]Verdict, len(verdicts))
	operator = make(map[int64]Verdict, len(verdicts))
	for _, v := range verdicts {
		target := judge
		if v.JudgeIdent == JudgeOperator {
			target = operator
		}
		prev, ok := target[v.ItemID]
		if !ok || v.CreatedAt.After(prev.CreatedAt) {
			target[v.ItemID] = v
		}
	}
	return judge, operator
}

// candidateOf names the non-baseline side of a pair.
func candidateOf(assign Assignment, baselineID int64) int64 {
	if assign.A == baselineID {
		return assign.B
	}
	return assign.A
}

func tallyJudgment(opts SummaryOpts, outcomes []pairOutcome, baselineID, candID int64) Judgment {
	j := Judgment{WinThreshold: opts.WinThreshold}
	var agreeItems, agreed int
	for _, po := range outcomes {
		if po.candidate != candID {
			continue
		}
		j.Pairs++
		agreeItems += po.agreeItems
		agreed += po.agreed
		if !po.decided {
			continue
		}
		j.JudgedPairs++
		switch po.winner {
		case candID:
			j.Wins++
		case baselineID:
			j.Losses++
		default:
			j.Ties++
		}
	}
	if j.JudgedPairs > 0 {
		j.WinRate = float64(j.Wins) / float64(j.JudgedPairs)
	}
	if agreeItems > 0 {
		j.OperatorAgreement = &Agreement{
			Items: agreeItems, Agreed: agreed,
			Rate: float64(agreed) / float64(agreeItems),
		}
	}
	return j
}

// --- Per-category breakdown ---

// catAcc accumulates one (variant, category) cell.
type catAcc struct {
	okSamples int
	rounds    int
	cost      float64
	tools     int
	rejected  int
	taskIDs   map[int64]struct{}
}

type catKey struct {
	variant  int64
	category string
}

// catMetrics is the objective slice of one (variant, category) cell.
type catMetrics struct {
	rejectedRate    float64
	meanRounds      float64
	meanCostPerTask float64
}

func categoryMetrics(tasks []Task, samples []Sample) map[catKey]catMetrics {
	taskCat := make(map[int64]string, len(tasks))
	for _, t := range tasks {
		taskCat[t.ID] = t.Category
	}
	accs := make(map[catKey]*catAcc)
	for _, smp := range samples {
		if smp.Status != SampleOK {
			continue
		}
		key := catKey{variant: smp.VariantID, category: taskCat[smp.TaskID]}
		acc, ok := accs[key]
		if !ok {
			acc = &catAcc{taskIDs: make(map[int64]struct{})}
			accs[key] = acc
		}
		acc.okSamples++
		acc.rounds += smp.Rounds
		acc.cost += smp.Cost
		acc.taskIDs[smp.TaskID] = struct{}{}
		acc.tools += smp.OutcomeOK + smp.OutcomeRejected + smp.OutcomeFailed + smp.OutcomeDenied
		acc.rejected += smp.OutcomeRejected
	}

	out := make(map[catKey]catMetrics, len(accs))
	for key, acc := range accs {
		var m catMetrics
		if acc.tools > 0 {
			m.rejectedRate = float64(acc.rejected) / float64(acc.tools)
		}
		if acc.okSamples > 0 {
			m.meanRounds = float64(acc.rounds) / float64(acc.okSamples)
		}
		if n := len(acc.taskIDs); n > 0 {
			m.meanCostPerTask = acc.cost / float64(n)
		}
		out[key] = m
	}
	return out
}

func buildCategories(opts SummaryOpts, outcomes []pairOutcome, taskCat map[int64]string,
	cats map[catKey]catMetrics, baselineID, candID int64) []CategoryResult {

	tally := make(map[string]*CategoryResult, len(Categories()))
	for _, po := range outcomes {
		if po.candidate != candID {
			continue
		}
		cat := taskCat[po.taskID]
		cr, ok := tally[cat]
		if !ok {
			cr = &CategoryResult{Category: cat}
			tally[cat] = cr
		}
		if !po.decided {
			continue
		}
		cr.JudgedPairs++
		switch po.winner {
		case candID:
			cr.Wins++
		case baselineID:
			cr.Losses++
		default:
			cr.Ties++
		}
	}

	// Iterate the canonical category order so the breakdown is stable; a map
	// walk would reshuffle it on every request.
	out := make([]CategoryResult, 0, len(tally))
	for _, cat := range Categories() {
		cr, ok := tally[cat]
		if !ok {
			continue
		}
		if cr.JudgedPairs > 0 {
			cr.WinRate = float64(cr.Wins) / float64(cr.JudgedPairs)
		}
		base := cats[catKey{variant: baselineID, category: cat}]
		cand := cats[catKey{variant: candID, category: cat}]
		cr.DeltaRejectedPP = (cand.rejectedRate - base.rejectedRate) * 100
		cr.DeltaRoundsPct = relDelta(base.meanRounds, cand.meanRounds)
		cr.DeltaCostPct = relDelta(base.meanCostPerTask, cand.meanCostPerTask)
		cr.Regressed = categoryRegressed(opts, *cr)
		out = append(out, *cr)
	}
	return out
}

func relDelta(base, value float64) float64 {
	if base <= 0 {
		return 0
	}
	return (value - base) / base * 100
}

func categoryRegressed(opts SummaryOpts, cr CategoryResult) bool {
	if cr.DeltaRejectedPP > opts.GateRejectedPP ||
		cr.DeltaRoundsPct > opts.GateRoundsPct ||
		cr.DeltaCostPct > opts.GateCostPct {
		return true
	}
	return cr.JudgedPairs > 0 && cr.WinRate < opts.WinThreshold
}

// --- The rule itself ---

// decide applies the decision rule and returns the verdict with its one-line
// reason. Order matters: thin data first (nothing below it means anything), then
// the objective gates (which can reject without spending judging effort), then
// the judge.
func decide(opts SummaryOpts, complete Completeness, vv VariantVerdict) (string, string) {
	if !complete.Conclusive {
		return VerdictInconclusive, fmt.Sprintf(
			"inconclusive: %d of %d samples completed, below the %.0f%% completeness floor",
			complete.SamplesOK, complete.SamplesExpected, complete.Floor*100)
	}
	if g, failed := failedGate(vv.Gates); failed {
		return VerdictDowngrade, fmt.Sprintf(
			"downgrade: %s regressed %+.1f%s against a %+.1f%s threshold",
			gateLabel(g.Name), g.Delta, g.Unit, g.Threshold, g.Unit)
	}
	j := vv.Judgment
	if j.JudgedPairs == 0 {
		return VerdictNoRegressions, fmt.Sprintf(
			"no objective regressions; judgment pending (%d pair(s) unjudged)", j.Pairs)
	}
	if j.WinRate >= opts.WinThreshold {
		return VerdictUpgrade, fmt.Sprintf(
			"upgrade: judge win-rate %.0f%% over %d judged pair(s) meets the %.0f%% threshold, and no objective gate regressed",
			j.WinRate*100, j.JudgedPairs, opts.WinThreshold*100)
	}
	return VerdictDowngrade, fmt.Sprintf(
		"downgrade: no objective regression, but the judge win-rate %.0f%% over %d judged pair(s) is below the %.0f%% threshold",
		j.WinRate*100, j.JudgedPairs, opts.WinThreshold*100)
}

// divergence names the categories that disagree with a favourable aggregate.
// Surfaced, not gated: a per-category floor is a later knob if surfacing proves
// insufficient.
func divergence(vv VariantVerdict) string {
	if vv.Verdict != VerdictUpgrade && vv.Verdict != VerdictNoRegressions {
		return ""
	}
	var bad []string
	for _, cr := range vv.Categories {
		if cr.Regressed {
			bad = append(bad, cr.Category)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	lead := "wins overall"
	if vv.Verdict == VerdictNoRegressions {
		lead = "no regressions overall"
	}
	return fmt.Sprintf("%s; regresses on %s", lead, joinCategories(bad))
}

func joinCategories(cats []string) string {
	switch len(cats) {
	case 1:
		return cats[0]
	case 2:
		return cats[0] + " and " + cats[1]
	}
	out := ""
	for i, c := range cats[:len(cats)-1] {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out + ", and " + cats[len(cats)-1]
}
