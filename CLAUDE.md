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
just build-ui                 # Build web UI (required before go build/test)
just build-full               # Build web then binary
just web-dev                  # Vite dev server with hot-reload
just test-integration         # E2E integration tests
```

## Architecture

Denkeeper is a single-binary personal AI agent with multi-agent routing. Messages flow through:

```
Adapter (Telegram/Discord) → Dispatcher → Engine (per agent) → LLM Router → Provider (Anthropic/OpenRouter/OpenAI/Ollama)
                                               ↕                    ↕
                                           MemoryStore          CostTracker
                                           (SQLite)              + Pricing Registry
```

**Dispatcher** (`internal/agent/dispatcher.go`) routes messages to the correct Engine based on adapter bindings (`"telegram"` wildcard or `"telegram:12345"` specific). Falls back to the `"default"` agent. Handles `tool_approval` ChatEvents by sending inline keyboard approval messages.

**Engine** (`internal/agent/engine.go`) is the per-agent orchestrator. Pipeline: check permissions → get/create conversation → store user message → load history → build system prompt (persona + skills) → call `Router.Complete()` → tool-call loop (with supervised approval if applicable) → emit usage event → extract memory update → store assistant message → return text.

**Three key interfaces**:
- `adapter.Adapter` — platform integrations (Telegram, Discord)
- `llm.Provider` — LLM backends (Anthropic, OpenRouter, OpenAI, Ollama)
- `agent.MemoryStore` — conversation persistence (SQLite)

**Multi-agent config**: `[[agents]]` in TOML. Each agent has `name`, `persona_dir`, `adapters`, `llm_model`, `session_tier`. If no `[[agents]]` section exists, a single `"default"` agent is synthesized.

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

## Permission Tiers & Approval Workflows

Three tiers: `autonomous` (all actions), `supervised` (chat + tools with approval), `restricted` (chat + read-only tools).

`internal/approval/` manages requests requiring human sign-off. Flow: Engine submits to Manager → Manager persists + registers closure → Engine attaches Approve/Deny inline keyboard → user clicks → callback handler resolves → closure invoked.

Seven action kinds: `user_update`, `soul_update`, `create_skill`, `modify_schedule`, `install_tool`, `browser_profile`, `tool_call`.

**Supervised tool call approval**: When `permission_tier = "supervised"`, each MCP tool call is submitted for approval before execution. Engine first checks `Manager.ShouldAutoApprove()` — if a matching rule exists, the tool executes immediately and a `tool_approval` ChatEvent with `approval_status: "auto_approved"` is emitted. Otherwise Engine blocks on `Manager.WaitForResolution(ctx, id)`. Dispatcher intercepts pending `"tool_approval"` ChatEvents and sends inline keyboard messages with four buttons: Approve, Deny, Auto (session), Auto (always). Denied tool calls feed "Tool call was denied by the operator." to the LLM.

**Auto-approve rules**: Two scopes — `session` (in-memory, conversation-scoped, cleared on restart) and `permanent` (persisted in SQLite, agent-scoped). `Manager.ShouldAutoApprove()` checks session rules first, then permanent rules. Future config-based rules (`ScopeConfig`) can be added as a third check. Rules are created from Telegram inline buttons (`:approve_session`, `:approve_always` callback suffixes), from the web UI chat (Always Approve button), or via the REST API. `approval.ExtractToolName()` parses the tool name from the approval summary to key rules.

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

**Security**: SSRF protection, header injection prevention, env var denylist, URL/arg redaction in API responses.

## External REST API

`internal/api/` — HTTP API server (enabled by default). Auth via Bearer token (API keys) or session cookies (password/OIDC).

Key endpoints (all require auth unless noted):
- `GET /api/v1/health` (no auth)
- `POST /api/v1/chat` (scope `chat`) — JSON or SSE streaming
- `GET /api/v1/models` (scope `agents:read`) — available LLM models from all providers
- `GET/POST/DELETE /api/v1/approvals/...` — approval CRUD; `POST /approve` accepts `?auto_approve=session|permanent` to simultaneously create an auto-approve rule
- `GET/POST/DELETE /api/v1/auto-approve` (scope `approvals:read/write`) — auto-approve rule CRUD; `GET` accepts `?agent=` filter
- `GET/POST/PATCH/DELETE /api/v1/schedules/...` — schedule CRUD
- `GET/POST/PUT/DELETE /api/v1/skills/...` — skill CRUD
- `GET/PUT /api/v1/agents/{name}/persona/{section}` — persona sections
- `GET/DELETE /api/v1/kv/...` — KV store
- `GET/POST/DELETE /api/v1/tools/...` — tool/plugin CRUD
- `GET /api/v1/tools/{name}/health` (scope `tools:read`) — server health status
- `POST /api/v1/tools/{name}/restart` (scope `tools:write`) — manually restart a tool server
- `PATCH /api/v1/agents/{name}` — agent config mutation

SSE chat events: `thinking`, `tool_start`, `tool_end`, `tool_approval`, `usage`, `content`, `done`.

## Web Dashboard

`internal/web/` embeds a Svelte SPA compiled to `web/dist/` via `//go:embed dist`.

13 pages: Login, Overview, Chat, Approvals, Sessions, Schedules, Skills, Tools, Browser, KV Store, Costs, Agents, API Keys.

## UI/UX Standards

Every user-facing feature must include thoughtful UX treatment.

**Web (Svelte)**: Loading spinners for async ops, empty states with CTAs, inline error messages, confirmation for destructive actions, success feedback, disabled buttons while in-flight, responsive (≥ 320px), use existing CSS variables (`--accent`, `--surface`, `--border`, `--text-muted`, `--danger`).

**CLI (Cobra)**: Progress feedback for >500ms ops, `tabwriter` for tables, actionable errors (what failed + next step), non-zero exit codes via `RunE`.

**Adapters**: Typing indicators before LLM calls, platform-native formatting, inline keyboards for approvals.

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

## Current State

Phase 8 (cost accuracy) complete. All core systems implemented: multi-agent routing, 4 LLM providers, Telegram/Discord adapters, MCP tools with health monitoring, plugin system (subprocess + Docker + K8s), approval workflows (including supervised tool calls), KV store, browser automation, web search/fetch, pricing registry, OAuth2/OIDC auth, OTel observability, web dashboard (13 pages).

CI/CD: golangci-lint, gosec, govulncheck, Grype, Gitleaks, GoReleaser, Homebrew tap, Docker (ghcr.io) with cosign + SLSA, GitHub Pages docs.

See `design/denkeeper-prd.md` for the full roadmap.
