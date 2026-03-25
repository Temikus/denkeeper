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
Adapter (Telegram) → Dispatcher → Engine (per agent) → LLM Router → Provider (OpenRouter)
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

- `adapter.Adapter` — platform integrations (Telegram implemented; add new ones here)
- `llm.Provider` — LLM backends (OpenRouter implemented; add new ones under `internal/llm/`)
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

## Current State (Phase 3 in progress)

- Multi-agent routing: Dispatcher routes messages to named agents via adapter bindings. Each agent has its own persona, skills, LLM model, and permission tier.
- Three permission tiers: autonomous, supervised, restricted (configurable via TOML, per-agent or global).
- OpenRouter as LLM provider, Telegram as adapter.
- Persona system (load/write), skill system (trigger-based filtering, per-agent merge), scheduler (per-schedule agent targeting, session modes), fallback strategies, cost tracking, voice (STT/TTS) are all implemented.
- MCP tool support: engine spawns MCP stdio servers at startup, discovers tools, passes them to the LLM, executes tool calls in an agentic loop (serial, no Docker sandboxing yet).
- External REST API: auth, rate limiting, CORS, TLS, health, read-only data endpoints, chat endpoint with SSE streaming, session deletion.
- Next: approval workflows, Config MCP server, plugin system, web dashboard.
- See `design/denkeeper-prd.md` for the full roadmap.
