---
title: "Denkeeper as an MCP Server"
description: "Expose your instance to other MCP clients — Claude Code, IDEs, or another agent."
slug: "mcp-server"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-14T00:00:00+00:00
draft: false
weight: 40
toc: true
---

Elsewhere in these docs, MCP points *outward*: `[tools.*]` connects Denkeeper to servers that give its agents new capabilities.

This is the other direction. With `[api.mcp_server]` enabled, Denkeeper *is* an MCP server, and any MCP client — Claude Code, an IDE, another agent — can drive its agents, skills, schedules, and audit log.

## Enable it

Off by default. Turn it on:

```toml
[api.mcp_server]
enabled = true
transport = "streamable"   # or "sse" (legacy)
session_timeout = "30m"
chat_timeout = "2m"
```

The endpoint is mounted at `/api/v1/mcp`. While disabled it returns `404`, so enabling it is a deliberate act rather than a matter of knowing the path.

## Authentication

Every request needs a Bearer token — the same scoped API keys the REST API uses:

```bash
denkeeper keys create claude-code --scopes chat,skills:read,sessions:read
```

Scopes are enforced per tool, so a key that can chat cannot necessarily read your audit log. Give each client its own key with the narrowest scope set that does the job; revoking one then does not disturb the others.

## Connecting a client

Point your client at the endpoint with an `Authorization` header. For Claude Code:

```bash
claude mcp add --transport http denkeeper https://denkeeper.example.com/api/v1/mcp \
  --header "Authorization: Bearer dk_..."
```

Behind a reverse proxy, set `api.external_url` so generated URLs are correct.

## What it exposes

| Area | Tools |
|---|---|
| Chat | `chat` |
| Agents | `agent_list`, `agent_info` |
| Skills | `skill_list`, `skill_get`, `skill_create`, `skill_update`, `skill_delete`, `skill_revert` |
| Schedules | `schedule_list`, `schedule_create`, `schedule_update`, `schedule_delete` |
| Sessions | `session_list`, `session_messages`, `session_search`, `session_clear`, `session_compact` |
| Approvals | `approval_list`, `approval_resolve` |
| Tools | `tool_list`, `tool_health`, `tool_restart` |
| Channels | `channel_list` |
| Audit | `audit_events`, `audit_summary` |
| Telemetry | `cost_summary`, `telemetry_summary` |
| KV | `kv_get`, `kv_set`, `kv_list`, `kv_delete` |
| Safety | `panic`, `panic_status`, `resume` |

`agent_info` reports an agent's name, tier, provider, model, and skills, and adds `supervisor`, `persona_sections`, and `channels` only when they are non-empty — presence is the signal. Supervisor information is read from live wiring, so it reflects the state after a config reload rather than what the file said at startup.

Skill writes are disk-first: the file is written before memory is updated, so an IO error leaves the skill intact rather than creating a version that exists only in RAM until the next restart.

They are also journaled. Before any skill write, the file's exact prior bytes are recorded, and `skill_revert` replays them — undoing a create, update, rename or delete exactly once. Call it with a `skill` to undo that skill's most recent change, with a `transition_id` to undo a whole multi-skill edit (newest change first, so a rename followed by an update unwinds in the order that works), or with neither to undo the agent's most recent skill change. A revert is itself a recorded change, so calling it twice in a row *redoes* the original change rather than stepping further back. It restores skill files and nothing else: messages already sent, tool calls already made and KV keys already written while the changed skill was live are unaffected.

## Why do this

**Editing skills where you write code.** Skills are markdown with frontmatter; an IDE agent with `skill_*` tools can draft and revise them against the real instance instead of you pasting into a web form.

**Answering questions about the running system.** `audit_events`, `telemetry_summary`, and `cost_summary` turn "why did the 7am job cost so much yesterday" into a question you can just ask.

**Resolving approvals from wherever you are.** `approval_list` and `approval_resolve` mean a client already on your screen can clear a queue without opening the dashboard.

**Agent-to-agent.** One Denkeeper instance can be another's tool server, with scopes as the boundary between them.

{{< callout context="danger" >}}
This endpoint can create schedules, resolve approvals, and trigger an emergency stop. Treat a key with broad scopes as equivalent to dashboard admin access.

Enable TLS, or keep the port on a trusted network. Prefer several narrow keys over one `admin` key shared between clients.
{{< /callout >}}

## Interactive clients and headless runs

Sessions are tracked by default and cleaned up after `session_timeout`. Set `stateless = true` for clients that do not keep a session, at the cost of per-session context.

See the [configuration reference](/docs/reference/config/) for every `[api.mcp_server]` option.
