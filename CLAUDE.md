# CLAUDE.md

Guidance for Claude Code (claude.ai/code) when working in this repository.

## Build & Development Commands

Tooling: `just` (user-facing) → Task (`Taskfile.yml`, per-target fingerprint caching, cache in gitignored `.task/`) → mise for tool pinning (`.mise.toml`: Go 1.26.5, swag 1.16.6 — swag pinned because its output is a committed, CI-gated artifact). Bust one step: `mise x -- task <name> --force`; bust the whole hook chain: `JUST_HOOK_FORCE=1 just hook`.

```bash
just build                    # Build binary → pkg/bin/denkeeper
just serve                    # go run (optional config path)
just test                     # All Go tests with -race
just test-v                   # Verbose test output
just test-pkg internal/agent  # Single package
just test-ui                  # Web UI tests (vitest)
just lint                     # golangci-lint
just fmt                      # gofmt -w .
just check                    # CI equivalent: fmt-check + vet + lint + test + test-ui + openapi-check
just hook                     # `just check`, minimal output, cached — prefer this for full-suite runs
just scan                     # gosec + govulncheck
just build-ui                 # Build web UI (auto-run by build/test/vet/lint when web/dist missing)
just build-full               # Build web then binary
just openapi                  # Generate OpenAPI spec (requires swag CLI)
just openapi-check            # Fail if the committed spec is stale (CI gate)
just web-dev                  # Vite dev server (hot reload)
just test-integration         # E2E integration tests
```

## Architecture

Denkeeper is a single-binary personal AI agent with multi-agent routing:

```
Adapter (Telegram/Discord) ─┐
Web Dashboard (WS/SSE) ─────┼→ Dispatcher → Engine (per agent) → LLM Router → Provider (Anthropic/OpenRouter/OpenAI/Ollama)
REST API (/api/v1/chat) ────┘                    ↕                    ↕
                                             MemoryStore          CostTracker
                                             (SQLite)              + Pricing Registry
```

- **Dispatcher** (`internal/agent/dispatcher.go`): routes messages to Engines via channel bindings (or legacy adapter bindings), falling back to the `"default"` agent. Sends inline-keyboard approval messages for `tool_approval` ChatEvents; intercepts `/session` for runtime channel switching.
- **Channels** (`internal/agent/channel.go`): named routing endpoints decoupling sessions from 1:1 agent–adapter binding. A channel points to one agent, can bind multiple adapters (cross-adapter session sharing). Config `[[channels]]` (`name`, `agent`, `adapters`); auto-synthesized from agent `adapters` when absent. Conversation ID `"chan:{name}"`. `/session <name>` switches; selections persist in SQLite `active_channels`.
- **Engine** (`internal/agent/engine.go`): per-agent orchestrator. Pipeline: permissions → get/create conversation → store user msg → load history → system prompt (persona + skills) → `Router.Complete()` → tool-call loop (with approval if supervised) → usage event → memory extraction → store assistant msg → return.
- **Three key interfaces**: `adapter.Adapter` (platforms), `llm.Provider` (LLM backends), `agent.MemoryStore` (SQLite persistence).
- **Multi-agent config**: `[[agents]]` with `name`, `persona_dir`, `adapters`, `llm_provider` (overrides global `default_provider`), `llm_model`, `session_tier`. With no `[[agents]]`, a single `"default"` agent is synthesized when an adapter token is set (headless, bound to those adapters) **or** `api.enabled` is explicitly true (API-only, no bindings). Nothing configured → no agent, so the web setup wizard can guide creation; synthesis runs before `api.enabled` defaults to true so "explicit" is distinguishable.
- **Named provider instances**: `[[llm.providers]]` allows multiple instances of one provider type (`name`, `type`, `api_key`, `base_url`, `organization`). Legacy `[llm.openai]` single-slot syntax auto-converts. Per-agent `llm_provider` references by name.
- **Data directory**: all default paths (db/persona/skills) derive from one base dir — `DENKEEPER_DATA_DIR` env > `data_dir` TOML > `~/.denkeeper`. Helm sets `DENKEEPER_DATA_DIR=/data`.
- **Wiring** is in `cmd/denkeeper/main.go`; all behavior should be TOML-configurable, not hardcoded.

## Conventions

- **Error wrapping**: always `fmt.Errorf("context: %w", err)`.
- **Logging**: `log/slog` with contextual fields.
- **Context**: all I/O functions accept `context.Context`.
- **Concurrency**: channels for message passing; `sync.Mutex` for shared state.
- **Config**: three-phase — parse TOML → apply defaults (incl. env) → validate. `applyEnvOverrides()` reads an explicit allowlist of `DENKEEPER_*` vars.
- **Cyclomatic complexity**: gocyclo threshold 15 for all non-test functions.

## Testing Patterns

- **Coverage thresholds are quality gates** — never lower to make CI pass; add tests instead. Only the project owner may approve lowering one.
- Hand-written mocks (no codegen); in-memory SQLite via `NewInMemoryStore()`; individual `TestName_Scenario` functions (not table-driven); always `-race`.
- Web UI must be built before Go steps that embed `internal/web/dist/` (`ensure-web-dist` recipe handles it; CI uses an explicit `build-ui` job).
- **Web UI**: Vitest + jsdom + MSW; tests in `web/src/__tests__/` and `web/src/components/__tests__/`.
- **E2E** (`internal/integration/`): full in-process API server, mock LLM, in-memory stores via `NewHarness(t, &HarnessOpts{...})` (`ConfigPath`, `WithLifecycleMgr`, `Responses`). WebSocket tests need `httptest.NewServer`; the rest use `httptest.NewRecorder`.

## Permission Tiers & Approval Workflows

Tiers: `autonomous` (all actions), `supervised` (chat + tools with approval), `restricted` (chat + read-only tools).

`internal/approval/` manages human sign-off. Flow: Engine submits → Manager persists + registers closure → inline Approve/Deny keyboard → callback resolves → closure invoked. Eleven action kinds: `user_update`, `soul_update`, `identity_update`, `create_skill`, `update_skill`, `delete_skill`, `modify_schedule`, `install_tool`, `modify_config`, `browser_profile`, `tool_call`.

**Supervised tool calls**: each MCP call needs approval. Engine checks `Manager.ShouldAutoApprove()` first (match → immediate execution, `tool_approval` event with `auto_approved`), else blocks on `WaitForResolution`. Dispatcher sends four buttons: Approve, Deny, Auto (session), Auto (always). Denied calls feed "Tool call was denied by the operator." to the LLM. Within one turn, a name+args exact match against an already-denied call is auto-denied (status `auto_denied`); dedup map resets on next user message.

**Auto-approve rules**: `session` scope (in-memory, conversation-scoped) and `permanent` (SQLite, agent-scoped); session checked first. Created from Telegram buttons (`:approve_session`/`:approve_always`), web UI Always Approve, or REST. `approval.ExtractToolName()` keys rules from the approval summary.

**Supervisor agents**: `supervisor = "agent-name"` on a supervised agent inserts an LLM reviewer between auto-approve and human approval: APPROVE / DENY (reason fed to LLM) / ESCALATE (→ human). One-shot LLM call via the supervisor's Router with tool details + recent history — no storage/skills/tool loops. Emits `audit.CategorySupervisor` events and `tool_approval` statuses `supervisor_approved`/`_denied`/`_escalated`/`_error` (error falls through to human). Web UI: supervisor controls in Agents, statuses in Chat, filter chip in AuditLog.

## Cost Tracking & Pricing

`internal/llm/pricing/` — registry with bundled defaults for ~70 models. `TokenCost(resp, reg)` returns `(cost, source)`; source becomes the `pricing_source` OTel attribute. Unknown models log a warning. `TokenUsage.CachedPrompt` from Anthropic `cache_read_input_tokens` / OpenAI `prompt_tokens_details.cached_tokens`.

Config: `[costs] default_rate_per_1k_tokens` (fallback when model unknown; 0 = $0 + warn); `[costs.model_prices.<model>]` with `input`/`output`/`cached_input` in $ per million tokens (`cached_input` 0 = same as input).

## MCP Tools & Health Monitoring

`internal/tool/manager.go` manages MCP servers (stdio subprocess or SSE remote).

- **Health**: `StartHealthChecker` probes via ListTools every 30s; crashed servers auto-restart with backoff. `[mcp]`: `auto_restart` (true), `max_restart_attempts` (3), `restart_cooldown` ("5m"). `ServerStatus`: `connected`/`error`/`disabled`/`config_error` + restart_count/last_error/uptime. Manual restart & enable/disable via Manager/LifecycleManager, REST, or Config MCP.
- **OAuth 2.1** (`internal/tool/oauth/`): MCP OAuth for remote SSE servers — per-tool `auth = "oauth"` (+ optional `client_id`/`client_secret`/`scopes`), routes at `/api/v1/tools/{name}/oauth/...`, tokens in SQLite, `api.external_url` for callbacks.
- **Security**: SSRF protection, header injection prevention, env var denylist, URL/arg redaction in API responses.
- **Stdio env scoping** (`internal/tool/env.go`, `buildStdioEnv`): children do NOT inherit the parent env (would leak `DENKEEPER_*` secrets). They get a built-in non-secret allowlist (`PATH`, `HOME`, `TMPDIR`, `USER`, `LOGNAME`, `SHELL`, `LANG`, `LC_*`, `TZ`, `TERM`; `NODE_PATH`/`NODE_OPTIONS`/`PYTHONPATH`/`VIRTUAL_ENV`/`GOPATH`; Windows equivalents) plus the tool's own `env` (appended last, wins). `env_passthrough = [...]` on `[mcp]` and/or `[tools.*]` forwards extras. A hard exclusion filter (`isExcludedEnvVar`: any `DENKEEPER_*` or the `forbiddenEnvPatterns` denylist) applies to every forwarded name — allowlist AND passthrough — so secrets can never reach the child (blocked passthroughs are logged and dropped). Only spawn site: `registerStdio`.

## External REST API

`internal/api/` — HTTP API (enabled by default). Auth: Bearer token (API keys) or session cookies (password/OIDC). Canonical machine-readable reference: the generated OpenAPI spec (`internal/api/docs/swagger.json`, served at `GET /api/v1/openapi.json`, no auth). All paths below are under `/api/v1/` unless noted.

| Endpoints | Scope | Non-obvious semantics |
|---|---|---|
| `GET health`, `GET openapi.json`, `GET /llms.txt` | none | `llms.txt` = LLM-readable instance summary (base URL, auth notes, key endpoints, configured agents) |
| `POST chat` | `chat` | JSON or SSE streaming |
| `GET ws` | — | WebSocket upgrade; auth via `?token=` or session cookie |
| `GET models`, `GET models/details` | `agents:read` | details includes pricing info |
| `approvals` CRUD | — | `POST .../approve` accepts `?auto_approve=session\|permanent` to simultaneously create an auto-approve rule |
| `auto-approve` CRUD | `approvals:read/write` | `GET` accepts `?agent=` filter |
| `schedules` (PATCH edit), `skills` (PUT edit), `kv`, `GET/PUT agents/{name}/persona/{section}` | — | plain CRUD |
| `POST agents` | `admin` | body `{name, llm_provider, llm_model, session_tier, description, create_supervisor}`; optional `create_supervisor: {name, llm_model, timeout, context_messages}` atomically creates a companion supervisor when `session_tier="supervised"`; creates persona dir, persists `[[agents]]` to TOML |
| `PATCH agents/{name}` | — | mutable: `name` (rename), `session_tier`, `llm_provider`, `llm_model`, `description`, `browser_url_allowlist`, `fallbacks`, `cost_limit_soft`, `cost_limit_hard`, `supervisor`, `supervisor_timeout`, `supervisor_context_messages` |
| `DELETE agents/{name}` | `admin` | rejects if referenced by channels/schedules or last agent; removes from TOML; does NOT delete persona files |
| `llm/providers` CRUD, `PATCH llm/config` | `admin` | create body `{name, type, api_key, base_url, organization}`; delete rejects if referenced by agents or `default_provider`; `llm/config` = global defaults |
| `auth`: `GET status`, `GET/DELETE sessions`, `POST password`, `GET oidc/test`, `POST preferences` | `admin` | password change = bcrypt verify + re-hash + persist; `oidc/test` does fresh discovery with 10s timeout; preferences = preferred login method (auto/password/apikey) |
| `GET onboarding`, `POST onboarding/dismiss\|wizard-complete` | `admin` | checklist of 5 milestones; `show_onboarding` false when all done or dismissed; includes `wizard_completed`; dismiss/wizard-complete persist `onboarding_dismissed`/`wizard_completed` to TOML |
| `GET/PATCH server/config`, `POST server/reload\|restart` | `admin` | config = version, build info, CORS, WebSocket settings; reload re-reads TOML from disk |
| `GET sessions/{id}/stats\|tool-calls\|skills` | `sessions:read` | per-session telemetry, tool-call records, skill usage |
| `POST sessions/{id}/clear\|compact` | `sessions:write` | both accept `?agent=` hint; compact returns `{"summary": "..."}`; see `ClearMessages` invariant |
| `POST sessions/{id}/stop` | `chat` | cancel in-flight request for a session |
| `GET telemetry/summary` | `costs:read` | `?since=&until=` filtering |
| `GET audit`, `GET audit/stats` | `audit:read` | list filters `?category=&agent=&status=&source=&search=&since=&until=&limit=&offset=`; stats accepts `?since=` |
| `GET channels(/{name})` | `channels:read` | list: agent, adapter bindings, implicit flag, active adapter keys; detail adds `conversation_id` |
| `POST/DELETE channels/{name}/activate` | `channels:write` | body `{"adapter_key": "telegram:12345"}`; DELETE clears the override and 409s if the key is not active on this channel |
| `tools` CRUD (PUT edit), `GET {name}/health`, `POST {name}/restart\|enable\|disable` | `tools:read`/`tools:write` | enable starts the MCP process, disable stops it; both persist to TOML; 404 convention in invariants |
| `POST panic`, `POST resume`, `GET panic` | `admin` | emergency stop: cancel all in-flight requests + pause scheduler; resume clears; GET returns `{panicked, panic_time}` |

Chat streaming events (SSE and WS): `thinking`, `tool_start`, `tool_end`, `tool_approval`, `usage`, `content`, `done`.

## Web Dashboard & WebSocket Transport

`internal/web/` embeds a Svelte SPA (`//go:embed dist`). 17 pages, roughly one per subsystem (routes in `web/src`).

**WebSocket** (`internal/api/websocket.go`): `GET /api/v1/ws` upgrades to bidirectional WS; dashboard auto-connects and falls back to SSE after 3 failed reconnects. `WSHub` keeps a per-connection replay buffer. Config: `api.websocket_enabled` (true), `api.websocket_max_connections`, `api.websocket_replay_buffer_ttl` (5m). Frame types in `wsframes.go`.

## UI/UX Standards

Every user-facing feature gets thoughtful UX:

- **Web (Svelte)**: loading spinners, empty states with CTAs, inline errors, confirm destructive actions, success feedback, disabled buttons in-flight, responsive ≥320px, existing CSS variables (`--accent`, `--surface`, `--border`, `--text-muted`, `--danger`).
- **CLI (Cobra)**: progress feedback for >500ms ops, `tabwriter` tables, actionable errors, non-zero exits via `RunE`.
- **Adapters**: typing indicators before LLM calls, platform-native formatting, inline keyboards for approvals. Telegram registers built-in commands (`/start`, `/help`, `/stop`, `/panic`, `/resume`, `/debug`, `/clear`, `/compact`) plus skill `command:` triggers via `setMyCommands` (`RegisterSkillCommands`).

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

- **TOML config writer** (`config.AddX/UpdateX/RemoveX`): atomic read-modify-write under a file mutex, `.bak` backup before each save. Comments/formatting are NOT preserved — everything round-trips through the parser.
- **Engine knobs** (all re-applied to live engines on hot-reload via `buildReloadFunc`): `SetMaxContextMessages` (50), `SetApprovalConfig`, `SetSupervisorConfig` (30s / 5 messages), `SetMaxToolRounds` (50 — counts **rounds**, not calls; a round may fan out to many parallel calls). The loop appends `[engine: N of M tool-call rounds remaining this turn]` (`toolBudgetHint`) to the **final** tool result of each round so the model reads its budget — skills should not tell the model to self-count tool calls. Stalled SSE streams capped by `IdleTimeoutReader`.
- **Tool-loop wrap-up on model-behavior stops** (unconditional, no config knob): when the loop stops due to repeated identical tool calls (threshold 3) or `maxToolRounds` exhaustion with a *healthy* context, the engine does NOT fail the turn — it appends synthetic tool results for suppressed calls, then issues one final **tools-stripped** completion (`Router.CompleteFinal`, `Tools: nil` on the wire) to summarize executed work. Fallback: wrap-up text → accumulated intermediate content → error into `persistInterruptedProgress` (still the path for transport faults). Content carries `[engine: turn ended early — <reason>]`; the wrap-up round audits `wrap_up: true` (no `round` field). Design: `design/loop-guard-wrapup-round.md`.
- **Telemetry retention**: `[memory]` `retention_days` 90, `max_conversations` 10000. Audit: `retention_days` 30, plus `cleanup_interval`, `buffer_size`.
- **Tool-call outcome split & skill attribution**: `tool_calls.outcome` ∈ `ok`/`rejected` (healthy tool, bad args)/`failed` (transport/exec)/`denied` (approval). Summaries expose `rejection_count`/`failure_count`/`denial_count` — use `failure_count` as the "broken tool" signal; a denial is not a fault. The legacy `error_count` (all three summed) was removed from JSON payloads in the #215 fix (DB rows unchanged; reconstruct by summing if needed). Each call is attributed to one owning `skill_name`+`skill_version` (`Engine.persistTelemetry` → `attributeSkill`): explicit `msg.SkillName` wins, else a lone matched skill; ambiguous → blank, never guessed. `by_tool_skill` in `GetTelemetrySummary` groups reliability per `(skill,version)` over attributed calls only (surfaced via `get_cost_summary`/`telemetry_summary`), so a skill's tool behaviour can be compared across versions. Attribution is forward-only; pre-migration rows have empty skill fields, backfilled `outcome='failed'`.
- **Conversation IDs**: `chan:{name}` for shared channels; `chan:{name}:{unix_nano}` for `session_mode = "ephemeral"` (fresh per interaction; cross-adapter ephemeral rejected at config validation).
- **Channel resolution priority**: active override (`/session`) > specific binding > wildcard binding > legacy `resolveAgent` fallback. Sentinel errors: `ErrChannelNotFound`, `ErrChannelsNotConfigured`, `ErrAdapterKeyNotActive`.
- **Audit emission boundaries**: one `audit.CategoryLLM` event per tool-loop round (indexed from 1, no `-1` sentinel); one `audit.CategorySupervisor` event per supervisor decision, `raw_response` preserved alongside parsed decision/reason.
- **Engine prompt injection**: if the agent has KV MCP tools, Engine adds a KV usage note to the system prompt automatically — no config knob.
- **Telegram activity log**: tool approvals render inline in the activity-log message (collapsible blockquote) with buttons, not as separate messages. Every size budget in `dispatcher.go` counts **rendered, HTML-escaped** bytes, never source text (`&` → 5 bytes, `<`/`>` fourfold). Budgets sum-bounded by construction: blockquote gets `activityChunkMaxBytes` (3000) alone or `activityChunkWithApprovalMaxBytes` when sharing with an approval; approval section gets `approvalSectionMaxBytes`; `truncateEscaped` enforces shares; `setPending` starts a fresh chunk if no room. Over-cap sends are rejected by Telegram — with an approval attached that silently loses the keyboard, so those failures log Error (`activityLog.logSendFailure`, `sendDebugApprovalPrompt`, `sendStandaloneApprovalPrompt`); cosmetic activity-log failures stay Debug.
- **`MemoryStore.ClearMessages`**: deletes messages + telemetry in one transaction but preserves the conversation row (session identity survives `/clear`). `/compact` replaces history with a single `[Session compacted]` summary message.
- **Safety commands**: panic state is transient (cleared on restart). `Scheduler.Pause()/Resume()` cancels entry goroutines without cancelling the root context. Dispatcher keys in-flight requests by `adapter:externalID`.
- **Pricing lookup priority**: provider-reported > registry exact > registry longest-prefix > `[costs]` fallback > $0 (with warning).
- **MCP health debounce**: `health_fail` audit events for remote (sse/http) servers emit only after `health_fail_threshold` consecutive failures (default 3); stdio emits on first failure. Restart/log behavior is not debounced.
- **Tool endpoint not-found convention**: every `/api/v1/tools/{name}` endpoint/sub-resource returns **404** when the tool isn't registered, signaled by the `tool.ErrToolNotFound` sentinel — Manager/LifecycleManager methods wrap it with `%w`, handlers classify with `errors.Is`. Other errors keep their own codes (400 malformed/rejected, 500 failed restart/removal, 503 lifecycle manager unwired). New tool endpoints follow the same wrap-and-classify pattern.
- **Tool `enabled` field**: `ToolConfig.Enabled *bool` — `nil` defaults to true (`applyToolDefaults`); explicit `enabled = false` disables without removing. `IsEnabled()` helper. Config writer omits the key when true.
- **Graceful tool validation**: `validateTools` is non-fatal — invalid tools are auto-disabled (`Enabled = &false`), errors stored in `Config.ToolWarnings`, server runs with the rest; invalid tools show `config_error` status.
- **`run_javascript` (Script MCP)**: in-process per-agent tool running short JS (goja, pure-Go ES5.1) against JSON `input` — fresh VM per call, no host globals (no network/fs/require). `[script]` bounds: `timeout` (2s, via `vm.Interrupt` + ctx cancel), `max_output_chars` (16000, truncates), `max_input_bytes` (262144, rejects), `max_concurrent` (4) — a process-global semaphore built once in main.go and shared across all agents' `Deps`; negative = unbounded. `max_concurrent_per_agent` (0 = off) adds a per-agent semaphore; `acquireSlot` takes per-agent then global in fixed order (no deadlock), releases in reverse. Disabled in `restricted` tier. Residual risk: no per-VM heap cap — `max_concurrent` bounds the multiplier (~`max_concurrent × rate × timeout`) but is not a hard memory ceiling.
- **Supervisor config validation**: supervisor must exist, must not itself be supervised (no chaining), must not use `supervised` tier (deadlock), and `supervisor` is only valid on supervised agents. Delete guard rejects removing a referenced supervisor. `supervisor_timeout` is a Go duration string; `supervisor_context_messages` int (0 = default 5).
- **Config MCP tools** (in-process per-agent set, not REST): `schedule_update`, `schedule_delete`, `set_fallback`, `get_cost_summary`, `skill_delete`, `channel_list`, `channel_switch`, `channel_info`.
- **External MCP agent tools** (`internal/mcpserver/tools_agents.go`): `agent_info` returns name/display_name/permission_tier/provider/model/skills plus `supervisor`/`persona_sections`/`channels` — the last three **omitted when empty**, so presence is the signal. `agent_list` carries the same omitempty `supervisor`. `supervisor` is read from live wiring (`Engine.Supervisor().Name()`), reflecting post-hot-reload state (REST agents handlers read config instead). `channels` derives from `Dispatcher.Channels()` filtered by agent and **sorted by name** (the registry is a map — never emit unsorted).
- **External MCP audit tools** (`tools_audit.go`): `audit_events`/`audit_summary` mirror REST `GET /audit` and `/audit/stats` (same store, scope, filters). They read `Deps.AuditStore` (`audit.Store`), distinct from write-path `Deps.Auditor` (`audit.Emitter`); unconfigured store → graceful `toolError("audit not configured")`, not unregistered.
- **External MCP skill tools** (`tools_skills.go`): `skill_create`/`skill_update`/`skill_delete` are **disk-first** — create/update/rename write via `configmcp.ApplySkill*` then mutate memory; delete checks `GetSkill` → `RemoveSkillFile` (fail-loud) → `RemoveSkill`, so an IO error leaves the skill intact. Persist failure → `toolError`, memory unchanged. REST `handleDeleteSkill` matches (500, not 204, on IO error). `skill_update` supports optional `version` and `new_name` (rename). `validSkillName` rejects path separators and `..`.
- **Skill-file IO confined via `os.Root`**: all skill writes/removes in `configmcp` go through an `os.Root` on `agentSkillsDir` (`openSkillRoot` + `writeSkillFileAtomic`) — OS-level refusal of `..` traversal and escaping symlinks, backstopping `ValidateSkillName` (the shared denylist enforced in the write helpers on every surface: REST, config MCP, external MCP). Requires Go ≥ 1.24.
- **Skill write hardening**: randomized temp name (`.skill-<rand>.tmp`, `O_EXCL`) + `Root.Rename`, so concurrent writers can't share/corrupt a temp file. Per-file cap `[skills] max_bytes` (1 MiB default; negative = unlimited), enforced inside `ApplySkill*` (`checkSkillPayloadSize`) — skill content is written verbatim and would otherwise be an unbounded-write DoS. REST/external-MCP read the cap via `skillMaxBytes()`; config MCP via `Deps.MaxSkillBytes`. Fuzz-tested (`FuzzApplySkillCreate_NeverEscapes`); concurrency `-race` tested.
- **OpenAPI spec is a gated generated artifact**: `internal/api/docs/swagger.json` is committed (`//go:embed`-ed, served at `/api/v1/openapi.json`) and must match handler annotations. `just openapi-check` regenerates into a throwaway dir and diffs — never mutates the tree, deliberately uncached. After touching an annotated handler: `just openapi` and commit. Gotcha: `swag` only reads an annotation block **directly attached** to its handler func — a blank line or intervening declaration silently drops the endpoint from the spec.
- **Shared validators** (`internal/config`): `ValidResourceName`, `ValidProviderType`, `IsProviderReferenced` — use for new CRUD endpoints.
- **`internal/agentctx`**: context-key package shared between `agent` and `configmcp` to avoid an import cycle; use it to thread agent identity through MCP handlers.
- **Date/week injection (two-point)**: the model never infers "today". (1) Scheduled messages render fire time via `scheduler.FormatScheduledText` — `[Scheduled: heartbeat | 2026-07-07T10:45:00+10:00 Australia/Sydney | 2026-W28]` — computed in the fire callback (both producers: `registerSchedules`/`buildScheduledMessage` in main.go and `configmcp.BuildScheduleJob`), `msg.Timestamp` from the same instant. (2) `buildSystemPrompt` appends `## Current Date` at **day resolution by design** (a clock time would bust the prompt cache every turn). Timezone precedence: agent `timezone` > `api.timezone` > UTC (`agentLocation` in main.go; engine knob `SetLocation`, hot-reloaded). Cron *evaluation* stays `api.timezone` and restart-only. Midnight-crossing runs can see header date ≠ prompt date — the fire-time header wins for dated keys.
- **Released-PR stamping** (`stamp-prs` in `release.yml`): comments the version on each released PR; resolves commits→PRs via the GitHub API (`commits/{sha}/pulls`), not `(#N)` subject parsing (mixed merge strategies break parsing).

CI/CD: golangci-lint, gosec, govulncheck, Grype, Gitleaks, GoReleaser, Homebrew tap, Docker (ghcr.io) with cosign + SLSA, GitHub Pages docs, released-PR stamping.

See `design/denkeeper-prd.md` for the full roadmap.
