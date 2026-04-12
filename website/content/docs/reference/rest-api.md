---
title: "REST API Reference"
description: "HTTP API endpoints for external integrations."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-04-13T00:00:00+00:00
draft: false
weight: 30
toc: true
---

The REST API is enabled with `[api] enabled = true` in your config. All authenticated endpoints require a `Authorization: Bearer dk_...` header.

## Health

### `GET /api/v1/health`

No authentication required. Returns `200 OK` when the server is running.

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

### `DELETE /api/v1/sessions/{id}`

**Scope:** `sessions:read`

Delete a conversation and all its messages. Returns `204 No Content`. Idempotent.

## Agents

### `GET /api/v1/agents`

**Scope:** `admin`

List all agents with metadata.

### `GET /api/v1/agents/{name}`

**Scope:** `admin`

Get agent details including persona directory, loaded persona sections, and MCP tool names.

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

List all auto-approve rules. Filter by agent with `?agent=name`.

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

- `scope`: `"session"` (in-memory, cleared on restart) or `"permanent"` (persisted in SQLite).

### `DELETE /api/v1/auto-approve/{id}`

**Scope:** `approvals:write`

Delete an auto-approve rule. Returns `204 No Content`.

## Setup

### `GET /api/v1/setup`

No authentication required. Returns the first-run setup status.

### `POST /api/v1/setup`

No authentication required. Initialize the first-run configuration.

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

## KV Store

### `GET /api/v1/kv`

**Scope:** `kv:read`

List KV keys for the authenticated agent (or all agents with `admin` scope).

### `GET /api/v1/kv/{key}`

**Scope:** `kv:read`

Get a value by key.

### `PUT /api/v1/kv/{key}`

**Scope:** `kv:write`

Set a value. Optionally accepts a `ttl` field (seconds).

### `DELETE /api/v1/kv/{key}`

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

## Rate limiting

Per-key rate limiting is configured via `api.rate_limit` (requests per second). When exceeded, the API returns `429 Too Many Requests`.
