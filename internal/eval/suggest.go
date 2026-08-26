package eval

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
)

// Signals a past turn can carry. A turn with none of them is not offered at
// all — "interesting" is the whole point, and an unremarkable turn teaches the
// eval set nothing.
const (
	// SignalToolFault: the reply had a rejected (bad args) or failed
	// (transport) tool call. The sharpest model-fault signal we record.
	SignalToolFault = "tool_fault"
	// SignalManyRounds: the reply took three or more tool rounds.
	SignalManyRounds = "many_rounds"
	// SignalHighCost: the reply's cost is in the candidate pool's top decile.
	SignalHighCost = "high_cost"
	// SignalCommandSkill: a command-triggered skill drove the turn.
	SignalCommandSkill = "command_skill"
)

// roundsThreshold is the round count at which a turn reads as tool-heavy,
// matching the toolCallsThreshold below: both are "the model had to work".
const roundsThreshold = 3

// toolCallsThreshold is the call count at which a turn reads as tool-heavy.
const toolCallsThreshold = 3

// scheduledPrefix is what scheduler.FormatScheduledText opens with, for both
// its labels ("[Scheduled: <skill>" and "[Scheduled trigger: <name>"). Matched
// as a prefix rather than parsed — the category only needs to know a schedule
// fired this turn.
const scheduledPrefix = "[Scheduled"

// PrecedingMessage is one {role, content} pair of context preceding a
// suggested turn, ready to be pinned as a task's history.
type PrecedingMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Candidate is one past turn offered as a test case.
type Candidate struct {
	Prompt         string             `json:"prompt"`
	Category       string             `json:"category"`
	ConversationID string             `json:"conversation_id"`
	MessageID      int64              `json:"message_id"`
	CreatedAt      time.Time          `json:"created_at"`
	Signals        []string           `json:"signals"`
	Preceding      []PrecedingMessage `json:"preceding"`
}

// SuggestOpts bounds a suggestion pass.
type SuggestOpts struct {
	// Limit is the total number of candidates returned across all categories.
	Limit int
	// Exclude holds SourceKey values for turns already saved as tasks, so an
	// accepted suggestion does not resurface.
	Exclude map[string]struct{}
}

// SourceKey identifies a turn by its source conversation and message, the pair
// eval_tasks records when a suggestion is accepted.
func SourceKey(convID string, messageID int64) string {
	return fmt.Sprintf("%s:%d", convID, messageID)
}

// scored pairs a candidate with the signal count that ranks it.
type scored struct {
	candidate Candidate
	score     int
}

// Suggest turns a pool of past turns into stratified test-case candidates:
// top-N per category rather than top-N overall, because a set drawn purely by
// interestingness is all failures and represents nothing the agent normally
// does. Turns with no signal, and turns already saved as tasks, are dropped.
func Suggest(turns []agent.InterestingTurn, opts SuggestOpts) []Candidate {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	threshold := topDecileCost(turns)
	byCategory := make(map[string][]scored, len(HistoryCategories()))
	for _, t := range turns {
		if _, seen := opts.Exclude[SourceKey(t.ConversationID, t.MessageID)]; seen {
			continue
		}
		signals := signalsFor(t, threshold)
		if len(signals) == 0 {
			continue
		}
		category := categoryFor(t)
		byCategory[category] = append(byCategory[category], scored{
			candidate: Candidate{
				Prompt:         t.Content,
				Category:       category,
				ConversationID: t.ConversationID,
				MessageID:      t.MessageID,
				CreatedAt:      t.CreatedAt,
				Signals:        signals,
				Preceding:      precedingOf(t),
			},
			score: len(signals),
		})
	}

	for cat := range byCategory {
		group := byCategory[cat]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].score != group[j].score {
				return group[i].score > group[j].score
			}
			return group[i].candidate.CreatedAt.After(group[j].candidate.CreatedAt)
		})
	}
	return stratify(byCategory, limit)
}

// stratify draws limit/len(HistoryCategories()) from each category, then hands
// the leftover slots round-robin to whichever categories still have surplus, so
// a thin category costs the total nothing.
func stratify(byCategory map[string][]scored, limit int) []Candidate {
	cats := HistoryCategories()
	share := limit / len(cats)
	taken := make(map[string]int, len(cats))
	out := make([]Candidate, 0, limit)

	for _, cat := range cats {
		n := min(share, len(byCategory[cat]))
		for i := 0; i < n; i++ {
			out = append(out, byCategory[cat][i].candidate)
		}
		taken[cat] = n
	}

	// Round-robin the remainder so no single category can eat every spare slot.
	for len(out) < limit {
		progressed := false
		for _, cat := range cats {
			if len(out) >= limit {
				break
			}
			if taken[cat] >= len(byCategory[cat]) {
				continue
			}
			out = append(out, byCategory[cat][taken[cat]].candidate)
			taken[cat]++
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return out
}

// signalsFor lists the reasons this turn is worth saving, most diagnostic
// first.
func signalsFor(t agent.InterestingTurn, costThreshold float64) []string {
	var signals []string
	if t.Faults > 0 {
		signals = append(signals, SignalToolFault)
	}
	if t.MaxRound >= roundsThreshold {
		signals = append(signals, SignalManyRounds)
	}
	if costThreshold > 0 && t.ReplyCost >= costThreshold {
		signals = append(signals, SignalHighCost)
	}
	if t.CommandMatches > 0 {
		signals = append(signals, SignalCommandSkill)
	}
	return signals
}

// categoryFor infers which history category a turn belongs to. CategoryProbe
// is never inferred: a probe is generated from written intent, not sampled.
// The order is the discriminating one: a command match is what the turn *was*,
// while tool weight is only how it went.
func categoryFor(t agent.InterestingTurn) string {
	switch {
	case t.CommandMatches > 0:
		return CategorySkillCommand
	case strings.HasPrefix(t.Content, scheduledPrefix):
		return CategoryScheduled
	case t.ToolCalls >= toolCallsThreshold || t.MaxRound >= roundsThreshold:
		return CategoryToolHeavy
	default:
		return CategoryChat
	}
}

// topDecileCost returns the cost a reply must reach to count as expensive
// within this pool: the cost of the ceil(n/10)-th priciest reply. Zero when
// nothing in the pool cost anything, which disables the signal rather than
// handing it to every free turn.
func topDecileCost(turns []agent.InterestingTurn) float64 {
	costs := make([]float64, 0, len(turns))
	for _, t := range turns {
		if t.ReplyCost > 0 {
			costs = append(costs, t.ReplyCost)
		}
	}
	if len(costs) == 0 {
		return 0
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(costs)))
	decile := len(costs) / 10
	if decile < 1 {
		decile = 1
	}
	return costs[decile-1]
}

// precedingOf converts the store's context window into the response shape.
func precedingOf(t agent.InterestingTurn) []PrecedingMessage {
	out := make([]PrecedingMessage, 0, len(t.Preceding))
	for _, m := range t.Preceding {
		out = append(out, PrecedingMessage{Role: m.Role, Content: m.Content})
	}
	return out
}
