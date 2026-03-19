# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

This project uses [just](https://github.com/casey/just) as the command runner and [mise](https://mise.jdx.dev) for tool versioning (Go 1.25.8).

```bash
just build                    # Build binary → ./foxbox
just serve                    # Run via go run (accepts optional config path)
just test                     # All tests with -race
just test-v                   # Verbose test output
just test-pkg internal/agent  # Single package
just lint                     # golangci-lint
just fmt                      # gofmt -w .
just check                    # fmt-check + vet + lint + test (CI equivalent)
```

## Architecture

Foxbox is a single-binary personal AI agent. Messages flow through a pipeline:

```
Adapter (Telegram) → Engine → LLM Router → Provider (OpenRouter)
                       ↕           ↕
                   MemoryStore  CostTracker
                   (SQLite)
```

**Engine** (`internal/agent/engine.go`) is the orchestrator. On each incoming message it:
1. Checks permissions via `security.PermissionEngine`
2. Loads/creates conversation from `MemoryStore` (keyed as `"adapter:externalID"`)
3. Builds message history with system prompt
4. Calls `Router.Complete()` which checks budget, delegates to a `Provider`, and records cost
5. Stores the response and sends it back through the originating adapter

**Three key interfaces** define the extension points:

- `adapter.Adapter` — platform integrations (Telegram implemented; add new ones here)
- `llm.Provider` — LLM backends (OpenRouter implemented; add new ones under `internal/llm/`)
- `agent.MemoryStore` — conversation persistence (SQLite implemented)

**Wiring** happens in `cmd/foxbox/main.go` — config drives everything. All behavior should be configurable via TOML, not hardcoded.

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

Cron matching uses bitsets for O(1) field checks. The scheduler is not yet wired into the engine — it runs independently.

## Current State (Phase 1)

- Only the "supervised" permission tier exists (hardcoded allowlist: chat, read_memory, write_memory).
- Only OpenRouter as an LLM provider, Telegram as an adapter.
- Skills, tools, plugins, multi-agent routing, and the web dashboard are planned for later phases.
- See the product requirements document for the full roadmap.
