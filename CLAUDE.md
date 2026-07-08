# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

This project uses [just](https://github.com/casey/just) as the user-facing command runner, [Task](https://taskfile.dev) (`Taskfile.yml`) underneath for per-target fingerprint caching, and [mise](https://mise.jdx.dev) for tool versioning (Go 1.26.2). `just` recipes for `build`, `test`, `lint`, `vet`, `fmt-check`, `lint-ui`, `build-ui`, `test-ui`, `openapi`, and `hook` delegate to `mise x -- task <name>`; Task skips any step whose declared sources/inputs haven't changed. Cache lives in `.task/` (gitignored). Bust a single step with `mise x -- task <name> --force`; bust the whole hook chain with `JUST_HOOK_FORCE=1 just hook`.

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
just hook                     # Equivalent of `just check` with minimal output (for agent use)
just scan                     # Security scans (gosec + govulncheck)
just build-ui                 # Build web UI (auto-run by build/test/vet/lint when web/dist is missing)
just build-full               # Build web then binary
just openapi                  # Generate OpenAPI spec (requires swag CLI)
just web-dev                  # Vite dev server with hot-reload
just test-integration         # E2E integration tests
```

Note: if you need to run the full test suite, you should prefer `just hook` as that command is cached

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

- **Coverage thresholds are quality gates** — never lower them to make CI pass. If coverage drops below the threshold, add tests to cover the gap. Only the project owner may approve lowering a threshold.
- Hand-written mocks that satisfy interfaces — no codegen.
- In-memory SQLite (`:memory:`) for persistence tests via `NewInMemoryStore()`.
- Individual `TestName_Scenario` functions (not table-driven).
- Always run with `-race` flag.
- Web UI must be built before any Go step that embeds `internal/web/dist/`. The `ensure-web-dist` justfile recipe handles this automatically for `build`/`test`/`vet`/`lint`; CI uses an explicit `build-ui` job.
- **Web UI tests**: Vitest + jsdom + MSW (mock service worker). Test files in `web/src/__tests__/` and `web/src/components/__tests__/`. Run via `just test-ui`.
- **E2E integration tests**: `internal/integration/` boots a full in-process API server with a mock LLM and in-memory stores (`just test-integration`). Use `NewHarness(t, &HarnessOpts{...})` — options include `ConfigPath` (for handlers that persist to TOML), `WithLifecycleMgr` (enables tool CRUD endpoints), and `Responses` (mock LLM response sequence). WebSocket tests require `httptest.NewServer` for the upgrade handshake; all other tests use `httptest.NewRecorder`.

## Permission Tiers & Approval Workflows

Three tiers: `autonomous` (all actions), `supervised` (chat + tools with approval), `restricted` (chat + read-only tools).

`internal/approval/` manages requests requiring human sign-off. Flow: Engine submits to Manager → Manager persists + registers closure → Engine attaches Approve/Deny inline keyboard → user clicks → callback handler resolves → closure invoked.

Eleven action kinds: `user_update`, `soul_update`, `identity_update`, `create_skill`, `update_skill`, `delete_skill`, `modify_schedule`, `install_tool`, `modify_config`, `browser_profile`, `tool_call`.

**Supervised tool call approval**: When `permission_tier = "supervised"`, each MCP tool call is submitted for approval before execution. Engine first checks `Manager.ShouldAutoApprove()` — if a matching rule exists, the tool executes immediately and a `tool_approval` ChatEvent with `approval_status: "auto_approved"` is emitted. Otherwise Engine blocks on `Manager.WaitForResolution(ctx, id)`. Dispatcher intercepts pending `"tool_approval"` ChatEvents and sends inline keyboard messages with four buttons: Approve, Deny, Auto (session), Auto (always). Denied tool calls feed "Tool call was denied by the operator." to the LLM. Within one message turn, a tool call whose name+args exactly match one already denied (by supervisor or human) is auto-denied without another approval round-trip, emitting a `tool_approval` ChatEvent with status `auto_denied`; the dedup map resets on the next user message.

**Auto-approve rules**: Two scopes — `session` (in-memory, conversation-scoped, cleared on restart) and `permanent` (persisted in SQLite, agent-scoped). `Manager.ShouldAutoApprove()` checks session rules first, then permanent rules. Future config-based rules (`ScopeConfig`) can be added as a third check. Rules are created from Telegram inline buttons (`:approve_session`, `:approve_always` callback suffixes), from the web UI chat (Always Approve button), or via the REST API. `approval.ExtractToolName()` parses the tool name from the approval summary to key rules.

**Supervisor agents**: A supervised agent can designate another agent as its supervisor via `supervisor = "agent-name"` in TOML. The supervisor sits between auto-approve rules and human approval in the resolution chain: auto-approve → supervisor review → human approval. The supervisor receives a structured prompt with tool call details, skill/schedule invocation context (if applicable), and recent conversation history, and returns APPROVE (tool executes), DENY (denied with reason fed to LLM), or ESCALATE (falls through to human approval). Supervisor calls are lightweight one-shot LLM calls via the supervisor's Router — no conversation storage, skill matching, or tool loops. Config validation: supervisor must exist, must not itself be supervised (no chaining), must not use supervised tier (would deadlock), and `supervisor` is only valid on supervised agents. Delete guard prevents removing agents referenced as supervisors. Supervisor decisions emit `audit.CategorySupervisor` events and `tool_approval` ChatEvents with status `supervisor_approved`/`supervisor_denied`/`supervisor_escalated`/`supervisor_error` (LLM failure; falls through to human). Default timeout: 30s (configurable via `supervisor_timeout`; `supervisor_context_messages` controls how many messages are passed, default 5). Web UI: supervisor dropdown plus timeout and context-messages knobs in Agents permission panel, meta line shows supervisor relationship, Chat page renders supervisor statuses, AuditLog has Supervisor filter chip.

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

**Health monitoring**: `StartHealthChecker(ctx, interval)` probes servers via ListTools every 30s. Crashed servers are auto-restarted with exponential backoff. Config: `[mcp]` section with `auto_restart` (default true), `max_restart_attempts` (default 3), `restart_cooldown` (default "5m"). `ServerStatus` reports `connected`/`error`/`disabled`/`config_error` with `restart_count`, `last_error`, `uptime_secs`, `enabled`, `config_error`. Manual restart via `Manager.RestartServer()`, `LifecycleManager.RestartTool()`, REST `POST /api/v1/tools/{name}/restart`, or Config MCP `tool_restart`. Enable/disable via `LifecycleManager.EnableTool()`/`DisableTool()`, REST `POST /api/v1/tools/{name}/enable` and `/disable`.

**OAuth 2.1 for MCP tools**: `internal/tool/oauth/` implements the MCP OAuth 2.1 spec for remote SSE tool servers that require authorization. Config per tool: `auth = "oauth"` with optional `client_id`, `client_secret`, `scopes`. OAuth routes are mounted at `/api/v1/tools/{name}/oauth/...`. Token storage in SQLite. `api.external_url` used for callback URL construction.

**Security**: SSRF protection, header injection prevention, env var denylist, URL/arg redaction in API responses.

## External REST API

`internal/api/` — HTTP API server (enabled by default). Auth via Bearer token (API keys) or session cookies (password/OIDC).

Key endpoints (all require auth unless noted):
- `GET /api/v1/health` (no auth)
- `GET /api/v1/openapi.json` (no auth) — generated OpenAPI 2.0 spec
- `GET /llms.txt` (no auth) — LLM-readable instance summary: base URL, auth notes, key endpoints, configured agents
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
- `POST /api/v1/tools/{name}/enable` (scope `tools:write`) — enable a disabled tool server; starts the MCP process and persists to TOML
- `POST /api/v1/tools/{name}/disable` (scope `tools:write`) — disable a tool server; stops the MCP process and persists `enabled = false` to TOML
- `POST /api/v1/agents` (scope `admin`) — create a new agent; body: `{name, llm_provider, llm_model, session_tier, description, create_supervisor}`. Optional `create_supervisor: {name, llm_model, timeout, context_messages}` atomically creates a companion supervisor when `session_tier="supervised"`. Creates persona directory, persists to `[[agents]]` in TOML.
- `PATCH /api/v1/agents/{name}` — agent config mutation; supports `name` (rename), `session_tier`, `llm_provider`, `llm_model`, `description`, `browser_url_allowlist`, `fallbacks`, `cost_limit_soft`, `cost_limit_hard`, `supervisor`, `supervisor_timeout`, `supervisor_context_messages`
- `DELETE /api/v1/agents/{name}` (scope `admin`) — remove agent. Rejects if referenced by channels/schedules or if it is the last agent. Removes from TOML. Does not delete persona files.
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
- `GET /api/v1/onboarding` (scope `admin`) — checklist of 5 setup milestones; `show_onboarding` false when all done or dismissed; includes `wizard_completed` bool
- `POST /api/v1/onboarding/dismiss` (scope `admin`) — persist `onboarding_dismissed=true` to TOML, hide card
- `POST /api/v1/onboarding/wizard-complete` (scope `admin`) — persist `wizard_completed=true` to TOML, suppress post-auth setup wizard
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
| MCP Server | `internal/mcpserver/` | `[api.mcp_server]` |
| Script MCP | `internal/scriptmcp/` | `[script]` |

## Non-obvious Defaults & Invariants

Things you can't infer by reading the code at a glance:

- **TOML config writer** (`config.AddX/UpdateX/RemoveX`): atomic read-modify-write under a file mutex, with a `.bak` backup written before each save. Writers don't preserve comments or formatting — anything you add round-trips through the parser.
- **Engine knobs**: `Engine.SetMaxContextMessages` (default 50) caps loaded history; `Engine.SetApprovalConfig` controls approval retries; `Engine.SetSupervisorConfig` sets supervisor timeout (default 30s) and context message count (default 5); `Engine.SetMaxToolRounds` caps the tool-call loop (default 50 — counts **rounds**, not individual calls; a round may fan out to many parallel calls). The loop appends an authoritative `[engine: N of M tool-call rounds remaining this turn]` hint to the **final** tool result of each round (`toolBudgetHint`) so the model reads its remaining budget instead of reconstructing the count from its own history — skills should not carry prose that tells the model to self-count tool calls. All knobs are re-applied to live engines on hot-reload (`buildReloadFunc` iterates `cfg.Agents`). Stalled SSE streams are capped by `IdleTimeoutReader`.
- **Tool-loop wrap-up on model-behavior stops**: when the loop stops because the model misbehaved with a *healthy* context — repeated identical tool calls (threshold 3) or `maxToolRounds` exhausted — the engine does NOT fail the turn. It appends synthetic tool results for suppressed calls, then issues one final **tools-stripped** completion (`Router.CompleteFinal`, `Tools: nil` on the wire, so the provider cannot return tool calls) asking the model to summarize the executed work. Fallback ordering: wrap-up text → accumulated intermediate content → error into `persistInterruptedProgress` (the pre-wrap-up marker behavior, still the path for transport faults). Delivered/stored content carries a `[engine: turn ended early — <reason>]` marker; the wrap-up round audits as `wrap_up: true` (sibling of `nudge_retry`, no `round` field). No config knob — the behavior is unconditional. Design: `design/loop-guard-wrapup-round.md`.
- **Telemetry retention**: `retention_days` default 90, `max_conversations` default 10000 (both in `[memory]`). Audit log defaults: `retention_days` 30, plus `cleanup_interval` and `buffer_size`.
- **Tool-call telemetry outcome split & skill attribution**: `tool_calls.outcome` is `ok`/`rejected` (healthy tool, bad args)/`failed` (transport/exec)/`denied` (approval). `ToolUsageSummary.error_count` is the **legacy combined** total (rejected+failed+denied) kept for back-compat — use `failure_count` for a "broken tool" signal; `denial_count` is not a fault. Each tool call is attributed to a single owning `skill_name`+`skill_version` in `Engine.persistTelemetry` via `attributeSkill`: an explicit `msg.SkillName` invocation (scheduled/command) wins, else a lone matched skill; ambiguous (0 or >1 matched, interactive) turns are left blank — never guessed. `GetTelemetrySummary.by_tool_skill` groups reliability per `(skill,version)` over attributed calls only (surfaced via `get_cost_summary`/`telemetry_summary`), so a skill's tool behaviour can be compared across versions. Attribution is forward-only; pre-migration rows have empty skill fields and were backfilled `outcome='failed'`.
- **WebSocket replay buffer**: per-connection, `websocket_replay_buffer_ttl` default 5m. WS frame types live in `internal/api/wsframes.go`.
- **Conversation ID format**: `chan:{channel_name}` for shared channels; `chan:{name}:{unix_nano}` for `session_mode = "ephemeral"` (fresh conversation per interaction; cross-adapter ephemeral is rejected at config-validation time).
- **Channel resolution priority**: active override (`/session`) > specific binding > wildcard binding > legacy `resolveAgent` fallback. Sentinel errors for classification: `ErrChannelNotFound`, `ErrChannelsNotConfigured`, `ErrAdapterKeyNotActive`.
- **Audit emission boundaries**: Engine emits one `audit.CategoryLLM` event per tool-call loop round (round indexed from 1, no `-1` sentinel). Engine also emits one `audit.CategorySupervisor` event per supervisor decision, with `raw_response` preserved alongside the parsed decision/reason.
- **Engine prompt injection**: when the agent has KV MCP tools available, Engine adds a short KV usage note to the system prompt automatically — no config knob.
- **Telegram activity log**: tool approvals render inline inside the activity-log message (collapsible blockquote) with approval buttons, not as separate messages. The renderer splits chunks at ~3500 chars to stay under Telegram's 4096 cap.
- **`MemoryStore.ClearMessages`**: deletes messages + telemetry in one transaction but preserves the conversation row (so session identity / channel mapping survives `/clear`). `/compact` replaces history with a single `[Session compacted]` summary message.
- **Safety commands**: panic state is transient (cleared on restart). `Scheduler.Pause()/Resume()` cancels entry goroutines without cancelling the root context. Dispatcher keys in-flight requests by `adapter:externalID`.
- **Pricing lookup priority**: provider-reported > registry exact > registry longest-prefix > `[costs]` fallback > $0 (with warning). Source ends up as the `pricing_source` OTel attribute.
- **MCP server health**: `StartHealthChecker` polls via `ListTools` every 30s. Restart defaults: `auto_restart=true`, `max_restart_attempts=3`, `restart_cooldown=5m`. `health_fail` audit events for remote (sse/http) servers are debounced: emitted only after `health_fail_threshold` consecutive probe failures (default 3); stdio servers emit on the first failure. Restart/log behavior is not debounced.
- **Tool `enabled` field**: `ToolConfig.Enabled *bool` — `nil` (absent in TOML) defaults to `true` via `applyToolDefaults`. Explicit `enabled = false` disables without removing the config. `IsEnabled()` helper method. Config writer omits the key when true (backward compat).
- **Graceful tool validation**: `validateTools` is non-fatal — invalid tools are auto-disabled with `Enabled = &false` and their validation error is stored in `Config.ToolWarnings`. The server starts and runs with the remaining valid tools. Invalid tools appear in the tools list with `config_error` status.
- **`run_javascript` (Script MCP)**: in-process per-agent tool that runs a short JS snippet (goja, pure-Go ES5.1) against JSON `input` to move deterministic formatting/classification off the completion-token path. Fresh VM per call with no host globals (no network/fs/require). Bounded by `[script]` `timeout` (default `2s`, enforced via `vm.Interrupt` + ctx cancel), `max_output_chars` (16000, truncates), `max_input_bytes` (262144, rejects), and `max_concurrent` (default 4) — a process-global semaphore (`scriptmcp.NewSemaphore`, built once in main.go and shared across every agent's `Deps`) that bounds simultaneous VM executions so concurrent snippets can't multiply allocation against the shared heap; a waiting call blocks until a slot frees or its context is cancelled. Set `max_concurrent` negative for unbounded. `max_concurrent_per_agent` (default 0 = off) adds a second per-agent semaphore (built fresh per agent, not shared) so one agent can't monopolize the global pool; `acquireSlot` takes the per-agent slot then the global slot in that fixed order across all callers (no deadlock) and releases in reverse. Disabled in `restricted` tier. Residual risk: goja has no hard *per-VM* heap cap, so within the concurrency cap a runaway allocator still grows process memory until the timeout fires — `max_concurrent` bounds the worst-case multiplier (~`max_concurrent × rate × timeout`) but is not a hard memory ceiling.
- **Supervisor config validation**: supervisor must exist, must not itself be supervised (no chaining), must not use `supervised` tier (would deadlock), and `supervisor` is only valid on supervised agents. Delete guard rejects removing an agent referenced as a supervisor. `supervisor_timeout` is a Go duration string (e.g. `"30s"`, `"1m"`); `supervisor_context_messages` is an int (0 = use default of 5).
- **Config MCP tools** (the in-process per-agent set, not the REST API): `schedule_update`, `schedule_delete`, `set_fallback`, `get_cost_summary`, `skill_delete`, `channel_list`, `channel_switch`, `channel_info`.
- **External MCP audit tools** (`internal/mcpserver/tools_audit.go`): `audit_events` and `audit_summary` mirror the REST `GET /api/v1/audit` and `/audit/stats` endpoints (same `audit.Store.List`/`Stats`, same `audit:read` scope, same filters/pagination). They read via `Deps.AuditStore` (`audit.Store`), distinct from the write-path `Deps.Auditor` (`audit.Emitter`); when the store is unconfigured they return a graceful `toolError("audit not configured")` rather than being unregistered.
- **External MCP skill tools** (`internal/mcpserver/tools_skills.go`): `skill_create`, `skill_update`, `skill_delete` persist to disk and return a `toolError` on persist failure — in-memory state is left unchanged. All four ops are **disk-first**: create/update/rename write via `configmcp.ApplySkill*` then mutate memory; delete checks existence (`GetSkill`) → `RemoveSkillFile` (fail-loud) → `RemoveSkill`, so a real file-removal IO error leaves the skill intact in memory and on the next reload. REST `handleDeleteSkill` matches (returns 500, not 204, on file-removal IO error). `skill_update` supports optional `version` and `new_name` (rename) fields. `validSkillName` rejects names with path separators or `..` (friendly outer guard).
- **Skill-file IO is confined to the skills dir via `os.Root`**: all skill writes/removes in `configmcp` (`ApplySkillCreate/Update/Rename`, `RemoveSkillFile`) go through an `os.Root` opened on `agentSkillsDir` (`openSkillRoot` + `writeSkillFileAtomic`). The OS-level path resolution refuses any write or remove that escapes the directory — `..` traversal or escaping symlinks — a hard boundary that backstops `ValidateSkillName` (the shared name denylist enforced inside the write helpers on every surface). Covers all three surfaces (REST, in-process config MCP, external MCP). Requires Go ≥ 1.24 (`os.OpenRoot`); `Root.OpenFile`/`Rename`/`Remove` are used (≥ 1.24/1.25).
- **Skill write hardening (beyond path-escape)**: writes use a *randomized* temp name (`.skill-<rand>.tmp`, created `O_EXCL`) + `Root.Rename`, so concurrent writers to the same skill name can't share/corrupt one temp file. Per-file size is capped by `[skills] max_bytes` (default 1 MiB; negative = unlimited) — enforced inside `ApplySkill*` via the `maxBytes` param (`checkSkillPayloadSize`), since skill content is written verbatim and would otherwise be an unbounded-write DoS. REST/external-MCP read the cap from `deps.Config.Skills.MaxBytes` (helper `skillMaxBytes()`); config MCP from `Deps.MaxSkillBytes`. Property is fuzz-tested (`FuzzApplySkillCreate_NeverEscapes`) and the concurrency invariant is `-race` tested.
- **Shared validators** (in `internal/config`): `ValidResourceName`, `ValidProviderType`, `IsProviderReferenced` — use these when adding new CRUD endpoints to keep validation consistent.
- **`internal/agentctx`**: context key package shared between `agent` and `configmcp` to avoid an import cycle. Use it when threading agent identity through MCP handlers.
- **Date/week injection (two-point)**: the model never has to infer "today". (1) Scheduled messages render the fire time via `scheduler.FormatScheduledText` — `[Scheduled: heartbeat | 2026-07-07T10:45:00+10:00 Australia/Sydney | 2026-W28]` — computed inside the fire callback (both producers: `registerSchedules`/`buildScheduledMessage` in main.go and `configmcp.BuildScheduleJob`), with `msg.Timestamp` set from the same instant. (2) `buildSystemPrompt` appends a `## Current Date` section at **day resolution by design** — the system prompt is the prompt-cache prefix; a clock time would bust the cache every turn. Timezone precedence: agent `timezone` > `api.timezone` > UTC (`agentLocation` in main.go; engine knob `SetLocation`, hot-reloaded). Cron *evaluation* remains `api.timezone` and restart-only. Midnight-crossing runs can see header date ≠ prompt date — the fire-time header wins for dated keys (skill prose rule).

CI/CD: golangci-lint, gosec, govulncheck, Grype, Gitleaks, GoReleaser, Homebrew tap, Docker (ghcr.io) with cosign + SLSA, GitHub Pages docs.

See `design/denkeeper-prd.md` for the full roadmap.
