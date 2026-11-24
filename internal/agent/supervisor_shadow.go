package agent

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

const (
	// shadowPairWindow bounds the gap between a decider's shadow review and
	// the supervisor review of the same call: the supervisor timeout plus slack.
	shadowPairWindow = 15 * time.Minute
	shadowArgsRunes  = 200

	// DefaultShadowReviewWindow is how far back LoadShadowReviews callers look
	// when the user names no start.
	DefaultShadowReviewWindow = 30 * 24 * time.Hour
	shadowReviewsMaxScan      = 10000
	shadowReviewsPageSize     = 200 // audit.Store.List's cap
)

// ShadowReview is one shadow-mode decider review paired with the supervisor's
// verdict on the same tool call.
type ShadowReview struct {
	AuditID        int64              `json:"audit_id"`
	Time           time.Time          `json:"time"`
	ConversationID string             `json:"conversation_id"`
	Tool           string             `json:"tool"`
	Arguments      string             `json:"arguments"`
	Model          string             `json:"model"`
	Scores         map[string]float64 `json:"scores"`         // check scores by name; high means safe
	Lowest         string             `json:"lowest"`         // check with the lowest score; "" when an answer is missing, except a review missing only self_grant (it predates that question and is scored on the checks it has)
	MinScore       *float64           `json:"min_score"`      // nil: an answer is missing, so the call always escalates; set for a review missing only self_grant, scored on the checks it has
	MinDenyScore   *float64           `json:"min_deny_score"` // lowest check that can deny (no_leak and no_self_grant only escalate); nil when an answer is missing, except a review missing only self_grant
	DeciderCost    float64            `json:"decider_cost"`
	Supervisor     string             `json:"supervisor"` // APPROVE, DENY or ESCALATE; "" when no supervisor verdict was recorded
	SupervisorName string             `json:"supervisor_name,omitempty"`
	SupervisorCost *float64           `json:"supervisor_cost"` // nil: not recorded (reviews audited before the cost field)
}

// shadowDetail and supervisorDetail read the audit details written by
// runSupervisorDecider and supervisorReview.
type shadowDetail struct {
	Tool      string                `json:"tool"`
	Arguments string                `json:"arguments"`
	Decision  string                `json:"decision"`
	Model     string                `json:"model"`
	Answers   map[string]llm.Answer `json:"answers"`
	Cost      float64               `json:"cost"`
}

type supervisorDetail struct {
	Tool       string   `json:"tool"`
	Arguments  string   `json:"arguments"`
	Decision   string   `json:"decision"`
	Supervisor string   `json:"supervisor"`
	Cost       *float64 `json:"cost"`
}

// PairShadowReviews pairs the shadow reviews of the named decider with the
// supervisor review of the same call. events are one agent's supervisor-
// category audit events in any order. A decider review and a supervisor review
// pair when they share conversation, tool and arguments, and the supervisor's
// comes after it within shadowPairWindow; duplicates pair first-in first-out.
// failed counts decider reviews that errored. Enforce-mode reviews are left
// out: the supervisor sees only the escalated calls there.
func PairShadowReviews(events []audit.Event, decider string) (reviews []ShadowReview, failed int) {
	sorted := slices.Clone(events)
	slices.SortStableFunc(sorted, func(a, b audit.Event) int { return a.Timestamp.Compare(b.Timestamp) })

	deciderSource := "decider:" + decider
	pending := newPairQueue[pairKey, int]() // indexes into reviews awaiting a supervisor verdict
	for _, ev := range sorted {
		switch {
		case ev.Source == deciderSource:
			if ev.Status == audit.StatusError {
				failed++
				continue
			}
			r, ok := shadowReviewFrom(ev)
			if !ok {
				continue
			}
			pending.add(shadowPairKey(ev.ConversationID, r.Tool, r.Arguments), r.Time, len(reviews))
			r.Arguments = cutRunes(r.Arguments, shadowArgsRunes)
			reviews = append(reviews, r)
		case strings.HasPrefix(ev.Source, "supervisor:"):
			var d supervisorDetail
			if json.Unmarshal([]byte(ev.Detail), &d) != nil {
				continue
			}
			i, ok := pending.take(shadowPairKey(ev.ConversationID, d.Tool, d.Arguments), ev.Timestamp)
			if !ok {
				continue
			}
			r := &reviews[i]
			r.SupervisorName = d.Supervisor
			if d.Decision != "error" {
				r.Supervisor = d.Decision
				r.SupervisorCost = d.Cost
			}
		}
	}
	slices.Reverse(reviews)
	return reviews, failed
}

// ShadowReviewSet is one agent's paired shadow reviews in a time window.
type ShadowReviewSet struct {
	Reviews   []ShadowReview // newest first, never nil
	Failed    int            // decider reviews that errored in the window
	Truncated bool           // the scan cap was hit; the oldest reviews are missing
}

// LoadShadowReviews reads agentName's supervisor-category audit events in
// [since, until] and pairs the named decider's shadow reviews. The REST and
// MCP surfaces both call it, so they page and cap identically.
func LoadShadowReviews(ctx context.Context, store audit.Store, agentName, decider string, since, until time.Time) (ShadowReviewSet, error) {
	return loadShadowReviews(ctx, store, agentName, decider, since, until, shadowReviewsMaxScan)
}

func loadShadowReviews(ctx context.Context, store audit.Store, agentName, decider string, since, until time.Time, maxScan int) (ShadowReviewSet, error) {
	var events []audit.Event
	var set ShadowReviewSet
	for offset := 0; ; offset += shadowReviewsPageSize {
		page, _, err := store.List(ctx, audit.ListOpts{
			Categories: []string{audit.CategorySupervisor},
			Agent:      agentName,
			Since:      &since,
			Until:      &until, // pins the window so offsets stay stable
			Limit:      shadowReviewsPageSize,
			Offset:     offset,
		})
		if err != nil {
			return set, fmt.Errorf("listing supervisor events: %w", err)
		}
		events = append(events, page...)
		if len(page) < shadowReviewsPageSize {
			break
		}
		if len(events) >= maxScan {
			set.Truncated = true
			break
		}
	}
	set.Reviews, set.Failed = PairShadowReviews(events, decider)
	if set.Reviews == nil {
		set.Reviews = []ShadowReview{}
	}
	return set, nil
}

func shadowReviewFrom(ev audit.Event) (ShadowReview, bool) {
	var d shadowDetail
	if json.Unmarshal([]byte(ev.Detail), &d) != nil || d.Decision != "shadow" {
		return ShadowReview{}, false
	}
	// Scores from the old bundled safety question are not comparable with
	// the split checks, so they stay out of calibration.
	if isLegacyDeciderAnswers(d.Answers) {
		return ShadowReview{}, false
	}
	r := ShadowReview{
		AuditID:        ev.ID,
		Time:           ev.Timestamp,
		ConversationID: ev.ConversationID,
		Tool:           d.Tool,
		Arguments:      d.Arguments,
		Model:          d.Model,
		DeciderCost:    d.Cost,
	}
	// Same rule as decideSupervisorOutcome: the lowest check decides, and a
	// missing answer escalates. Exception: a review missing only self_grant
	// predates that question, so it is scored on the checks it has. The live
	// path still escalates when self_grant is missing.
	scores, order, missing := deciderChecks(d.Answers)
	r.Scores = scores
	if missing == "" || onlySelfGrantMissing(d.Answers) {
		lowID, low := lowestCheck(scores, order)
		_, denyLow := lowestCheck(scores, denyingChecks(order))
		r.Lowest, r.MinScore, r.MinDenyScore = lowID, &low, &denyLow
	}
	return r, true
}

// Verdict is what the decider would decide at these thresholds. It must stay
// in step with decideSupervisorOutcome: deny first, a missing answer escalates.
func (r ShadowReview) Verdict(approveAt, denyAt float64) string {
	if r.MinScore == nil {
		return string(supervisorEscalate)
	}
	deny := r.MinScore
	if r.MinDenyScore != nil {
		deny = r.MinDenyScore
	}
	switch {
	case *deny <= denyAt:
		return string(supervisorDeny)
	case *r.MinScore >= approveAt:
		return string(supervisorApprove)
	}
	return string(supervisorEscalate)
}

// UnsafeApproval reports whether the decider would approve a call the
// supervisor denied or escalated.
func (r ShadowReview) UnsafeApproval(approveAt, denyAt float64) bool {
	return r.Verdict(approveAt, denyAt) == string(supervisorApprove) &&
		(r.Supervisor == string(supervisorDeny) || r.Supervisor == string(supervisorEscalate))
}

// CountUnsafeApprovals counts the agent's shadow reviews after `after` (the
// review mark, or zero) that the decider would approve and the supervisor did
// not. `after` is raised to DefaultShadowReviewWindow ago. Events load from
// shadowPairWindow before it, so a verdict on an earlier call cannot pair with
// a later identical one.
//
// Unlike the reviews list it reads the whole window, so a busy agent is never
// undercounted, and it stays linear: one ScanEvents pass, keeping a few fields
// per event rather than the event and its Detail. It pairs through the same
// pairQueue as PairShadowReviews, so the badge and the panel cannot disagree.
func CountUnsafeApprovals(ctx context.Context, store audit.Store, agentName, decider string, after time.Time, approveAt, denyAt float64) (int, error) {
	now := time.Now().UTC()
	if floor := now.Add(-DefaultShadowReviewWindow); after.Before(floor) {
		after = floor
	}
	since := after.Add(-shadowPairWindow)
	deciderSource := "decider:" + decider
	var events []countEvent
	err := store.ScanEvents(ctx, audit.ListOpts{
		Categories: []string{audit.CategorySupervisor}, Agent: agentName, Since: &since, Until: &now,
	}, func(ev audit.Event) error {
		if ce, ok := countEventFrom(ev, deciderSource, approveAt, denyAt); ok {
			events = append(events, ce)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scanning supervisor events: %w", err)
	}
	// The same order as PairShadowReviews: by time, ties in id order.
	slices.SortStableFunc(events, func(a, b countEvent) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})

	pending := newPairQueue[[sha256.Size]byte, countEvent]()
	n := 0
	for _, e := range events {
		if e.review {
			pending.add(e.key, e.at, e)
			continue
		}
		r, ok := pending.take(e.key, e.at)
		if ok && r.wouldApprove && r.at.After(after) &&
			(e.decision == string(supervisorDeny) || e.decision == string(supervisorEscalate)) {
			n++
		}
	}
	return n, nil
}

// countEvent is what the badge count keeps from one supervisor-category event.
// The pairing key is hashed, since arguments can be as large as the call.
type countEvent struct {
	at           time.Time
	id           int64
	key          [sha256.Size]byte
	review       bool   // a decider review; otherwise a supervisor verdict
	wouldApprove bool   // review: the decider would approve at the count's thresholds
	decision     string // verdict: APPROVE, DENY, ESCALATE or error
}

func countEventFrom(ev audit.Event, deciderSource string, approveAt, denyAt float64) (countEvent, bool) {
	switch {
	case ev.Source == deciderSource:
		if ev.Status == audit.StatusError {
			return countEvent{}, false
		}
		r, ok := shadowReviewFrom(ev)
		if !ok {
			return countEvent{}, false
		}
		return countEvent{at: ev.Timestamp, id: ev.ID, key: hashPairKey(ev.ConversationID, r.Tool, r.Arguments),
			review: true, wouldApprove: r.Verdict(approveAt, denyAt) == string(supervisorApprove)}, true
	case strings.HasPrefix(ev.Source, "supervisor:"):
		var d supervisorDetail
		if json.Unmarshal([]byte(ev.Detail), &d) != nil {
			return countEvent{}, false
		}
		return countEvent{at: ev.Timestamp, id: ev.ID, key: hashPairKey(ev.ConversationID, d.Tool, d.Arguments),
			decision: d.Decision}, true
	}
	return countEvent{}, false
}

// pairKey identifies one tool call: there is no shared call id between the
// decider's and the supervisor's events.
type pairKey = string

func shadowPairKey(convID, tool, args string) pairKey {
	return convID + "\x00" + tool + "\x00" + args
}

func hashPairKey(convID, tool, args string) [sha256.Size]byte {
	return sha256.Sum256([]byte(shadowPairKey(convID, tool, args)))
}

// pairQueue holds decider reviews waiting for the supervisor's verdict on the
// same call. PairShadowReviews and CountUnsafeApprovals both pair through it.
type pairQueue[K comparable, V any] struct {
	pending map[K][]pendingReview[V]
}

type pendingReview[V any] struct {
	at time.Time
	v  V
}

func newPairQueue[K comparable, V any]() *pairQueue[K, V] {
	return &pairQueue[K, V]{pending: map[K][]pendingReview[V]{}}
}

func (q *pairQueue[K, V]) add(key K, at time.Time, v V) {
	q.pending[key] = append(q.pending[key], pendingReview[V]{at: at, v: v})
}

// take pairs a verdict at `at` with the oldest waiting review of the same call,
// first in first out. Reviews older than shadowPairWindow are dropped first:
// their verdict never came (no supervisor was wired, or it was cancelled), and
// they must not pair with a later call.
func (q *pairQueue[K, V]) take(key K, at time.Time) (V, bool) {
	queue := q.pending[key]
	for len(queue) > 0 && at.Sub(queue[0].at) > shadowPairWindow {
		queue = queue[1:]
	}
	if len(queue) == 0 {
		q.pending[key] = queue
		var zero V
		return zero, false
	}
	q.pending[key] = queue[1:]
	return queue[0].v, true
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
