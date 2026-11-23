---
title: "Observability"
description: "The audit log, tool-call telemetry, cost tracking, and OpenTelemetry."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-11-23T00:00:00+00:00
draft: false
weight: 60
toc: true
---

An agent that acts on your behalf while you are not watching has to be answerable for it afterwards. Denkeeper records what happened at four levels: an **audit log** of decisions, **telemetry** on tool reliability, **cost tracking** per model and agent, and optional **OpenTelemetry** export.

## Audit log

Every consequential decision emits an event. Enabled by default.

```toml
[audit]
enabled = true
retention_days = 30
buffer_size = 1000
```

Emission never blocks an agent turn, so if a burst overruns the buffer the excess events are dropped rather than stalling the agent. Drops are counted, along with events lost to a failed write, and once the buffer drains the log gets one `audit`/`dropped_events` event (status `error`) with the counts and the time window, so the gap is on record.

Events are grouped into categories:

| Category | Records |
|---|---|
| `llm` | One event per tool-loop round |
| `tool_call` | Tool invocations and their outcomes |
| `approval` | Approvals, denials, auto-approvals (with the deciding scope) |
| `supervisor` | Supervisor decisions, with the raw response preserved |
| `skill` | Skill lifecycle |
| `schedule` | Schedule firing |
| `session` | Session and channel switches |
| `channel` | Channel changes |
| `config` | Configuration changes |
| `mcp` | Tool-server health and lifecycle |
| `safety` | Panic and resume |
| `eval` | Dry-run and eval lifecycle |
| `audit` | The log's own records: prune checkpoints and counts of lost events |

Query them via `GET /api/v1/audit` — filterable by category, agent, status, source, free text, and time range — or from the dashboard's Audit Log page.

{{< callout context="note" >}}
Health-check failures for **remote** (SSE/HTTP) tool servers only emit a `health_fail` event after `health_fail_threshold` consecutive failures, default 3. A remote server blipping once is not worth waking you for. Stdio servers emit on the first failure, because a local subprocess dying is unambiguous.
{{< /callout >}}

### Integrity

The audit log is tamper-evident, not tamper-proof. Each event stores a hash of all its fields and of the event before it, so the events form a chain. `denkeeper audit verify` ([CLI reference](/docs/reference/cli/#denkeeper-audit-verify)) walks the chain and names the first event that was changed, removed, added or reordered.

- **Retention:** pruning removes the oldest events and writes an `audit`/`prune_checkpoint` event that records what it removed, so the chain still verifies afterwards. The checkpoint counts only when it is itself part of the verified chain.
- **Lost events:** a `dropped_events` event (above) marks where events never reached the log.
- **Older events:** events written before this version have no hash. Verify reports them as not covered, and they age out with `retention_days`.

What the chain does not catch:

- **Removing the newest events.** Nothing after them disagrees. Neither does wiping the table or restoring an older copy of the file.
- **A full rewrite.** The hash has no secret, so anyone who can write `audit.db` can change an event and recompute every hash after it.
- **A forged checkpoint.** For the same reason, someone can delete the oldest events and write a matching checkpoint. It stays visible, with a cutoff and a count you can compare with `retention_days`.
- **Edits to older events** without a hash.

To catch the first two, keep a copy of verify's `Head` line somewhere other than this machine and check it later, as described in [Checking a saved Head line](/docs/reference/cli/#checking-a-saved-head-line).

{{< callout context="caution" >}}
Do not run an older denkeeper against an upgraded `audit.db`. It writes events with no hash, which breaks the chain at the first of them: verify reports that event as `unhashed_row` and leaves every later event unchecked, until retention removes the chained events written before them. After that, the unhashed events show as not covered and age out in turn.
{{< /callout >}}

## Tool-call telemetry

Every tool call is recorded with an outcome, and the distinction between them is the useful part:

| Outcome | Meaning |
|---|---|
| `ok` | Succeeded |
| `rejected` | The tool was healthy and refused the arguments |
| `failed` | Transport or execution fault |
| `denied` | A decider, supervisor or operator said no |
| `approval_timeout` | Sent to an operator, and nobody answered in time |
| `supervisor_error` | The supervisor (or an enforcing decider) failed, and the hand-off to an operator went unanswered |
| `cached` | Served from the within-turn memo cache |

Summaries expose these as `rejection_count`, `failure_count`, `denial_count`, `approval_timeout_count`, `supervisor_error_count`, and `cached_count`. **`failure_count` is the "broken tool" signal** — a denial is a policy decision, and a rejection usually means the model passed bad arguments, so folding them together produces a number that cannot tell you anything. `supervisor_error_count` is the "broken reviewer" signal: the audit log's `supervisor` events carry the cause. `cached` results are excluded from fault counts and from duration averages, since nothing executed.

When a reviewer fails but an operator then answers, the operator's answer is the outcome. A decider failure only counts as `supervisor_error` when no supervisor ran after it, since a working supervisor decides why the call reached a human. Rows written before these two outcomes existed recorded both cases as `denied`.

An agent can read its own `supervisor` and `approval` audit events through the config MCP tool `approval_audit`. It never returns other agents' events or other categories.

Each call is attributed to at most one owning skill and version. Attribution is conservative: an explicit skill name wins, a single matched skill is used, and anything ambiguous is left blank rather than guessed. `by_tool_skill` then groups reliability per skill and version, so you can tell whether the edit you made to a skill last week made its tool use worse.

Per-session detail is available at `GET /api/v1/sessions/{id}/stats`, `/tool-calls`, and `/skills`; the aggregate at `GET /api/v1/telemetry/summary`.

## Cost tracking

Denkeeper ships a pricing registry covering roughly 70 models, so most setups need no cost configuration at all.

```toml
[costs]
default_rate_per_1k_tokens = 0

[costs.model_prices."my-org/custom-model"]
input = 3.0          # USD per million tokens
output = 15.0
cached_input = 0.3
```

Lookup priority:

1. Cost reported by the provider
2. Registry exact match
3. Registry longest-prefix match
4. `default_rate_per_1k_tokens`
5. `$0`, with a warning logged

Whichever source won is recorded as the `pricing_source` attribute, so a real price is always distinguishable from a fallback. An unknown model logs a warning rather than failing quietly — a cost of exactly zero in your dashboard should be treated as a missing price, not a free model.

Cached prompt tokens are tracked separately, read from the provider's own cache-read counts.

Limits are enforced per session against the agent's `cost_limit_soft` and `cost_limit_hard`; see [Sessions & Permissions](/docs/concepts/sessions/). Fallback rules can switch to a cheaper model when a limit is approached rather than simply stopping.

## OpenTelemetry

```toml
[otel]
enabled = true
traces_endpoint = "http://localhost:4318"
service_name = "denkeeper"
```

When enabled, Prometheus metrics are served at `GET /metrics` — **no authentication**, so do not expose that port publicly. Traces are exported only when `traces_endpoint` is set, so metrics alone need no collector.

Spans carry the attributes you would want when a turn behaves oddly: the model, the pricing source, and the effective tool-round cap including which skill imposed it.

Both settings have environment overrides — `DENKEEPER_OTEL_ENABLED` and `DENKEEPER_OTEL_TRACES_ENDPOINT` — for deployments that configure telemetry separately from the rest of the config.

## Retention

| Data | Setting | Default |
|---|---|---|
| Conversations | `[memory] retention_days` | 90 |
| Conversations | `[memory] max_conversations` | 10000 |
| Audit events | `[audit] retention_days` | 30 |

All three accept `0` for unlimited. Cleanup runs on `cleanup_interval` in each section.
