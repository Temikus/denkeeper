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
- **Multi-agent routing** — run multiple named agents, each with their own persona, skills, LLM model, and permission tier
- **Telegram integration** — chat with your agent from your phone
- **User allowlist** — only approved Telegram user IDs can interact
- **LLM routing** — pluggable provider interface, currently backed by OpenRouter (access to Claude, GPT, Llama, and hundreds more)
- **Fallback strategies** — automatic model/provider switching on errors, rate limits, or low funds
- **Cost tracking** — per-session budgets with automatic cutoff
- **Conversation memory** — SQLite-backed, persistent across restarts
- **Scheduler** — cron expressions, named intervals, and `@daily`/`@hourly` shorthand; per-schedule agent targeting and session modes
- **Skills** — flat markdown files with TOML frontmatter; trigger-based filtering (`command:`/`schedule:`) and per-agent skill merging
- **MCP tools** — spawn MCP stdio servers, discover tools, and execute tool calls in an agentic loop
- **Voice** — speech-to-text and text-to-speech via OpenAI (Whisper + TTS)
- **Permission tiers** — autonomous, supervised (default), and restricted; configurable per-agent or per-schedule
- **External REST API** — HTTP server with scoped API key auth, rate limiting, CORS, and TLS support; chat endpoint with SSE streaming and session management
- **Personality** — ships with a [`SOUL.md`](agents/default/SOUL.md) that gives the agent character (editable)

## Architecture

```
Adapter (Telegram) → Dispatcher → Engine (per agent) → LLM Router → Provider (OpenRouter)
                                       ↕                    ↕
                                   MemoryStore          CostTracker
                                   (SQLite)

API Server (/api/v1/...) ──────────────┘
Scheduler ─────────────────────────────┘
```

The Dispatcher routes incoming messages to named agent Engines based on adapter bindings. Each Engine checks permissions, loads conversation history, builds the system prompt (persona + skills), calls the LLM (with tool-call loop if MCP tools are configured), stores the response, and sends it back through the adapter.

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
./pkg/bin/denkeeper serve
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
| `[[llm.fallback]]` | Fallback strategies (error/rate_limit/low_funds triggers) |
| `[session]` | Default permission tier (supervised/autonomous/restricted) |
| `[[agents]]` | Multi-agent definitions (persona, skills, LLM model, adapter bindings) |
| `[tools.*]` | MCP tool server definitions |
| `[voice]` | STT/TTS configuration (OpenAI) |
| `[api]` | External REST API (listen addr, TLS, CORS, rate limiting, API keys with scopes) |
| `[[schedules]]` | Recurring tasks (cron, interval, or named schedules) |
| `[memory]` | SQLite database path |
| `[log]` | Log level and format |

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

Place skill files in `~/.denkeeper/skills/` (configurable via `[agent] skills_dir`). Subdirectories with a `SKILL.md` file are also supported. Skills with `triggers` are only injected when matched; skills without triggers are always included.

Agent-specific skills in `<persona_dir>/skills/` override global skills of the same name.

A sample `help` skill is included in `agents/default/skills/`.

### Multi-Agent

Define multiple agents, each with their own persona, skills, LLM model, and adapter bindings:

```toml
[[agents]]
name = "default"
persona_dir = "~/.denkeeper/agents/default"
adapters = ["telegram"]              # wildcard: all Telegram messages

[[agents]]
name = "work-assistant"
persona_dir = "~/.denkeeper/agents/work-assistant"
adapters = ["telegram:987654321"]    # specific chat only
llm_model = "openai/gpt-4o"
session_tier = "restricted"
```

If no `[[agents]]` section is present, a single `"default"` agent is synthesized from `[agent]`/`[session]`.

### Schedules

Schedules support three expression formats, per-schedule agent targeting, and configurable session modes:

```toml
[[schedules]]
name = "daily-briefing"
type = "agent"
schedule = "0 8 * * *"
skill = "daily-briefing"
agent = "default"                # target agent (default: "default")
session_tier = "supervised"
session_mode = "isolated"        # fresh context each run (default: "shared")
channel = "telegram:YOUR_CHAT_ID"
enabled = true

[[schedules]]
name = "hourly-check"
type = "agent"
schedule = "@every 1h"           # or @daily, @hourly, @weekly
channel = "telegram:YOUR_CHAT_ID"
```

`session_mode = "isolated"` creates a fresh conversation context for each run so scheduled jobs don't mix into your regular chat history.

### REST API

Enable the API with `[api] enabled = true` in your config. All endpoints (except `/health`) require a `Bearer` token matching a configured API key.

```toml
[api]
enabled = true
listen = "0.0.0.0:8080"

[[api.keys]]
name = "my-client"
key  = "dk-your-secret-key"
scopes = ["chat", "sessions:read", "costs:read"]
```

**Available scopes**: `chat`, `admin`, `sessions:read`, `costs:read`, `skills:read`, `schedules:read`

**Endpoints:**

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| `GET` | `/api/v1/health` | — | Health check (no auth) |
| `POST` | `/api/v1/chat` | `chat` | Send a message; returns `{ session_id, response }`. Add `Accept: text/event-stream` for SSE. |
| `GET` | `/api/v1/sessions` | `sessions:read` | List all conversations |
| `GET` | `/api/v1/sessions/{id}/messages` | `sessions:read` | Get messages for a session |
| `DELETE` | `/api/v1/sessions/{id}` | `sessions:read` | Delete a session and its history |
| `GET` | `/api/v1/agents` | `admin` | List agents with metadata |
| `GET` | `/api/v1/agents/{name}` | `admin` | Agent details and skills |
| `GET` | `/api/v1/skills` | `skills:read` | List all skills across agents |
| `GET` | `/api/v1/schedules` | `schedules:read` | List schedules with run times |
| `GET` | `/api/v1/costs` | `costs:read` | Cost summary |

**Chat example:**

```bash
# Non-streaming
curl -X POST http://localhost:8080/api/v1/chat \
  -H "Authorization: Bearer dk-your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello!", "session_id": "my-session"}'

# SSE streaming
curl -X POST http://localhost:8080/api/v1/chat \
  -H "Authorization: Bearer dk-your-secret-key" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{"message": "Hello!", "session_id": "my-session"}'
```

Pass the same `session_id` in subsequent requests to continue the conversation. Omit it to start a new session with an auto-generated ID.

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
  agent/             Dispatcher, engine, and conversation memory
  api/               External REST API server
  config/            TOML config parsing and validation
  llm/               Provider interface, router, cost tracking
    openrouter/      OpenRouter client
  persona/           Persona file loader (SOUL.md, USER.md, MEMORY.md)
  scheduler/         Cron and interval scheduling
  security/          Permission engine (tiers)
  skill/             Skill file loader, trigger matching, merging
  tool/              MCP tool server manager
  voice/             STT/TTS provider interface
    openai/          OpenAI Whisper + TTS client
pkg/bin/             Build output (gitignored)
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

**Phase 2 — Core Features** ✅
- [x] Multi-agent routing with per-agent personas, skills, LLM models, and permissions
- [x] Scheduler with cron/interval/named expressions, per-schedule agent targeting
- [x] Configurable session modes for schedules (`shared`/`isolated`)
- [x] Skills system with trigger-based filtering and per-agent merge
- [x] MCP tool support (agentic tool-call loop)
- [x] Fallback strategies (error/rate_limit/low_funds → switch_provider/switch_model/wait_and_retry)
- [x] Voice messages (STT/TTS via OpenAI)
- [x] Three permission tiers (autonomous/supervised/restricted), per-agent and per-schedule
- [x] External REST API server skeleton (auth, rate limiting, CORS, TLS, health endpoint)

**Phase 3 — Extensibility** (in progress)
- [x] REST API chat endpoint — `POST /api/v1/chat` with JSON response and SSE streaming, `session_id` for conversation continuity, `DELETE /api/v1/sessions/:id`
- [ ] Approval workflows (inline in Telegram + `POST /api/v1/approvals/:id/approve|deny`)
- [ ] Config MCP server (agent self-modification of skills, schedules, fallbacks)
- [ ] Plugin system (subprocess + Docker sandboxing)
- [ ] Plugin signing and verification
- [ ] Web dashboard

**Phase 4 — Polish**
- [ ] Additional adapters (Discord)
- [ ] Additional LLM providers
- [ ] GoReleaser, .deb/.rpm packages, Homebrew tap
- [ ] Hugo documentation website

## License

[Apache 2.0](LICENSE)
