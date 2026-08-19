---
name: judge-eval
description: Judge a denkeeper eval run's blinded response pairs over MCP. Use when the user asks to judge an eval run, work the judging queue, calibrate the judge, or decide whether a candidate model is an upgrade.
---

# Judging a denkeeper eval run

Rubric version: **v1** (2026-08-17)

You are the judge in a blinded A/B comparison. A denkeeper eval run executed
every test case in a task set against two agent configurations — an incumbent
and a candidate — through the real engine, with writes suppressed. Your job is
to say which of two responses is better, without knowing which configuration
produced either.

**You never learn the assignment.** Model, provider, variant name, cost, latency
and token usage are withheld server-side. Do not speculate about them, and do
not let a guess influence a verdict. If you find yourself reasoning about "this
looks like a bigger model", stop and go back to the response.

## Setup

The tools come from denkeeper's MCP server. The judging key needs exactly two
scopes and nothing else:

```bash
denkeeper keys create judge --scopes eval:read,eval:write
```

A judge key with wider scopes can start runs and spend money; this one can read
pairs and write verdicts, and that is all it should ever be able to do.

Add the server to Claude Code (adjust host and key):

```bash
claude mcp add --transport http denkeeper https://your-denkeeper/api/v1/mcp --header "Authorization: Bearer $DENKEEPER_JUDGE_KEY"
```

## Tools

| Tool | What it does |
|---|---|
| `eval_run_status` | Run status, progress, pairs produced, items still pending |
| `eval_pending` | The queue. `run_id` scopes it; `sample_n` draws a random subset |
| `eval_get_pair` | One blinded item: prompt, notes, pinned history, Response A and B with tool traces |
| `eval_verdict` | Record `{item_id, winner, dimensions, notes, rubric_version}` |
| `eval_summary` | Unblinds: gate table, win-rate, per-category breakdown, verdict |

Each pair of responses is queued **twice**, with the presentation order swapped.
Both items must be judged before the pair counts, and if your two verdicts name
different sides the pair records as a tie — that is the position-bias control,
and it only works if you judge each item on its own. Never try to recognise a
pair you have already seen, and never look up your earlier verdict.

## Workflow

1. `eval_run_status` — confirm the run is terminal (`done`, `capped`, or
   `stopped`). Judging a running run wastes effort on a moving queue.
2. **First run against a new rubric: calibrate.** `eval_pending` with
   `sample_n: 20` and work that subset interactively, showing the operator your
   reasoning for each. The operator records their own call with
   `eval_verdict` and `judge_ident: "operator"` — that sits alongside your
   verdict rather than replacing it, and `eval_summary` reports the agreement
   rate. **Below roughly 80 % agreement, stop and fix the rubric below before
   judging the rest.** A drifted rubric silently devalues every later run.
3. Once calibrated, work the remaining queue: `eval_pending` → `eval_get_pair`
   → `eval_verdict`, one item at a time. For a large queue, fan out to parallel
   subagents; each subagent gets its own item ids and must not be told anything
   about the other items.
4. `eval_summary` — read the verdict with its gate table and per-category
   breakdown back to the operator.

## The rubric

Judge in this order. An earlier dimension outranks a later one: a beautifully
written response that did not do the job loses to a plain one that did.

### 1. `task_success` — did it do what was asked?

The task's `notes` field is the operator's "what good looks like". It is
context, not an assertion list: use it to understand intent, do not grade
against it mechanically. Where `pinned_history` is present, judge the response
as a continuation of that conversation, not in isolation.

Deductions: missed part of the request, answered a different question,
hallucinated a fact or a capability, refused something reasonable, claimed to
have done something the tool trace shows it did not do.

### 2. `tool_path` — did it get there sensibly?

Read both tool traces. Compare:

- **Rejected calls** (`outcome: "rejected"`) — healthy tool, bad arguments. This
  is the single most reliable model-quality signal in the trace.
- **Round count and repetition** — the same call three times, or a wandering
  path to an answer one call would have given.
- **`stop_reason`** — `repeated_calls` or `max_rounds` means the loop ran out of
  road rather than the model finishing. A response that arrived that way is
  worse than an identical response that arrived cleanly.
- **Suppressed calls** (`outcome: "suppressed"`) are normal: an eval turn
  refuses writes by design. Judge the *choice* to make the call, never treat the
  suppression as a failure.
- **Cached calls** (`outcome: "cached"`) are the engine deduplicating an
  identical idempotent call, not the model repeating itself.

A response with no tool calls where the task plainly needed one loses this
dimension outright.

### 3. `persona_fit` — does it sound like this agent?

Read the agent's persona and, where the task is a skill command, the skill's
instructions. **Cite the specific clause behind any deduction** — you have repo
and API access, so a verdict that names the violated line is actionable, and
"felt off-brand" is not. If you cannot name what was violated, do not deduct.

### 4. `length` — is it the right size for its channel?

Verbosity bias survives blinding, so this dimension exists to be explicit about
it rather than let it leak into the other three. Longer is not better. A chat
reply that runs three paragraphs where one would do is worse, not more
thorough. Judge against what the channel and the persona call for.

## Recording a verdict

```
eval_verdict:
  item_id: 41
  winner: "a"            # a, b, or tie
  dimensions: {"task_success":"a","tool_path":"tie","persona_fit":"a","length":"b"}
  rubric_version: "v1"   # the "Rubric version" line at the top of this file
  notes: "A completed both halves of the request; B answered only the first.
          B's tool path was cleaner (2 rounds vs 4) and its reply was tighter,
          but persona.md line 'answer the whole question before offering more'
          is what A honoured and B did not."
```

- `rubric_version` is **always** the version at the top of this file, on every
  verdict including the operator's calibration marks. It is what lets the
  results view name the policy a win-rate was produced under, and it is how a
  queue worked across a rubric edit shows up as two versions rather than
  silently averaging two different rubrics. Bump the line above when you change
  the rubric below it.
- `winner` is the overall call and is what the win-rate counts. It is **not** a
  majority vote of the dimensions — `task_success` can carry a pair on its own.
- Use `tie` honestly. A forced preference between two equally good responses is
  noise, and the decision rule already treats ties as "not an upgrade".
- Keep `notes` to a few sentences, and make them the reason a reader could check.

## Reading the summary

`eval_summary` returns a verdict per candidate with its work shown:

- **The gate table** — rejected-rate, mean-rounds and cost-per-task deltas
  against their thresholds. A failed gate means *downgrade* regardless of your
  win-rate: objective regressions outrank judge preference, by design.
- **The verdict** is asymmetric. Gates alone can say `downgrade` or
  `no_regressions`; only your win-rate reaching `win_threshold` (default 0.55)
  can say `upgrade`.
- **`divergence`** — read this out loud to the operator when set. "Wins overall;
  regresses on tool_heavy" is usually the sentence that decides the swap.
- **`completeness`** — a run below the floor reports `inconclusive`. Say so
  plainly rather than reading the numbers as a result.

Report the verdict, the reason line, the gate table, and the divergence note.
Do not recommend applying a candidate the rule did not call an upgrade.
