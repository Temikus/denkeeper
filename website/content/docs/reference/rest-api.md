---
title: "REST API Reference"
description: "HTTP API endpoints for external integrations."
slug: "rest-api"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-25T00:00:00+00:00
draft: false
weight: 30
toc: true
---

The REST API is enabled with `[api] enabled = true` in your config. All authenticated endpoints require a `Authorization: Bearer dk_...` header.

## Health

### `GET /api/v1/health`

No authentication required. Returns `200 OK` when the server is running.

## Discovery

### `GET /llms.txt`

No authentication required. Returns a plain-text summary of this Denkeeper instance intended for LLM clients — base URL, authentication notes, key endpoints, and a list of configured agents with their descriptions. Useful for programmatic discovery when connecting an AI assistant to a Denkeeper instance.

### `GET /api/v1/openapi.json`

No authentication required. Returns the generated OpenAPI 2.0 specification for this instance. This spec is the canonical machine-readable reference — it is generated from the handler annotations and CI-gated against drift, so it is authoritative where this page and the spec disagree.

## Chat

### `POST /api/v1/chat`

**Scope:** `chat`

Send a message to an agent and receive a response.

**Request body:**

```json
{
  "agent": "default",
  "session_id": "optional-session-id",
  "message": "Hello!",
  "user_id": "api-user",
  "user_name": "API User",
  "channel": "optional-channel-name"
}
```

- `session_id` is auto-generated if omitted. Pass the same value in subsequent requests to continue the conversation.
- `agent` defaults to `"default"` if omitted.
- `channel` is optional; when set, the message routes through the named [channel](/docs/concepts/channels/) instead of a direct agent binding.

**Response (JSON):**

```json
{
  "session_id": "abc123",
  "response": "Hello! How can I help you?"
}
```

**Response (SSE):** Set `Accept: text/event-stream` for streaming:

```
data: {"type":"content","text":"Hello! "}

data: {"type":"content","text":"How can I help you?"}

data: {"type":"done","session_id":"abc123"}
```

**SSE event types:** `content`, `thinking`, `tool_start`, `tool_end`, `tool_approval`, `usage`, `done`, `error`.

### `GET /api/v1/ws`

**Scope:** `chat`

Upgrades to a bidirectional WebSocket connection. Authentication is via `?token=` query parameter (API key auth) or session cookie. The WebSocket carries the same event types as SSE, plus supports sending chat requests and approval responses as JSON frames.

The web dashboard connects via WebSocket by default and falls back to SSE after 3 failed reconnect attempts. Configure with `api.websocket_enabled`, `api.websocket_max_connections`, and `api.websocket_replay_buffer_ttl` in your config.

### `GET /api/v1/models`

**Scope:** `agents:read`

List available LLM models from all configured providers.

### `GET /api/v1/models/details`

**Scope:** `agents:read`

Get detailed model information including pricing data.

## LLM Providers

### `GET /api/v1/llm/providers`

**Scope:** `admin`

List all LLM providers with their current configuration (API keys are redacted).

### `POST /api/v1/llm/providers`

**Scope:** `admin`

Create a named provider instance.

**Request body:**

```json
{
  "name": "lmstudio",
  "type": "openai",
  "base_url": "http://localhost:1234/v1",
  "api_key": "lm-studio"
}
```

### `DELETE /api/v1/llm/providers/{name}`

**Scope:** `admin`

Delete a provider instance. Rejected if any agent references it, or if it is the global `default_provider`.

### `PATCH /api/v1/llm/providers/{name}`

**Scope:** `admin`

Update a provider's configuration (API key, base URL, etc.). Changes take effect immediately and are persisted to config.

**Request body:**

```json
{
  "api_key": "sk-...",
  "base_url": "https://api.openai.com/v1"
}
```

### `PATCH /api/v1/llm/config`

**Scope:** `admin`

Update global LLM configuration (default provider, default model).

**Request body:**

```json
{
  "default_provider": "anthropic",
  "default_model": "claude-sonnet-4-5"
}
```

## Server Admin

### `GET /api/v1/server/config`

**Scope:** `admin`

Server configuration including version, build info, CORS origins, and WebSocket settings.

### `PATCH /api/v1/server/config`

**Scope:** `admin`

Update server config (CORS origins, WebSocket settings).

### `POST /api/v1/server/reload`

**Scope:** `admin`

Reload the server configuration from disk without restarting.

### `POST /api/v1/server/restart`

**Scope:** `admin`

Restart the server process.

## Sessions

### `GET /api/v1/sessions`

**Scope:** `sessions:read`

List all conversations.

### `GET /api/v1/sessions/{id}/messages`

**Scope:** `sessions:read`

Get all messages for a session.

### `GET /api/v1/sessions/{id}/stats`

**Scope:** `sessions:read`

Session telemetry summary (model, provider, cost, token breakdown per message).

### `GET /api/v1/sessions/{id}/tool-calls`

**Scope:** `sessions:read`

Tool call records for a session (name, server, duration, success/error, round).

### `GET /api/v1/sessions/{id}/skills`

**Scope:** `sessions:read`

Skill usage records for a session.

### `DELETE /api/v1/sessions/{id}`

**Scope:** `sessions:write`

Delete a conversation and all its messages. Returns `204 No Content`. Idempotent.

### `POST /api/v1/sessions/{id}/clear`

**Scope:** `sessions:write`

Delete the session's messages and telemetry, but **keep the conversation row** — session identity survives, so the same ID continues to work. Accepts an optional `?agent=` hint. This is what `/clear` does in Telegram.

### `POST /api/v1/sessions/{id}/compact`

**Scope:** `sessions:write`

Replace the session's history with a single `[Session compacted]` summary message. Accepts an optional `?agent=` hint.

**Response:**

```json
{ "summary": "..." }
```

### `POST /api/v1/sessions/{id}/stop`

**Scope:** `chat`

Cancel the in-flight request for a session, if any.

## Telemetry

### `GET /api/v1/telemetry/summary`

**Scope:** `costs:read`

Aggregate telemetry summary. Accepts `?since=` and `?until=` query parameters for date filtering.

## Agents

### `GET /api/v1/agents`

**Scope:** `admin`

List all agents with metadata.

### `GET /api/v1/agents/{name}`

**Scope:** `admin`

Get agent details including persona directory, loaded persona sections, and MCP tool names.

### `POST /api/v1/agents`

**Scope:** `admin`

Create an agent. Creates the persona directory and persists an `[[agents]]` block to the TOML config.

**Request body:**

```json
{
  "name": "research",
  "llm_provider": "anthropic",
  "llm_model": "claude-sonnet-4-5",
  "session_tier": "supervised",
  "description": "Research assistant",
  "create_supervisor": {
    "name": "research-supervisor",
    "llm_model": "claude-haiku-4-5",
    "timeout": "30s",
    "context_messages": 5
  }
}
```

`create_supervisor` is optional and only valid when `session_tier` is `"supervised"`. When present, the companion supervisor agent is created atomically with the agent it reviews.

### `PATCH /api/v1/agents/{name}`

**Scope:** `agents:write`

Update an agent's configuration. Mutable fields: `name` (rename), `session_tier`, `llm_provider`, `llm_model`, `description`, `max_tool_rounds`, `browser_url_allowlist`, `fallbacks`, `cost_limit_soft`, `cost_limit_hard`, `supervisor`, `supervisor_timeout`, `supervisor_context_messages`, `supervisor_body_excerpt_len`, `supervisor_tool_desc_len`, `reviewer_model`, `reviewer_provider`, `review_max_iterations`, `review_timeout`, `nudge_memory_interval`, `nudge_skill_interval`. Every field is optional and only present ones change; omit a field to leave it as-is.

### `DELETE /api/v1/agents/{name}`

**Scope:** `admin`

Delete an agent and remove its `[[agents]]` block from the config. Rejected if the agent is referenced by a channel or schedule, is the last remaining agent, or is another agent's supervisor.

Persona files on disk are **not** deleted.

## Persona

### `GET /api/v1/agents/{name}/persona/{section}`

**Scope:** `agents:read`

Read one persona section. `{section}` is `soul`, `user`, or `memory` — corresponding to `SOUL.md`, `USER.md`, and `MEMORY.md`.

### `PUT /api/v1/agents/{name}/persona/{section}`

**Scope:** `agents:write`

Replace a persona section's contents.

## Channels

Named routing endpoints. See the [config reference](/docs/reference/config/) for the `[[channels]]` schema.

### `GET /api/v1/channels`

**Scope:** `channels:read`

List channels with their agent, adapter bindings, implicit flag, and currently-active adapter keys.

### `GET /api/v1/channels/{name}`

**Scope:** `channels:read`

Get one channel. The detail response adds `conversation_id`.

### `POST /api/v1/channels`

**Scope:** `channels:write`

Create a channel.

### `PATCH /api/v1/channels/{name}`

**Scope:** `channels:write`

Update a channel.

### `DELETE /api/v1/channels/{name}`

**Scope:** `channels:write`

Delete a channel.

### `POST /api/v1/channels/{name}/activate`

**Scope:** `channels:write`

Make this channel the active one for a given adapter key — the API equivalent of `/session <name>`.

**Request body:**

```json
{ "adapter_key": "telegram:12345" }
```

### `DELETE /api/v1/channels/{name}/activate`

**Scope:** `channels:write`

Clear the active override for an adapter key. Returns `409 Conflict` if that key is not currently active on this channel.

## Audit

### `GET /api/v1/audit`

**Scope:** `audit:read`

List audit events. Filters: `?category=`, `?agent=`, `?status=`, `?source=`, `?search=`, `?since=`, `?until=`, `?limit=`, `?offset=`.

`?exclude_source=eval,dryrun` omits preview turns. Dry-run and eval events carry the ordinary `llm` and `tool_call` categories, so `source` is the only axis that separates them from live traffic — which is why the exclusion applies to the statistics endpoint too.

### `GET /api/v1/audit/stats`

**Scope:** `audit:read`

Aggregate audit statistics. Accepts `?since=` and the same `?exclude_source=` filter as the list endpoint.

## Turn traces

### `GET /api/v1/traces`

**Scope:** `sessions:read`

Turn trace headers, newest first, without their payloads. Filters: `?agent=`, `?conversation_id=`, `?source=` (`live`/`dryrun`/`eval`), `?since=`/`?until=` (RFC3339), `?limit=` (default 50, max 200), `?offset=`.

The response repeats `capture`, `retention_days` and `max_trace_bytes` alongside the rows, so a caller can tell "nothing recorded yet" from "recording is off" without a second request.

### `GET /api/v1/traces/{id}`

**Scope:** `sessions:read`

One trace in full: `system_prompt` as it was assembled post-skill-injection, `history` as it went on the wire, `prompt`, `response`, and `tool_calls` with each call's round, arguments, result and outcome. When the trace exceeded `[eval] max_trace_bytes`, `truncation` reports what was dropped — oldest rounds first — so a trimmed trace is never read as a short turn.

Traces sit behind `sessions:read`, not `eval:read`: a trace is turn content, and the eval scopes exist for a judge that must never resolve a live prompt. Live turns are recorded only when `[eval] capture` is on; eval samples always are.

## Evals

An eval run compares two or more config variants of one agent over a saved set of test cases and reports an objective scorecard. The loop these endpoints serve is described in [Evals](/docs/concepts/evals/). Samples execute on the agent's live engine under the same execution policy dry runs use: reads run for real, writes are suppressed, and nothing is persisted to conversations, telemetry, or memory. Runs spend real tokens, bounded by a per-run cost cap and by `[eval] max_concurrent`.

### `GET /api/v1/eval/config`

**Scope:** `eval:read`

Returns the `[eval]` defaults and gate thresholds used to size and judge a run — `default_k`, `max_cost_per_run`, `max_concurrent`, `completeness_floor`, `win_threshold`, and the rest of the config used by `POST /eval/runs` and the verdict rule when a request doesn't override them.

### `GET /api/v1/eval/suggest`

**Scope:** `eval:read`

Past turns worth saving as test cases: any rejected or failed tool call, three or more tool rounds, a reply cost in the pool's top decile, or a command-triggered skill. Filters: `?agent=`, `?limit=` (default 20, max 100), `?since=` (RFC3339, default 90 days ago).

Each candidate carries `prompt`, `category`, `conversation_id`, `message_id`, `created_at`, the `signals` that earned it a place, and `preceding` — the turns before it, ready to pin as the test case's history.

Candidates are **stratified across the four categories** rather than ranked overall — a set drawn purely by interestingness would be all failures and represent nothing the agent normally does. Turns already saved as a task are skipped, and a turn carrying no signal is never offered. Nothing is written: accepting a candidate is a separate call to the task create endpoint. `501` when the store carries no telemetry.

### `POST /api/v1/eval/estimate`

**Scope:** `eval:read`

Prices a prospective run before creating it — same request shape as `POST /eval/runs` — so a cost cap can be sized sensibly ahead of spending real tokens.

Per (task, variant) the `basis` is, in order: `history` (the task's source conversation has real telemetry, giving an honest per-exchange average, scaled by the list-price ratio when the variant runs a different model), `list_price` (the variant's advertised per-million-token price against a nominal per-turn token budget), or `unknown`. Nothing is fabricated — a variant priceable neither way reports `unknown` with a zero range and the caller shows the cap alone. The response carries `low`, `high`, `currency`, `basis`, `tasks`, `k`, a `per_variant` breakdown, and a `note` when a sampled subset or an unpriceable task makes the figure less than a straight sum.

### `POST /api/v1/eval/task-sets`

**Scope:** `eval:write`

Create a named, empty test set. `409 Conflict` if the name is taken.

### `GET /api/v1/eval/task-sets`

**Scope:** `eval:read`

List test sets with their task counts.

### `GET /api/v1/eval/task-sets/{name}`

**Scope:** `eval:read`

One test set with its tasks, in creation order.

### `PATCH /api/v1/eval/task-sets/{name}`

**Scope:** `eval:write`

Rename a set or change its description.

### `DELETE /api/v1/eval/task-sets/{name}`

**Scope:** `eval:write`

Delete a set and its tasks. Returns `409 Conflict` while any run references it — a run's samples are only interpretable against the tasks that produced them.

### `POST /api/v1/eval/task-sets/{name}/tasks`

**Scope:** `eval:write`

Add a test case. This is what the Chat UI's "Save as test case" calls.

**Request body:**

```json
{
  "prompt": "what's on my plate today",
  "category": "chat",
  "pinned_history": [{ "role": "user", "content": "earlier turn" }],
  "notes": "should list the standup before anything else"
}
```

`category` is one of `chat`, `skill_command`, `scheduled`, `tool_heavy` (default `chat`). `pinned_history` is captured now and replayed verbatim at run time rather than re-read from the source conversation, which drifts. `notes` are judge context, never parsed as assertions.

### `PATCH` / `DELETE /api/v1/eval/task-sets/{name}/tasks/{id}`

**Scope:** `eval:write`

Edit or remove one test case.

### `GET /api/v1/eval/task-sets/{name}/export`

**Scope:** `eval:read`

JSONL, one task per line, for hand-editing or git-versioning a curated set.

### `POST /api/v1/eval/task-sets/{name}/import`

**Scope:** `eval:write`

Append JSONL tasks. All-or-none: every line is validated first, so a typo halfway down leaves the set untouched and the `400` names the offending line.

### `POST /api/v1/eval/runs`

**Scope:** `eval:write`

Create and start a run.

**Request body:**

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

At least two variants are required. An empty variant runs the agent's live config; by convention the incumbent is listed first, and per-task deltas are measured against it. `k` and `cost_cap` default to `[eval] default_k` and `max_cost_per_run`. An unregistered `llm_provider` is rejected here rather than failing every sample later.

`sample_tasks` runs a stratified random subset of the set instead of all of it; `0` or a value at or above the set size runs everything. The server draws the subset, because the drawn ids are pinned on the run and every expected-sample figure counts what was drawn — which also means a task added to the set afterwards cannot change what an existing run was measuring. `as_of` (RFC3339) pins the clock the samples see, so a replay is date-deterministic.

### `GET /api/v1/eval/runs`

**Scope:** `eval:read`

List runs newest first. Filters: `?task_set=`, `?status=`.

### `GET /api/v1/eval/runs/{id}`

**Scope:** `eval:read`

Status and progress: samples done out of expected, spend against the cap, and a rough ETA. This is the authoritative view; the `eval_progress` WebSocket frame is a droppable convenience on top of it.

### `POST /api/v1/eval/runs/{id}/stop`

**Scope:** `eval:write`

Cancel an active run. In-flight calls die on the context, queued samples never start, and the run finishes `stopped` with the samples it already produced. `409 Conflict` if the run is already terminal. The panic switch stops every active run the same way; resume does not revive them.

### `GET /api/v1/eval/runs/{id}/summary`

**Scope:** `eval:read`

The objective scorecard. Rates are tool-call level with cached and suppressed calls excluded, because nothing executed in either case. A run below `[eval] completeness_floor` still reports its numbers but is flagged inconclusive. The judgment block carries the win rate against its threshold, the operator agreement figure when calibration marks exist, and `rubric_versions`, the distinct set of rubric revisions the counted verdicts were made under.

### `GET /api/v1/eval/runs/{id}/pairs`

**Scope:** `eval:read`

The judged pairs with the blinding lifted: which variant produced each side, every recorded verdict with the presented letter resolved back to a variant name, its per-dimension winners, notes and rubric version, and the pair's resolved outcome from the candidate's point of view. Filter to one task with `?task_id=`.

Outcomes follow the aggregation rules exactly — `win` or `loss` only when both presentation orders carry a judge verdict naming the same variant, `tie` when the orders disagree (the judge tracked position, not quality), `pending` while half-judged. Operator calibration marks (`judge_ident` `operator`) are listed but never drive the outcome.

This is the operator's results view and is deliberately **not** reachable from the judge's MCP tools, which must not be able to look up which variant produced which response.

### `GET /api/v1/eval/runs/{id}/samples`

**Scope:** `eval:read`

Per-sample transcripts, including the full tool trace with arguments and results.

## Safety

### `POST /api/v1/panic`

**Scope:** `admin`

Emergency stop: cancels all in-flight requests and pauses the scheduler.

### `POST /api/v1/resume`

**Scope:** `admin`

Clear the panic state and resume the scheduler.

### `GET /api/v1/panic`

**Scope:** `admin`

**Response:**

```json
{ "panicked": true, "panic_time": "2026-08-11T09:15:00Z" }
```

Panic state is transient — it is cleared by a restart.

## Skills

### `GET /api/v1/skills`

**Scope:** `skills:read`

List all skills across all agents.

### `GET /api/v1/skills/{agent}`

**Scope:** `skills:read`

List skills for a specific agent.

### `GET /api/v1/skills/{agent}/{name}`

**Scope:** `skills:read`

Get full skill details including body content.

### `POST /api/v1/skills/{agent}`

**Scope:** `skills:write`

Create a new skill. The skill file is written to the agent's skills directory and registered in memory.

**Request body:**

```json
{
  "name": "daily-report",
  "description": "Generate daily summary",
  "version": "1.0.0",
  "triggers": ["command:report"],
  "body": "# Daily Report\nGenerate a summary of today's events."
}
```

### `PUT /api/v1/skills/{agent}/{name}`

**Scope:** `skills:write`

Update an existing skill. Fields are merged with existing values — only provided fields are changed.

**Request body:**

```json
{
  "description": "Updated description",
  "version": "2.0.0",
  "body": "# Updated content"
}
```

### `POST /api/v1/skills/{agent}/{name}/dry-run`

**Scope:** `skills:write`

Preview what a skill would do without persisting anything. The turn stores no messages, telemetry, or memory; only idempotent tools actually execute, and every other tool call returns a suppressed marker instead of running.

It sits behind the **write** scope despite persisting nothing, because it executes read tools and spends real tokens.

**Request body:**

```json
{
  "message": "log $12 for coffee",
  "mode": "command",
  "model": "claude-haiku-4-5",
  "as_of": "2026-08-11T08:00:00+10:00",
  "args": "--verbose"
}
```

All fields are optional. `mode` is `schedule`, `command`, or `message`; omitted, it is inferred — an explicit `message` implies message semantics, otherwise a `command:` trigger or a `[[schedules]]` entry naming the skill decides, defaulting to `message`. The modes are not cosmetic: only `schedule` injects the skill body directly, while `command` and `message` leave normal trigger matching to run, so a command preview genuinely exercises the trigger.

`as_of` pins the `## Current Date` in the system prompt. `model` runs the preview against a different model without touching the agent's live configuration.

**Response:** a transcript containing `prompt`, `response`, `rounds`, `model`, `requested_model`, `mode`, `scheduled_by`, `tokens_total`, `cost_usd`, `suppressed_count`, and a `tool_calls` array in which each entry carries `suppressed` alongside its outcome.

### `DELETE /api/v1/skills/{agent}/{name}`

**Scope:** `skills:write`

Delete a skill. Removes it from memory and deletes the skill file. Returns `204 No Content`.

## Schedules

### `GET /api/v1/schedules`

**Scope:** `schedules:read`

List all schedules with next/last run times.

### `POST /api/v1/schedules`

**Scope:** `schedules:write`

Create a new schedule. The schedule is registered in the scheduler and persisted to TOML config.

**Request body:**

```json
{
  "name": "morning-report",
  "schedule": "@daily",
  "channel": "telegram:123456",
  "skill": "daily-report",
  "session_mode": "isolated",
  "session_tier": "autonomous",
  "agent": "default",
  "tags": ["reporting"],
  "enabled": true
}
```

- `schedule`: cron expression (`0 8 * * 1-5`), named (`@daily`, `@hourly`), or interval (`@every 5m`).
- `channel`: format `adapter:externalID` (e.g. `telegram:123456`).
- `session_mode`: `isolated` (default) or `shared`.
- `enabled`: defaults to `true` if omitted.

### `PATCH /api/v1/schedules/{name}`

**Scope:** `schedules:write`

Partially update a schedule. Only provided fields are changed. The schedule is unregistered and re-registered with the new configuration.

### `POST /api/v1/schedules/{name}/dry-run`

**Scope:** `schedules:write`

Preview what a schedule would do when it fires, with the same no-persistence semantics as the skill dry-run above. Accepts the optional `model` and `as_of` fields.

The preview message is built by the same code the live scheduler uses, so the header, skill, cron expression, and tier match a real firing rather than being reconstructed.

### `DELETE /api/v1/schedules/{name}`

**Scope:** `schedules:write`

Delete a schedule. Unregisters it from the scheduler and removes it from the TOML config. Returns `204 No Content`.

## Costs

### `GET /api/v1/costs`

**Scope:** `costs:read`

Get cost summary.

## Approvals

### `GET /api/v1/approvals`

**Scope:** `approvals:read`

List all approval requests.

### `GET /api/v1/approvals/{id}`

**Scope:** `approvals:read`

Get a single approval request.

### `POST /api/v1/approvals/{id}/approve`

**Scope:** `approvals:write`

Approve a pending request. Add `?auto_approve=session` or `?auto_approve=permanent` to simultaneously create an auto-approve rule for future tool calls of the same type.

### `POST /api/v1/approvals/{id}/deny`

**Scope:** `approvals:write`

Deny a pending request.

## Auto-Approve Rules

### `GET /api/v1/auto-approve`

**Scope:** `approvals:read`

List all auto-approve rules. Filter by agent with `?agent=name`. The list includes TOML-declared `config`-scoped rules (`auto_approve_tools` on `[[agents]]`); these are read-only and returned with an empty `id`.

### `POST /api/v1/auto-approve`

**Scope:** `approvals:write`

Create an auto-approve rule.

**Request body:**

```json
{
  "agent": "default",
  "tool_name": "web_search",
  "scope": "permanent"
}
```

- `scope`: `"session"` (in-memory, cleared on restart) or `"permanent"` (persisted in SQLite). `"config"` is rejected with `400` — config-scoped rules can only be declared in TOML.

### `DELETE /api/v1/auto-approve/{id}`

**Scope:** `approvals:write`

Delete an auto-approve rule. Returns `204 No Content`.

## Setup

### `GET /api/v1/setup`

No authentication required. Returns the first-run setup status.

### `POST /api/v1/setup`

No authentication required — the endpoint locks itself after first use. Creates the first API key, defaulting to `name: "admin"` / `scopes: ["admin"]` if the body omits them, and returns the plaintext token (`201`). Returns `409 Conflict` once any active key already exists.

**Request body:**

```json
{ "name": "admin", "scopes": ["admin"] }
```

### `POST /api/v1/setup/account`

No authentication required, but gated by the one-time setup PIN written to the server logs on first run. Creates the admin account.

**Request body:**

```json
{ "pin": "482937", "password": "..." }
```

The PIN is single-use and is never exposed through any API endpoint — reading it requires access to the logs, which is what stops someone who can merely reach the port from claiming the instance.

## API Keys

### `POST /api/v1/keys`

**Scope:** `admin`

Create a new API key. The plaintext key is returned once in the response.

### `GET /api/v1/keys`

**Scope:** `admin`

List all API keys (secrets are never returned).

### `DELETE /api/v1/keys/{id}`

**Scope:** `admin`

Revoke an API key by ID.

### `DELETE /api/v1/keys/{id}/permanent`

**Scope:** `admin`

Permanently delete a revoked API key.

### `POST /api/v1/keys/{id}/rotate`

**Scope:** `admin`

Rotate an API key. Returns the new plaintext key once.

## Authentication

All API endpoints (except health, setup, auth, and metrics) require authentication. Two mechanisms are supported:

1. **Bearer token** — `Authorization: Bearer dk_...` header. API keys are scoped; a key with only `chat` scope cannot access `/api/v1/approvals`.
2. **Session cookie** — set by the password or OIDC login flow. Used by the web dashboard.

```bash
curl -H "Authorization: Bearer dk_yourkey" https://localhost:8080/api/v1/approvals
```

## Auth Admin

These endpoints require `admin` scope.

### `GET /api/v1/auth/status`

Returns auth configuration summary (password enabled, OIDC enabled, session settings, preferred login method).

### `GET /api/v1/auth/sessions`

List all active sessions.

### `DELETE /api/v1/auth/sessions/{id}`

Revoke a session.

### `DELETE /api/v1/auth/sessions`

Revoke every active session at once, forcing all dashboard users to log in again.

### `POST /api/v1/auth/password`

Change the server password. Verifies the current password before re-hashing.

```json
{ "current_password": "old", "new_password": "new" }
```

### `GET /api/v1/auth/oidc/test`

Test OIDC provider reachability (fresh discovery, 10 s timeout).

### `POST /api/v1/auth/preferences`

Set preferred login method (`auto`, `password`, or `apikey`).

```json
{ "preferred_method": "password" }
```

### `GET /api/v1/onboarding`

Checklist of 5 setup milestones. `show_onboarding` is `false` when all milestones are complete or the card has been dismissed.

### `POST /api/v1/onboarding/dismiss`

Persist `onboarding_dismissed = true` to the TOML config and hide the onboarding card.

### `POST /api/v1/onboarding/wizard-complete`

Persist `wizard_completed = true` to the TOML config, marking the guided setup wizard as finished.

## KV Store

### `GET /api/v1/kv/{agent}`

**Scope:** `kv:read`

List KV keys for an agent. Accepts optional `?prefix=` query parameter.

### `GET /api/v1/kv/{agent}/{key}`

**Scope:** `kv:read`

Get a value by key. Returns `404` if not found.

### `PUT /api/v1/kv/{agent}/{key}`

**Scope:** `kv:write`

Set a value. Body: `{"value": "...", "ttl": "5m"}` (`ttl` is optional; omit for no expiry).

### `DELETE /api/v1/kv/{agent}/{key}`

**Scope:** `kv:write`

Delete a key.

## Auth Endpoints

These endpoints do not require authentication.

### `GET /auth/config`

Returns the server's authentication configuration.

```json
{
  "password_enabled": true,
  "oidc_enabled": false
}
```

### `POST /auth/login`

Password login. Sets a session cookie on success.

**Request body:**

```json
{
  "password": "your-password"
}
```

**Response:**

```json
{
  "authenticated": true,
  "email": "admin"
}
```

Rate limited: 5 attempts per 15 minutes per IP. Returns `429 Too Many Requests` when exceeded.

### `POST /auth/logout`

Clears the session cookie.

```json
{
  "ok": true
}
```

### `GET /auth/session`

Check the current session status.

```json
{
  "authenticated": true,
  "email": "user@example.com"
}
```

Returns `{"authenticated": false}` when no valid session exists.

### `GET /auth/oidc/login`

Redirects to the OIDC provider's authorization endpoint. Only available when `[api.auth.oidc] enabled = true`.

### `GET /auth/callback`

OIDC callback. Exchanges the authorization code, verifies the ID token (including nonce), creates a session cookie, and redirects to `/#/overview`.

## Metrics

### `GET /metrics`

Prometheus metrics endpoint. No authentication required. Only available when `[otel] enabled = true`.

## Tools & Plugins

### `GET /api/v1/tools`

**Scope:** `tools:read`

List all configured MCP tool servers.

### `GET /api/v1/tools/{name}`

**Scope:** `tools:read`

Get details for a specific tool server.

### `POST /api/v1/tools`

**Scope:** `tools:write`

Add a new MCP tool server. The tool is started immediately and its configuration is persisted to TOML.

**Request body:**

```json
{
  "name": "filesystem",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/data"]
}
```

### `PUT /api/v1/tools/{name}`

**Scope:** `tools:write`

Edit a tool server's configuration. The server is restarted with the new settings and the configuration is persisted to TOML.

### `DELETE /api/v1/tools/{name}`

**Scope:** `tools:write`

Remove a tool server. The process is stopped and the configuration is removed from TOML.

### `GET /api/v1/tools/{name}/health`

**Scope:** `tools:read`

Get health status for a specific tool server. Returns `connected`, `error`, or `disabled` status with restart count, last error, and uptime.

### `GET /api/v1/tools/{name}/defs`

**Scope:** `tools:read`

List the tool definitions this server advertises.

### `POST /api/v1/tools/{name}/enable`

**Scope:** `tools:write`

Enable a tool server, starting its MCP process. Persisted to the TOML config.

### `POST /api/v1/tools/{name}/disable`

**Scope:** `tools:write`

Disable a tool server, stopping its process without removing its configuration. Persisted to the TOML config.

### `PUT /api/v1/tools/{name}/disabled-tools`

**Scope:** `tools:write`

Set which individual tools from this server are hidden from agents, leaving the server itself running.

### `POST /api/v1/tools/{name}/restart`

**Scope:** `tools:write`

Manually restart a tool server.

### `GET /api/v1/plugins`

**Scope:** `tools:read`

List all configured plugins.

### `GET /api/v1/plugins/{name}`

**Scope:** `tools:read`

Get details for a specific plugin.

### `POST /api/v1/plugins`

**Scope:** `tools:write`

Add a new plugin (subprocess or Docker).

### `DELETE /api/v1/plugins/{name}`

**Scope:** `tools:write`

Remove a plugin.

{{< callout context="note" >}}
Every `/api/v1/tools/{name}` endpoint returns **404** when the named tool is not registered. Other failures keep their own codes: `400` for malformed or rejected input, `500` for a failed restart or removal, `503` when the lifecycle manager is not wired.
{{< /callout >}}

## MCP OAuth

For remote SSE tool servers configured with `auth = "oauth"`.

### `GET /api/v1/tools/{name}/oauth`

**Scope:** `tools:read`

Get the OAuth token status for a tool.

### `POST /api/v1/tools/{name}/oauth/connect`

**Scope:** `tools:write`

Begin the authorization code flow with PKCE. Returns the URL to send the operator to.

### `DELETE /api/v1/tools/{name}/oauth/token`

**Scope:** `tools:write`

Revoke and delete the stored token for a tool.

### `GET /api/v1/tools/oauth/pending`

**Scope:** `tools:read`

List authorizations awaiting completion.

### `GET /api/v1/tools/oauth/callback`

No authentication — this is the browser redirect target for the provider. Set `api.external_url` so the callback URL is constructed correctly behind a reverse proxy.

## Browser

Available when `[browser] enabled = true`. These endpoints return `503` when browser automation is not configured.

### `GET /api/v1/browser/config`

**Scope:** `browser:read`

Get the effective browser configuration.

### `GET /api/v1/browser/profiles`

**Scope:** `browser:read`

List browser profiles.

### `GET /api/v1/browser/profiles/{name}`

**Scope:** `browser:read`

Get one browser profile.

### `DELETE /api/v1/browser/profiles/{name}`

**Scope:** `browser:write`

Delete a browser profile.

### `GET /api/v1/browser/sessions`

**Scope:** `browser:read`

List active browser sessions.

## Rate limiting

Per-key rate limiting is configured via `api.rate_limit` (requests per second). When exceeded, the API returns `429 Too Many Requests`.
