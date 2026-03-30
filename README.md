<p align="center">
  <img src="assets/logo_text.png" alt="Denkeeper" width="300">
</p>

<p align="center">
  <a href="https://github.com/Temikus/denkeeper/actions/workflows/ci.yml"><img src="https://github.com/Temikus/denkeeper/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/Temikus/denkeeper/releases/latest"><img src="https://img.shields.io/github/v/release/Temikus/denkeeper?label=release" alt="Latest Release"></a>
  <a href="https://github.com/Temikus/denkeeper/pkgs/container/denkeeper"><img src="https://img.shields.io/github/v/release/Temikus/denkeeper?label=ghcr.io&logo=docker" alt="Docker Image"></a>
  <a href="https://goreportcard.com/report/github.com/Temikus/denkeeper"><img src="https://goreportcard.com/badge/github.com/Temikus/denkeeper" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/Temikus/denkeeper" alt="License"></a>
</p>

A security-first personal AI agent that lives in your chat. Built in Go as a single binary, designed to run anywhere from a Raspberry Pi to a cloud VM.

Denkeeper connects to your Telegram or Discord, routes messages through LLM providers via [Anthropic](https://anthropic.com), [OpenRouter](https://openrouter.ai), or a local [Ollama](https://ollama.com) instance, and remembers conversations across sessions using a local SQLite database. It enforces per-session cost budgets, user allowlists, and a tiered permission system — so you stay in control of what it can do and how much it can spend.

## Installation

### One-liner (Linux and macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/Temikus/denkeeper/main/install.sh | sh
```

To install to a custom prefix (e.g. without sudo):

```sh
curl -fsSL https://raw.githubusercontent.com/Temikus/denkeeper/main/install.sh | sh -s -- --prefix ~/.local
```

The installer detects OS/arch, downloads the correct release archive, verifies the SHA-256 checksum, and places the binary in `<prefix>/bin`.

### Debian / Ubuntu (.deb)

```sh
VERSION=$(curl -fsSL https://api.github.com/repos/Temikus/denkeeper/releases/latest | grep '"tag_name"' | sed 's/.*"\(v[^"]*\)".*/\1/')
curl -fsSL "https://github.com/Temikus/denkeeper/releases/download/${VERSION}/denkeeper_${VERSION#v}_linux_amd64.deb" -o denkeeper.deb
sudo dpkg -i denkeeper.deb
```

Configure and start the service:

```sh
sudo cp /etc/denkeeper/denkeeper.toml.example /etc/denkeeper/denkeeper.toml
sudoedit /etc/denkeeper/denkeeper.toml
sudo systemctl enable --now denkeeper
journalctl -u denkeeper -f
```

### RHEL / Fedora (.rpm)

```sh
VERSION=$(curl -fsSL https://api.github.com/repos/Temikus/denkeeper/releases/latest | grep '"tag_name"' | sed 's/.*"\(v[^"]*\)".*/\1/')
curl -fsSL "https://github.com/Temikus/denkeeper/releases/download/${VERSION}/denkeeper_${VERSION#v}_linux_amd64.rpm" -o denkeeper.rpm
sudo rpm -i denkeeper.rpm
```

### Docker

```sh
docker pull ghcr.io/temikus/denkeeper:latest
docker run -d --name denkeeper \
  -v ~/.denkeeper:/data \
  ghcr.io/temikus/denkeeper:latest
```

The container reads config from `DENKEEPER_CONFIG` (default `/data/denkeeper.toml`). Override with `-e DENKEEPER_CONFIG=/path/to/config.toml`.

### Helm (Kubernetes)

A Helm chart is available in [`deploy/helm/denkeeper/`](deploy/helm/denkeeper/) with support for Ingress, PVC persistence, secrets management, and security-hardened pod defaults:

```sh
helm install denkeeper deploy/helm/denkeeper/ \
  --set secrets.llmAnthropicApiKey=sk-ant-... \
  --set secrets.telegramToken=123456:ABC...
```

### Homebrew (macOS)

```sh
brew install Temikus/denkeeper/denkeeper
```

### Verify release signatures

All release archives are signed with [cosign](https://github.com/sigstore/cosign) (keyless OIDC — no long-lived keys):

```sh
cosign verify-blob \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='https://github.com/Temikus/denkeeper/.github/workflows/release.yml.*' \
  checksums.txt
```

Docker images are signed and carry SLSA build provenance attestations:

```sh
cosign verify \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='https://github.com/Temikus/denkeeper/.github/workflows/release.yml.*' \
  ghcr.io/temikus/denkeeper:latest
```

## Features

- **Single binary** — no runtime dependencies, no containers required
- **Multi-agent routing** — run multiple named agents, each with their own persona, skills, LLM model, and permission tier
- **Telegram + Discord** — chat with your agent from your phone or Discord server, including inline Approve/Deny buttons for supervised actions; both adapters can run simultaneously
- **User allowlist** — only approved user IDs can interact (per-adapter)
- **LLM routing** — pluggable provider interface; Anthropic (direct), OpenRouter (cloud, hundreds of models), and Ollama (local inference) built-in
- **Fallback strategies** — automatic model/provider switching on errors, rate limits, or low funds
- **Cost tracking** — per-session budgets with automatic cutoff
- **Conversation memory** — SQLite-backed, persistent across restarts
- **Scheduler** — cron expressions, named intervals, and `@daily`/`@hourly` shorthand; per-schedule agent targeting and session modes
- **Skills** — flat markdown files with TOML frontmatter; trigger-based filtering (`command:`/`schedule:`) and per-agent skill merging
- **MCP tools** — spawn MCP stdio servers, discover tools, and execute tool calls in an agentic loop
- **Plugin system** — subprocess and Docker-sandboxed plugins with capability declarations and Ed25519 signature verification; tools capability wires plugin tools into the agent's LLM loop
- **Runtime tool management** — add and remove MCP tools and plugins at runtime without restarting; changes are persisted to TOML config
- **Agent KV store** — per-agent key-value storage with optional TTL, exposed as MCP tools (`kv_get`/`kv_set`/`kv_delete`/`kv_list`/`kv_set_nx`); useful for locks, counters, caches, and cross-session state
- **Web dashboard** — embedded Svelte UI (served via the API server) with overview, chat, sessions, approvals, schedules, skills, tools, agent context viewer, and API key management
- **Voice** — speech-to-text and text-to-speech via OpenAI (Whisper + TTS)
- **Permission tiers** — autonomous, supervised (default), and restricted; configurable per-agent or per-schedule
- **Approval workflows** — supervised-tier actions (profile updates, skill creation, schedule additions, tool installation) require explicit human approval via chat buttons (Telegram/Discord) or REST API
- **Config MCP server** — per-agent in-process MCP tools let the LLM manage skills, schedules, tools, plugins, KV storage, and inspect its own permission tier at runtime
- **External REST API** — HTTP server with scoped API key auth, rate limiting, CORS, and TLS support; chat endpoint with SSE streaming, session management, approval CRUD, tool/plugin CRUD, and API key management
- **CLI plugin signing** — `denkeeper plugin keygen/sign/verify` commands for Ed25519 plugin binary signing and verification
- **Personality** — ships with a [`SOUL.md`](agents/default/SOUL.md) that gives the agent character (editable)

## Architecture

```
Adapter (Telegram/Discord) → Dispatcher → Engine (per agent) → LLM Router → Provider (Anthropic/OpenRouter/Ollama)
                                               ↕                    ↕
                                           MemoryStore          CostTracker
                                           (SQLite)

API Server (/api/v1/...) ──────────────────────┘
Scheduler ─────────────────────────────────────┘
```

The Dispatcher routes incoming messages to named agent Engines based on adapter bindings. Each Engine checks permissions, loads conversation history, builds the system prompt (persona + skills), calls the LLM (with tool-call loop if MCP tools are configured), stores the response, and sends it back through the adapter.

## Quick start

### Prerequisites

- Go 1.25+ (managed via [mise](https://mise.jdx.dev) — see `mise.toml`)
- A Telegram bot token (from [@BotFather](https://t.me/BotFather))
- An API key for your chosen LLM provider: [OpenRouter](https://openrouter.ai/keys), [Anthropic](https://console.anthropic.com/settings/keys), or a local [Ollama](https://ollama.com) instance
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

Denkeeper uses a single TOML file (default `~/.denkeeper/denkeeper.toml`). See [`denkeeper.toml.example`](denkeeper.toml.example) for all options. The config path can be set via `--config` flag or `DENKEEPER_CONFIG` env var.

**Health check**: `GET /api/v1/health` returns `{"status":"ok"}` with no authentication required. Use this for Docker `HEALTHCHECK` or Kubernetes liveness/readiness probes (requires `api.enabled = true`).

Key sections:

| Section | Purpose |
|---------|---------|
| `[telegram]` | Bot token and allowed user IDs |
| `[discord]` | Bot token and allowed user snowflake IDs |
| `[llm]` | Default provider (`anthropic`/`openrouter`/`ollama`), model, and per-session cost cap |
| `[llm.anthropic]` | Anthropic API key (direct provider; no OpenRouter key needed) |
| `[llm.openrouter]` | OpenRouter API key |
| `[llm.ollama]` | Ollama base URL (default: `http://localhost:11434`) |
| `[[llm.fallback]]` | Fallback strategies (error/rate_limit/low_funds triggers) |
| `[session]` | Default permission tier (supervised/autonomous/restricted) |
| `[[agents]]` | Multi-agent definitions (persona, skills, LLM model, adapter bindings) |
| `[tools.*]` | MCP tool server definitions |
| `[plugins.*]` | Plugin definitions — subprocess or Docker-sandboxed (capability declarations) |
| `[security]` | Ed25519 plugin signing config (`trusted_keys`, `allow_unsigned`) |
| `[voice]` | STT/TTS configuration (OpenAI) |
| `[api]` | External REST API (listen addr, TLS, CORS, rate limiting, API keys with scopes) |
| `[[schedules]]` | Recurring tasks (cron, interval, or named schedules) |
| `[kv]` | Agent KV store limits (`max_keys_per_agent`, `max_value_bytes`, `cleanup_interval`) |
| `[memory]` | SQLite database path |
| `[log]` | Log level and format |

### Environment Variables

Secrets and select config fields can be set via environment variables, which take precedence over values in `denkeeper.toml`. This enables the standard Kubernetes pattern of using a ConfigMap for config and a Secret for credentials.

| Env Var | Config Field |
|---------|-------------|
| `DENKEEPER_CONFIG` | Config file path (replaces `--config` flag) |
| `DENKEEPER_TELEGRAM_TOKEN` | `telegram.token` |
| `DENKEEPER_DISCORD_TOKEN` | `discord.token` |
| `DENKEEPER_LLM_PROVIDER` | `llm.default_provider` |
| `DENKEEPER_LLM_MODEL` | `llm.default_model` |
| `DENKEEPER_LLM_OPENROUTER_API_KEY` | `llm.openrouter.api_key` |
| `DENKEEPER_LLM_ANTHROPIC_API_KEY` | `llm.anthropic.api_key` |
| `DENKEEPER_LLM_ANTHROPIC_BASE_URL` | `llm.anthropic.base_url` |
| `DENKEEPER_LLM_OLLAMA_BASE_URL` | `llm.ollama.base_url` |
| `DENKEEPER_VOICE_OPENAI_API_KEY` | `voice.openai.api_key` |
| `DENKEEPER_LOG_LEVEL` | `log.level` |
| `DENKEEPER_LOG_FORMAT` | `log.format` |
| `DENKEEPER_MEMORY_DB_PATH` | `memory.db_path` |
| `DENKEEPER_API_ENABLED` | `api.enabled` (accepts `"true"` or `"1"`) |
| `DENKEEPER_API_LISTEN` | `api.listen` |
| `DENKEEPER_SESSION_TIER` | `session.tier` |

A Helm chart is available in [`deploy/helm/denkeeper/`](deploy/helm/denkeeper/) for Kubernetes deployments.

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

**Available scopes**: `chat`, `admin`, `sessions:read`, `costs:read`, `skills:read`, `schedules:read`, `approvals:read`, `approvals:write`, `tools:read`, `tools:write`

**Endpoints:**

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| `GET` | `/api/v1/health` | — | Health check (no auth) |
| `GET` | `/api/v1/setup` | — | First-run setup status |
| `POST` | `/api/v1/setup` | — | Initialize first-run configuration |
| `POST` | `/api/v1/chat` | `chat` | Send a message; returns `{ session_id, response }`. Add `Accept: text/event-stream` for SSE. |
| `GET` | `/api/v1/sessions` | `sessions:read` | List all conversations |
| `GET` | `/api/v1/sessions/{id}/messages` | `sessions:read` | Get messages for a session |
| `DELETE` | `/api/v1/sessions/{id}` | `sessions:read` | Delete a session and its history |
| `GET` | `/api/v1/agents` | `admin` | List agents with metadata |
| `GET` | `/api/v1/agents/{name}` | `admin` | Agent details and skills |
| `GET` | `/api/v1/skills` | `skills:read` | List all skills across agents |
| `GET` | `/api/v1/skills/{agent}` | `skills:read` | List skills for a specific agent |
| `GET` | `/api/v1/schedules` | `schedules:read` | List schedules with run times |
| `GET` | `/api/v1/costs` | `costs:read` | Cost summary |
| `GET` | `/api/v1/approvals` | `approvals:read` | List approval requests (filter by `?status=pending`) |
| `GET` | `/api/v1/approvals/{id}` | `approvals:read` | Get a single approval request |
| `POST` | `/api/v1/approvals/{id}/approve` | `approvals:write` | Approve a pending request |
| `POST` | `/api/v1/approvals/{id}/deny` | `approvals:write` | Deny a pending request |
| `GET` | `/api/v1/keys` | `admin` | List API keys (secrets not returned) |
| `POST` | `/api/v1/keys` | `admin` | Create a new API key |
| `DELETE` | `/api/v1/keys/{id}` | `admin` | Revoke an API key |
| `DELETE` | `/api/v1/keys/{id}/permanent` | `admin` | Permanently delete a revoked key |
| `POST` | `/api/v1/keys/{id}/rotate` | `admin` | Rotate an API key |
| `GET` | `/api/v1/tools` | `tools:read` | List MCP tool servers |
| `GET` | `/api/v1/tools/{name}` | `tools:read` | Get tool server details |
| `POST` | `/api/v1/tools` | `tools:write` | Add a tool server |
| `DELETE` | `/api/v1/tools/{name}` | `tools:write` | Remove a tool server |
| `GET` | `/api/v1/plugins` | `tools:read` | List plugins |
| `GET` | `/api/v1/plugins/{name}` | `tools:read` | Get plugin details |
| `POST` | `/api/v1/plugins` | `tools:write` | Add a plugin |
| `DELETE` | `/api/v1/plugins/{name}` | `tools:write` | Remove a plugin |

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
just build           # Build the denkeeper binary (requires web/dist/ to exist)
just build-ui        # Build the Svelte web dashboard (requires Node.js)
just build-full      # Build web dashboard then Go binary in one step
just serve           # Start the agent (just serve ./path/to/config.toml)
just web-dev         # Start Vite dev server for dashboard hot-reload
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
  adapter/           Platform integrations
    telegram/        Telegram bot adapter
    discord/         Discord bot adapter
  agent/             Dispatcher, engine, and conversation memory
  api/               External REST API server
  approval/          Approval workflow manager, store, registry, and callback handler
  config/            TOML config parsing and validation
  configmcp/         Per-agent Config MCP server (skill/schedule/tier/tool/KV tools)
  kv/                Per-agent key-value store with TTL
  llm/               Provider interface, router, cost tracking
    anthropic/       Anthropic direct client
    openrouter/      OpenRouter client
    ollama/          Ollama local inference client
  persona/           Persona file loader (SOUL.md, USER.md, MEMORY.md)
  plugin/            Plugin manager (subprocess and Docker-sandboxed)
  sandbox/           Pluggable sandbox runtime (Docker and Kubernetes backends)
  scheduler/         Cron and interval scheduling
  security/          Permission engine (tiers) and Ed25519 plugin signing
  skill/             Skill file loader, trigger matching, merging
  tool/              MCP tool server manager
  voice/             STT/TTS provider interface
    openai/          OpenAI Whisper + TTS client
  web/               Embedded web dashboard handler (serves web/dist/)
web/                 Svelte dashboard source (npm build → web/dist/)
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

**Phase 3 — Extensibility** ✅
- [x] REST API chat endpoint — `POST /api/v1/chat` with JSON response and SSE streaming, `session_id` for conversation continuity, `DELETE /api/v1/sessions/:id`
- [x] Approval workflows — supervised-tier Telegram inline buttons (Approve/Deny) + REST API (`GET|POST /api/v1/approvals/...`); TTL expiry, stale callback UX, keyboard auto-removal on resolution
- [x] Config MCP server — per-agent in-process MCP tools for skill, schedule, tool, plugin, and KV self-modification
- [x] Ollama LLM provider — local inference with conditional OpenRouter API key validation
- [x] Plugin system — subprocess and Docker-sandboxed plugins with capability declarations and Ed25519 signature verification
- [x] Runtime tool management — add/remove MCP tools and plugins at runtime; REST API CRUD (`tools:read`/`tools:write` scopes); TOML config persistence
- [x] Agent KV store — per-agent SQLite-backed key-value storage with TTL, exposed as Config MCP tools
- [x] CLI plugin signing — `denkeeper plugin keygen/sign/verify` commands for Ed25519 binary signing
- [x] Web dashboard — embedded Svelte UI with overview, chat, sessions, approvals, schedules, skills, tools, agents, and API key management

**Phase 4 — Polish** ✅
- [x] Discord adapter — DM and guild channel support, allowlist, typing indicator, action-row approval buttons
- [x] Anthropic direct LLM provider — Anthropic Messages API, tool_use support, no OpenRouter dependency
- [x] API Key CRUD — runtime key management (create/revoke/rotate) without TOML restarts
- [x] Web dashboard Chat page — SSE streaming chat UI in the dashboard
- [x] GoReleaser, .deb/.rpm packages, Homebrew tap config
- [x] CI/CD pipeline (golangci-lint, govulncheck, cosign signing, SBOM generation)
- [x] One-liner install script + systemd service unit

**Phase 5 — Documentation** ✅
- [x] Hugo documentation website (Hugo + Doks theme, deployed via GitHub Pages at [denkeeper.io](https://denkeeper.io))
- [x] Getting-started guides, concept docs, and reference pages
- [x] One-liner install script hosted at `get.denkeeper.io`

**Phase 6 — Browser Automation** (planned)
- [ ] Browser automation — first-party Docker plugin with headless Chromium + Playwright MCP server
- [ ] Persistent browser profiles — per-agent encrypted profile storage with volume mounts
- [ ] URL allowlist enforcement — egress filtering at plugin level, configurable per-agent
- [ ] Browser orchestrator skill — built-in skill for multi-step browser workflow patterns
- [ ] Screenshot-to-text fallback — DOM extraction pipeline for non-vision LLMs

## License

[Apache 2.0](LICENSE)
