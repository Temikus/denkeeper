# Denkeeper

<p align="center">
  <a href="https://github.com/Temikus/denkeeper/actions/workflows/ci.yml"><img src="https://github.com/Temikus/denkeeper/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/Temikus/denkeeper/actions/workflows/release.yml"><img src="https://github.com/Temikus/denkeeper/actions/workflows/release.yml/badge.svg" alt="Release"></a>
  <a href="https://github.com/Temikus/denkeeper/releases/latest"><img src="https://img.shields.io/github/v/release/Temikus/denkeeper" alt="Latest Release"></a>
  <a href="https://github.com/Temikus/denkeeper/pkgs/container/denkeeper"><img src="https://ghcr-badge.egpl.dev/temikus/denkeeper/latest_tag?trim=major&label=ghcr.io" alt="Docker Image"></a>
  <a href="https://goreportcard.com/report/github.com/Temikus/denkeeper"><img src="https://goreportcard.com/badge/github.com/Temikus/denkeeper" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/Temikus/denkeeper" alt="License"></a>
</p>

A security-first personal AI agent that lives in your chat. Built in Go as a single binary, designed to run anywhere from a Raspberry Pi to a cloud VM.

Denkeeper connects to your Telegram (more adapters planned), routes messages through LLM providers via [OpenRouter](https://openrouter.ai), and remembers conversations across sessions using a local SQLite database. It enforces per-session cost budgets, user allowlists, and a tiered permission system — so you stay in control of what it can do and how much it can spend.

## Features

- **Single binary** — no runtime dependencies, no containers required
- **Telegram integration** — chat with your agent from your phone
- **User allowlist** — only approved Telegram user IDs can interact
- **LLM routing** — pluggable provider interface, currently backed by OpenRouter (access to Claude, GPT, Llama, and hundreds more)
- **Cost tracking** — per-session budgets with automatic cutoff
- **Conversation memory** — SQLite-backed, persistent across restarts
- **Scheduler** — cron expressions, named intervals, and `@daily`/`@hourly` shorthand; wired into the agent engine with configurable session modes
- **Skills** — flat markdown files with TOML frontmatter that teach the agent how to handle specific tasks; auto-injected into the system prompt at startup
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
git clone https://github.com/Temikus/denkeeper.git
cd denkeeper

# Copy and edit the config
mkdir -p ~/.denkeeper
cp denkeeper.toml.example ~/.denkeeper/denkeeper.toml
# Fill in your token, API key, and user ID
$EDITOR ~/.denkeeper/denkeeper.toml

# Build and run
just build
./denkeeper serve
```

Or run directly without building:

```bash
just serve
```

### Configuration

Denkeeper uses a single TOML file (default `~/.denkeeper/denkeeper.toml`). See [`denkeeper.toml.example`](denkeeper.toml.example) for all options.

Key sections:

| Section | Purpose |
|---------|---------|
| `[telegram]` | Bot token and allowed user IDs |
| `[llm]` | Default provider, model, and per-session cost cap |
| `[llm.openrouter]` | OpenRouter API key |
| `[memory]` | SQLite database path |
| `[log]` | Log level and format |
| `[agent]` | `persona_dir` and `skills_dir` (default: `~/.denkeeper/skills`) |
| `[[schedules]]` | Recurring tasks (cron, interval, or named schedules) |

### Skills

Skills are markdown files that teach the agent how to handle specific tasks. They use TOML frontmatter enclosed in `+++` delimiters:

```markdown
+++
name = "daily-briefing"
description = "Compile and deliver a daily briefing"
version = "1.0.0"
triggers = ["schedule:daily:08:00", "command:briefing"]
+++

# Daily Briefing

When triggered, compile a briefing with:
1. Weather forecast for the user's location
2. Top 3 news headlines
3. Any pending reminders
```

Place skill files in `~/.denkeeper/skills/` (configurable via `[agent] skills_dir`). Subdirectories with a `SKILL.md` file are also supported. All skills are injected into the agent's system prompt at startup.

A sample `help` skill is included in `agents/default/skills/`.

### Schedules

Schedules support three expression formats and can target any adapter channel:

```toml
[[schedules]]
name = "daily-briefing"
type = "cron"
schedule = "0 8 * * *"
skill = "daily-briefing"
session_tier = "supervised"
session_mode = "isolated"        # fresh context each run (default: "shared")
channel = "telegram:YOUR_CHAT_ID"
enabled = true

[[schedules]]
name = "hourly-check"
type = "interval"
schedule = "@every 1h"           # or @daily, @hourly, @weekly
channel = "telegram:YOUR_CHAT_ID"
```

`session_mode = "isolated"` creates a fresh conversation context for each run so scheduled jobs don't mix into your regular chat history.

## Development

[just](https://github.com/casey/just) is used as the command runner. Run `just` to see all available recipes:

```
just build           # Build the denkeeper binary
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
cmd/denkeeper/       Entry point
internal/
  adapter/           Platform integrations (Telegram, ...)
  agent/             Core engine and conversation memory
  config/            TOML config parsing and validation
  llm/               Provider interface, router, cost tracking
    openrouter/      OpenRouter client
  scheduler/         Cron and interval scheduling (wired to engine)
  security/          Permission engine
  skill/             Skill file loader and system prompt builder
agents/default/
  skills/            Bundled skills (e.g. help.md)
  SOUL.md            Agent personality
```

## Roadmap

Denkeeper is built in phases:

**Phase 1 — Foundation** ✅
- [x] Telegram adapter with user allowlist
- [x] LLM routing via OpenRouter
- [x] Conversation memory (SQLite)
- [x] Per-session cost budgets
- [x] Permission engine (supervised tier)
- [x] Agent persona system (SOUL.md, USER.md, MEMORY.md injection)

**Phase 2 — Core Features** (in progress)
- [x] Scheduler with cron/interval/named expressions, wired to engine
- [x] Configurable session modes for schedules (`shared`/`isolated`)
- [x] Skills system (flat-file markdown, TOML frontmatter, system prompt injection)
- [ ] MCP tool support
- [ ] Fallback strategies and cost-aware model switching
- [ ] Voice messages (STT/TTS)
- [ ] External REST API

**Phase 3 — Extensibility**
- [ ] Plugin system (subprocess + Docker sandboxing)
- [ ] Config MCP server (agent self-modification)
- [ ] Approval workflows
- [ ] Web dashboard

**Phase 4 — Polish**
- [ ] Additional adapters (Discord)
- [ ] Additional LLM providers
- [ ] Package distribution (.deb, .rpm, Homebrew)

## License

[Apache 2.0](LICENSE)
