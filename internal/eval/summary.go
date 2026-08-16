package eval

import "context"

// Stop-reason slugs that mean the tool loop ran out of road rather than the
// model finishing. These are TurnResult.StopReason's machine-readable slugs,
// never the prose loopStopReason.String() forms — the two renderings are
// deliberately separate and must not be conflated here.
const (
	stopRepeatedCalls = "repeated_calls"
	stopMaxRounds     = "max_rounds"
)

// VariantMetrics is one variant's objective scorecard, computed over its
// status-ok samples.
type VariantMetrics struct {
	VariantID int64   `json:"variant_id"`
	Name      string  `json:"name"`
	Overlay   Overlay `json:"overlay"`

	// RejectedRate and FailedRate are tool-call level: the denominator is
	// ok+rejected+failed+denied. Cached and suppressed calls are excluded
	// because nothing executed, so counting them would dilute both rates with
	// non-events.
	RejectedRate float64 `json:"rejected_rate"`
	FailedRate   float64 `json:"failed_rate"`
	// ToolCalls is that denominator, so a reader can tell a 0 % rate over 200
	// calls from a 0 % rate over none.
	ToolCalls int `json:"tool_calls"`

	MeanRounds float64 `json:"mean_rounds"`
	// WrapupCount is samples whose loop was cut short by repeated identical
	// calls or by exhausting the round budget — the "flaily" signal.
	WrapupCount     int     `json:"wrapup_count"`
	MeanCostPerTask float64 `json:"mean_cost_per_task"`
	MeanLatencyMs   float64 `json:"mean_latency_ms"`
	TotalCost       float64 `json:"total_cost"`

	SamplesOK     int `json:"samples_ok"`
	SamplesFailed int `json:"samples_failed"`
}

// TaskVariantMetrics is one cell of the per-task breakdown.
type TaskVariantMetrics struct {
	VariantID   int64   `json:"variant_id"`
	Name        string  `json:"name"`
	SamplesOK   int     `json:"samples_ok"`
	MeanCost    float64 `json:"mean_cost"`
	MeanRounds  float64 `json:"mean_rounds"`
	MeanLatency float64 `json:"mean_latency_ms"`
	// Deltas are against the baseline variant and are zero on the baseline
	// row itself.
	DeltaCost    float64 `json:"delta_cost"`
	DeltaRounds  float64 `json:"delta_rounds"`
	DeltaLatency float64 `json:"delta_latency_ms"`
}

// TaskMetrics groups one task's per-variant cells.
type TaskMetrics struct {
	TaskID   int64                `json:"task_id"`
	Prompt   string               `json:"prompt"`
	Category string               `json:"category"`
	Variants []TaskVariantMetrics `json:"variants"`
}

// Completeness reports how much of the run actually landed. A run that
// finishes below the floor still reports its numbers — partial results are the
// point of the capped and stopped statuses — but says they are inconclusive
// rather than dressing thin data as a verdict.
type Completeness struct {
	SamplesOK       int     `json:"samples_ok"`
	SamplesExpected int     `json:"samples_expected"`
	Ratio           float64 `json:"ratio"`
	Floor           float64 `json:"floor"`
	Conclusive      bool    `json:"conclusive"`
}

// Summary is the objective scorecard for one run — everything computable
// without a judge.
type Summary struct {
	RunID     int64   `json:"run_id"`
	Status    string  `json:"status"`
	BaseAgent string  `json:"base_agent"`
	TaskSet   string  `json:"task_set"`
	K         int     `json:"k"`
	CostCap   float64 `json:"cost_cap"`
	CostSpent float64 `json:"cost_spent"`
	// BaselineVariant names the variant per-task deltas are measured against:
	// the first by creation order. This is a convention (the incumbent is
	// created first), not something the API enforces.
	BaselineVariant string           `json:"baseline_variant"`
	Variants        []VariantMetrics `json:"variants"`
	PerTask         []TaskMetrics    `json:"per_task"`
	Completeness    Completeness     `json:"completeness"`
}

// Summarize aggregates a run's samples in Go rather than SQL. A run holds at
// most a few hundred samples, and the arithmetic is far easier to test as
// ordinary code than as window functions.
func (s *Store) Summarize(ctx context.Context, runID int64, floor float64) (*Summary, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	variants, err := s.ListVariants(ctx, runID)
	if err != nil {
		return nil, err
	}
	tasks, err := s.ListTasks(ctx, run.TaskSetID)
	if err != nil {
		return nil, err
	}
	samples, err := s.ListSamples(ctx, runID)
	if err != nil {
		return nil, err
	}
	set, err := s.GetTaskSetByID(ctx, run.TaskSetID)
	if err != nil {
		return nil, err
	}

	sum := &Summary{
		RunID:     run.ID,
		Status:    run.Status,
		BaseAgent: run.BaseAgent,
		TaskSet:   set.Name,
		K:         run.K,
		CostCap:   run.CostCap,
		CostSpent: run.CostSpent,
	}
	if len(variants) > 0 {
		sum.BaselineVariant = variants[0].Name
	}
	sum.Variants = variantMetrics(variants, samples)
	sum.PerTask = taskMetrics(tasks, variants, samples)
	sum.Completeness = completeness(samples, len(tasks)*len(variants)*run.K, floor)
	return sum, nil
}

// variantAcc accumulates one variant's ok samples.
type variantAcc struct {
	okSamples  int
	failed     int
	rounds     int
	wrapups    int
	latency    int64
	cost       float64
	tools      int
	rejected   int
	toolFailed int
	taskIDs    map[int64]struct{}
}

func variantMetrics(variants []Variant, samples []Sample) []VariantMetrics {
	accs := make(map[int64]*variantAcc, len(variants))
	for _, v := range variants {
		accs[v.ID] = &variantAcc{taskIDs: make(map[int64]struct{})}
	}
	for _, smp := range samples {
		acc, ok := accs[smp.VariantID]
		if !ok {
			continue
		}
		if smp.Status != SampleOK {
			acc.failed++
			continue
		}
		acc.okSamples++
		acc.rounds += smp.Rounds
		acc.latency += smp.LatencyMs
		acc.cost += smp.Cost
		acc.taskIDs[smp.TaskID] = struct{}{}
		if smp.StopReason == stopRepeatedCalls || smp.StopReason == stopMaxRounds {
			acc.wrapups++
		}
		acc.tools += smp.OutcomeOK + smp.OutcomeRejected + smp.OutcomeFailed + smp.OutcomeDenied
		acc.rejected += smp.OutcomeRejected
		acc.toolFailed += smp.OutcomeFailed
	}

	out := make([]VariantMetrics, 0, len(variants))
	for _, v := range variants {
		acc := accs[v.ID]
		ov, _ := DecodeOverlay(v.Overlay)
		m := VariantMetrics{
			VariantID:     v.ID,
			Name:          v.Name,
			Overlay:       ov,
			ToolCalls:     acc.tools,
			WrapupCount:   acc.wrapups,
			TotalCost:     acc.cost,
			SamplesOK:     acc.okSamples,
			SamplesFailed: acc.failed,
		}
		if acc.tools > 0 {
			m.RejectedRate = float64(acc.rejected) / float64(acc.tools)
			m.FailedRate = float64(acc.toolFailed) / float64(acc.tools)
		}
		if acc.okSamples > 0 {
			m.MeanRounds = float64(acc.rounds) / float64(acc.okSamples)
			m.MeanLatencyMs = float64(acc.latency) / float64(acc.okSamples)
		}
		if n := len(acc.taskIDs); n > 0 {
			m.MeanCostPerTask = acc.cost / float64(n)
		}
		out = append(out, m)
	}
	return out
}

// cellAcc accumulates one (task, variant) cell.
type cellAcc struct {
	n       int
	cost    float64
	rounds  int
	latency int64
}

func (c cellAcc) means() (cost, rounds, latency float64) {
	if c.n == 0 {
		return 0, 0, 0
	}
	n := float64(c.n)
	return c.cost / n, float64(c.rounds) / n, float64(c.latency) / n
}

type cellKey struct {
	task    int64
	variant int64
}

func taskMetrics(tasks []Task, variants []Variant, samples []Sample) []TaskMetrics {
	cells := make(map[cellKey]*cellAcc)
	for _, smp := range samples {
		if smp.Status != SampleOK {
			continue
		}
		key := cellKey{task: smp.TaskID, variant: smp.VariantID}
		acc, ok := cells[key]
		if !ok {
			acc = &cellAcc{}
			cells[key] = acc
		}
		acc.n++
		acc.cost += smp.Cost
		acc.rounds += smp.Rounds
		acc.latency += smp.LatencyMs
	}

	out := make([]TaskMetrics, 0, len(tasks))
	for _, task := range tasks {
		tm := TaskMetrics{TaskID: task.ID, Prompt: task.Prompt, Category: task.Category}
		var baseCost, baseRounds, baseLatency float64
		for i, v := range variants {
			acc := cells[cellKey{task: task.ID, variant: v.ID}]
			var cell cellAcc
			if acc != nil {
				cell = *acc
			}
			cost, rounds, latency := cell.means()
			if i == 0 {
				baseCost, baseRounds, baseLatency = cost, rounds, latency
			}
			tvm := TaskVariantMetrics{
				VariantID:   v.ID,
				Name:        v.Name,
				SamplesOK:   cell.n,
				MeanCost:    cost,
				MeanRounds:  rounds,
				MeanLatency: latency,
			}
			if i > 0 {
				tvm.DeltaCost = cost - baseCost
				tvm.DeltaRounds = rounds - baseRounds
				tvm.DeltaLatency = latency - baseLatency
			}
			tm.Variants = append(tm.Variants, tvm)
		}
		out = append(out, tm)
	}
	return out
}

func completeness(samples []Sample, expected int, floor float64) Completeness {
	c := Completeness{SamplesExpected: expected, Floor: floor}
	for _, smp := range samples {
		if smp.Status == SampleOK {
			c.SamplesOK++
		}
	}
	if expected > 0 {
		c.Ratio = float64(c.SamplesOK) / float64(expected)
	}
	c.Conclusive = expected > 0 && c.Ratio >= floor
	return c
}
