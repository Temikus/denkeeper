package eval

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// The operator's unblinded view of a run's judging grid.
//
// This is the deliberate counterpart to GetBlindedItem: the judge sees two
// responses with every identity hint stripped, and the operator reading the
// results afterwards sees who won. It exists because the results view has to
// render "judged pairs with the judge's dimension scores and notes"
// (design/eval-subsystem.md §6 layer 3) and the only pair reader before it was
// the blinded one.
//
// It is REST-only by design: nothing here may be advertised on the judge's MCP
// surface, since a judge that can call it can unblind its own queue.

// PairSide is one side of a pair once the assignment is applied.
type PairSide struct {
	VariantID int64  `json:"variant_id"`
	Variant   string `json:"variant"`
	SampleID  int64  `json:"sample_id"`
}

// PairVerdict is one recorded call on one item, unblinded.
type PairVerdict struct {
	// JudgeIdent names who judged. JudgeOperator marks a calibration call: it
	// is listed here but never drives the pair's outcome.
	JudgeIdent string `json:"judge_ident"`
	// Winner is the presented letter the judge named — what it actually saw.
	Winner string `json:"winner"`
	// WinnerVariant is that letter resolved through the item's presentation
	// order and the pair's assignment. Empty on a tie.
	WinnerVariant string `json:"winner_variant,omitempty"`
	// Dimensions is the stored per-dimension map, omitted when the judge
	// recorded none or the stored value will not decode. Values are the
	// presented letters — what the judge actually saw — and are kept so an
	// audit can check the call against the queue it was answering.
	Dimensions map[string]string `json:"dimensions,omitempty"`
	// DimensionsVariant is Dimensions with every letter resolved through the
	// item's presentation order and the pair's assignment, ties preserved as
	// "tie". Same key set as Dimensions; a value that is neither a letter nor
	// a tie is carried through unchanged rather than dropped.
	DimensionsVariant map[string]string `json:"dimensions_variant,omitempty"`
	Notes         string            `json:"notes,omitempty"`
	RubricVersion string            `json:"rubric_version,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// PairItem is one presentation order of a pair with the verdicts against it.
type PairItem struct {
	ItemID            int64         `json:"item_id"`
	PresentationOrder string        `json:"presentation_order"`
	Status            string        `json:"status"`
	Verdicts          []PairVerdict `json:"verdicts"`
}

// PairDetail is one pair, unblinded, with its resolved outcome.
type PairDetail struct {
	PairID     int64  `json:"pair_id"`
	TaskID     int64  `json:"task_id"`
	TaskPrompt string `json:"task_prompt"`
	Category   string `json:"category"`
	// K is the pair's sample index within the task, so a k > 1 run is readable.
	K         int        `json:"k"`
	Baseline  PairSide   `json:"baseline"`
	Candidate PairSide   `json:"candidate"`
	Items     []PairItem `json:"items"`
	// Outcome is win/loss/tie/pending from the candidate's point of view.
	Outcome string `json:"outcome"`
}

// PairView is a run's whole judging grid, unblinded.
type PairView struct {
	RunID int64 `json:"run_id"`
	// BaselineVariant names the incumbent every pair is measured against.
	BaselineVariant string       `json:"baseline_variant"`
	Pairs           []PairDetail `json:"pairs"`
}

// PairDetails returns a run's pairs with their verdicts and resolved outcomes,
// optionally narrowed to one task (taskID 0 = every task).
//
// Outcomes come from resolvePairs, the same resolver the win-rate is tallied
// from, so a pair this view calls a tie is a tie in the verdict too. Deriving
// the both-orders-must-agree rule a second time here is exactly how the two
// views would drift apart.
func (s *Store) PairDetails(ctx context.Context, runID, taskID int64) (*PairView, error) {
	in, tasks, err := s.loadPairView(ctx, runID)
	if err != nil {
		return nil, err
	}
	view := &PairView{RunID: runID, Pairs: []PairDetail{}}
	if len(in.variants) < 2 {
		return view, nil
	}
	baseline := in.variants[0]
	view.BaselineVariant = baseline.Name

	names := make(map[int64]string, len(in.variants))
	for _, v := range in.variants {
		names[v.ID] = v.Name
	}
	byTask := make(map[int64]Task, len(tasks))
	for _, t := range tasks {
		byTask[t.ID] = t
	}
	outcomes := make(map[int64]pairOutcome, len(in.pairs))
	for _, po := range resolvePairs(in) {
		outcomes[po.pairID] = po
	}
	itemsByPair := groupItems(in.items)
	verdictsByPairItem := groupVerdicts(in.verdicts)

	for _, p := range in.pairs {
		if taskID > 0 && p.TaskID != taskID {
			continue
		}
		po, ok := outcomes[p.ID]
		if !ok {
			// resolvePairs drops a pair whose assignment will not decode. It
			// cannot be unblinded, so it is left out here too rather than shown
			// with a guessed outcome.
			continue
		}
		view.Pairs = append(view.Pairs, buildPairDetail(pairDetailInput{
			pair:     p,
			outcome:  po,
			baseline: baseline.ID,
			names:    names,
			task:     byTask[p.TaskID],
			items:    itemsByPair[p.ID],
			verdicts: verdictsByPairItem,
		}))
	}
	return view, nil
}

// loadPairView reads the grid in one place, so a pair's outcome and its listed
// verdicts always come from the same snapshot. Samples are deliberately not
// loaded: their traces are large, and the results view fetches them from
// /samples when a row is expanded.
func (s *Store) loadPairView(ctx context.Context, runID int64) (verdictInput, []Task, error) {
	var in verdictInput
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return in, nil, err
	}
	if in.variants, err = s.ListVariants(ctx, runID); err != nil {
		return in, nil, err
	}
	if in.pairs, err = s.ListPairs(ctx, runID); err != nil {
		return in, nil, err
	}
	if in.items, err = s.ListItems(ctx, runID); err != nil {
		return in, nil, err
	}
	if in.verdicts, err = s.ListVerdicts(ctx, runID); err != nil {
		return in, nil, err
	}
	// RunTasks, not ListTasks: a run's pairs only ever cover the task list
	// pinned at creation, and every other reader resolves it through here.
	tasks, err := s.RunTasks(ctx, run)
	if err != nil {
		return in, nil, err
	}
	return in, tasks, nil
}

// pairDetailInput bundles one pair's row-building inputs; they travel together
// and a positional call would be seven arguments of the same two types.
type pairDetailInput struct {
	pair     Pair
	outcome  pairOutcome
	baseline int64
	names    map[int64]string
	task     Task
	items    []JudgmentItem
	verdicts map[int64][]Verdict
}

func buildPairDetail(in pairDetailInput) PairDetail {
	p := in.pair
	assign, _ := DecodeAssignment(p.Assignment)
	detail := PairDetail{
		PairID:     p.ID,
		TaskID:     p.TaskID,
		TaskPrompt: in.task.Prompt,
		Category:   in.task.Category,
		K:          p.KIndex,
		Baseline:   pairSide(p, assign, in.baseline, in.names),
		Candidate:  pairSide(p, assign, in.outcome.candidate, in.names),
		Items:      []PairItem{},
		Outcome:    in.outcome.outcome(in.baseline),
	}
	for _, it := range in.items {
		detail.Items = append(detail.Items, PairItem{
			ItemID:            it.ID,
			PresentationOrder: it.PresentationOrder,
			Status:            it.Status,
			Verdicts:          pairVerdicts(assign, it, in.verdicts[it.ID], in.names),
		})
	}
	return detail
}

// pairSide names one variant's side of a pair and the sample it produced.
func pairSide(p Pair, a Assignment, variantID int64, names map[int64]string) PairSide {
	side := PairSide{VariantID: variantID, Variant: names[variantID]}
	switch variantID {
	case a.A:
		side.SampleID = p.SampleA
	case a.B:
		side.SampleID = p.SampleB
	}
	return side
}

func pairVerdicts(a Assignment, it JudgmentItem, rows []Verdict, names map[int64]string) []PairVerdict {
	out := make([]PairVerdict, 0, len(rows))
	for _, v := range rows {
		pv := PairVerdict{
			JudgeIdent:    v.JudgeIdent,
			Winner:        v.Winner,
			Notes:         v.Notes,
			RubricVersion: v.RubricVersion,
			CreatedAt:     v.CreatedAt,
		}
		if id := VariantFor(a, it.PresentationOrder, v.Winner); id != 0 {
			pv.WinnerVariant = names[id]
		}
		var dims map[string]string
		if err := json.Unmarshal([]byte(v.Dimensions), &dims); err == nil && len(dims) > 0 {
			pv.Dimensions = dims
			pv.DimensionsVariant = resolveDimensions(a, it.PresentationOrder, dims, names)
		}
		out = append(out, pv)
	}
	return out
}

// resolveDimensions names the variant behind each per-dimension letter, using
// the same VariantFor resolver the overall winner goes through. A tie stays a
// tie (there is no variant to name), and anything the judge wrote that is
// neither a letter nor a tie is passed through: a value this cannot read is
// still the judge's own answer, and dropping it would hide it from the reader.
func resolveDimensions(a Assignment, order string, dims map[string]string, names map[int64]string) map[string]string {
	out := make(map[string]string, len(dims))
	for dim, raw := range dims {
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case WinnerTie:
			out[dim] = WinnerTie
		case WinnerA, WinnerB:
			if name := names[VariantFor(a, order, value)]; name != "" {
				out[dim] = name
			} else {
				out[dim] = raw
			}
		default:
			out[dim] = raw
		}
	}
	return out
}

func groupItems(items []JudgmentItem) map[int64][]JudgmentItem {
	out := make(map[int64][]JudgmentItem, len(items))
	for _, it := range items {
		out[it.PairID] = append(out[it.PairID], it)
	}
	return out
}

func groupVerdicts(verdicts []Verdict) map[int64][]Verdict {
	out := make(map[int64][]Verdict, len(verdicts))
	for _, v := range verdicts {
		out[v.ItemID] = append(out[v.ItemID], v)
	}
	return out
}
