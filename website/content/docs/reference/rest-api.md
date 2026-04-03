---
title: "REST API Reference"
description: "HTTP API endpoints for external integrations."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-29T00:00:00+00:00
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

Approve a pending request.

### `POST /api/v1/approvals/{id}/deny`

**Scope:** `approvals:write`

Deny a pending request.

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

All requests (except health) require a bearer token:

```bash
curl -H "Authorization: Bearer dk_yourkey" https://localhost:8080/api/v1/approvals
```

API keys are scoped — a key with only `chat` scope cannot access `/api/v1/approvals`.

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

### `DELETE /api/v1/tools/{name}`

**Scope:** `tools:write`

Remove a tool server. The process is stopped and the configuration is removed from TOML.

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
