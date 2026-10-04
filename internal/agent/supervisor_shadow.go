package agent

import (
	"encoding/json"
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
	Scores         map[string]float64 `json:"scores"`
	Lowest         string             `json:"lowest"`    // check with the lowest score; "" when an answer is missing
	MinScore       *float64           `json:"min_score"` // nil: an answer is missing, so the call always escalates
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
	pending := map[string][]int{} // pairing key → indexes into reviews awaiting a supervisor verdict
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
			key := shadowPairKey(ev.ConversationID, r.Tool, r.Arguments)
			pending[key] = append(pending[key], len(reviews))
			r.Arguments = cutRunes(r.Arguments, shadowArgsRunes)
			reviews = append(reviews, r)
		case strings.HasPrefix(ev.Source, "supervisor:"):
			var d supervisorDetail
			if json.Unmarshal([]byte(ev.Detail), &d) != nil {
				continue
			}
			key := shadowPairKey(ev.ConversationID, d.Tool, d.Arguments)
			queue := pending[key]
			// Drop reviews whose supervisor verdict never came (none was wired,
			// or it was cancelled) so they cannot pair with a later call.
			for len(queue) > 0 && ev.Timestamp.Sub(reviews[queue[0]].Time) > shadowPairWindow {
				queue = queue[1:]
			}
			if len(queue) == 0 {
				pending[key] = queue
				continue
			}
			r := &reviews[queue[0]]
			pending[key] = queue[1:]
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

func shadowReviewFrom(ev audit.Event) (ShadowReview, bool) {
	var d shadowDetail
	if json.Unmarshal([]byte(ev.Detail), &d) != nil || d.Decision != "shadow" {
		return ShadowReview{}, false
	}
	r := ShadowReview{
		AuditID:        ev.ID,
		Time:           ev.Timestamp,
		ConversationID: ev.ConversationID,
		Tool:           d.Tool,
		Arguments:      d.Arguments,
		Model:          d.Model,
		Scores:         make(map[string]float64, len(deciderQuestionOrder)),
		DeciderCost:    d.Cost,
	}
	// Same rule as decideSupervisorOutcome: the lowest answer decides, and a
	// missing one escalates.
	low, complete := 2.0, true
	for _, id := range deciderQuestionOrder {
		a, ok := d.Answers[id]
		if !ok {
			complete = false
			continue
		}
		r.Scores[id] = a.Noul
		if a.Noul < low {
			r.Lowest, low = id, a.Noul
		}
	}
	if complete {
		r.MinScore = &low
	} else {
		r.Lowest = ""
	}
	return r, true
}

func shadowPairKey(convID, tool, args string) string {
	return convID + "\x00" + tool + "\x00" + args
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
