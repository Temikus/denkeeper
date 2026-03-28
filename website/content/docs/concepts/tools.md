---
title: "Tools (MCP)"
description: "External tool servers connected via the Model Context Protocol."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-29T00:00:00+00:00
draft: false
weight: 30
toc: true
---

Tools are external processes that expose capabilities via the [Model Context Protocol](https://modelcontextprotocol.io/) (MCP). The agent discovers available tools at startup and can invoke them during conversations.

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

## Tool calls and permissions

Tool execution respects the agent's permission tier:

- **Autonomous** — tools execute without approval
- **Supervised** — tools execute without approval (future: configurable per-tool approval)
- **Restricted** — only read-only tools are available

## Config MCP server

Each agent also has access to a built-in MCP server that exposes Denkeeper's own configuration: `list_skills`, `create_skill`, `list_schedules`, `add_schedule`, and `get_permission_tier`.

## Plugins

Plugins extend the agent with external processes. Two execution strategies are available:

- **Subprocess** (`type = "subprocess"`) — trusted plugins run as child processes with direct MCP stdio
- **Docker** (`type = "docker"`) — sandboxed plugins run in Docker/Podman containers with `--cap-drop ALL`, `--read-only`, `--network none` by default

Subprocess plugins can optionally be verified with Ed25519 signatures. See the [security](/docs/concepts/security/) and [config reference](/docs/reference/config/) pages for details.
