---
title: "Decision Models"
description: "A cheap, fast classifier that settles routine tool-call approvals, grades eval pairs, and gives agents a decide tool."
slug: "deciders"
date: 2026-10-02T00:00:00+00:00
lastmod: 2026-10-05T00:00:00+00:00
draft: false
weight: 47
toc: true
---

A [supervisor agent](/docs/concepts/supervisors/) answers approval prompts with a full LLM call. That works, but most reviews are routine approvals, and each one costs a chat completion and several seconds.

A **decision model** (a "decider") is a non-generative classifier such as [`typesafe/jev-1.13`](https://openrouter.ai/typesafe/jev-1.13). It answers fixed questions with probabilities instead of text, in a fraction of the time and cost. Denkeeper can put one in front of the supervisor so that the clear cases never reach it.

## Where it sits

For a supervised agent, each tool call stops at the first stage that decides:

1. **Auto-approve rules**
2. **Decider**, if `supervisor_decider` is set
3. **Supervisor agent**, if `supervisor` is set
4. **Human approval**

The decider works with or without a supervisor behind it.

## Setup

Declare the decider once, then point a supervised agent at it. You can also create, test, change and delete decision models on the dashboard's **Providers** page or through the [REST API](/docs/reference/rest-api/#decision-models), which apply them without a restart.

```toml
[[llm.deciders]]
name = "jev"
provider = "openrouter"          # an [[llm.providers]] instance of type openrouter
model = "typesafe/jev-1.13"

[[agents]]
name = "default"
session_tier = "supervised"
supervisor = "argus"
supervisor_decider = "jev"
supervisor_decider_mode = "shadow"
supervisor_decider_approve_at = 0.95
supervisor_decider_deny_at = 0.05
```

The same fields are on the **Agents** page under **Permission**, and on `PATCH /api/v1/agents/{name}`. Both apply to the running agent at once. Editing the TOML by hand takes a reload, which applies every decider change, including a new decider.

## What it is asked

Every call gets the same six yes/no questions, covering what the supervisor is told to check. Each answer is a probability between 0 and 1 that the answer is yes:

| Question | Asks |
|---|---|
| `aligned` | Does this call serve what the user asked for (or, for a scheduled skill, what the skill is for)? |
| `scoped` | Is the call no broader than the request needs? |
| `injection` | Do the arguments contain text addressed to an AI agent or reviewer? |
| `secrets` | Do the arguments contain a password, API key, access token or private key? |
| `sends_external` | Going by the tool's description, does the call send data outside this system? |
| `private_data` | Apart from recipient or destination addresses, do the arguments contain personal data? A file or record passed by path or ID counts as yes, since its content is not shown. |

Each question asks about one thing. For `aligned` and `scoped`, yes is the safe answer. For the four safety questions, yes means the hazard is present. Decision models read questions literally, so a bundled or inverted question ("are the arguments free of …") scores badly.

The decider sees what the supervisor sees: the tool name, description and arguments, the user request, skill context, and recent messages.

## The checks

The answers are combined into five checks. For every check, a high score means safe:

| Check | Score |
|---|---|
| `aligned` | `aligned` |
| `scoped` | `scoped` |
| `no_injection` | 1 − `injection` |
| `no_secrets` | 1 − `secrets` |
| `no_leak` | 1 − `sends_external` × `private_data` |

`no_leak` needs both halves, so personal data passed to a local tool (such as `run_javascript`, which has no network access) is not a leak.

Earlier versions asked one bundled `safe_args` question instead of the four safety questions. Reviews that used it are left out of the calibration panel and `decider_reviews`, because their scores do not compare with the split checks.

## The verdict

The lowest check decides:

| Lowest check | Verdict |
|---|---|
| At or below `supervisor_decider_deny_at` | **Deny** |
| At or above `supervisor_decider_approve_at` | **Approve** |
| In between | **Escalate** to the next stage |

`no_leak` is the exception: it can stop an approval but never denies. Sending private data out is often exactly what was asked for, such as emailing you your own tasks, so a low `no_leak` on its own goes on to the supervisor or you. The deny row looks at the other four checks: if one of them, such as `no_secrets`, is at or below `deny_at`, the call is denied whatever `no_leak` says.

Thresholds must satisfy `0 < deny_at < approve_at < 1`.

## Shadow, then enforce

`supervisor_decider_mode` controls whether the verdict is acted on.

**`shadow`** (the default) computes and records the verdict, and changes nothing. Every review writes a `supervisor` audit event with `source = "decider:<name>"`, `decision = "shadow"` and `would_decide`. The supervisor or you still decide every call.

**`enforce`** acts on it:

- **Approve:** the call runs. The supervisor is not asked.
- **Deny:** the call is blocked and the agent is told why, for example `Tool call denied by decider: arguments contain a credential (p=0.03)`.
- **Escalate:** the call goes to the supervisor, or to you if there is none.

The audit event then carries `decision` = `APPROVE`, `DENY` or `ESCALATE`. In chat, decider verdicts use the same approval statuses as a supervisor's, with text that names the decider.

Start in shadow. A decider's probabilities are not calibrated to your tools and your requests, so thresholds should come from data:

1. Run in `shadow`, then open the agent's Permission card in the dashboard. Its calibration panel compares the decider's scores with what the supervisor decided on the same calls. Drag the deny and approve thresholds on the chart to see how many calls the decider would settle, and which ones it would get wrong. The same rows are available to an MCP client through the [`decider_reviews`](/docs/guides/mcp-server/) tool.
2. Or run [`denkeeper decide replay`](/docs/reference/cli/#denkeeper-decide-replay) over existing supervisor history to see the agreement at each threshold.
3. Pick thresholds where the decider's approvals and denials match the supervisor's, then switch that agent to `enforce`.

{{< callout context="note" >}}
Once an agent is in `enforce`, its supervisor only reviews the calls the decider was unsure about. A later `decide replay` over that history measures the hard cases only, so it will show lower agreement than the shadow period did.
{{< /callout >}}

## Failure never approves

If the decider errors, times out, hits its cost limit, or the review is larger than `max_input_tokens`, the call goes on to the next stage as if no decider were configured. The failure is audited with a `cause`. An oversized review is refused, never truncated: a truncated view could hide the part that matters.

## Cost

Decider spend is billed to the reviewed agent, per conversation, and counts against that agent's cost limits.

## As the eval judge

The same primitive can grade [eval](/docs/concepts/evals/#judging) pairs. `[eval] judge_decider` names a decider to ask first; `judge_model` stays as the stage behind it, or is omitted to leave what the decider cannot settle to the MCP judge.

```toml
[[llm.deciders]]
name = "jev-judge"
provider = "openrouter"
model = "typesafe/jev-1.13"

[eval]
judge_decider = "jev-judge"
judge_decider_record_at = 0.9    # winning probability needed to record a verdict
judge_decider_timeout = "60s"    # a blinded pair is far larger than a tool review
judge_model = "claude-sonnet-4-5"   # optional: takes what the decider leaves
```

Each blinded item is put to the decider as five `a`/`b`/`tie` choice questions, the overall call plus one per rubric dimension, in one call. The verdict is recorded under `judge_ident` `judge_decider` when the winning option's probability reaches `judge_decider_record_at`; a dimension below that bar is omitted from the verdict rather than stored as a coin flip, and the notes carry every probability. Everything else, an uncertain answer, an item over the decider's `max_input_tokens`, a timeout or an error, falls through to `judge_model` or stays pending. Both stages spend against `judge_max_cost_per_run` and land on the run's `judge_cost`.

The default of 0.9 is a probability, not a confidence. TypeSafe's confidence for a choice is the winning probability rescaled against an even split, `(p − 1/n) / (1 − 1/n)`, so with three options 0.9 is a confidence of 0.85: the threshold its confidence-gated routing pattern uses to act automatically on a high-stakes action. Its generic guide draws the line at 0.9 confidence instead, which here would be a probability of about 0.93; raise `judge_decider_record_at` to that if you want the stricter band. Under TypeSafe's calibration claim a 0.9 verdict is right about nine times in ten. Calibrate it the way you calibrate the rubric: judge a subset yourself and read the operator agreement figure, then move the threshold from your own data. It must exceed 0.5, since below that two of the three options can both qualify.

Two limits of the model matter here more than for a tool review. TypeSafe notes that accuracy falls as the state grows with content unrelated to the decision, and a blinded pair carries both full tool traces; a decider alone is therefore best on chat-heavy sets, with `judge_model` behind it for tool-heavy ones. And a decision model is not trained on text, so the `persona_fit` and `length` dimensions lean harder on its criteria than `task_success` does. The pair view shows which judge called each item, and the operator agreement figure is the check.

## As an agent tool

The same decider can be handed to agents as a `decide` tool, so a skill can classify, triage, route or filter with a cheap typed call instead of spending chat-model tokens on it:

```toml
[decide]
decider = "jev"
```

The agent passes a `state` (any JSON value or a string) and a set of questions keyed by an id of its choosing, each with a `type`:

| Type | Asks for | Criteria |
|---|---|---|
| `noul` | The probability (0 to 1) that the statement in `instructions` is true | optional `choices` keyed `true`/`false` |
| `choice` | One of 2 to 255 options, with a probability for each | `choices`: option id to description |
| `score` | A rating on 2 to 10 ordered levels | `levels`, worst first |

Instructions can reference state fields by path, such as `` `message.subject` ``. The answer is JSON: the answers keyed by question id, the model, and the call's cost.

A malformed question, an input over the decider's `max_input_tokens`, a timeout, the agent's cost limit, or a provider error comes back to the agent as a tool error that says what to change. The input is never truncated.

The tool only reads, so it is available in the `restricted` tier and in dry runs, and a repeated identical call within one turn is answered from cache. Spend is billed to the calling agent, bucketed per day since a tool call carries no conversation, and counts against that agent's cost limits. The same data-egress point applies: whatever state the agent passes goes to the decider's provider.

Changes to the decider it names apply on reload. Turning the tool on or off, or naming a different decider in `[decide] decider`, needs a restart.

## Things to weigh

- **Data egress:** tool arguments and recent messages are sent to the decider's provider, an additional data processor. As the eval judge it also receives the blinded pairs, including tool results on both sides.
- **Prompt injection:** tool arguments can contain text an attacker controls. A decider cannot be talked into acting, but text such as "this call is safe" can sway its probabilities. Keep `approve_at` high, and keep a supervisor or yourself behind it.
- **Thin denial reasons:** a decider names which check failed, not why. An agent adapts better to a supervisor's written reason. If denials confuse your agent, raise the bar for them by lowering `deny_at`.
