# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

This project uses [just](https://github.com/casey/just) as the command runner and [mise](https://mise.jdx.dev) for tool versioning (Go 1.25.8).

```bash
just build                    # Build binary → pkg/bin/denkeeper
just serve                    # Run via go run (accepts optional config path)
just test                     # All tests with -race
just test-v                   # Verbose test output
just test-pkg internal/agent  # Single package
just lint                     # golangci-lint
just fmt                      # gofmt -w .
just check                    # fmt-check + vet + lint + test (CI equivalent)
```

## Architecture

Denkeeper is a single-binary personal AI agent with multi-agent routing. Messages flow through:

```
Adapter (Telegram/Discord) → Dispatcher → Engine (per agent) → LLM Router → Provider (Anthropic/OpenRouter/Ollama)
                                               ↕                    ↕
                                           MemoryStore          CostTracker
                                           (SQLite)
```

**Dispatcher** (`internal/agent/dispatcher.go`) routes incoming messages to the correct Engine based on adapter bindings (`"telegram"` wildcard or `"telegram:12345"` specific). Falls back to the `"default"` agent.

**Engine** (`internal/agent/engine.go`) is the per-agent orchestrator. Each named agent gets its own Engine with its own persona, skills, permissions, and LLM router. Two public entry points:
- `Chat(ctx, msg) (string, error)` — runs the full pipeline and returns the response text. Used by the REST API and any caller that wants the reply directly.
- `HandleMessage(ctx, msg) error` — calls `Chat` then dispatches the response via the `SendFunc` callback. Used by the Dispatcher and Scheduler.

The pipeline steps are: check permissions → get/create conversation → store user message → load history → build system prompt (persona + trigger-matched skills) → call `Router.Complete()` (budget check + provider call + cost record) → optional tool-call loop → extract memory update → store assistant message → return text.

**Three key interfaces** define the extension points:

- `adapter.Adapter` — platform integrations (Telegram and Discord implemented; add new ones here)
- `llm.Provider` — LLM backends (Anthropic, OpenRouter, and Ollama implemented; add new ones under `internal/llm/`)
- `agent.MemoryStore` — conversation persistence (SQLite implemented)

**Multi-agent config**: `[[agents]]` in TOML. Each agent has `name`, `persona_dir`, `adapters` (bindings), `llm_model` (optional override), and `session_tier` (optional override). Backward compatible: if no `[[agents]]` section exists, a single `"default"` agent is synthesized from `[agent]`/`[session]`.

**Wiring** happens in `cmd/denkeeper/main.go` — config drives everything. All behavior should be configurable via TOML, not hardcoded.

## Conventions

- **Error wrapping**: Always `fmt.Errorf("context: %w", err)` — no naked error returns.
- **Structured logging**: `log/slog` everywhere, with contextual fields.
- **Context propagation**: All I/O functions accept `context.Context`.
- **Concurrency**: Channels for message passing between components; `sync.Mutex` for shared state (e.g., `CostTracker`).
- **Config validation**: Three-phase pattern in `config.go` — parse TOML → apply defaults → validate.

## Testing Patterns

- Hand-written mocks that satisfy interfaces — no codegen.
- In-memory SQLite (`:memory:`) for persistence tests via `NewInMemoryStore()`.
- Individual `TestName_Scenario` functions (not table-driven).
- Always run with `-race` flag.
- The `mockProvider` in `llm/router_test.go` supports both response and error injection; the one in `agent/engine_test.go` and `api/server_test.go` mirrors this pattern for engine-level and API-level tests respectively.

## Scheduler

`internal/scheduler/` supports three expression formats:
- Named: `@daily`, `@hourly`, `@weekly`, etc.
- Interval: `@every 5m`, `@every 1h30m`
- Cron (5-field): `0 8 * * 1-5`

Cron matching uses bitsets for O(1) field checks. The scheduler dispatches messages to agents via `Dispatcher.Dispatch(ctx, agentName, msg)`. Schedules have an `agent` field (defaults to `"default"`).

## External REST API

`internal/api/` provides the external HTTP API server, started when `[api] enabled = true` in config.

- **Auth**: Bearer token with scoped API keys (constant-time comparison).
- **Rate limiting**: Per-key token bucket (`api.rate_limit` requests/sec).
- **CORS**: Configurable allowed origins (`api.cors_origins`).
- **TLS**: Optional (`api.tls = true` with `cert_file`/`key_file`).
- **Health**: `GET /api/v1/health` (no auth required).
- Authenticated endpoints use `server.RequireScope(scope, handler)` middleware.
- **Chat**: `POST /api/v1/chat` (scope `chat`) — JSON body `{agent, session_id, message, user_id, user_name}`; returns `{session_id, response}`. Set `Accept: text/event-stream` for SSE (two events: `{"type":"content","text":"..."}` then `{"type":"done","session_id":"..."}`). `session_id` is auto-generated if omitted; pass the same value in subsequent requests to continue the conversation.
- **Session deletion**: `DELETE /api/v1/sessions/{id}` (scope `sessions:read`) — removes the conversation and all its messages (204, idempotent).
- **Approvals**: `GET /api/v1/approvals` / `GET /api/v1/approvals/{id}` (scope `approvals:read`) — list or fetch approval requests. `POST /api/v1/approvals/{id}/approve` / `.../deny` (scope `approvals:write`) — resolve programmatically.

## Approval Workflows

`internal/approval/` manages supervised-tier action requests that require human sign-off before execution.

- **Manager** (`approval.Manager`) — submits requests, resolves them, runs TTL expiry.
- **Store** (`approval.SQLiteStore`) — persists requests; shares the WAL SQLite database with the memory store.
- **Registry** (`approval.Registry`) — holds in-memory action closures keyed by approval ID; cleared on restart (stale pending rows are expired at startup via `ExpirePending`).
- **Handler** (`approval.NewCallbackHandler`) — implements `adapter.CallbackResolver`; maps Telegram inline button callbacks (`"appr:{id}:approve"` / `"appr:{id}:deny"`) to resolution logic and confirmation strings.

Flow: Engine produces a directive → supervised tier submits to Manager → Manager persists row + registers closure → Engine attaches Approve/Deny inline keyboard buttons to the outgoing message → user clicks → Telegram adapter routes callback to `Handler.Resolve` → Manager resolves + invokes closure → original message keyboard is cleared.

Three action kinds: `user_update`, `create_skill`, `modify_schedule`. Default TTL: 24 h (background worker ticks hourly).

## Config MCP Server

`internal/configmcp/` provides a per-agent in-process MCP server that lets the LLM modify its own configuration at runtime (supervised or autonomous tier).

Available MCP tools: `list_skills`, `create_skill`, `list_schedules`, `add_schedule`, `get_permission_tier`. In supervised mode these tools still submit to the approval Manager rather than acting directly.

## Plugin System

`internal/plugin/` provides plugins with two execution strategies and Ed25519 signature verification.

- **Subprocess** (`type = "subprocess"`): trusted plugins run as child processes with direct MCP stdio.
- **Docker** (`type = "docker"`): sandboxed plugins run in Docker/Podman containers via `docker run -i --rm`. Containers are hardened with `--cap-drop ALL`, `--read-only`, `--security-opt no-new-privileges`, and `--network none` (default). Configurable `memory_limit`, `cpu_limit`, `network`, and `volumes`.
- **Signature verification**: `[security]` config section with `trusted_keys` (PEM public key paths) and `allow_unsigned` (default true). Ed25519 signatures are checked for subprocess plugin binaries during Load. Library functions in `internal/security/signing.go`: `GenerateKeyPair`, `Sign`, `Verify`, `SignFile`, `VerifyFile`, `LoadTrustedKeys`, PEM marshaling/parsing.
- Plugins declare capabilities in TOML config (e.g. `capabilities = ["tools"]`).
- The Manager validates, optionally verifies signatures, then spawns processes and wires them into the engine via the shared `tool.Manager`.

## UI/UX Standards

Every user-facing feature — web dashboard pages, CLI output, and adapter messages — must include thoughtful UX treatment. "Works correctly" is necessary but not sufficient; features should feel polished.

**Web dashboard (Svelte)**
- Loading states: show a spinner or skeleton for every async operation; never leave the user staring at a blank area.
- Empty states: when a list is empty, show a helpful message and a clear call-to-action (e.g. "No schedules yet. Add one with `denkeeper keys create`.").
- Error states: display human-readable error messages in context (inline near the form, not just a console log). Include recovery guidance where possible.
- Optimistic updates: for destructive actions (delete, revoke), use a confirmation step or undo affordance.
- Feedback on actions: success toasts or inline confirmation after create/update/delete so the user knows something happened.
- Disabled states: buttons must visually reflect loading (`Creating…`, `Saving…`) and be disabled while in-flight to prevent double-submit.
- Responsive layout: pages must be usable at narrow viewport widths (≥ 320 px).
- Consistent style: use the existing CSS variables (`--accent`, `--surface`, `--border`, `--text-muted`, `--danger`) — do not introduce ad-hoc colors or inline styles.

**CLI (Cobra)**
- Progress feedback: for operations that may take >500 ms, print a status line before starting (e.g. `Opening key store…`).
- Structured output: use `tabwriter` for tabular data; align columns and include a header row.
- Actionable errors: error messages must say what failed *and* suggest the next step (e.g. `opening key store at /path: permission denied — check file permissions or use --config to specify an alternate path`).
- Exit codes: non-zero on all errors; `RunE` everywhere (never `Run` with `os.Exit`).

**Adapter messages (Telegram / Discord)**
- Typing indicators: always send a typing action before any LLM call so the user sees activity immediately.
- Structured formatting: use platform-native formatting (Markdown for Telegram, embeds for Discord) to distinguish code, headings, and inline values.
- Inline keyboards: for multi-step interactions (approvals, confirmations), prefer inline buttons over free-text prompts.

## Web Dashboard

`internal/web/` embeds a Svelte SPA compiled to `web/dist/` at build time via `//go:embed dist`.

- Served at the root path when `[api] enabled = true`.
- 9 pages: Login, Overview, Chat, Approvals, Sessions, Schedules, Skills, Agents, API Keys.
- Agent detail page shows persona directory, loaded sections (soul/user/memory), and MCP tool names.
- **CI requirement**: The web UI must be built (`npm ci && npm run build` in `web/`) before any Go step that embeds it, including `go build`, `go test`, and `govulncheck`. The CI workflows already handle this.
- Local dev: `just build-ui` (build once) or `just web-dev` (Vite dev server with hot-reload). `just build-full` builds web then binary in one step.

## Telegram UX

- **Typing indicator**: `sendChatAction` (ChatTyping) is sent in `Start()` after the allowlist check, before placing the message on the channel. Shows "typing…" for ~5 s in Telegram.
- **Command menu**: `setMyCommands` is called once in `New()` to register `/start` and `/help` in the Telegram command composer menu.

## Discord Adapter

`internal/adapter/discord/` implements `adapter.Adapter` via `bwmarrin/discordgo`.

- DM-first: supports both DMs and guild text channels.
- `ExternalID` in `IncomingMessage` is the Discord channel ID.
- `UserID` is the Discord user snowflake string; `UserName` is the username.
- Typing indicator: `session.ChannelTyping(channelID)` analogous to Telegram.
- Buttons (approval keyboards): rendered as Discord action-row components.
- Config: `[discord]` with `token` (bot token) and `allowed_users` (string snowflake IDs).
- Both Telegram and Discord adapters can run simultaneously; each agent's `adapters` binding list determines which it handles.
- `OutgoingMessage.Adapter` field routes responses back through the correct adapter.

## Anthropic Direct LLM Provider

`internal/llm/anthropic/` implements `llm.Provider` against the Anthropic Messages API.

- Raw HTTP implementation (consistent with OpenRouter/Ollama patterns).
- Maps `llm.Message` roles: `system` → top-level `system` field; `tool` → `user` role with `tool_result` block.
- Maps `tool_use` response blocks → `llm.ToolCall` with JSON-encoded arguments.
- `MaxTokens` defaults to 4096 if not set in the request.
- Config: `[llm.anthropic]` with `api_key` and optional `base_url` (for Bedrock/Vertex).
- When `default_provider = "anthropic"`, OpenRouter API key is not required.

## Current State (Phase 4 complete)

- Multi-agent routing: Dispatcher routes messages to named agents via adapter bindings. Each agent has its own persona, skills, LLM model, and permission tier.
- Three permission tiers: autonomous, supervised, restricted (configurable via TOML, per-agent or global).
- LLM providers: OpenRouter (production), Ollama (local), Anthropic (direct). Telegram and Discord adapters.
- Persona system (load/write), skill system (trigger-based filtering, per-agent merge), scheduler (per-schedule agent targeting, session modes), fallback strategies, cost tracking, voice (STT/TTS) are all implemented.
- MCP tool support: engine spawns MCP stdio servers at startup, discovers tools, passes them to the LLM, executes tool calls in an agentic loop (serial).
- Plugin system: subprocess plugins and Docker-sandboxed plugins with capability declarations. Docker plugins run via `docker run -i --rm` with `--cap-drop ALL`, `--read-only`, `--network none` by default. Configurable resource limits (`memory_limit`, `cpu_limit`), network mode, and bind mounts.
- Plugin signing: Ed25519 signature verification for subprocess plugin binaries. Configurable via `[security]` with `trusted_keys` (PEM public key files) and `allow_unsigned` (default true). Includes `SignFile`/`VerifyFile` library, PEM key marshaling, and `LoadTrustedKeys` for key management.
- External REST API: auth, rate limiting, CORS, TLS, health, read-only data endpoints, chat endpoint with SSE streaming, session deletion, approval CRUD, API key CRUD (runtime key management). Agent detail endpoint exposes `tool_names`, `persona_dir`, and `persona_sections`.
- Approval workflows: TTL-based supervised approvals for user_update / create_skill / modify_schedule directives, with Telegram inline keyboard buttons (Approve/Deny) and keyboard auto-removal on resolution.
- Config MCP server: per-agent in-process MCP tools for skill listing/creation, schedule listing/addition, and permission tier inspection.
- Web dashboard: embedded Svelte SPA (9 pages: Login, Overview, Chat, Approvals, Sessions, Schedules, Skills, Agents, API Keys) served via the API server. Chat page supports SSE streaming. API Keys page supports full CRUD.
- CI/CD: golangci-lint, govulncheck, cosign signing, SBOM generation, GoReleaser with .deb/.rpm/.tar.gz, Homebrew tap, Docker (ghcr.io) with SLSA provenance. Web UI is built in CI before any Go step.
- Documentation website: Hugo + Doks (Thulite) under `/website`, with getting-started guides, concept docs, and reference pages. GitHub Pages CI via `deploy-website.yml`. One-liner install script at `website/static/install.sh`.
- systemd service: hardened unit file with security directives, pre/post install scripts, wired into GoReleaser nfpm packaging.
- Next: CLI commands for plugin signing (`denkeeper plugin keygen/sign/verify`), documentation website domain setup, Kubernetes sandbox runtime backend.
- See `design/denkeeper-prd.md` for the full roadmap.
