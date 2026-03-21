# Foxbox

<p align="center">
  <a href="https://github.com/Temikus/foxbox/actions/workflows/ci.yml"><img src="https://github.com/Temikus/foxbox/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/Temikus/foxbox/actions/workflows/release.yml"><img src="https://github.com/Temikus/foxbox/actions/workflows/release.yml/badge.svg" alt="Release"></a>
  <a href="https://github.com/Temikus/foxbox/releases/latest"><img src="https://img.shields.io/github/v/release/Temikus/foxbox" alt="Latest Release"></a>
  <a href="https://github.com/Temikus/foxbox/pkgs/container/foxbox"><img src="https://ghcr-badge.egpl.dev/temikus/foxbox/latest_tag?trim=major&label=ghcr.io" alt="Docker Image"></a>
  <a href="https://goreportcard.com/report/github.com/Temikus/foxbox"><img src="https://goreportcard.com/badge/github.com/Temikus/foxbox" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/Temikus/foxbox" alt="License"></a>
</p>

A security-first personal AI agent that lives in your chat. Built in Go as a single binary, designed to run anywhere from a Raspberry Pi to a cloud VM.

Foxbox connects to your Telegram (more adapters planned), routes messages through LLM providers via [OpenRouter](https://openrouter.ai), and remembers conversations across sessions using a local SQLite database. It enforces per-session cost budgets, user allowlists, and a tiered permission system — so you stay in control of what it can do and how much it can spend.

## Features

- **Single binary** — no runtime dependencies, no containers required
- **Telegram integration** — chat with your agent from your phone
- **User allowlist** — only approved Telegram user IDs can interact
- **LLM routing** — pluggable provider interface, currently backed by OpenRouter (access to Claude, GPT, Llama, and hundreds more)
- **Cost tracking** — per-session budgets with automatic cutoff
- **Conversation memory** — SQLite-backed, persistent across restarts
- **Scheduler** — cron expressions, named intervals, and `@daily`/`@hourly` shorthand for recurring tasks
- **Permission tiers** — supervised mode by default, extensible for future autonomy levels
- **Personality** — ships with a [`SOUL.md`](agents/default/SOUL.md) that gives the agent character (editable)

## Architecture

```
Telegram ──► Adapter ──► Engine ──► LLM Router ──► OpenRouter API
                           │
                       Memory Store (SQLite)
                           │
                       Scheduler
                           │
                       Permissions
```

The core loop: adapters receive messages, the engine checks permissions, loads conversation history, calls the LLM, stores the response, and sends it back through the adapter.

## Quick start

### Prerequisites

- Go 1.25+ (managed via [mise](https://mise.jdx.dev) — see `mise.toml`)
- A Telegram bot token (from [@BotFather](https://t.me/BotFather))
- An OpenRouter API key (from [openrouter.ai/keys](https://openrouter.ai/keys))
- Your Telegram user ID (from [@userinfobot](https://t.me/userinfobot))

### Setup

```bash
# Clone
git clone https://github.com/Temikus/foxbox.git
cd foxbox

# Copy and edit the config
mkdir -p ~/.foxbox
cp foxbox.toml.example ~/.foxbox/foxbox.toml
# Fill in your token, API key, and user ID
$EDITOR ~/.foxbox/foxbox.toml

# Build and run
just build
./foxbox serve
```

Or run directly without building:

```bash
just serve
```

### Configuration

Foxbox uses a single TOML file (default `~/.foxbox/foxbox.toml`). See [`foxbox.toml.example`](foxbox.toml.example) for all options.

Key sections:

| Section | Purpose |
|---------|---------|
| `[telegram]` | Bot token and allowed user IDs |
| `[llm]` | Default provider, model, and per-session cost cap |
| `[llm.openrouter]` | OpenRouter API key |
| `[memory]` | SQLite database path |
| `[log]` | Log level and format |
| `[[schedules]]` | Recurring tasks (cron, interval, or named schedules) |

## Development

[just](https://github.com/casey/just) is used as the command runner. Run `just` to see all available recipes:

```
just build           # Build the foxbox binary
just serve           # Start the agent (just serve ./path/to/config.toml)
just test            # Run all tests with race detector
just test-v          # Verbose test output
just test-pkg <pkg>  # Test a single package (e.g. just test-pkg internal/agent)
just test-cover      # Tests with coverage report
just test-cover-html # Open coverage in browser
just lint            # Run golangci-lint
just lint-fix        # Lint with auto-fix
just fmt             # Format all Go files
just fmt-check       # CI-friendly format check
just vet             # Run go vet
just check           # Run all checks (fmt + vet + lint + test)
just tidy            # go mod tidy
just clean           # Remove build artifacts
just loc             # Count lines of source vs test code
```

### Project structure

```
cmd/foxbox/          Entry point
internal/
  adapter/           Platform integrations (Telegram, ...)
  agent/             Core engine and conversation memory
  config/            TOML config parsing and validation
  llm/               Provider interface, router, cost tracking
    openrouter/      OpenRouter client
  scheduler/         Cron and interval scheduling
  security/          Permission engine
agents/default/      Agent personality (SOUL.md)
```

## Roadmap

Foxbox is built in phases. Phase 1 (current) covers the core agent loop:

- [x] Telegram adapter with user allowlist
- [x] LLM routing via OpenRouter
- [x] Conversation memory (SQLite)
- [x] Per-session cost budgets
- [x] Permission engine (supervised tier)
- [x] Scheduler with cron/interval/named expressions
- [ ] Skills system (text-based agent capabilities)
- [ ] Additional adapters (Discord)
- [ ] Tool integration (MCP protocol)
- [ ] Web dashboard
- [ ] Autonomous permission tiers

## License

[Apache 2.0](LICENSE)
