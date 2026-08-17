package eval

import (
	"context"
	"strings"
	"testing"
)

// testOpts are the shipped defaults, spelled out so a test that moves one
// threshold shows exactly what it moved.
func testOpts() SummaryOpts {
	return SummaryOpts{
		CompletenessFloor: 0.8,
		WinThreshold:      0.55,
		GateRejectedPP:    2.0,
		GateRoundsPct:     20,
		GateCostPct:       25,
	}
}

func (f *pairFixture) summarizeWith(t *testing.T, opts SummaryOpts) *Summary {
	t.Helper()
	sum, err := f.store.Summarize(context.Background(), f.run.ID, opts)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	return sum
}

func (f *pairFixture) onlyVerdict(t *testing.T, sum *Summary) VariantVerdict {
	t.Helper()
	if len(sum.Verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(sum.Verdicts))
	}
	return sum.Verdicts[0]
}

// cleanRun fills the grid with samples that regress on nothing: identical
// rounds, cost and tool outcomes on both sides.
func (f *pairFixture) cleanRun(t *testing.T) {
	t.Helper()
	for _, task := range f.tasks {
		for k := 0; k < f.run.K; k++ {
			for _, v := range f.variants {
				f.addSample(t, Sample{
					VariantID: v.ID, TaskID: task.ID, KIndex: k,
					Response: "an answer", Rounds: 2, Cost: 0.10, LatencyMs: 1000,
					OutcomeOK: 10,
				})
			}
		}
	}
}

// letterFor returns the presented letter that means `variant` on this item —
// the inverse of the unblinding the summary does.
func letterFor(assign Assignment, order string, variant int64) string {
	for _, w := range []string{WinnerA, WinnerB} {
		if VariantFor(assign, order, w) == variant {
			return w
		}
	}
	return WinnerTie
}

// judgeRun records a judge verdict on every item, with the winning variant
// chosen per pair by the caller. Returning 0 records a tie.
func (f *pairFixture) judgeRun(t *testing.T, winner func(p Pair) int64) {
	t.Helper()
	ctx := context.Background()
	pairs, err := f.store.ListPairs(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListPairs: %v", err)
	}
	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	byPair := make(map[int64]Pair, len(pairs))
	for _, p := range pairs {
		byPair[p.ID] = p
	}
	for _, it := range items {
		p := byPair[it.PairID]
		assign, err := DecodeAssignment(p.Assignment)
		if err != nil {
			t.Fatalf("DecodeAssignment: %v", err)
		}
		w := WinnerTie
		if target := winner(p); target != 0 {
			w = letterFor(assign, it.PresentationOrder, target)
		}
		if _, err := f.store.RecordVerdict(ctx, Verdict{
			ItemID: it.ID, Winner: w, JudgeIdent: "claude-code",
		}); err != nil {
			t.Fatalf("RecordVerdict: %v", err)
		}
	}
}

func TestVerdict_NoRegressionsWhenNothingJudged(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat, CategoryToolHeavy})
	f.cleanRun(t)
	f.createPairs(t)

	vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
	if vv.Verdict != VerdictNoRegressions {
		t.Fatalf("verdict = %q (%s), want %q", vv.Verdict, vv.Reason, VerdictNoRegressions)
	}
	if vv.Judgment.Pairs != 2 || vv.Judgment.JudgedPairs != 0 {
		t.Errorf("judgment = %+v, want 2 pairs and 0 judged", vv.Judgment)
	}
	if !strings.Contains(vv.Reason, "judgment pending") {
		t.Errorf("reason %q does not say judging is outstanding", vv.Reason)
	}
	// Gates alone can never promote a candidate.
	if vv.Verdict == VerdictUpgrade {
		t.Error("objective gates alone declared an upgrade")
	}
}

func TestVerdict_UpgradeNeedsWinRateAndCleanGates(t *testing.T) {
	f := newPairFixture(t, 2, []string{CategoryChat, CategoryToolHeavy})
	f.cleanRun(t)
	f.createPairs(t)
	cand := f.variants[1].ID
	f.judgeRun(t, func(Pair) int64 { return cand })

	vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
	if vv.Verdict != VerdictUpgrade {
		t.Fatalf("verdict = %q (%s), want %q", vv.Verdict, vv.Reason, VerdictUpgrade)
	}
	if vv.Judgment.Wins != 4 || vv.Judgment.WinRate != 1 {
		t.Errorf("judgment = %+v, want 4 wins at a win-rate of 1", vv.Judgment)
	}
	for _, g := range vv.Gates {
		if !g.Pass {
			t.Errorf("gate %s failed on an identical-metrics run: %+v", g.Name, g)
		}
	}
}

func TestVerdict_WinRateBelowThresholdIsNotAnUpgrade(t *testing.T) {
	f := newPairFixture(t, 2, []string{CategoryChat, CategoryToolHeavy})
	f.cleanRun(t)
	f.createPairs(t)
	base, cand := f.variants[0].ID, f.variants[1].ID

	// Two of four pairs to the candidate: 0.5, just under the 0.55 threshold.
	// Keyed by pair, since the callback fires once per presentation order.
	pairs, err := f.store.ListPairs(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("ListPairs: %v", err)
	}
	toCandidate := map[int64]bool{pairs[0].ID: true, pairs[1].ID: true}
	f.judgeRun(t, func(p Pair) int64 {
		if toCandidate[p.ID] {
			return cand
		}
		return base
	})

	vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
	if vv.Verdict != VerdictDowngrade {
		t.Fatalf("verdict = %q (%s), want %q", vv.Verdict, vv.Reason, VerdictDowngrade)
	}
	if vv.Judgment.WinRate != 0.5 {
		t.Errorf("win rate = %v, want 0.5", vv.Judgment.WinRate)
	}
	if !strings.Contains(vv.Reason, "win-rate") {
		t.Errorf("reason %q does not name the win-rate as the cause", vv.Reason)
	}
}

// A judge that names the same *presented* side in both orders was tracking
// position, not quality. The pair records a tie.
func TestVerdict_BothOrderDisagreementRecordsATie(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat, CategoryToolHeavy})
	ctx := context.Background()
	f.cleanRun(t)
	f.createPairs(t)

	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range items {
		if _, err := f.store.RecordVerdict(ctx, Verdict{
			ItemID: it.ID, Winner: WinnerA, JudgeIdent: "claude-code",
		}); err != nil {
			t.Fatalf("RecordVerdict: %v", err)
		}
	}

	vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
	if vv.Judgment.JudgedPairs != 2 {
		t.Fatalf("judged pairs = %d, want 2", vv.Judgment.JudgedPairs)
	}
	if vv.Judgment.Ties != 2 || vv.Judgment.Wins != 0 || vv.Judgment.Losses != 0 {
		t.Errorf("judgment = %+v, want 2 ties and nothing else", vv.Judgment)
	}
}

// A pair with only one of its two orders judged is not evidence and must not
// reach the tally.
func TestVerdict_HalfJudgedPairIsNotCounted(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat})
	ctx := context.Background()
	f.cleanRun(t)
	f.createPairs(t)

	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if _, err := f.store.RecordVerdict(ctx, Verdict{
		ItemID: items[0].ID, Winner: WinnerA, JudgeIdent: "claude-code",
	}); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}

	vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
	if vv.Judgment.JudgedPairs != 0 {
		t.Errorf("judged pairs = %d on a half-judged pair, want 0", vv.Judgment.JudgedPairs)
	}
	if vv.Verdict != VerdictNoRegressions {
		t.Errorf("verdict = %q, want %q while judging is incomplete", vv.Verdict, VerdictNoRegressions)
	}
}

// Each gate must be able to fail on its own, and a failed gate must name itself
// in the reason line — the point of showing the work.
func TestVerdict_EachGateFailsAloneAndIsNamed(t *testing.T) {
	cases := []struct {
		name     string
		gate     string
		mutate   func(smp *Sample)
		wantWord string
	}{
		{
			name: "rejected rate", gate: GateRejectedRate, wantWord: "rejected tool-call rate",
			mutate: func(smp *Sample) { smp.OutcomeOK, smp.OutcomeRejected = 9, 1 },
		},
		{
			name: "mean rounds", gate: GateMeanRounds, wantWord: "mean rounds",
			mutate: func(smp *Sample) { smp.Rounds = 3 },
		},
		{
			name: "cost per task", gate: GateCostPerTask, wantWord: "cost per task",
			mutate: func(smp *Sample) { smp.Cost = 0.16 },
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newPairFixture(t, 1, []string{CategoryChat, CategoryToolHeavy})
			for _, task := range f.tasks {
				for _, v := range f.variants {
					smp := Sample{
						VariantID: v.ID, TaskID: task.ID, KIndex: 0,
						Response: "an answer", Rounds: 2, Cost: 0.10, LatencyMs: 1000,
						OutcomeOK: 10,
					}
					if v.ID == f.variants[1].ID {
						c.mutate(&smp)
					}
					f.addSample(t, smp)
				}
			}
			f.createPairs(t)
			// Judged a clean sweep, so only the gate can produce a downgrade.
			f.judgeRun(t, func(Pair) int64 { return f.variants[1].ID })

			vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
			if vv.Verdict != VerdictDowngrade {
				t.Fatalf("verdict = %q (%s), want %q despite a perfect win-rate",
					vv.Verdict, vv.Reason, VerdictDowngrade)
			}
			if !strings.Contains(vv.Reason, c.wantWord) {
				t.Errorf("reason %q does not name the failing gate %q", vv.Reason, c.wantWord)
			}
			var failed []string
			for _, g := range vv.Gates {
				if !g.Pass {
					failed = append(failed, g.Name)
				}
			}
			if len(failed) != 1 || failed[0] != c.gate {
				t.Errorf("failed gates = %v, want exactly [%s]", failed, c.gate)
			}
		})
	}
}

func TestVerdict_ThinDataIsInconclusive(t *testing.T) {
	// Half the grid never landed: 2 of 4 expected samples, under the 0.8 floor.
	f := newPairFixture(t, 2, []string{CategoryChat})
	inc, cand := f.variants[0], f.variants[1]
	f.addSample(t, Sample{VariantID: inc.ID, TaskID: f.tasks[0].ID, KIndex: 0,
		Response: "an answer", Rounds: 2, Cost: 0.1, OutcomeOK: 10})
	f.addSample(t, Sample{VariantID: cand.ID, TaskID: f.tasks[0].ID, KIndex: 0,
		Response: "an answer", Rounds: 2, Cost: 0.1, OutcomeOK: 10})
	f.createPairs(t)
	f.judgeRun(t, func(Pair) int64 { return cand.ID })

	sum := f.summarizeWith(t, testOpts())
	vv := f.onlyVerdict(t, sum)
	if vv.Verdict != VerdictInconclusive {
		t.Fatalf("verdict = %q (%s), want %q", vv.Verdict, vv.Reason, VerdictInconclusive)
	}
	if !strings.Contains(vv.Reason, "completeness floor") {
		t.Errorf("reason %q does not name thin data as the cause", vv.Reason)
	}
	// The pair count sits next to the completeness figure so the hole in the
	// judging grid is visible too.
	if sum.Completeness.Pairs != 1 || sum.Completeness.PairsJudged != 1 {
		t.Errorf("completeness = %+v, want 1 pair, 1 judged", sum.Completeness)
	}
}

func TestVerdict_OperatorAgreementIsReported(t *testing.T) {
	f := newPairFixture(t, 2, []string{CategoryChat})
	ctx := context.Background()
	f.cleanRun(t)
	f.createPairs(t)
	f.judgeRun(t, func(Pair) int64 { return f.variants[1].ID })

	items, err := f.store.ListItems(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	judged, err := f.store.ListVerdicts(ctx, f.run.ID)
	if err != nil {
		t.Fatalf("ListVerdicts: %v", err)
	}
	byItem := make(map[int64]Verdict, len(judged))
	for _, v := range judged {
		byItem[v.ItemID] = v
	}
	// Agree on three of four calibration items.
	for i, it := range items {
		winner := byItem[it.ID].Winner
		if i == 0 {
			winner = WinnerTie
		}
		if _, err := f.store.RecordVerdict(ctx, Verdict{
			ItemID: it.ID, Winner: winner, JudgeIdent: JudgeOperator,
		}); err != nil {
			t.Fatalf("RecordVerdict(operator): %v", err)
		}
	}

	vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
	ag := vv.Judgment.OperatorAgreement
	if ag == nil {
		t.Fatal("no operator agreement figure reported")
	}
	if ag.Items != 4 || ag.Agreed != 3 || ag.Rate != 0.75 {
		t.Errorf("agreement = %+v, want 3 of 4 at 0.75", *ag)
	}
}

// A candidate that wins overall while losing a category must say so: the
// rolled-up number is exactly what hides a tool-heavy regression.
func TestVerdict_CategoryDivergenceIsSurfaced(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat, CategoryChat, CategoryChat, CategoryToolHeavy})
	f.cleanRun(t)
	f.createPairs(t)
	base, cand := f.variants[0].ID, f.variants[1].ID
	toolHeavy := f.tasks[3].ID
	f.judgeRun(t, func(p Pair) int64 {
		if p.TaskID == toolHeavy {
			return base
		}
		return cand
	})

	vv := f.onlyVerdict(t, f.summarizeWith(t, testOpts()))
	if vv.Verdict != VerdictUpgrade {
		t.Fatalf("verdict = %q (%s), want %q at a 0.75 win-rate", vv.Verdict, vv.Reason, VerdictUpgrade)
	}
	if !strings.Contains(vv.Divergence, CategoryToolHeavy) {
		t.Errorf("divergence = %q, want it to name %s", vv.Divergence, CategoryToolHeavy)
	}

	var chat, heavy *CategoryResult
	for i := range vv.Categories {
		switch vv.Categories[i].Category {
		case CategoryChat:
			chat = &vv.Categories[i]
		case CategoryToolHeavy:
			heavy = &vv.Categories[i]
		}
	}
	if chat == nil || heavy == nil {
		t.Fatalf("categories = %+v, want both chat and tool_heavy", vv.Categories)
	}
	if chat.WinRate != 1 || chat.Regressed {
		t.Errorf("chat = %+v, want a clean sweep", *chat)
	}
	if heavy.WinRate != 0 || !heavy.Regressed {
		t.Errorf("tool_heavy = %+v, want it flagged as regressed", *heavy)
	}
}

// A summary built with a zero-valued options struct must fall back to the
// shipped defaults: a zero threshold would fail every gate.
func TestSummaryOpts_ZeroValueUsesShippedDefaults(t *testing.T) {
	f := newPairFixture(t, 1, []string{CategoryChat, CategoryToolHeavy})
	f.cleanRun(t)
	f.createPairs(t)

	vv := f.onlyVerdict(t, f.summarizeWith(t, SummaryOpts{}))
	if vv.Judgment.WinThreshold != 0.55 {
		t.Errorf("win threshold = %v, want the 0.55 default", vv.Judgment.WinThreshold)
	}
	for _, g := range vv.Gates {
		if g.Threshold <= 0 {
			t.Errorf("gate %s has threshold %v — a zero would fail every run", g.Name, g.Threshold)
		}
	}
	if vv.Verdict != VerdictNoRegressions {
		t.Errorf("verdict = %q (%s), want %q", vv.Verdict, vv.Reason, VerdictNoRegressions)
	}
}
