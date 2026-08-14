---
title: "Tools (MCP)"
description: "External tool servers connected via the Model Context Protocol."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-11T00:00:00+00:00
draft: false
weight: 30
toc: true
---

Tools are external processes that expose capabilities via the [Model Context Protocol](https://modelcontextprotocol.io/) (MCP). The agent discovers available tools at startup and can invoke them during conversations.

Denkeeper also ships tools that need no external process — web search and fetch, JavaScript execution, a KV store, and browser automation. See [Built-in Tools](/docs/concepts/built-in-tools/) for those. For pointing MCP the other way, so that other clients can drive *this* instance, see [Denkeeper as an MCP Server](/docs/guides/mcp-server/).

## Configuration

Define tools in your config file:

```toml
[tools.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/documents"]

[tools.web-search]
command = "mcp-server-brave-search"
env = { BRAVE_API_KEY = "$BRAVE_API_KEY" }
```

## How it works

1. On startup, Denkeeper spawns each tool as a subprocess using MCP's stdio transport
2. It discovers available tools via the MCP `tools/list` method
3. Tool descriptions are included in the LLM system prompt
4. When the LLM requests a tool call, Denkeeper executes it and returns the result
5. This continues in an agentic loop until the LLM produces a final response

### Duplicate tool names

Two MCP servers can advertise a tool with the same name. When that happens, neither keeps the bare name: both are offered to the model as `<server>__<tool>` — a GitHub server and a Gitea server both exposing `create_issue` become `github__create_issue` and `gitea__create_issue`. The bare name stops being offered and stops routing, so a call that used it fails with an error naming both servers and the qualified alternatives rather than silently landing on whichever server connected last. The collision is logged as a warning and recorded in the audit log (`tool_name_collision`); removing one of the servers gives the survivor its bare name back.

Tool names you have written down elsewhere follow the same rule, deliberately: an `auto_approve_tools` entry naming the bare form of a now-duplicated tool stops matching — and says so in a startup warning — instead of quietly granting one server's blessing to another server's tool. Update it to the qualified name, or rename the tool on one of the servers.

### Within-turn memoization

Models occasionally repeat a tool call with byte-identical arguments in the same turn. For tools declared *idempotent*, the second identical call returns the first call's cached result instead of re-executing — skipping the tool latency and, on supervised agents, the approval round-trip (the original call already passed approval, and a cache hit executes nothing). The cache lives for a single message turn.

The built-in `web_fetch`, `web_search`, `kv_get`, and `kv_list` tools are always cache-eligible. External MCP servers opt in via `idempotent = true` (whole server) or `idempotent_tools = ["lookup", ...]` (mixed read/write servers) on their `[tools.*]` entry. Server-supplied MCP annotations are never trusted by default, but `trust_annotations = true` extends eligibility to tools the server marks with `readOnlyHint` — an explicit per-server statement that you trust its self-description. Cached calls appear in telemetry with a `cached` outcome and in the audit log as `cache_hit` events.

## Tool calls and permissions

Tool execution respects the agent's permission tier:

- **Autonomous** — tools execute without approval
- **Supervised** — each tool call requires human approval via Approve/Deny buttons (Telegram/Discord inline keyboards, web dashboard, or REST API). Auto-approve rules can be created per-tool with session or permanent scope to skip future approvals for trusted tools.
- **Restricted** — only read-only tools are available

## Runtime tool management

Tools and plugins can be added and removed at runtime without restarting:

- **Config MCP tools**: The agent can self-manage tools via `tool_add`, `tool_remove`, `tool_list`, `plugin_add`, `plugin_remove`, `plugin_list` (respects permission tiers — restricted denies, supervised requires approval)
- **REST API**: `POST/DELETE /api/v1/tools` and `POST/DELETE /api/v1/plugins` with `tools:write` scope
- **Web dashboard**: The Tools page provides a UI for managing tools and plugins

All runtime changes are persisted to the TOML config file, so they survive restarts.

## Config MCP server

Each agent has access to a built-in MCP server that exposes Denkeeper's own configuration:

- **Skills**: `skill_list`, `skill_get`, `skill_create`, `skill_update`, `skill_patch`, `skill_delete`, `skill_read_file`, `skill_write_file`
- **Schedules**: `schedule_list`, `schedule_add`, `schedule_update`, `schedule_delete`
- **Tools**: `tool_list`, `tool_add`, `tool_remove`, `tool_restart`
- **Plugins**: `plugin_list`, `plugin_add`, `plugin_remove`
- **KV store**: `kv_get`, `kv_set`, `kv_delete`, `kv_list`, `kv_set_nx`
- **Channels**: `channel_list`, `channel_switch`, `channel_info`
- **Persona**: `persona_get`, `persona_update`, `persona_memory_manage`
- **Browser profiles**: `browser_profile_list`, `browser_profile_info`, `browser_profile_clear`, `browser_profile_delete`
- **Sessions**: `session_search`
- **Fallback**: `set_fallback`
- **Costs**: `get_cost_summary`

Registration is dependency-gated: a tool is advertised only when the subsystem it needs is wired. An agent with no browser configured does not see the `browser_profile_*` tools at all, rather than seeing them and getting errors — so the advertised tool set is an accurate statement of what the agent can actually do.

### Agent KV store

The KV store provides per-agent key-value storage with optional TTL. It's useful for:

- **Locks**: "I'm already processing this task, don't start another" (via `kv_set_nx`)
- **Counters**: Track how many times something has happened
- **Caches**: Remember recent API results with automatic expiry
- **State machines**: Track multi-step workflow progress
- **Cross-session coordination**: Check if a daily routine already ran today

KV reads are allowed for all permission tiers. Writes are denied for restricted tier. Configure limits in the `[kv]` config section.

## Plugins

Plugins extend the agent with external processes. Two execution strategies are available:

- **Subprocess** (`type = "subprocess"`) — trusted plugins run as child processes with direct MCP stdio
- **Docker** (`type = "docker"`) — sandboxed plugins run via the configurable sandbox runtime:
  - **Docker** (default) — `docker run -i --rm` with `--cap-drop ALL`, `--read-only`, `--network none`
  - **Kubernetes** — ephemeral Pods with init-container network isolation, dropped capabilities, read-only root filesystem, Pod Security Admission labels, and optional gVisor/Kata RuntimeClass

Select the sandbox backend in config with `[sandbox] runtime = "docker"` or `"kubernetes"`. See the [config reference](/docs/reference/configuration-reference/) for all sandbox options.

Subprocess plugins can optionally be verified with Ed25519 signatures. Use `denkeeper plugin keygen/sign/verify` to manage signing keys and signatures. See the [security](/docs/concepts/security/), [CLI reference](/docs/reference/cli-reference/), and [config reference](/docs/reference/configuration-reference/) pages for details.

## OAuth 2.1 for remote tools

Remote MCP tool servers that require authorization can use the OAuth 2.1 flow. Configure per tool:

```toml
[tools.todoist]
transport = "sse"
url = "https://mcp.todoist.com/sse"
auth = "oauth"
client_id = "your-client-id"       # optional — some servers use dynamic registration
client_secret = "your-secret"      # optional
scopes = ["task:read", "task:write"]
```

When `auth = "oauth"` is set, Denkeeper handles the authorization code flow with PKCE. OAuth callback routes are mounted at `/api/v1/tools/{name}/oauth/...`. Tokens are stored in SQLite and refreshed automatically. Set `api.external_url` in your config to ensure correct callback URL construction when behind a reverse proxy.
