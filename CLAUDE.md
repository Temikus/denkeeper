# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

This project uses [just](https://github.com/casey/just) as the command runner and [mise](https://mise.jdx.dev) for tool versioning (Go 1.26.2).

```bash
just build                    # Build binary → pkg/bin/denkeeper
just serve                    # Run via go run (accepts optional config path)
just test                     # All Go tests with -race
just test-v                   # Verbose test output
just test-pkg internal/agent  # Single package
just test-ui                  # Web UI tests (vitest)
just lint                     # golangci-lint
just fmt                      # gofmt -w .
just check                    # fmt-check + vet + lint + test + test-ui (CI equivalent)
just scan                     # Security scans (gosec + govulncheck)
just build-ui                 # Build web UI (required before go build/test)
just build-full               # Build web then binary
just openapi                  # Generate OpenAPI spec (requires swag CLI)
just web-dev                  # Vite dev server with hot-reload
just test-integration         # E2E integration tests
```

## Architecture

Denkeeper is a single-binary personal AI agent with multi-agent routing. Messages flow through:

```
Adapter (Telegram/Discord) ─┐
Web Dashboard (WS/SSE) ─────┼→ Dispatcher → Engine (per agent) → LLM Router → Provider (Anthropic/OpenRouter/OpenAI/Ollama)
REST API (/api/v1/chat) ────┘                    ↕                    ↕
                                             MemoryStore          CostTracker
                                             (SQLite)              + Pricing Registry
```

**Dispatcher** (`internal/agent/dispatcher.go`) routes messages to the correct Engine based on channel bindings or legacy adapter bindings. Falls back to the `"default"` agent. Handles `tool_approval` ChatEvents by sending inline keyboard approval messages. When channels are configured, the dispatcher intercepts `/session` commands to allow runtime channel switching.

**Channels** (`internal/agent/channel.go`) are named routing endpoints that decouple sessions from the rigid 1:1 agent-adapter binding. A channel points to an agent and can be bound to multiple adapters (cross-adapter session sharing). Config: `[[channels]]` in TOML with `name`, `agent`, `adapters`. When `[[channels]]` is absent, channels are auto-synthesized from agent `adapters` bindings (backward compatible). Conversation ID format: `"chan:{channel_name}"`. Users switch channels via `/session <name>` in adapters; selections persist in SQLite (`active_channels` table) across restarts. Resolution priority: active override (/session) > specific binding > wildcard binding > legacy resolveAgent fallback.

**Engine** (`internal/agent/engine.go`) is the per-agent orchestrator. Pipeline: check permissions → get/create conversation → store user message → load history → build system prompt (persona + skills) → call `Router.Complete()` → tool-call loop (with supervised approval if applicable) → emit usage event → extract memory update → store assistant message → return text.

**Three key interfaces**:
- `adapter.Adapter` — platform integrations (Telegram, Discord)
- `llm.Provider` — LLM backends (Anthropic, OpenRouter, OpenAI, Ollama)
- `agent.MemoryStore` — conversation persistence (SQLite)

**Multi-agent config**: `[[agents]]` in TOML. Each agent has `name`, `persona_dir`, `adapters`, `llm_provider`, `llm_model`, `session_tier`. If no `[[agents]]` section exists, a single `"default"` agent is synthesized. `llm_provider` overrides the global `default_provider` for that agent, enabling different agents to use different LLM backends.

**Named provider instances**: `[[llm.providers]]` array allows multiple instances of the same provider type (e.g. two OpenAI-compatible endpoints). Each entry has `name`, `type` (`anthropic`/`openai`/`openrouter`/`ollama`), `api_key`, `base_url`, `organization`. Legacy `[llm.openai]` single-slot syntax is still supported and auto-converted. Per-agent `llm_provider` references instances by name.

**Data directory**: All default paths (db, persona, skills) are derived from a single base directory. Set via `DENKEEPER_DATA_DIR` env var, `data_dir` in TOML, or defaults to `~/.denkeeper`. The Helm chart sets `DENKEEPER_DATA_DIR=/data` so everything lands on the writable PVC.

**Wiring** happens in `cmd/denkeeper/main.go` — config drives everything. All behavior should be configurable via TOML, not hardcoded.

## Conventions

- **Error wrapping**: Always `fmt.Errorf("context: %w", err)` — no naked error returns.
- **Structured logging**: `log/slog` everywhere, with contextual fields.
- **Context propagation**: All I/O functions accept `context.Context`.
- **Concurrency**: Channels for message passing; `sync.Mutex` for shared state.
- **Config validation**: Three-phase pattern — parse TOML → apply defaults (including env overrides) → validate.
- **Env var overrides**: `applyEnvOverrides()` reads an explicit allowlist of `DENKEEPER_*` env vars.
- **Cyclomatic complexity**: gocyclo threshold is 15. All non-test functions must be ≤ 15.

## Testing Patterns

- Hand-written mocks that satisfy interfaces — no codegen.
- In-memory SQLite (`:memory:`) for persistence tests via `NewInMemoryStore()`.
- Individual `TestName_Scenario` functions (not table-driven).
- Always run with `-race` flag.
- Web UI must be built before any Go step that embeds it (CI handles via `build-ui` job).
- **Web UI tests**: Vitest + jsdom + MSW (mock service worker). Test files in `web/src/__tests__/` and `web/src/components/__tests__/`. Run via `just test-ui`.
- **E2E integration tests**: `internal/integration/` boots a full in-process API server with a mock LLM and in-memory stores (`just test-integration`). Use `NewHarness(t, &HarnessOpts{...})` — options include `ConfigPath` (for handlers that persist to TOML), `WithLifecycleMgr` (enables tool CRUD endpoints), and `Responses` (mock LLM response sequence). WebSocket tests require `httptest.NewServer` for the upgrade handshake; all other tests use `httptest.NewRecorder`.

## Permission Tiers & Approval Workflows

Three tiers: `autonomous` (all actions), `supervised` (chat + tools with approval), `restricted` (chat + read-only tools).

`internal/approval/` manages requests requiring human sign-off. Flow: Engine submits to Manager → Manager persists + registers closure → Engine attaches Approve/Deny inline keyboard → user clicks → callback handler resolves → closure invoked.

Eleven action kinds: `user_update`, `soul_update`, `identity_update`, `create_skill`, `update_skill`, `delete_skill`, `modify_schedule`, `install_tool`, `modify_config`, `browser_profile`, `tool_call`.

**Supervised tool call approval**: When `permission_tier = "supervised"`, each MCP tool call is submitted for approval before execution. Engine first checks `Manager.ShouldAutoApprove()` — if a matching rule exists, the tool executes immediately and a `tool_approval` ChatEvent with `approval_status: "auto_approved"` is emitted. Otherwise Engine blocks on `Manager.WaitForResolution(ctx, id)`. Dispatcher intercepts pending `"tool_approval"` ChatEvents and sends inline keyboard messages with four buttons: Approve, Deny, Auto (session), Auto (always). Denied tool calls feed "Tool call was denied by the operator." to the LLM.

**Auto-approve rules**: Two scopes — `session` (in-memory, conversation-scoped, cleared on restart) and `permanent` (persisted in SQLite, agent-scoped). `Manager.ShouldAutoApprove()` checks session rules first, then permanent rules. Future config-based rules (`ScopeConfig`) can be added as a third check. Rules are created from Telegram inline buttons (`:approve_session`, `:approve_always` callback suffixes), from the web UI chat (Always Approve button), or via the REST API. `approval.ExtractToolName()` parses the tool name from the approval summary to key rules.

**Supervisor agents**: A supervised agent can designate another agent as its supervisor via `supervisor = "agent-name"` in TOML. The supervisor sits between auto-approve rules and human approval in the resolution chain: auto-approve → supervisor review → human approval. The supervisor receives a structured prompt with tool call details and recent conversation context, and returns APPROVE (tool executes), DENY (denied with reason fed to LLM), or ESCALATE (falls through to human approval). Supervisor calls are lightweight one-shot LLM calls via the supervisor's Router — no conversation storage, skill matching, or tool loops. Config validation: supervisor must exist, must not itself be supervised (no chaining), must not use supervised tier (would deadlock), and `supervisor` is only valid on supervised agents. Delete guard prevents removing agents referenced as supervisors. Supervisor decisions emit `audit.CategorySupervisor` events and `tool_approval` ChatEvents with status `supervisor_approved`/`supervisor_denied`/`supervisor_escalated`. Web UI: supervisor dropdown in Agents permission panel, meta line shows supervisor relationship, Chat page renders supervisor statuses, AuditLog has Supervisor filter chip.

## Cost Tracking & Pricing

`internal/llm/pricing/` — central pricing registry with bundled defaults for ~70 models (Anthropic, OpenAI, Gemini, Llama, Mistral, DeepSeek + OpenRouter-prefixed). Exact match > longest prefix match > fallback rate.

`TokenCost(resp, reg)` returns `(cost, source)` with priority: provider-reported > registry > `[costs]` fallback > $0. Source is used as `pricing_source` OTel attribute. Unknown models emit a structured log warning.

`TokenUsage.CachedPrompt` populated from Anthropic `cache_read_input_tokens` and OpenAI `prompt_tokens_details.cached_tokens`.

Config:
```toml
[costs]
default_rate_per_1k_tokens = 0.01  # fallback when model not in registry (0 = $0 + warn)

[costs.model_prices.my-custom-model]
input = 2.0       # per million input tokens
output = 8.0      # per million output tokens
cached_input = 0.5 # per million cached input tokens (0 = same as input)
```

## MCP Tools & Health Monitoring

`internal/tool/manager.go` manages MCP server connections (stdio subprocess or SSE remote).

**Health monitoring**: `StartHealthChecker(ctx, interval)` probes servers via ListTools every 30s. Crashed servers are auto-restarted with exponential backoff. Config: `[mcp]` section with `auto_restart` (default true), `max_restart_attempts` (default 3), `restart_cooldown` (default "5m"). `ServerStatus` reports `connected`/`error`/`disabled` with `restart_count`, `last_error`, `uptime_secs`. Manual restart via `Manager.RestartServer()`, `LifecycleManager.RestartTool()`, REST `POST /api/v1/tools/{name}/restart`, or Config MCP `tool_restart`.

**OAuth 2.1 for MCP tools**: `internal/tool/oauth/` implements the MCP OAuth 2.1 spec for remote SSE tool servers that require authorization. Config per tool: `auth = "oauth"` with optional `client_id`, `client_secret`, `scopes`. OAuth routes are mounted at `/api/v1/tools/{name}/oauth/...`. Token storage in SQLite. `api.external_url` used for callback URL construction.

**Security**: SSRF protection, header injection prevention, env var denylist, URL/arg redaction in API responses.

## External REST API

`internal/api/` — HTTP API server (enabled by default). Auth via Bearer token (API keys) or session cookies (password/OIDC).

Key endpoints (all require auth unless noted):
- `GET /api/v1/health` (no auth)
- `GET /api/v1/openapi.json` (no auth) — generated OpenAPI 2.0 spec
- `POST /api/v1/chat` (scope `chat`) — JSON or SSE streaming
- `GET /api/v1/ws` — WebSocket upgrade (auth via `?token=` or session cookie)
- `GET /api/v1/models` (scope `agents:read`) — available LLM models from all providers
- `GET /api/v1/models/details` (scope `agents:read`) — model details with pricing info
- `GET/POST/DELETE /api/v1/approvals/...` — approval CRUD; `POST /approve` accepts `?auto_approve=session|permanent` to simultaneously create an auto-approve rule
- `GET/POST/DELETE /api/v1/auto-approve` (scope `approvals:read/write`) — auto-approve rule CRUD; `GET` accepts `?agent=` filter
- `GET/POST/PATCH/DELETE /api/v1/schedules/...` — schedule CRUD
- `GET/POST/PUT/DELETE /api/v1/skills/...` — skill CRUD
- `GET/PUT /api/v1/agents/{name}/persona/{section}` — persona sections
- `GET/PUT/DELETE /api/v1/kv/...` — KV store
- `GET/POST/PUT/DELETE /api/v1/tools/...` — tool/plugin CRUD (PUT for edit)
- `GET /api/v1/tools/{name}/health` (scope `tools:read`) — server health status
- `POST /api/v1/tools/{name}/restart` (scope `tools:write`) — manually restart a tool server
- `POST /api/v1/agents` (scope `admin`) — create a new agent; body: `{name, llm_provider, llm_model, session_tier, description}`. Creates persona directory, persists to `[[agents]]` in TOML.
- `PATCH /api/v1/agents/{name}` — agent config mutation; supports `name` (rename), `session_tier`, `llm_provider`, `llm_model`, `description`, `browser_url_allowlist`, `fallbacks`, `cost_limit_soft`, `cost_limit_hard`
- `DELETE /api/v1/agents/{name}` (scope `admin`) — remove agent. Rejects if referenced by channels/schedules. Removes from TOML. Does not delete persona files.
- `GET /api/v1/llm/providers` (scope `admin`) — list LLM providers with current config
- `POST /api/v1/llm/providers` (scope `admin`) — create a named provider instance; body: `{name, type, api_key, base_url, organization}`
- `PATCH /api/v1/llm/providers/{name}` (scope `admin`) — update provider config (API key, base URL, etc.)
- `DELETE /api/v1/llm/providers/{name}` (scope `admin`) — remove a provider instance (rejects if referenced by agents or default_provider)
- `PATCH /api/v1/llm/config` (scope `admin`) — update global LLM config (default provider, model, etc.)
- `GET /api/v1/auth/status` (scope `admin`) — auth config summary (password, OIDC, sessions, preferences)
- `GET/DELETE /api/v1/auth/sessions` (scope `admin`) — session list + revoke
- `POST /api/v1/auth/password` (scope `admin`) — change password (bcrypt verify + re-hash + persist)
- `GET /api/v1/auth/oidc/test` (scope `admin`) — test OIDC provider reachability (fresh discovery, 10s timeout)
- `POST /api/v1/auth/preferences` (scope `admin`) — set preferred login method (auto/password/apikey)
- `GET /api/v1/onboarding` (scope `admin`) — checklist of 5 setup milestones; `show_onboarding` false when all done or dismissed
- `POST /api/v1/onboarding/dismiss` (scope `admin`) — persist `onboarding_dismissed=true` to TOML, hide card
- `GET/PATCH /api/v1/server/config` (scope `admin`) — server config (version, build info, CORS, WebSocket settings)
- `POST /api/v1/server/reload` (scope `admin`) — reload config from disk
- `POST /api/v1/server/restart` (scope `admin`) — restart the server process
- `GET /api/v1/sessions/{id}/stats` (scope `sessions:read`) — session telemetry summary
- `GET /api/v1/sessions/{id}/tool-calls` (scope `sessions:read`) — tool call records for a session
- `GET /api/v1/sessions/{id}/skills` (scope `sessions:read`) — skill usage for a session
- `GET /api/v1/telemetry/summary` (scope `costs:read`) — aggregate telemetry; `?since=&until=` filtering
- `GET /api/v1/audit` (scope `audit:read`) — list audit events with filtering/pagination; `?category=&agent=&status=&source=&search=&since=&until=&limit=&offset=`
- `GET /api/v1/audit/stats` (scope `audit:read`) — aggregate counts by category/status; `?since=`
- `GET /api/v1/channels` (scope `channels:read`) — list all configured channels with agent, adapter bindings, implicit flag, active adapter keys
- `GET /api/v1/channels/{name}` (scope `channels:read`) — channel detail including conversation_id and active adapter keys
- `POST /api/v1/channels/{name}/activate` (scope `channels:write`) — set active channel for an adapter key; body: `{"adapter_key": "telegram:12345"}`
- `DELETE /api/v1/channels/{name}/activate` (scope `channels:write`) — clear active channel override for an adapter key (returns 409 if key is not active on this channel)
- `POST /api/v1/sessions/{id}/clear` (scope `sessions:write`) — clear all messages in a session (keeps conversation row); accepts `?agent=` hint
- `POST /api/v1/sessions/{id}/compact` (scope `sessions:write`) — compact session into LLM summary; accepts `?agent=` hint; returns `{"summary": "..."}`
- `POST /api/v1/sessions/{id}/stop` (scope `chat`) — cancel in-flight request for a session
- `POST /api/v1/panic` (scope `admin`) — emergency stop: cancel all in-flight requests, pause scheduler
- `POST /api/v1/resume` (scope `admin`) — clear panic state, resume scheduler
- `GET /api/v1/panic` (scope `admin`) — returns `{panicked, panic_time}`

Chat streaming events (SSE and WebSocket): `thinking`, `tool_start`, `tool_end`, `tool_approval`, `usage`, `content`, `done`.

## Web Dashboard & WebSocket Transport

`internal/web/` embeds a Svelte SPA compiled to `web/dist/` via `//go:embed dist`.

17 pages: Login, Overview, Chat, Approvals, Sessions, Schedules, Skills, Tools, Browser, KV Store, Costs, Agents, API Keys, Providers, Server Config, Settings, Audit Log.

**WebSocket transport** (`internal/api/websocket.go`): `GET /api/v1/ws` upgrades to a bidirectional WebSocket. The web dashboard auto-connects via WS and falls back to SSE after 3 failed reconnect attempts. `WSHub` manages connections with per-connection replay buffer (`websocket_replay_buffer_ttl`, default 5m). Config: `api.websocket_enabled` (default true), `api.websocket_max_connections`, `api.websocket_replay_buffer_ttl`. Frame types defined in `wsframes.go`.

## UI/UX Standards

Every user-facing feature must include thoughtful UX treatment.

**Web (Svelte)**: Loading spinners for async ops, empty states with CTAs, inline error messages, confirmation for destructive actions, success feedback, disabled buttons while in-flight, responsive (≥ 320px), use existing CSS variables (`--accent`, `--surface`, `--border`, `--text-muted`, `--danger`).

**CLI (Cobra)**: Progress feedback for >500ms ops, `tabwriter` for tables, actionable errors (what failed + next step), non-zero exit codes via `RunE`.

**Adapters**: Typing indicators before LLM calls, platform-native formatting, inline keyboards for approvals. Telegram adapter registers built-in commands (`/start`, `/help`, `/stop`, `/panic`, `/resume`, `/debug`, `/clear`, `/compact`) plus skill `command:` triggers via `setMyCommands` on startup (`RegisterSkillCommands`).

## Key Subsystems

| Subsystem | Package | Config Section |
|-----------|---------|----------------|
| Scheduler | `internal/scheduler/` | `[[schedules]]` |
| Config MCP | `internal/configmcp/` | (in-process, per-agent) |
| Web MCP | `internal/webmcp/` | `[web]` |
| Browser | `internal/browser/` | `[browser]` |
| KV Store | `internal/kv/` | `[kv]` |
| Plugins | `internal/plugin/` | `[plugins.*]` |
| Sandbox | `internal/sandbox/` | `[sandbox]` |
| OTel | `internal/otel/` | `[otel]` |
| Pricing | `internal/llm/pricing/` | `[costs]` |
| Auth | `internal/api/session.go`, `oidc.go` | `[api.auth]` |
| MCP OAuth | `internal/tool/oauth/` | `[tools.*.auth]` |
| Telemetry | `internal/agent/memory.go` | `[memory]` |
| Audit Log | `internal/audit/` | `[audit]` |
| Channels | `internal/agent/channel.go`, `dispatcher.go` | `[[channels]]` |

## Current State

Phase 12 (Auth & Onboarding UX Uplift) complete: Settings page with all 5 sections (including "(this session)" indicator), all backend auth endpoints (password change, OIDC test, preferences, session tracking), login page improvements, onboarding checklist (12e) with Overview page card and dismiss flow, auth test coverage (12f). Shared `Collapsible.svelte` component extracted from Settings page. All core systems implemented: multi-agent routing, 4 LLM providers, Telegram/Discord adapters, MCP tools with health monitoring and OAuth 2.1, plugin system (subprocess + Docker + K8s), approval workflows (including supervised tool calls with auto-approve rules and auto-resolve), KV store, browser automation, web search/fetch, pricing registry, OAuth2/OIDC auth, OTel observability (HTTP middleware, tool execution spans, approval wait spans, scheduler spans, per-provider LLM spans, WS/SSE connection gauges), Prometheus metrics at `GET /metrics`, web dashboard (17 pages) with real-time token-by-token WebSocket streaming. Per-agent fallback rules, per-agent provider routing (`llm_provider`), inline agent rename, LLM provider config via web UI, server reload/restart via admin API. Deployment improvements: env var overrides (`DENKEEPER_*`), `DENKEEPER_CONFIG` for config path, Helm chart (`deploy/helm/denkeeper/`), Dockerfile non-root user, docker-compose with port bindings. Config MCP tools: `schedule_update`, `schedule_delete`, `set_fallback`, `get_cost_summary`, `skill_delete`. Named provider instances via `[[llm.providers]]` — multiple instances of the same provider type (e.g. OpenAI + LM Studio), backward compat with legacy single-slot config. Skill rename via `PUT /api/v1/skills/{agent}/{name}`. LLM stream idle timeout (`IdleTimeoutReader`) prevents infinite hangs on stalled SSE streams. Configurable conversation context limit (`Engine.SetMaxContextMessages`, default 50) and approval retries (`Engine.SetApprovalConfig`). MCP init retry for remote SSE tools on startup. E2E integration test suite expanded from 30 to 106 tests (skills, KV, schedule API, agent config, auto-approve, tools CRUD with in-process MCP server, chat with tool calls, channels, WebSocket); `just test-integration` working. `mcp_go_client_oauth` build tag removed (MCP Go SDK v1.5.0 exports all OAuth interfaces). Persistent session telemetry: per-message model/provider/cost/token breakdown, tool call records (name, server, duration, success/error, round), skill usage tracking, `conversation_stats` table for incremental session summaries, time-based and count-based retention (`retention_days` default 90, `max_conversations` default 10000), telemetry API endpoints (`/api/v1/sessions/{id}/stats`, `/api/v1/sessions/{id}/tool-calls`, `/api/v1/sessions/{id}/skills`, `/api/v1/telemetry/summary` with `?since=&until=` filtering). Build version exposed via `GET /api/v1/server/config` (`version`, `commit`, `build_date`, `go_version` fields) and displayed in Settings UI. Config MCP `skill_delete` and `schedule_delete` tools added (with `delete_skill` approval action kind). KV `PUT /api/v1/kv/{agent}/{key}` endpoint added for setting values via REST. `internal/agentctx` package for context key sharing between `agent` and `configmcp` packages. Go upgraded to 1.26.2. **Channels** (Phase 13 complete): `[[channels]]` config for decoupled session routing — named routing endpoints that bind adapter chats to agents with explicit session identity. Cross-adapter session sharing, `/session` command for runtime channel switching, `active_channels` SQLite table for persistence. Auto-synthesized from agent `adapters` bindings when `[[channels]]` absent (backward compatible). REST API: `GET /api/v1/channels`, `GET /api/v1/channels/{name}`, `POST /api/v1/channels` (create), `PATCH /api/v1/channels/{name}` (update), `DELETE /api/v1/channels/{name}` (remove), `POST/DELETE /api/v1/channels/{name}/activate`; TOML persistence via `AddChannelToConfig`/`UpdateChannelInConfig`/`RemoveChannelFromConfig` (atomic read-modify-write with file mutex and `.bak` backup). `channel` field on `POST /api/v1/chat` and `ChatRequestFrame` (WebSocket) for channel-routed chat; `channel` field on `ActivityFrame`; `GET /api/v1/sessions` enriched with channel names. Sentinel errors (`ErrChannelNotFound`, `ErrChannelsNotConfigured`, `ErrAdapterKeyNotActive`) for reliable error classification. Web dashboard: Channels management page with full CRUD (inline create/edit forms, delete with confirmation, delivery mode and session mode selectors), Chat page channel selector dropdown, Sessions page channel column. Scheduler harmonization: `@channelname` syntax in `[[schedules]]`, broadcast multi-target delivery. Ephemeral channels: `session_mode = "ephemeral"` creates a fresh conversation per interaction (`chan:{name}:{unix_nano}`); validation prevents cross-adapter ephemeral channels. Config MCP tools: `channel_list`, `channel_switch`, `channel_info` for agent self-service channel discovery and switching. **Audit Log**: unified audit trail (`internal/audit/`) with buffered emitter, SQLite storage (`audit.db`), 11 event categories (tool_call, skill, channel, approval, schedule, llm, config, session, mcp, safety, supervisor). Events emitted from Engine (tool execution, LLM calls, skill matches), Dispatcher (routing, session switches), Approval Manager (approve/deny), Scheduler (fires), Tool Manager (MCP health failures). REST API at `GET /api/v1/audit` + `/audit/stats`. Web UI page with timeline and table views, category/status/agent/time filters, expandable detail cards. Config: `[audit]` section with `enabled`, `retention_days` (default 30), `cleanup_interval`, `buffer_size`. Env override: `DENKEEPER_AUDIT_ENABLED`. **Safety Commands**: `/stop` cancels the current in-flight request for a chat, `/panic` is an emergency stop that cancels ALL in-flight requests and pauses the scheduler, `/resume` clears panic state and resumes. Available in Telegram, Discord (via Dispatcher interception), web UI (Stop button during streaming, Panic button in toolbar, panic banner with Resume), and REST API (`POST /api/v1/sessions/{id}/stop`, `POST /api/v1/panic`, `POST /api/v1/resume`, `GET /api/v1/panic`). Dispatcher tracks in-flight requests per `adapter:externalID` with cancellable contexts. Panic state is transient (cleared on restart). Scheduler supports `Pause()`/`Resume()` — cancels entry goroutines without cancelling root context. WebSocket `panic_status` frame broadcasts state changes. Audit events emitted under `safety` category. **Session History Management**: `/clear` removes all messages from a session (keeps conversation row for session identity), `/compact` summarises the conversation via LLM and replaces all messages with a single summary (prefixed `[Session compacted]`). Available in Telegram, Discord (via Dispatcher interception), web UI (Clear/Compact buttons in Chat toolbar), and REST API (`POST /api/v1/sessions/{id}/clear`, `POST /api/v1/sessions/{id}/compact`). Compact accepts `?agent=` query hint; infers agent from session ID prefix or channel mapping. `MemoryStore.ClearMessages()` deletes messages + telemetry in a transaction without removing the conversation row. Audit events emitted under `session` category (`clear`, `compact` actions). **Agent CRUD** (Phase 14c complete): `POST /api/v1/agents` creates a new agent at runtime — builds engine via `AgentFactory` callback, creates persona directory, registers in Dispatcher, persists to `[[agents]]` in TOML. `DELETE /api/v1/agents/{name}` removes an agent — rejects if referenced by channels or schedules, removes from Dispatcher and TOML, preserves persona files. `Dispatcher.AddAgent`/`RemoveAgent` methods for runtime agent registration. `AddAgentToConfig`/`RemoveAgentFromConfig` in config writer. Web dashboard: Agents page with "+ Add Agent" inline form (name, provider, model, tier, description) and Delete button with inline confirmation and dependency error display. Channels page responsive layout (600px breakpoint matching Agents pattern). **Provider CRUD** (Phase 14d complete): `POST /api/v1/llm/providers` creates a named provider instance with type/name validation and TOML persistence. `DELETE /api/v1/llm/providers/{name}` removes a provider, rejecting if referenced by agents or default_provider. `AddLLMProviderToConfig`/`RemoveLLMProviderFromConfig` in config writer. Shared `ValidResourceName`/`ValidProviderType`/`IsProviderReferenced` validators in config package. Providers.svelte add/delete inline panels. 11 integration tests. **Per-agent cost limits** (Phase 14e complete): `PATCH /api/v1/agents/{name}` accepts `cost_limit_soft`/`cost_limit_hard`, persists to TOML, syncs to live CostTracker via `SetAgentLimits`. Agent detail endpoint returns cost limit fields. Agents.svelte permission panel with inline cost limit inputs. Global cost limit PATCH now syncs to CostTracker via `SetDefaultLimits` (bug fix). E2E integration test suite expanded to 155+ tests (including supervised approval flow: approved, denied, auto-approve session, auto-approve permanent, supervisor integration tests). **OpenAPI spec**: `swaggo/swag` annotations on 15 endpoints (health, chat, agents CRUD, providers CRUD, LLM config, costs), served at `GET /api/v1/openapi.json` via embedded `swagger.json`. 11 schema definitions auto-generated from request/response structs. `just openapi` recipe for regeneration. **Supervisor agents**: supervised agents designate a supervisor via `supervisor = "agent-name"` in TOML; approval chain: auto-approve → supervisor LLM review → human; supervisor decisions emit `audit.CategorySupervisor` events and `tool_approval` ChatEvents with status `supervisor_approved`/`supervisor_denied`/`supervisor_escalated`; Telegram activity log surfaces supervisor outcomes; config validation prevents chaining and deadlocks. **Per-round LLM audit events**: Engine emits one `audit.CategoryLLM` event per LLM call round (tool-call loop iteration), round indexed from 1 (dropped -1 sentinel). **Consolidated Telegram activity log**: tool approvals are now rendered inline in the collapsible activity log message (with approval buttons) instead of as separate messages; chunk-splitting at 3500 chars prevents Telegram 4096-char cap overflow. **KV storage guidance**: Engine injects a brief KV tool usage note into the system prompt when the agent has KV MCP tools available.

CI/CD: golangci-lint, gosec, govulncheck, Grype, Gitleaks, GoReleaser, Homebrew tap, Docker (ghcr.io) with cosign + SLSA, GitHub Pages docs.

See `design/denkeeper-prd.md` for the full roadmap.
