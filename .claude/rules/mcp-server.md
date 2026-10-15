---
name: mcp-server
description: External MCP server tool surfaces — agent tools, audit tools, store/emitter split.
paths:
  - internal/mcpserver/**
---

# External MCP server invariants

- **External MCP agent tools** (`internal/mcpserver/tools_agents.go`): `agent_info` returns name/display_name/permission_tier/provider/model/skills plus `supervisor`/`supervisor_decider`/`persona_sections`/`channels` — the last four **omitted when empty**, so presence is the signal. `agent_list` carries the same omitempty `supervisor` and `supervisor_decider` (name only). Both are read from live wiring (`Engine.Supervisor()`, `Engine.SupervisorDecider()`/`SupervisorDeciderConfig()`), reflecting post-reload and post-PATCH state (REST agents handlers read config instead). `supervisor_decider.mode` reports `shadow` for any mode but `enforce`, matching how the engine runs it. `channels` derives from `Dispatcher.Channels()` filtered by agent and **sorted by name** (the registry is a map — never emit unsorted).
- **External MCP audit tools** (`tools_audit.go`): `audit_events`/`audit_summary` mirror REST `GET /audit` and `/audit/stats` (same store, scope, filters). They read `Deps.AuditStore` (`audit.Store`), distinct from write-path `Deps.Auditor` (`audit.Emitter`); unconfigured store → graceful `toolError("audit not configured")`, not unregistered. `detail_max_chars` truncation lives in `SQLiteStore.List` (`ListOpts.DetailMaxChars`), not in either handler, so both surfaces cut identically.
- **External MCP decider tool** (`tools_deciders.go`): `decider_reviews` mirrors REST `GET /agents/{name}/decider-reviews` (same `audit:read` scope, same requested > configured > wired decider order, same `agent.LoadShadowReviews` paging and 10k scan cap). It adds `limit` (default 50, max 200, newest first) and `total` because REST's up-to-10k rows would flood a model's context. `supervisor` comes from live wiring, as in `agent_info`, not from config as in REST.
- **Task tools** (`tools_tasks.go` → `internal/tasktools`): the same six `task_*` tools the per-agent config MCP registers, from one `tasktools.Register`, so names, inputs and results can't drift between surfaces. The caller is always `ext:<key name>` from `keyNameFromCtx`, **even with an admin key**: the MCP server is the agent surface and the operator acts over REST (where admin = `human`). Reads need `tasks:read`, writes `tasks:write` (admin passes both via `hasScope`). Unregistered when `Deps.Tasks` is nil.
