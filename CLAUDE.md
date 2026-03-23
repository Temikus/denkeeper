# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

This project uses [just](https://github.com/casey/just) as the command runner and [mise](https://mise.jdx.dev) for tool versioning (Go 1.25.8).

```bash
just build                    # Build binary → ./denkeeper
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

**Engine** (`internal/agent/engine.go`) is the per-agent orchestrator. Each named agent gets its own Engine with its own persona, skills, permissions, and LLM router. On each message it:
1. Checks permissions via `security.PermissionEngine`
2. Loads/creates conversation from `MemoryStore` (namespaced as `"agentName:adapter:externalID"`)
3. Builds message history with system prompt (persona + trigger-matched skills)
4. Calls `Router.Complete()` which checks budget, delegates to a `Provider`, and records cost
5. Stores the response and sends it back via the `SendFunc` callback

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
- The `mockProvider` in `llm/router_test.go` supports both response and error injection; the one in `agent/engine_test.go` mirrors this pattern for engine-level tests.

## Scheduler

`internal/scheduler/` supports three expression formats:
- Named: `@daily`, `@hourly`, `@weekly`, etc.
- Interval: `@every 5m`, `@every 1h30m`
- Cron (5-field): `0 8 * * 1-5`

Cron matching uses bitsets for O(1) field checks. The scheduler dispatches messages to agents via `Dispatcher.Dispatch(ctx, agentName, msg)`. Schedules have an `agent` field (defaults to `"default"`).

## Current State (Phase 2 nearly complete)

- Multi-agent routing: Dispatcher routes messages to named agents via adapter bindings. Each agent has its own persona, skills, LLM model, and permission tier.
- Three permission tiers implemented: autonomous, supervised, restricted (configurable via TOML, per-agent or global).
- OpenRouter as LLM provider, Telegram as adapter.
- Persona system (load/write), skill system (with trigger-based filtering and per-agent merge), scheduler (with per-schedule agent targeting), fallback strategies, cost tracking, and voice (STT/TTS) are all implemented.
- MCP tool support: the engine spawns MCP stdio servers at startup, discovers tools, passes them to the LLM, and executes tool calls in an agentic loop (serial execution, no Docker sandboxing yet).
- **Remaining in Phase 2**: External REST API.
- Plugins, web dashboard, and additional adapters are planned for Phase 3+.
- See `design/denkeeper-prd.md` for the full roadmap.
