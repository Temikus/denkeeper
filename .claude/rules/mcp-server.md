---
name: mcp-server
description: External MCP server tool surfaces — agent tools, audit tools, store/emitter split.
paths:
  - internal/mcpserver/**
---

# External MCP server invariants

- **External MCP agent tools** (`internal/mcpserver/tools_agents.go`): `agent_info` returns name/display_name/permission_tier/provider/model/skills plus `supervisor`/`persona_sections`/`channels` — the last three **omitted when empty**, so presence is the signal. `agent_list` carries the same omitempty `supervisor`. `supervisor` is read from live wiring (`Engine.Supervisor().Name()`), reflecting post-hot-reload state (REST agents handlers read config instead). `channels` derives from `Dispatcher.Channels()` filtered by agent and **sorted by name** (the registry is a map — never emit unsorted).
- **External MCP audit tools** (`tools_audit.go`): `audit_events`/`audit_summary` mirror REST `GET /audit` and `/audit/stats` (same store, scope, filters). They read `Deps.AuditStore` (`audit.Store`), distinct from write-path `Deps.Auditor` (`audit.Emitter`); unconfigured store → graceful `toolError("audit not configured")`, not unregistered.
