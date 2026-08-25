---
title: "Web Dashboard"
description: "The built-in browser UI for chat, approvals, configuration, and audit."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-25T00:00:00+00:00
draft: false
weight: 5
toc: true
---

Denkeeper embeds a dashboard in the binary. There is nothing extra to install or serve — if the API is running, the dashboard is at the same address, by default `http://localhost:8080`.

It is enabled by default. Disable it, along with the REST API, with `[api] enabled = false`.

## First login

On first run with no API keys and no password configured, Denkeeper writes a one-time setup PIN to its logs:

```
INFO FIRST-RUN SETUP PIN pin=482937
INFO Enter this PIN in the web dashboard to create your admin account.
```

Enter it in the dashboard to create an admin account. The PIN is single-use, and it is never exposed through any API endpoint — reading it requires access to the logs, which is what stops someone who can merely reach the port from claiming the instance.

See [First Run](/docs/getting-started/first-run/) for the full flow, and [Security](/docs/concepts/security/) for password, session-cookie, and OIDC options.

## The pages

| Page | What it is for |
|---|---|
| **Overview** | Instance health, recent activity, onboarding checklist |
| **Chat** | Talk to an agent, with streaming output and inline approvals |
| **Sessions** | Browse conversations; per-session cost, tool calls, and skill usage |
| **Agents** | Create and configure agents, personas, supervisors |
| **Channels** | Routing endpoints and which adapter each is active on |
| **Skills** | Create, edit, and preview skills |
| **Schedules** | Recurring jobs, with dry-run previews |
| **Tools** | MCP servers: health, enable/disable, restart, OAuth |
| **Browser** | Browser profiles and active sessions |
| **Approvals** | Pending approvals and auto-approve rules |
| **Evals** | Compare a candidate model against your current one on saved test cases ([Evals](/docs/concepts/evals/)) |
| **Audit Log** | Filterable event history |
| **Costs** | Spend by agent, model, and time range |
| **KV** | Inspect and edit the agent key-value store |
| **Providers** | LLM provider instances and global defaults |
| **API Keys** | Create, rotate, and revoke keys |
| **Server Config** | Runtime settings, reload, restart |
| **Settings** | Login preferences and session management |

Configuration changes made here are written back to your TOML file, so they survive a restart. Formatting and comments are **not** preserved — the file round-trips through the parser — and a `.bak` backup is written before each save.

## Live updates

The dashboard connects over WebSocket at `GET /api/v1/ws` and receives the same event stream the REST API exposes over SSE: `content`, `thinking`, `tool_start`, `tool_end`, `tool_approval`, `usage`, and `done`.

If the WebSocket fails to connect three times, it falls back to SSE automatically. A per-connection replay buffer means a brief network drop does not lose the events that arrived while you were disconnected.

```toml
[api]
websocket_enabled = true
websocket_max_connections = 0     # 0 = unlimited
websocket_replay_buffer_ttl = "5m"
```

## Approvals from the browser

Supervised agents surface tool-call approvals inline in Chat, and in the Approvals page. Alongside Approve and Deny, **Always Approve** creates an auto-approve rule so that tool stops asking — see [Supervisors & Auto-Approval](/docs/concepts/supervisors/) for how the scopes differ.

Approval requests expire after 24 hours.

## Behind a reverse proxy

Set `api.external_url` to the address users actually reach, so OAuth callback URLs are built correctly:

```toml
[api]
listen = ":8080"
external_url = "https://denkeeper.example.com"
```

The proxy must forward WebSocket upgrade headers, or the dashboard will silently fall back to SSE. If live updates feel laggy behind a proxy but work locally, check that first.

Serving the dashboard over anything other than a trusted network without TLS is a bad idea — session cookies are marked `Secure`, so they will not be sent over plain HTTP from a remote host.
