---
title: "Evals"
description: "Comparing a candidate model against the one you run today, on your own conversations."
slug: "evals"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-25T00:00:00+00:00
draft: false
weight: 57
toc: true
---

A [dry run](/docs/concepts/dry-runs/) answers one question once: what would this turn do? An eval asks it across a set of saved turns, twice over — once on your current configuration, once on a candidate — and does the arithmetic. It is the answer to "is this new model actually better *for my agent*", which a benchmark leaderboard cannot tell you: what is being evaluated is the model **inside your harness**, with your persona, your skills, and your tools.

## The loop

1. **Build a test set** from real turns — saved from Chat, suggested from history, or imported as JSONL.
2. **Run** the set against your current config and a candidate. Samples execute on the agent's live engine under the dry-run execution policy: reads happen, writes are suppressed, nothing is persisted.
3. **Read the objective scorecard** — rejected and failed tool-call rates, mean rounds, cost, latency, per-task deltas. This half can reject a candidate on its own.
4. **Judge the blinded A/B pairs** from Claude Code with the `/judge-eval` skill. This half is what can *promote* one.
5. **Read the verdict** with its gate table, and apply the model to the agent if it is an upgrade.

## Test sets

A test set is a named collection of test cases. A case is a prompt, a category, optional pinned history, and optional notes.

| Field | Meaning |
|---|---|
| `prompt` | The user turn to replay |
| `category` | One of `chat`, `skill_command`, `scheduled`, `tool_heavy` (default `chat`) |
| `pinned_history` | `{role, content}` turns replayed verbatim as the context preceding the prompt |
| `notes` | Judge context. Never parsed as assertions — there is no assertion DSL |

Pinned history is captured at save time rather than re-read from the source conversation at run time, because the source drifts: clearing a session empties it, retention prunes it, and its latest window is not the window that preceded the saved message. A test case that silently re-scopes itself between runs is not a test case.

There are three fill paths:

- **Save as test case** in the Chat page's message menu, optionally pinning the preceding turns.
- **Suggest from history** — `GET /api/v1/eval/suggest` mines past turns for ones worth saving: any rejected or failed tool call, three or more tool rounds, a reply cost in the pool's top decile, or a command-triggered skill. Candidates come back **stratified across the four categories** rather than ranked overall, because a set drawn purely by interestingness would be all failures and would represent nothing the agent normally does. Turns already saved as a task are skipped. Nothing is written — accepting a candidate is a separate call.
- **Import JSONL** — `POST /api/v1/eval/task-sets/{name}/import`, one case per line, all-or-none so a typo halfway down leaves the set untouched. `GET .../export` is the other half, so a curated set can be hand-edited or committed to git.

A test set is an appreciating asset: the next candidate model starts here rather than at a blank page.

## Runs

A run names a task set, a base agent, and two or more **variants**. A variant is an overlay on the base agent's configuration — `llm_model`, `llm_provider`, or neither:

```json
{
  "task_set": "regression",
  "base_agent": "pamela",
  "variants": [
    { "name": "incumbent" },
    { "name": "candidate", "llm_model": "moonshotai/kimi-k3" }
  ],
  "k": 3,
  "sample_tasks": 10,
  "cost_cap": 2.0
}
```

An empty variant runs the agent's live config. By convention the incumbent is listed first: per-task deltas and the pairing are baselined against the first variant by creation order.

- `k` is how many samples each (task, variant) pair runs, so the metrics have something to average over. Defaults to `[eval] default_k`.
- `sample_tasks` runs a **stratified random subset** of the set instead of all of it — the cheap first signal. The server draws it, and the drawn ids are pinned on the run, so a task added to the set afterwards cannot retroactively change what the run was measuring. `0`, or a value at or above the set size, runs everything.
- `cost_cap` defaults to `[eval] max_cost_per_run`.

`POST /api/v1/eval/estimate` prices a prospective run before it exists, with the same request shape. The per-(task, variant) basis is, in order: **history** (the task's source conversation has real telemetry, giving an honest per-exchange average, scaled by the list-price ratio when the variant runs a different model), **list price** (the advertised per-million-token price against a nominal per-turn budget), or **unknown**. Nothing is fabricated — a variant that can be priced neither way reports `basis: "unknown"` with a zero range, and the caller shows the hard cap alone.

### Bounds and failure

A run is bounded twice, by spend and by rate, and both are always in force — there is deliberately no uncapped value.

- The cost cap is checked *before* each dispatch, so a sample already in flight always finishes; the run then ends `capped` and keeps its partial results. Never a silent truncation.
- `[eval] max_concurrent` bounds how many samples run at once, process-wide across all runs.
- `POST /api/v1/panic` cancels active runs along with everything else, and resume deliberately does not revive them: a panic is not a pause.
- A sample that fails takes only itself down and records its error. A run-level `failed` means a store error, never a provider hiccup.
- Below `[eval] completeness_floor` the scorecard still reports its numbers but is flagged **inconclusive** rather than reading a verdict off thin data.

### Isolation

Samples run on the agent's **live** engine — real persona, real skills, real tools — because a capability-reduced engine would measure a different system than the one in production, and the unit of evaluation is the model in its harness. Safety comes from the execution policy instead: the eval path never reaches the persistence layer, so conversations, telemetry, memory, nudges and the post-turn reviewer are untouched structurally rather than by filtering. Variant overlays clone the router rather than mutating it, so a live turn in flight can never be retargeted.

Eval events are audited like live turns, attributed to a pseudo-agent `{agent}#eval:{variant}` with `source = "eval"`, and hidden from the dashboard behind the Audit Log's **Previews** toggle. See [Dry Runs & Previews](/docs/concepts/dry-runs/) for the full policy and the audit-marking rules.

## Judging

The objective half can reject a candidate but cannot promote one — "cheap and quiet" is not the same as "better". That call needs a judge.

When a run reaches a terminal state it **pairs itself**: for each (task, k) it pairs the baseline sample against each candidate's, assigns the pair a random A/B identity that never leaves the server, and queues **two** judgment items with the presentation order swapped. Pairing runs for `capped` and `stopped` runs too, so a terminal run arrives at the judge queue already populated. A (task, k) whose sample is missing or failed on either side yields no pair, which is why completeness counts pairs separately from samples.

Blinding is total and the payload is **built rather than filtered** — withheld: variant name, model, provider, serving upstream, cost, latency, token usage, tool-call duration (a consistently slower side is an identity hint), and the sample's conversation id. Filtering fails open the day a column is added; construction fails closed.

The judge is Claude Code over Denkeeper's [MCP server](/docs/guides/mcp-server/), working the queue with `eval_pending` → `eval_get_pair` → `eval_verdict`, then reading `eval_summary`. Give it a key scoped to `eval:read,eval:write` and nothing more: that key can judge a run but cannot start one, which is the right shape given runs spend real tokens.

```bash
denkeeper keys create judge --scopes eval:read,eval:write
claude mcp add --transport http denkeeper https://your-denkeeper/api/v1/mcp \
  --header "Authorization: Bearer $DENKEEPER_JUDGE_KEY"
```

The rubric lives in the repo at `.claude/skills/judge-eval/SKILL.md`, where it can be read and edited: four dimensions in priority order — `task_success`, `tool_path`, `persona_fit`, `length` — with instructions to cite the specific persona or skill clause behind any deduction. Unknown dimension names are rejected rather than stored, because a typo that silently vanishes from the results is worse than a failed call.

{{< callout context="note" >}}
**Calibrate a new rubric before trusting it headless.** Judge a random ~20-item subset yourself and record your own call alongside the judge's (`judge_ident: "operator"`). Operator marks sit beside the judge's rather than replacing it, are excluded from the win rate, and feed only the agreement figure `eval_summary` reports. Below roughly 80 % agreement, fix the rubric first — a drifted rubric quietly devalues every later run.
{{< /callout >}}

A pair counts only when **both** presentation orders carry a verdict. If the two calls name different sides, the judge tracked position rather than quality and the pair records as a tie — so position bias splits the vote instead of deciding it. A half-judged pair is not evidence and stays out of the tally.

## The verdict

`GET /api/v1/eval/runs/{id}/summary` returns the verdict with its work. The rule is conjunctive and asymmetric, evaluated in this order:

| Condition | Verdict |
|---|---|
| Below `completeness_floor` | `inconclusive` |
| Any objective gate failed | `downgrade`, naming the gate |
| Nothing judged yet | `no_regressions` |
| Judge win rate ≥ `win_threshold` | `upgrade` |
| Otherwise | `downgrade` |

The three gates are `gate_rejected_rate_pp` (rise in the rejected tool-call rate, in percentage points), `gate_rounds_pct` (rise in mean tool-call rounds) and `gate_cost_pct` (rise in mean cost per task). They can never promote a candidate, and a judge preference can never override one of them.

Every verdict carries the gate table (value, delta, threshold, unit, pass/fail), a one-line reason, the win-rate against its threshold, and a per-category breakdown, so a candidate that wins overall while regressing on tool-heavy tasks says so out loud rather than hiding inside an average:

```
downgrade: mean rounds regressed +35.0% against a +20.0% threshold
upgrade: judge win-rate 62% over 45 judged pair(s) meets the 55% threshold, and no objective gate regressed
  ↳ wins overall; regresses on tool_heavy
```

`GET /api/v1/eval/runs/{id}/pairs` is the operator's unblinded view of the same evidence: which variant produced each side, every verdict with its dimensions and notes, and each pair's resolved outcome. It is REST-only on purpose — the judge's MCP surface must not be able to look up which variant produced which response.

## In the dashboard

The **Evals** page carries the loop as far as it has shipped: an empty state that explains it, an inline JSONL importer, a launcher, and the run list.

The launcher is the simple case — "compare current vs candidate on a test set": pick the agent (its current model is shown), pick a candidate model with its pricing, pick a test set, then choose **Quick check** (10 cases, one run each) or **Full eval** (the whole set at `[eval] default_k`). The cost cap is editable and sits next to the live estimate, so it is never a mystery whether you are about to spend $0.10 or $2.

Each run shows a status chip, a progress bar of turns done against expected, spend against the cap, an ETA while active, and a Stop button behind a confirm. Progress is polled and nudged by the `eval_progress` WebSocket frame, which is a droppable convenience — `GET /api/v1/eval/runs/{id}` is the authoritative view. Leaving the page loses nothing; a run continues in the background.

{{< callout context="caution" >}}
The in-page results view is still in flight. Today the run card's **Results** panel is a placeholder: read the scorecard and verdict through `GET /api/v1/eval/runs/{id}/summary` and the judged pairs through `GET /api/v1/eval/runs/{id}/pairs`. The suggest-from-history cards are likewise API-only for now (`GET /api/v1/eval/suggest`).
{{< /callout >}}

## Configuration

Every `[eval]` key has a default, so the subsystem needs no configuration to work — see the [`[eval]` reference](/docs/reference/config/) for the table. `GET /api/v1/eval/config` returns the same values at runtime, which is how the dashboard shows the policy the server actually judges against instead of hardcoding a copy. Thresholds are TOML plus a reload; there is no runtime writer, and every verdict carries the thresholds it was measured against.

Full request and response shapes are in the [REST API reference](/docs/reference/rest-api/).
