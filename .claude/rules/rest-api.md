---
name: rest-api
description: REST endpoint map, scopes, non-obvious endpoint semantics, streaming events, OpenAPI generation gate.
paths:
  - internal/api/**
---

## External REST API

`internal/api/` — HTTP API (enabled by default). Auth: Bearer token (API keys) or session cookies (password/OIDC). Canonical machine-readable reference: the generated OpenAPI spec (`internal/api/docs/swagger.json`, served at `GET /api/v1/openapi.json`, no auth). All paths below are under `/api/v1/` unless noted.

| Endpoints | Scope | Non-obvious semantics |
|---|---|---|
| `GET health`, `GET openapi.json`, `GET /llms.txt` | none | `llms.txt` = LLM-readable instance summary (base URL, auth notes, key endpoints, configured agents) |
| `POST chat` | `chat` | JSON or SSE streaming |
| `GET ws` | — | WebSocket upgrade; auth via `?token=` or session cookie |
| `GET models`, `GET models/details` | `agents:read` | details includes pricing info |
| `approvals` CRUD | — | `POST .../approve` accepts `?auto_approve=session\|permanent` to simultaneously create an auto-approve rule |
| `auto-approve` CRUD | `approvals:read/write` | `GET` accepts `?agent=` filter and includes TOML `config`-scoped rules (empty `id`); `POST` with `scope: "config"` → 400 |
| `schedules` (PATCH edit), `skills` (PUT edit), `kv`, `GET/PUT agents/{name}/persona/{section}` | — | plain CRUD |
| `POST agents` | `admin` | body `{name, llm_provider, llm_model, session_tier, description, create_supervisor}`; optional `create_supervisor: {name, llm_model, timeout, context_messages}` atomically creates a companion supervisor when `session_tier="supervised"`; creates persona dir, persists `[[agents]]` to TOML |
| `PATCH agents/{name}` | — | mutable: `name` (rename), `session_tier`, `llm_provider`, `llm_model`, `description`, `browser_url_allowlist`, `fallbacks`, `cost_limit_soft`, `cost_limit_hard`, `supervisor`, `supervisor_timeout`, `supervisor_context_messages` |
| `DELETE agents/{name}` | `admin` | rejects if referenced by channels/schedules or last agent; removes from TOML; does NOT delete persona files |
| `llm/providers` CRUD, `PATCH llm/config` | `admin` | create body `{name, type, api_key, base_url, organization}`; delete rejects if referenced by agents or `default_provider`; `llm/config` = global defaults |
| `auth`: `GET status`, `GET/DELETE sessions`, `POST password`, `GET oidc/test`, `POST preferences` | `admin` | password change = bcrypt verify + re-hash + persist; `oidc/test` does fresh discovery with 10s timeout; preferences = preferred login method (auto/password/apikey) |
| `GET onboarding`, `POST onboarding/dismiss\|wizard-complete` | `admin` | checklist of 5 milestones; `show_onboarding` false when all done or dismissed; includes `wizard_completed`; dismiss/wizard-complete persist `onboarding_dismissed`/`wizard_completed` to TOML |
| `GET/PATCH server/config`, `POST server/reload\|restart` | `admin` | config = version, build info, CORS, WebSocket settings, in-process tool toggles + `web_fetch_max_response_chars`; reload re-reads TOML from disk |
| `GET sessions/{id}/stats\|tool-calls\|skills` | `sessions:read` | per-session telemetry, tool-call records, skill usage |
| `POST sessions/{id}/clear\|compact` | `sessions:write` | both accept `?agent=` hint; compact returns `{"summary": "..."}`; see `ClearMessages` invariant |
| `POST sessions/{id}/stop` | `chat` | cancel in-flight request for a session |
| `GET telemetry/summary` | `costs:read` | `?since=&until=` filtering |
| `GET audit`, `GET audit/stats` | `audit:read` | list filters `?category=&agent=&status=&source=&search=&since=&until=&limit=&offset=`; stats accepts `?since=` |
| `GET channels(/{name})` | `channels:read` | list: agent, adapter bindings, implicit flag, active adapter keys; detail adds `conversation_id` |
| `POST/DELETE channels/{name}/activate` | `channels:write` | body `{"adapter_key": "telegram:12345"}`; DELETE clears the override and 409s if the key is not active on this channel |
| `tools` CRUD (PUT edit), `GET {name}/health`, `POST {name}/restart\|enable\|disable` | `tools:read`/`tools:write` | enable starts the MCP process, disable stops it; both persist to TOML; 404 convention in invariants |
| `eval/task-sets` CRUD (+ `{name}/tasks` CRUD, `{name}/export|import`) | `eval:read`/`eval:write` | DELETE of a set referenced by a run → 409; import is all-or-none JSONL; `pinned_history` is replayed verbatim, never re-read from the source conversation |
| `POST eval/runs`, `GET eval/runs(/{id})`, `POST eval/runs/{id}/stop`, `GET eval/runs/{id}/summary\|samples\|pairs` | `eval:read`/`eval:write` | `sample_tasks` draws a stratified subset **server-side** and pins the ids on the run; `as_of` pins the clock; `/pairs` is the unblinded operator view and is deliberately REST-only (the judge's MCP tools must not resolve identity); `/summary` carries the gate table and verdict |
| `POST eval/estimate`, `GET eval/config`, `GET eval/suggest` | `eval:read` | estimate mirrors the run body, basis `history`/`list_price`/`unknown` and never fabricates; `config` is read-only by design (no runtime threshold writer); `suggest` is stratified across the four categories, skips already-saved turns, `501` without telemetry |
| `GET traces`, `GET traces/{id}` | `sessions:read` | L1 turn traces: list filters `?agent=&conversation_id=&source=&since=&until=&limit=&offset=`, headers only (no payload); detail carries the built system prompt, the history window as sent, the flattened tool calls with arguments/results, and what truncation dropped. **`sessions:read` deliberately, not `eval:read`** — a trace is turn content and the eval scopes are the judge's. The list repeats `capture`/`retention_days`/`max_trace_bytes` so the UI distinguishes "not recording" from "nothing yet" — echoed from a server-side snapshot (`refreshTraceSettings`, re-taken after each successful reload), never read from `deps.Config` at request time: hot reload overwrites that struct in place (`*cfg = *newCfg`) and a request-time read races it |
| `POST panic`, `POST resume`, `GET panic` | `admin` | emergency stop: cancel all in-flight requests + pause scheduler; resume clears; GET returns `{panicked, panic_time}` |

Chat streaming events (SSE and WS): `thinking`, `tool_start`, `tool_end`, `tool_approval`, `usage`, `content`, `done`. `tool_approval` carries `approval_status` plus, on `auto_approved`, a machine-readable `approval_scope` (`config`/`session`/`permanent`) alongside the human-readable text.


## Invariants

- **OpenAPI spec is a gated generated artifact**: `internal/api/docs/swagger.json` is committed (`//go:embed`-ed, served at `/api/v1/openapi.json`) and must match handler annotations. `just openapi-check` regenerates into a throwaway dir and diffs — never mutates the tree, deliberately uncached. After touching an annotated handler: `just openapi` and commit. Gotcha: `swag` only reads an annotation block **directly attached** to its handler func — a blank line or intervening declaration silently drops the endpoint from the spec.
