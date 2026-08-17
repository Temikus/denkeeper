package eval

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// CreatePairs is the run-finalization step that turns completed samples into
// blinded judgment work. It returns how many pairs it created.
//
// It runs for capped and stopped runs as well as done ones, since partial
// results are the whole point of those statuses — a run that spent real money
// before hitting its cap should still be judgeable on what it produced. A
// (task, k) whose sample is missing or failed on either side yields no pair;
// the count is reported next to the completeness figure so a reader can see how
// much of the grid survived.
//
// Pairing policy for more than two variants: every non-baseline variant is
// paired against the baseline (the first variant by creation order, the same
// convention per-task deltas use). N−1 pair sets rather than a round-robin,
// because the question the decision rule answers is "is this candidate an
// upgrade on the incumbent", and no consumer reads candidate-vs-candidate.
//
// Idempotent by guard: a run that already has pairs is left alone, so a retried
// finalization cannot double the queue.
func (s *Store) CreatePairs(ctx context.Context, runID int64) (int, error) {
	existing, err := s.CountPairs(ctx, runID)
	if err != nil {
		return 0, err
	}
	if existing > 0 {
		return 0, nil
	}
	grid, err := s.loadPairingGrid(ctx, runID)
	if err != nil {
		return 0, err
	}
	planned := planPairs(runID, grid)
	if len(planned) == 0 {
		return 0, nil
	}
	return len(planned), s.insertPairs(ctx, planned)
}

// pairingGrid is the completed-sample grid one run's pairing is planned over.
type pairingGrid struct {
	tasks    []Task
	variants []Variant
	k        int
	// samples indexes only ok samples: a failed one has no response to compare.
	samples map[sampleKey]int64
}

type sampleKey struct {
	task    int64
	k       int
	variant int64
}

func (s *Store) loadPairingGrid(ctx context.Context, runID int64) (pairingGrid, error) {
	var g pairingGrid
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return g, err
	}
	if g.variants, err = s.ListVariants(ctx, runID); err != nil {
		return g, err
	}
	if g.tasks, err = s.ListTasks(ctx, run.TaskSetID); err != nil {
		return g, err
	}
	samples, err := s.ListSamples(ctx, runID)
	if err != nil {
		return g, err
	}
	g.k = run.K
	g.samples = make(map[sampleKey]int64, len(samples))
	for _, smp := range samples {
		if smp.Status != SampleOK {
			continue
		}
		g.samples[sampleKey{task: smp.TaskID, k: smp.KIndex, variant: smp.VariantID}] = smp.ID
	}
	return g, nil
}

// planPairs walks the grid and emits one pair per (task, k, candidate) cell
// that completed on both the candidate and the baseline.
func planPairs(runID int64, g pairingGrid) []Pair {
	if len(g.variants) < 2 {
		return nil
	}
	baseline := g.variants[0]
	out := make([]Pair, 0, len(g.tasks)*g.k*(len(g.variants)-1))
	for _, task := range g.tasks {
		for k := 0; k < g.k; k++ {
			baseID, ok := g.samples[sampleKey{task: task.ID, k: k, variant: baseline.ID}]
			if !ok {
				continue
			}
			for _, cand := range g.variants[1:] {
				candID, ok := g.samples[sampleKey{task: task.ID, k: k, variant: cand.ID}]
				if !ok {
					continue
				}
				out = append(out, buildPair(runID, task.ID, k, baseline.ID, baseID, cand.ID, candID))
			}
		}
	}
	return out
}

// buildPair flips the coin that decides which side is shown as Response A.
// Per-pair rather than per-run: a single run-wide flip would let a judge that
// noticed one pair's identity carry it across the whole queue.
//
// The coin comes from crypto/rand, not math/rand: the assignment is the
// unblinding key, and a judge able to reproduce a seeded sequence would be able
// to reproduce the whole run's blinding. One bit per pair is not worth being
// clever about.
func buildPair(runID, taskID int64, k int, baseVariant, baseSample, candVariant, candSample int64) Pair {
	p := Pair{RunID: runID, TaskID: taskID, KIndex: k}
	assign := Assignment{}
	if coinFlip() {
		p.SampleA, p.SampleB = baseSample, candSample
		assign.A, assign.B = baseVariant, candVariant
	} else {
		p.SampleA, p.SampleB = candSample, baseSample
		assign.A, assign.B = candVariant, baseVariant
	}
	raw, _ := json.Marshal(assign)
	p.Assignment = string(raw)
	return p
}

// coinFlip returns one unbiased bit. A crypto/rand failure is not survivable
// here — silently falling back to a fixed side would blind nothing while
// looking like it did — so it panics, matching how the rest of the codebase
// treats an impossible primitive failure.
func coinFlip() bool { return randIndex(2) == 0 }

// randIndex returns a uniform value in [0, n). The whole package draws through
// here so there is one randomness source to reason about, rather than a
// security-grade one for the blinding coin and a seeded one next to it.
func randIndex(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(fmt.Sprintf("eval: reading randomness: %v", err))
	}
	return int(v.Int64())
}

// insertPairs writes the pairs and their two judgment items in one transaction:
// a pair without its items would be invisible work.
func (s *Store) insertPairs(ctx context.Context, pairs []Pair) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("creating pairs: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	for _, p := range pairs {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO eval_pairs (run_id, task_id, k_index, sample_a, sample_b, assignment, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			p.RunID, p.TaskID, p.KIndex, p.SampleA, p.SampleB, p.Assignment, now)
		if err != nil {
			return fmt.Errorf("creating pair for run %d: %w", p.RunID, err)
		}
		pairID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("creating pair for run %d: %w", p.RunID, err)
		}
		for _, order := range []string{OrderAB, OrderBA} {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO eval_judgment_items (pair_id, presentation_order, status, created_at)
				 VALUES (?, ?, ?, ?)`, pairID, order, ItemPending, now); err != nil {
				return fmt.Errorf("creating judgment item for pair %d: %w", pairID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("creating pairs: %w", err)
	}
	return nil
}

// CountPairs returns how many pairs a run has.
func (s *Store) CountPairs(ctx context.Context, runID int64) (int, error) {
	var n int
	if err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM eval_pairs WHERE run_id = ?`, runID); err != nil {
		return 0, fmt.Errorf("counting pairs of run %d: %w", runID, err)
	}
	return n, nil
}

// ListPairs returns a run's pairs in creation order.
func (s *Store) ListPairs(ctx context.Context, runID int64) ([]Pair, error) {
	out := []Pair{}
	if err := s.db.SelectContext(ctx, &out,
		`SELECT id, run_id, task_id, k_index, sample_a, sample_b, assignment, created_at
		 FROM eval_pairs WHERE run_id = ? ORDER BY id`, runID); err != nil {
		return nil, fmt.Errorf("listing pairs of run %d: %w", runID, err)
	}
	return out, nil
}

// GetPair returns one pair by id.
func (s *Store) GetPair(ctx context.Context, pairID int64) (*Pair, error) {
	var p Pair
	err := s.db.GetContext(ctx, &p,
		`SELECT id, run_id, task_id, k_index, sample_a, sample_b, assignment, created_at
		 FROM eval_pairs WHERE id = ?`, pairID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("pair %d: %w", pairID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting pair %d: %w", pairID, err)
	}
	return &p, nil
}

// DecodeAssignment parses a pair's unblinding key.
func DecodeAssignment(raw string) (Assignment, error) {
	var a Assignment
	if raw == "" || raw == "{}" {
		return a, nil
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return a, fmt.Errorf("decoding assignment: %w", err)
	}
	return a, nil
}

// VariantFor resolves a presented winner letter back to the variant that
// produced it, given the item's presentation order. Returns 0 for a tie.
//
// Two indirections, deliberately: the judge names a presented letter, the item
// says whether that letter was the pair's own A or B, and only the assignment
// says which variant that was. Nothing short of the pair row can unblind a
// verdict.
func VariantFor(a Assignment, order, winner string) int64 {
	if winner == WinnerTie {
		return 0
	}
	pairLetter := winner
	if order == OrderBA {
		if winner == WinnerA {
			pairLetter = WinnerB
		} else {
			pairLetter = WinnerA
		}
	}
	if pairLetter == WinnerA {
		return a.A
	}
	return a.B
}
