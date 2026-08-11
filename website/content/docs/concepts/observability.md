---
title: "Observability"
description: "The audit log, tool-call telemetry, cost tracking, and OpenTelemetry."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-11T00:00:00+00:00
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

Query them via `GET /api/v1/audit` — filterable by category, agent, status, source, free text, and time range — or from the dashboard's Audit Log page.

{{< callout context="note" >}}
Health-check failures for **remote** (SSE/HTTP) tool servers only emit a `health_fail` event after `health_fail_threshold` consecutive failures, default 3. A remote server blipping once is not worth waking you for. Stdio servers emit on the first failure, because a local subprocess dying is unambiguous.
{{< /callout >}}

## Tool-call telemetry

Every tool call is recorded with an outcome, and the distinction between them is the useful part:

| Outcome | Meaning |
|---|---|
| `ok` | Succeeded |
| `rejected` | The tool was healthy and refused the arguments |
| `failed` | Transport or execution fault |
| `denied` | Blocked at approval |
| `cached` | Served from the within-turn memo cache |

Summaries expose these as `rejection_count`, `failure_count`, `denial_count`, and `cached_count`. **`failure_count` is the "broken tool" signal** — a denial is a policy decision, and a rejection usually means the model passed bad arguments, so folding them together produces a number that cannot tell you anything. `cached` results are excluded from fault counts and from duration averages, since nothing executed.

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

Limits are enforced per session against the agent's `cost_limit_soft` and `cost_limit_hard`; see [Sessions & Permissions](/docs/concepts/sessions-permissions/). Fallback rules can switch to a cheaper model when a limit is approached rather than simply stopping.

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
