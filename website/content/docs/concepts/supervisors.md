---
title: "Supervisors & Auto-Approval"
description: "Letting an LLM reviewer, or a standing rule, answer approval prompts for you."
slug: "supervisors"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-14T00:00:00+00:00
draft: false
weight: 45
toc: true
---

Supervised agents ask before every tool call. That is the right default, but it means an agent is useless while you are asleep — a scheduled job that needs one tool call will sit waiting until you tap a button.

Denkeeper offers two ways to answer that prompt without you: **auto-approve rules**, which are standing decisions about specific tools, and a **supervisor agent**, which is a second LLM that reviews each call on its merits.

## The approval chain

For a supervised agent, each tool call runs this gauntlet in order, stopping at the first thing that decides:

1. **Auto-approve rules** — a matching rule executes immediately
2. **Supervisor agent** — if configured, reviews and returns APPROVE, DENY, or ESCALATE
3. **Human approval** — the Approve/Deny buttons

Anything not decided by 1 or 2 reaches you. Nothing skips the chain.

## Auto-approve rules

A rule says "this tool never needs asking about again". There are three scopes, checked in this order:

| Scope | Lives in | Created by | Lifetime |
|---|---|---|---|
| `config` | TOML | Operator, in the config file | Until config changes |
| `session` | Memory | The *Auto (session)* button, dashboard, or REST | 15 minutes, conversation-scoped |
| `permanent` | SQLite | The *Auto (always)* button, dashboard, or REST | Until deleted |

Declare `config` rules per agent:

```toml
[[agents]]
name = "default"
session_tier = "supervised"
auto_approve_tools = ["web_search", "kv_get"]
```

All three scopes answer the same question, so the ordering cannot change an approve/deny outcome — it fixes *attribution*, keeping `scope=config` stable across sessions, and skips a database lookup for the hottest calls.

{{< callout context="note" >}}
`config` rules cannot be weakened at runtime. `POST /api/v1/auto-approve` rejects `scope: "config"` with a `400`, and config rules are listed with an empty `id` so there is nothing for a `DELETE` to address. Removing one means editing the TOML file.

Names that do not match any advertised tool are **warned about and kept**, never dropped — a remote MCP server that connects late must not silently weaken your policy in the window before it appears.
{{< /callout >}}

## Supervisor agents

A supervisor is an ordinary agent pointed at the job of reviewing another agent's tool calls.

```toml
[[agents]]
name = "default"
session_tier = "supervised"
supervisor = "argus"
supervisor_timeout = "30s"
supervisor_context_messages = 5

[[agents]]
name = "argus"
session_tier = "autonomous"
llm_model = "claude-haiku-4-5"
```

When a tool call reaches the supervisor, it gets a one-shot LLM call — the tool name and arguments, its description, and the last few conversation messages — and answers with one of:

| Decision | Result |
|---|---|
| **APPROVE** | The call executes |
| **DENY** | The call is blocked, and the supervisor's reason is fed back to the agent's LLM so it can adapt |
| **ESCALATE** | Falls through to human approval |

A timeout or an error also falls through to human approval. The supervisor fails **open to you**, never open to the tool.

### Constraints

Validation rejects configurations that would deadlock or recurse:

- The supervisor must exist
- It must not itself be supervised — no chains
- It must not use the `supervised` tier, which would deadlock
- `supervisor` is only meaningful on a supervised agent
- An agent that is referenced as a supervisor cannot be deleted

The review is a single call with no storage, no skills, and no tool loop of its own, which is what keeps it cheap enough to sit in front of every tool call. A small fast model is usually the right choice.

### Observability

Every decision emits a `supervisor` audit event, preserving the raw response alongside the parsed decision and reason. Tool-approval events carry a matching status — `supervisor_approved`, `supervisor_denied`, `supervisor_escalated`, or `supervisor_error` — which surfaces in the dashboard's Chat view and as a filter chip in the Audit Log.

## The post-turn reviewer

Distinct from the supervisor, and easy to confuse with it. The supervisor gates tool calls *before* they run; the reviewer looks at a turn *after* it finishes, to maintain the agent's memory and suggest skill improvements.

```toml
[[agents]]
name = "default"
reviewer_model = "claude-haiku-4-5"
nudge_memory_interval = 10
nudge_skill_interval = 25
```

Leaving `reviewer_model` empty disables it. It runs fire-and-forget through a no-op sender, so it cannot message you.

{{< callout context="note" >}}
The reviewer is **capability-reduced, not supervised** — a deliberate design choice. Approval gating exists only at the engine tier, and the reviewer sends through a no-op, so it could never be meaningfully supervised. Instead it is handed a deliberately small tool set: `skill_list`, `skill_get`, `skill_read_file`, `persona_get`, and append-only `persona_memory_manage`. It can read skills and report improvements as text, but it cannot rewrite them.

It runs at the `supervised` tier rather than `restricted`, because `restricted` omits tool use altogether and would block every call it needs to make.
{{< /callout >}}

See the [configuration reference](/docs/reference/config/) for every supervisor and reviewer knob.
