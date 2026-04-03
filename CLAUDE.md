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
- **Config validation**: Three-phase pattern in `config.go` — parse TOML → apply defaults (including env overrides) → validate.
- **Env var overrides**: `applyEnvOverrides()` in `config.go` reads an explicit allowlist of `DENKEEPER_*` env vars (secrets + key config fields). Runs after TOML defaults but before validation. Config path: `DENKEEPER_CONFIG` env var in `main.go`.

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

Cron matching uses bitsets for O(1) field checks. The scheduler dispatches messages to agents via `Dispatcher.Dispatch(ctx, agentName, msg)`. Schedules have an `agent` field (defaults to `"default"`). Per-entry context cancellation supports `Unregister(name)` for runtime schedule removal and `GetEntry(name)` for lookups.

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
- **Schedule CRUD**: `POST /api/v1/schedules` (scope `schedules:write`) — create schedule with name, expression, channel, skill, session_mode, session_tier, agent, tags, enabled. `PATCH /api/v1/schedules/{name}` (scope `schedules:write`) — partial update. `DELETE /api/v1/schedules/{name}` (scope `schedules:write`) — unregister and remove from config. All persist to TOML config atomically.
- **Skill CRUD**: `GET /api/v1/skills/{agent}/{name}` (scope `skills:read`) — full skill including body. `POST /api/v1/skills/{agent}` (scope `skills:write`) — create skill file and register in-memory. `PUT /api/v1/skills/{agent}/{name}` (scope `skills:write`) — update with merge. `DELETE /api/v1/skills/{agent}/{name}` (scope `skills:write`) — remove from memory and delete file.
- **Persona read/write**: `GET /api/v1/agents/{name}/persona/{section}` (scope `agents:read`) — returns `{section, content, editable}` for soul/user/memory. `PUT /api/v1/agents/{name}/persona/{section}` (scope `agents:write`) — writes section content. Path traversal protected: section name validated against allowlist.
- **KV read/delete**: `GET /api/v1/kv/{agent}` (scope `kv:read`) — list keys with optional `?prefix=` filter. `GET /api/v1/kv/{agent}/{key...}` (scope `kv:read`) — get single key (wildcard path supports keys with slashes). `DELETE /api/v1/kv/{agent}/{key...}` (scope `kv:write`) — delete key.

## Approval Workflows

`internal/approval/` manages supervised-tier action requests that require human sign-off before execution.

- **Manager** (`approval.Manager`) — submits requests, resolves them, runs TTL expiry.
- **Store** (`approval.SQLiteStore`) — persists requests; shares the WAL SQLite database with the memory store.
- **Registry** (`approval.Registry`) — holds in-memory action closures keyed by approval ID; cleared on restart (stale pending rows are expired at startup via `ExpirePending`).
- **Handler** (`approval.NewCallbackHandler`) — implements `adapter.CallbackResolver`; maps Telegram inline button callbacks (`"appr:{id}:approve"` / `"appr:{id}:deny"`) to resolution logic and confirmation strings.

Flow: Engine produces a directive → supervised tier submits to Manager → Manager persists row + registers closure → Engine attaches Approve/Deny inline keyboard buttons to the outgoing message → user clicks → Telegram adapter routes callback to `Handler.Resolve` → Manager resolves + invokes closure → original message keyboard is cleared.

Five action kinds: `user_update`, `create_skill`, `modify_schedule`, `install_tool`, `browser_profile`. Default TTL: 24 h (background worker ticks hourly).

## Config MCP Server

`internal/configmcp/` provides a per-agent in-process MCP server that lets the LLM modify its own configuration at runtime (supervised or autonomous tier).

Available MCP tools: `list_skills`, `create_skill`, `skill_get`, `skill_update`, `list_schedules`, `add_schedule`, `schedule_update`, `get_permission_tier`, `tool_list`/`tool_add`/`tool_remove`, `plugin_list`/`plugin_add`/`plugin_remove`, `kv_get`/`kv_set`/`kv_delete`/`kv_list`/`kv_set_nx`, `browser_profile_list`/`browser_profile_info`/`browser_profile_clear`/`browser_profile_delete`, `set_fallback`, `get_cost_summary`. `skill_get` is read-only (all tiers). In supervised mode mutation tools submit to the approval Manager rather than acting directly. `browser_profile_delete` always requires approval regardless of tier.

## Web MCP Server

`internal/webmcp/` provides a per-agent in-process MCP server for web search and URL fetching. Follows the same pattern as `configmcp`: no subprocess, runs in-process via `mcp.NewInMemoryTransports`.

- **Two MCP tools**: `web_search` (query the web) and `web_fetch` (fetch URL content as Markdown with pagination).
- **Search providers** (`internal/websearch/`): DuckDuckGo (default, no API key) and Tavily (premium, requires API key). Extensible `Provider` interface for future backends.
- **URL fetching** (`internal/webfetch/`): Go HTTP client with built-in HTML-to-Markdown conversion (`html-to-markdown/v2`). Supports configurable size limits, timeouts, and optional robots.txt/agents.txt compliance. Optional Jina Reader fallback for JS-heavy pages via `ChainFetcher`.
- **Permission-aware**: restricted tier is denied. Both tools check `PermissionTier()`.
- **Pagination**: `web_fetch` truncates content to 8000 chars with `has_more` + `total_length` fields; callers use `start_index` for subsequent pages.
- **Config**: `[web]` section — enabled by default (set `enabled = false` to disable). `[web.search]` (provider, api_key, max_results) and `[web.fetch]` (timeout, max_size_bytes, user_agent, respect_robots_txt, respect_agents_txt, jina.enabled).
- **Env override**: `DENKEEPER_SEARCH_API_KEY` → `web.search.api_key`.

## Browser Automation

Browser automation uses the official Playwright MCP server (`@playwright/mcp`) running in a hardened Docker container. Auto-registered when `[browser] enabled = true`.

- **Config**: `[browser]` with `enabled`, `image` (default `ghcr.io/temikus/denkeeper-browser:latest`), `memory_limit` (default "512m"), `cpu_limit` (default "1"), `profile_dir` (default "data/browser-profiles"), `session_ttl` (default "10m"), `max_pages` (default 5).
- **URL allowlist**: `[browser.url_allowlist]` with `domains` list. Empty = unrestricted. Supports wildcards (`*.example.com`). Per-agent override via `browser_url_allowlist` in `[[agents]]`. Always blocks link-local (169.254.x.x), loopback, and cloud metadata endpoints. Domain matching logic in `internal/browser/allowlist.go`.
- **Persistent profiles**: per-agent profile directories under `~/.denkeeper/data/browser-profiles/<agent>/`. Auto-created with 0o700 permissions. Bind-mounted into the container at `/data/profile`. Stores cookies, localStorage, and session data across browser invocations.
- **Sandbox**: reuses the shared `sandbox.Runtime` (Docker or Kubernetes). Network policy = egress (outbound HTTP required for browsing). Container runs with `--read-only`, tmpfs at `/tmp` (64MB), and `/dev/shm` (64MB) for Chromium.
- **Hardened image** (`ghcr.io/temikus/denkeeper-browser`): lives in a separate repo (`Temikus/denkeeper-browser`), users can plug any custom MCP-compliant browser image.
- **Orchestrator skill**: `agents/default/skills/browser-orchestrator.md` — always-active skill teaching agents multi-step browser workflow patterns, with guidance for both vision and non-vision LLMs.

## Agent KV Store

`internal/kv/` provides per-agent key-value storage with optional TTL, exposed via Config MCP tools.

- **SQLite-backed**: shares the same WAL database as conversations and approvals.
- **Per-agent scoped**: agents cannot access each other's keys (enforced at store level).
- **TTL-native**: keys can have an expiry; expired keys are invisible to reads and lazily cleaned up by a background worker.
- **Permission-aware**: reads (`kv_get`, `kv_list`) allowed for all tiers; writes (`kv_set`, `kv_delete`, `kv_set_nx`) denied for restricted tier.
- **Atomic SetNX**: `kv_set_nx` is the primitive for distributed locks — set-if-not-exists with optional TTL.
- **Config**: `[kv]` section with `max_keys_per_agent` (default 1000), `max_value_bytes` (default 64KB), `cleanup_interval` (default "1h").
- See `design/kv-store.md` for the full design and use-case examples.

## Plugin System

`internal/plugin/` provides plugins with two execution strategies and Ed25519 signature verification.

- **Subprocess** (`type = "subprocess"`): trusted plugins run as child processes with direct MCP stdio.
- **Docker** (`type = "docker"`): sandboxed plugins run via the `sandbox.Runtime` interface. The Docker backend uses `docker run -i --rm` with `--cap-drop ALL`, `--read-only`, `--security-opt no-new-privileges`, and `--network none` (default). Configurable `memory_limit`, `cpu_limit`, `network`, and `volumes`.
- **Sandbox runtime**: `internal/sandbox/` provides a pluggable `Runtime` interface for sandboxed plugin execution. Backends: `DockerRuntime` (standalone, default) and `KubernetesRuntime` (K8s-native). Config: `[sandbox] runtime = "docker"` or `"kubernetes"`. See below.
- **Signature verification**: `[security]` config section with `trusted_keys` (PEM public key paths) and `allow_unsigned` (default true). Ed25519 signatures are checked for subprocess plugin binaries during Load. Library functions in `internal/security/signing.go`: `GenerateKeyPair`, `Sign`, `Verify`, `SignFile`, `VerifyFile`, `LoadTrustedKeys`, PEM marshaling/parsing.
- Plugins declare capabilities in TOML config (e.g. `capabilities = ["tools"]`).
- The Manager validates, optionally verifies signatures, then spawns processes via the sandbox runtime and wires them into the engine via the shared `tool.Manager`.

## Sandbox Runtime

`internal/sandbox/` provides the pluggable sandbox runtime interface for executing MCP server plugins in isolated environments.

- **Interface**: `Runtime` with `Spawn(ctx, name, opts)`, `Stop(ctx, name)`, `Close()`. `Spawn` returns a `Process` (command + args) whose stdin/stdout carry MCP JSON-RPC.
- **SpawnOpts**: `Image`, `Command`, `Args`, `Env`, `MemoryLimit`, `CPULimit`, `Network`, `Volumes`, `Tmpfs` (Docker `--tmpfs` / K8s emptyDir Memory), `ShmSize` (Docker `--shm-size` / K8s `/dev/shm` emptyDir Memory with sizeLimit).
- **DockerRuntime**: builds `docker run -i --rm` commands. Default for standalone installs.
- **KubernetesRuntime**: creates ephemeral Pods in a dedicated namespace (`denkeeper-sandboxes` by default). Uses `kubectl exec -i` for MCP stdio. Features:
  - Init container with `CAP_NET_ADMIN` sets iptables rules for network isolation (none/egress/full).
  - Main container drops ALL capabilities, read-only root FS, runAsNonRoot, no privilege escalation, seccomp RuntimeDefault.
  - Pod Security Admission: enforce=baseline (allows init container), warn/audit=restricted.
  - Optional RuntimeClassName (gVisor, Kata) via `[sandbox.kubernetes] runtime_class`.
  - Supports in-cluster (ServiceAccount) and out-of-cluster (kubeconfig) auth.
  - Crash recovery: deletes stale pods from previous runs before recreating.
  - Deterministic pod names from plugin name (DNS-1123 compliant).
  - AutomountServiceAccountToken disabled.
  - Resource limits set as requests=limits for Guaranteed QoS.
- **Config**: `[sandbox]` section with `runtime` ("docker" or "kubernetes") and `[sandbox.kubernetes]` with `namespace`, `kubeconfig`, `runtime_class`.

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
- 12 pages: Login, Overview, Chat, Approvals, Sessions, Schedules, Skills, Tools, Browser, KV Store, Agents, API Keys.
- Agent detail page shows persona directory, loaded sections (soul/user/memory), and MCP tool names.
- **CI requirement**: The web UI must be built (`npm ci && npm run build` in `web/`) before any Go step that embeds it, including `go build`, `go test`, and `govulncheck`. CI handles this via a dedicated `build-ui` job that builds once and shares the result as a GitHub Actions artifact to all downstream jobs (lint, test, vuln, build matrix). Go module and build caches are also configured.
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

## OpenAI-Compatible LLM Provider

`internal/llm/openai/` implements `llm.Provider` for the OpenAI Chat Completions API.

- Raw HTTP implementation (same pattern as OpenRouter/Ollama — OpenAI wire format).
- Compatible with any OpenAI-format endpoint: OpenAI direct, Azure OpenAI, vLLM, LiteLLM.
- Auth: `Authorization: Bearer {key}` + optional `OpenAI-Organization` header.
- Config: `[llm.openai]` with `api_key`, optional `base_url` (default `https://api.openai.com/v1`), optional `organization`.
- Env overrides: `DENKEEPER_LLM_OPENAI_API_KEY`, `DENKEEPER_LLM_OPENAI_BASE_URL`.
- Validation: `needsOpenAI()` checks both default provider and fallback rules (same pattern as `needsOpenRouter`).

## Session Management CLI

`cmd/denkeeper/sessions.go` provides offline session inspection and maintenance.

- `denkeeper sessions list` — tabular view of all sessions with adapter, message count, cost, creation date.
- `denkeeper sessions show <id>` — message thread with timestamps and content preview.
- `denkeeper sessions delete <id> [--yes]` — delete with confirmation prompt.
- `denkeeper sessions export <id> [--format text|json]` — full transcript to stdout.
- `denkeeper sessions prune --older-than <duration> [--yes]` — bulk delete old sessions.
- Uses `resolveDBPath()` (same as `keys` command) to locate the SQLite database.
- Three concrete methods on `SQLiteMemoryStore`: `ConversationCost`, `CountConversationsBefore`, `PruneConversations` (not on the interface).

## Tool Lifecycle Management

`internal/tool/lifecycle.go` provides runtime add/remove of MCP tools and plugins without restarting the process.

- **LifecycleManager** wraps `tool.Manager` and coordinates: validation, MCP server spawn/stop, TOML config persistence (atomic read-modify-write via `config_writer.go`).
- **Config is source of truth**: Changes are persisted to `denkeeper.toml` immediately. On restart, the same state loads.
- **Max tools limit**: `max_tools` in config (default 50, combined tools + plugins).
- **tool.Manager** gained: `UnregisterServer(name)`, `ServerNames()`, `ServerInfo(name)` with `sync.RWMutex` for thread safety.

### Config MCP tools (agent-initiated)

Six new tools in `configmcp`: `tool_list`, `tool_add`, `tool_remove`, `plugin_list`, `plugin_add`, `plugin_remove`. Follow the `applyOrSubmit` pattern:
- `autonomous` → immediate execution
- `supervised` → approval required (ActionKindInstallTool)
- `restricted` → denied

### REST API endpoints (operator/programmatic)

| Endpoint | Scope |
|----------|-------|
| `GET /api/v1/tools` | `tools:read` |
| `GET /api/v1/tools/{name}` | `tools:read` |
| `POST /api/v1/tools` | `tools:write` |
| `DELETE /api/v1/tools/{name}` | `tools:write` |
| `GET /api/v1/plugins` | `tools:read` |
| `GET /api/v1/plugins/{name}` | `tools:read` |
| `POST /api/v1/plugins` | `tools:write` |
| `DELETE /api/v1/plugins/{name}` | `tools:write` |

### Web dashboard

Tools page (`/dashboard/tools`) with MCP tools and plugins tables, add/remove dialogs, status indicators. 10th page in the SPA.

## Current State (Phase 6 browser automation complete)

- Multi-agent routing: Dispatcher routes messages to named agents via adapter bindings. Each agent has its own persona, skills, LLM model, and permission tier.
- Three permission tiers: autonomous, supervised, restricted (configurable via TOML, per-agent or global).
- LLM providers: OpenRouter (production), Ollama (local), Anthropic (direct), OpenAI (direct and compatible endpoints — Azure OpenAI, vLLM, LiteLLM). Telegram and Discord adapters.
- Persona system (load/write), skill system (trigger-based filtering, per-agent merge), scheduler (per-schedule agent targeting, session modes), fallback strategies, cost tracking, voice (STT/TTS) are all implemented.
- MCP tool support: engine spawns MCP stdio servers at startup, discovers tools, passes them to the LLM, executes tool calls in an agentic loop (serial).
- Plugin system: subprocess plugins and sandboxed plugins with capability declarations. Sandboxed plugins use the `sandbox.Runtime` interface with two backends: DockerRuntime (default, `docker run -i --rm`) and KubernetesRuntime (creates ephemeral Pods with init-container network isolation, PSA labels, and optional gVisor/Kata RuntimeClass). Both hardened with dropped capabilities, read-only root FS, and network isolation.
- Plugin signing: Ed25519 signature verification for subprocess plugin binaries. Configurable via `[security]` with `trusted_keys` (PEM public key files) and `allow_unsigned` (default true). Includes `SignFile`/`VerifyFile` library, PEM key marshaling, and `LoadTrustedKeys` for key management.
- External REST API: auth, rate limiting, CORS, TLS, health, read-only data endpoints, chat endpoint with SSE streaming, session deletion, approval CRUD, API key CRUD (runtime key management), tool/plugin CRUD (`tools:read`/`tools:write` scopes), browser profile/session/config endpoints (`browser:read`/`browser:write` scopes), schedule CRUD (`schedules:write` — create/update/delete with TOML persistence), skill CRUD (`skills:read`/`skills:write` — get/create/update/delete with file management), persona read/write (`agents:read`/`agents:write` — get/update persona sections with path traversal protection), KV store read/delete (`kv:read`/`kv:write` — list/get/delete agent-scoped keys with prefix filtering). Agent detail endpoint exposes `tool_names`, `persona_dir`, and `persona_sections`.
- Approval workflows: TTL-based supervised approvals for user_update / create_skill / modify_schedule / install_tool directives, with Telegram inline keyboard buttons (Approve/Deny) and keyboard auto-removal on resolution.
- Config MCP server: per-agent in-process MCP tools for skill listing/creation/get/update, schedule listing/addition/update, permission tier inspection, tool/plugin management (add/remove/list), KV store operations (get/set/delete/list/set_nx), and browser profile management (list/info/clear/delete).
- Tool lifecycle management: runtime add/remove of MCP tools and plugins via LifecycleManager, with atomic TOML config persistence, max_tools limit, and thread-safe tool.Manager.
- Web dashboard: embedded Svelte SPA (12 pages: Login, Overview, Chat, Approvals, Sessions, Schedules, Skills, Tools, Browser, KV Store, Agents, API Keys) served via the API server. Chat page supports SSE streaming. API Keys page supports full CRUD. Tools page supports add/remove of MCP tools and plugins. Schedules page supports full CRUD (add/edit/delete with modal dialogs). Skills page supports full CRUD (add/edit/delete with agent selection, body textarea, async skill content fetch). Browser page shows active sessions, profile management (delete with confirmation), and read-only configuration summary. KV Store page: agent-scoped key browser with prefix filter, expandable values, and delete with confirmation. Overview page: cost breakdown table with sortable per-session costs. Agent detail page: inline persona section viewer/editor (read/write for editable sections).
- CI/CD: golangci-lint (with gosec SAST), govulncheck, cosign signing, SBOM generation, GoReleaser with .deb/.rpm/.tar.gz, Homebrew tap, Docker (ghcr.io) with SLSA provenance. Web UI is built in CI before any Go step.
- Security scanning: dedicated `security.yml` workflow with gosec (SAST, SARIF → GitHub Security tab), Gitleaks (secret detection), and Grype (Anchore, filesystem vulnerability scan). Grype container image scan in `release.yml` before cosign signing. Dependabot for gomod/npm/docker/github-actions weekly updates.
- Documentation website: Hugo + Doks (Thulite) under `/website`, with getting-started guides, concept docs, and reference pages. GitHub Pages CI via `deploy-website.yml`. One-liner install script at `website/static/install.sh`.
- systemd service: hardened unit file with security directives, pre/post install scripts, wired into GoReleaser nfpm packaging.
- CLI plugin signing: `denkeeper plugin keygen <name>` (generate Ed25519 key pair), `denkeeper plugin sign <binary> -k <key>` (create detached `.sig`), `denkeeper plugin verify <binary> -k <pubkey>` (verify signature). Wraps `internal/security/signing.go`.
- CLI session management: `denkeeper sessions [list|show|delete|export|prune]` for offline inspection, export, and maintenance of conversation sessions. Three concrete methods on `SQLiteMemoryStore` (`ConversationCost`, `CountConversationsBefore`, `PruneConversations`).
- Agent KV store: per-agent key-value storage with TTL (`internal/kv/`), exposed as five Config MCP tools (`kv_get`/`kv_set`/`kv_delete`/`kv_list`/`kv_set_nx`). SQLite-backed (shared WAL DB), background cleanup worker, configurable limits (`[kv]` section).
- Config MCP tools: `schedule_update` (partial updates with unregister/re-register), `set_fallback` (replace LLM router fallback rules at runtime), `get_cost_summary` (read-only cost tracker snapshot). All respect permission tiers.
- Deployment improvements: env var overrides (`DENKEEPER_*`) for secrets and key config fields, `DENKEEPER_CONFIG` for config path (also used as Dockerfile entrypoint default), Helm chart (`deploy/helm/denkeeper/`) with Ingress support, non-root Docker container (UID 65534), docker-compose with port mapping.
- Web search & fetch: in-process Web MCP server (`internal/webmcp/`) with `web_search` and `web_fetch` tools. Search providers: DuckDuckGo (default) and Tavily. Fetch: built-in HTML-to-Markdown, optional Jina Reader fallback, configurable robots.txt/agents.txt compliance. Config: `[web]` section with `DENKEEPER_SEARCH_API_KEY` env override.
- Browser automation: `[browser]` section with auto-registration as Docker-based MCP server via shared sandbox runtime. Image: `ghcr.io/temikus/denkeeper-browser:latest`. Tmpfs (`/tmp:size=64m`) and ShmSize (`64m`) for read-only container + Chromium compatibility. Per-agent persistent profile directories (`data/browser-profiles/`) bind-mounted at `/data/profile`. URL allowlist (`[browser.url_allowlist]`) with wildcard domain matching and link-local/metadata blocking (`internal/browser/allowlist.go`). Per-agent override via `browser_url_allowlist` in `[[agents]]`. Browser orchestrator skill (`agents/default/skills/browser-orchestrator.md`) for multi-step workflows. Profile management via `ProfileService` (`internal/browser/profile.go`) — shared between Config MCP tools and REST API, with cookie domain extraction from Chromium SQLite DB.
- Browser image: separate repo [`Temikus/denkeeper-browser`](https://github.com/Temikus/denkeeper-browser) — hardened Docker image wrapping `@playwright/mcp` with a custom MCP server that proxies all upstream Playwright tools and adds `browser_extract_text` (Mozilla Readability + Markdown conversion + form extraction) and `browser_extract_html` (CSS selector-based). Non-root user (UID 10001), multi-arch (amd64/arm64), cosign-signed with SLSA attestations.
- Screenshot-to-text: `browser_extract_text` MCP tool in the browser image provides readability-based DOM extraction for non-vision LLMs. Supports `mode` (readability/all/auto), `include_forms`, `selector` scoping, and `max_length` truncation. Returns Markdown-formatted content. Auto mode tries Readability first, falls back to all visible text for SPAs/dashboards.
- Cyclomatic complexity: gocyclo threshold lowered from 20 to 15. All non-test functions ≤ 15.
- Next: See PRD §19 for full roadmap. Browser automation (Phase 6) is complete.
- See `design/denkeeper-prd.md` for the full roadmap.
