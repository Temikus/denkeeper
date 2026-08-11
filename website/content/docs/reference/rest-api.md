---
title: "REST API Reference"
description: "HTTP API endpoints for external integrations."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-11T00:00:00+00:00
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
  "user_name": "API User"
}
```

- `session_id` is auto-generated if omitted. Pass the same value in subsequent requests to continue the conversation.
- `agent` defaults to `"default"` if omitted.

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

Update an agent's configuration. Mutable fields: `name` (rename), `session_tier`, `llm_provider`, `llm_model`, `description`, `browser_url_allowlist`, `fallbacks`, `cost_limit_soft`, `cost_limit_hard`, `supervisor`, `supervisor_timeout`, `supervisor_context_messages`.

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

Named routing endpoints. See the [config reference](/docs/reference/configuration-reference/) for the `[[channels]]` schema.

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

No authentication required. Initialize the first-run configuration.

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
