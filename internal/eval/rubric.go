package eval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RubricVersion is the revision of the judging rubric the internal judge
// grades under, and what it stamps on every verdict it writes.
//
// It must match the `Rubric version:` line of `.claude/skills/judge-eval/SKILL.md`:
// the two judges have to be comparable, and a results view naming one version
// for verdicts produced under two different rubrics would be a lie.
// TestRubricVersion_MatchesTheSkillFile pins the pair.
const RubricVersion = "v1"

// JudgeInternal is the judge identity the internal judge records verdicts
// under. It is not JudgeOperator, so its calls count toward the win rate and
// flip items to judged, exactly like the MCP judge's; it is a fixed name
// rather than a key name because there is no key — the caller is the server.
const JudgeInternal = "judge_model"

// judgeSystemPrompt is the rubric, carrying the same instructions the
// /judge-eval skill gives Claude Code: the same four dimensions in the same
// precedence order, the same blinding warning, and the same demand that a
// persona_fit or task_success deduction cite the clause it rests on.
//
// It is a Go constant rather than the skill file itself because `.claude/` sits
// outside the module's embed root, and a runtime file read would make the
// judge's behaviour depend on a path that is absent in a container. The
// version line above is what keeps the two from drifting.
const judgeSystemPrompt = `You are the judge in a blinded A/B comparison of two AI agent responses.

An eval run executed one test case against two agent configurations — an
incumbent and a candidate — through the real engine with writes suppressed.
Say which of the two responses is better.

You never learn the assignment. Model, provider, variant name, cost, latency
and token usage are withheld. Do not speculate about them, and do not let a
guess influence the verdict. If you find yourself reasoning about "this looks
like a bigger model", stop and go back to the response.

Each pair is judged twice with the presentation order swapped. Judge this item
on its own: never try to recognise a pair you may have seen before.

# Rubric

Judge in this order. An earlier dimension outranks a later one: a beautifully
written response that did not do the job loses to a plain one that did.

1. task_success — did it do what was asked? The item's "notes" field is the
   operator's "what good looks like": context to understand intent, not an
   assertion list to grade against mechanically. Where "pinned_history" is
   present, judge the response as a continuation of that conversation, not in
   isolation. Deductions: missed part of the request, answered a different
   question, hallucinated a fact or a capability, refused something reasonable,
   claimed to have done something the tool trace shows it did not do.

2. tool_path — did it get there sensibly? Read both tool traces and compare:
   - rejected calls (outcome "rejected") are a healthy tool with bad arguments,
     the single most reliable model-quality signal in the trace;
   - round count and repetition — the same call three times, or a wandering
     path to an answer one call would have given;
   - stop_reason — "repeated_calls" or "max_rounds" means the loop ran out of
     road rather than the model finishing, and a response that arrived that way
     is worse than an identical one that arrived cleanly;
   - suppressed calls (outcome "suppressed") are normal — an eval turn refuses
     writes by design. Judge the choice to make the call, never the suppression;
   - cached calls (outcome "cached") are the engine deduplicating an identical
     idempotent call, not the model repeating itself.
   A response with no tool calls where the task plainly needed one loses this
   dimension outright.

3. persona_fit — does it sound like this agent? Cite the specific clause behind
   any deduction. If you cannot name what was violated, do not deduct.

4. length — is it the right size for its channel? Verbosity bias survives
   blinding, so be explicit about it here rather than letting it leak into the
   other three. Longer is not better.

# Output

Reply with one JSON object and nothing else — no prose, no code fence:

{"winner":"a","dimensions":{"task_success":"a","tool_path":"tie","persona_fit":"a","length":"b"},"notes":"one or two sentences a reader could check"}

- "winner" is the overall call and is what the win rate counts. It is not a
  majority vote of the dimensions: task_success can carry a pair on its own.
- Every value is exactly one of "a", "b" or "tie".
- Use "tie" honestly. A forced preference between two equally good responses is
  noise, and the decision rule already treats ties as "not an upgrade".
- "dimensions" may name only task_success, tool_path, persona_fit and length.`

// judgeCall is the internal judge's parsed reply.
type judgeCall struct {
	Winner     string            `json:"winner"`
	Dimensions map[string]string `json:"dimensions"`
	Notes      string            `json:"notes"`
}

// buildJudgeMessages renders one blinded item as the judge's request.
//
// The item is handed over as the JSON the MCP judge's eval_get_pair returns,
// serialised whole rather than field-by-field: the blinded payload is built
// once, in GetBlindedItem, and re-rendering it here would be a second
// derivation of what a judge may see — the exact drift the blinding is meant
// to make impossible.
func buildJudgeMessages(item *BlindedItem) ([]judgeMessage, error) {
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering judgment item %d: %w", item.ItemID, err)
	}
	return []judgeMessage{
		{Role: "system", Content: judgeSystemPrompt},
		{Role: "user", Content: "Judgment item:\n" + string(body) + "\n\nReturn the verdict JSON."},
	}, nil
}

// judgeMessage decouples the prompt builder from the llm package so the
// blinding test can assert on what is sent without constructing a router.
type judgeMessage struct {
	Role    string
	Content string
}

// parseJudgeCall reads the model's reply.
//
// It tolerates a code fence and surrounding prose (small models add both) by
// taking the outermost JSON object, but it does not tolerate an unknown
// dimension name or an invalid winner: that is the MCP judge's rule too, and a
// typo that silently vanishes from the results table is worse than a call that
// failed loudly.
func parseJudgeCall(raw string) (judgeCall, error) {
	var out judgeCall
	body := extractJSONObject(raw)
	if body == "" {
		return out, fmt.Errorf("no JSON object in the judge's reply")
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return out, fmt.Errorf("unreadable verdict JSON: %w", err)
	}
	out.Winner = strings.ToLower(strings.TrimSpace(out.Winner))
	if !ValidWinner(out.Winner) {
		return out, fmt.Errorf("invalid winner %q: want a, b, or tie", out.Winner)
	}
	valid := make(map[string]bool, len(Dimensions()))
	for _, d := range Dimensions() {
		valid[d] = true
	}
	dims := make(map[string]string, len(out.Dimensions))
	for name, winner := range out.Dimensions {
		if !valid[name] {
			return out, fmt.Errorf("unknown dimension %q: want one of %s",
				name, strings.Join(Dimensions(), ", "))
		}
		w := strings.ToLower(strings.TrimSpace(winner))
		if !ValidWinner(w) {
			return out, fmt.Errorf("dimension %q: invalid winner %q, want a, b, or tie", name, winner)
		}
		dims[name] = w
	}
	out.Dimensions = dims
	return out, nil
}

// extractJSONObject returns the outermost {...} span of s, or "" when there is
// none.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// encodeDimensions serialises the parsed dimensions for storage. An empty map
// stores as "{}", matching what RecordVerdict expects.
func encodeDimensions(dims map[string]string) string {
	if len(dims) == 0 {
		return "{}"
	}
	b, err := json.Marshal(dims)
	if err != nil {
		return "{}"
	}
	return string(b)
}
