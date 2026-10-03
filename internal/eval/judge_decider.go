package eval

import (
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/llm"
)

// JudgeDecider is the judge identity a decision model's verdicts are recorded
// under. Distinct from JudgeInternal so a results view can tell which backend
// graded an item, but not JudgeOperator: its calls count toward the win rate
// and flip items to judged exactly like the model judge's.
const JudgeDecider = "judge_decider"

// deciderWinnerQuestion is the id of the overall call; the rest are the
// rubric dimensions by name.
const deciderWinnerQuestion = "winner"

// deciderChoices are the three verdict outcomes as the decider sees them.
var deciderChoices = map[string]string{
	WinnerA:   "`response_a` is the better response",
	WinnerB:   "`response_b` is the better response",
	WinnerTie: "the two responses are equally good, or neither did the job",
}

// judgeDeciderQuestions renders the rubric as five choice questions over the
// blinded item: the overall call plus one per dimension, same names and same
// precedence as the model judge's prompt. Dimension instructions are one
// sentence each because a decision model classifies against criteria rather
// than reading an essay; the four-dimension order and the "not a majority
// vote" rule are what keep the two judges comparable.
func judgeDeciderQuestions() map[string]llm.Question {
	q := func(instructions string) llm.Question {
		return llm.Question{Type: llm.QuestionChoice, Instructions: instructions, Choices: deciderChoices}
	}
	return map[string]llm.Question{
		deciderWinnerQuestion: q("Comparing `response_a` and `response_b` as answers to `prompt` (continuing `pinned_history` when present, with `notes` as context for what good looks like), which is the better response overall? Task success outranks tool use, which outranks persona fit, which outranks length; this is not a majority vote across those, and a response that did the job beats a better-written one that did not."),
		DimTaskSuccess:        q("Which response better did what `prompt` asked, without missing part of the request, answering a different question, hallucinating a fact or capability, refusing something reasonable, or claiming to have done something its `tool_calls` show it did not?"),
		DimToolPath:           q("Judging by `tool_calls`, `rounds` and `stop_reason` on each side, which response got there more sensibly, with fewer rejected calls, less repetition, and a clean finish rather than running out of rounds, treating suppressed and cached calls as normal?"),
		DimPersonaFit:         q("Which response sounds more like the agent described in `notes` and `pinned_history`, judged only on clauses you can point to?"),
		DimLength:             q("Which response is the right size for its channel, where longer is not better and padding counts against a response?"),
	}
}

// recordP is the probability the decider assigned to its chosen option. The
// OpenRouter decoder leaves Probabilities nil when the provider omits the
// field, so Confidence stands in rather than reading as zero and abstaining on
// every item. Confidence is (p_max - 1/n)/(1 - 1/n), always below p_max for
// three options, so the fallback is stricter at the same threshold, never
// looser.
func recordP(a llm.Answer) float64 {
	if p, ok := a.Probabilities[a.Choice]; ok {
		return p
	}
	return a.Confidence
}

// deciderCall turns a decision response into a verdict, or abstains.
//
// The winner is recorded only when its probability reaches recordAt; a
// dimension is kept only when its own does, and is omitted otherwise, since a
// coin-flip dimension stored as a call is worse than a gap. Notes carry every
// probability so the row explains itself. An option outside a/b/tie fails the
// item, as an unknown dimension does for the model judge: rejected, never
// silently stored.
func deciderCall(answers map[string]llm.Answer, recordAt float64) (judgeCall, bool, error) {
	winner, ok := answers[deciderWinnerQuestion]
	if !ok {
		return judgeCall{}, false, fmt.Errorf("no %q answer", deciderWinnerQuestion)
	}
	if !ValidWinner(winner.Choice) {
		return judgeCall{}, false, fmt.Errorf("invalid winner %q: want a, b, or tie", winner.Choice)
	}
	notes := []string{fmt.Sprintf("decider: winner %s (p=%.2f)", winner.Choice, recordP(winner))}
	call := judgeCall{Winner: winner.Choice, Dimensions: make(map[string]string, len(Dimensions()))}
	for _, dim := range Dimensions() {
		a, ok := answers[dim]
		if !ok {
			continue
		}
		// Validated before the threshold check below: a malformed answer must
		// fail the item even when the winner is too uncertain to record,
		// not pass as an abstention.
		if !ValidWinner(a.Choice) {
			return judgeCall{}, false, fmt.Errorf("dimension %q: invalid winner %q, want a, b, or tie", dim, a.Choice)
		}
		if recordP(a) < recordAt {
			notes = append(notes, fmt.Sprintf("%s omitted (%s p=%.2f)", dim, a.Choice, recordP(a)))
			continue
		}
		call.Dimensions[dim] = a.Choice
		notes = append(notes, fmt.Sprintf("%s %s (p=%.2f)", dim, a.Choice, recordP(a)))
	}
	if recordP(winner) < recordAt {
		return judgeCall{}, false, nil
	}
	call.Notes = strings.Join(notes, "; ")
	return call, true, nil
}
