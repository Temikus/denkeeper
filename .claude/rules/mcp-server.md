---
name: mcp-server
description: External MCP server tool surfaces — agent tools, audit tools, store/emitter split.
paths:
  - internal/mcpserver/**
---

# External MCP server invariants

- **External MCP agent tools** (`internal/mcpserver/tools_agents.go`): `agent_info` returns name/display_name/permission_tier/provider/model/skills plus `supervisor`/`supervisor_decider`/`persona_sections`/`channels` — the last four **omitted when empty**, so presence is the signal. `agent_list` carries the same omitempty `supervisor` and `supervisor_decider` (name only). Both are read from live wiring (`Engine.Supervisor()`, `Engine.SupervisorDecider()`/`SupervisorDeciderConfig()`), reflecting post-reload and post-PATCH state (REST agents handlers read config instead). `supervisor_decider.mode` reports `shadow` for any mode but `enforce`, matching how the engine runs it. `channels` derives from `Dispatcher.Channels()` filtered by agent and **sorted by name** (the registry is a map — never emit unsorted).
- **External MCP audit tools** (`tools_audit.go`): `audit_events`/`audit_summary` mirror REST `GET /audit` and `/audit/stats` (same store, scope, filters). They read `Deps.AuditStore` (`audit.Store`), distinct from write-path `Deps.Auditor` (`audit.Emitter`); unconfigured store → graceful `toolError("audit not configured")`, not unregistered. `detail_max_chars` truncation lives in `SQLiteStore.List` (`ListOpts.DetailMaxChars`), not in either handler, so both surfaces cut identically.
