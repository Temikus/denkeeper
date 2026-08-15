---
title: "Configuration"
description: "Overview of Denkeeper's TOML configuration file."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-14T00:00:00+00:00
draft: false
weight: 30
toc: true
---

Denkeeper is configured via a single TOML file, typically at `~/.denkeeper/denkeeper.toml`. The `denkeeper serve` command reads this file on startup. All behavior is driven by config — nothing is hardcoded.

## Config file location

Denkeeper searches for the config in this order:

1. `--config` / `-c` flag (explicit path)
2. `DENKEEPER_CONFIG` environment variable
3. `<data_dir>/denkeeper.toml` — `~/.denkeeper/denkeeper.toml` unless the data directory has been moved (see below)

When installed via `.deb`/`.rpm`, the systemd service uses `/etc/denkeeper/denkeeper.toml`.

## Data directory

Every default path — the SQLite database, persona directories, skills, browser profiles — derives from a single base directory, resolved in this order:

1. `DENKEEPER_DATA_DIR` environment variable
2. `data_dir` in the TOML file
3. `~/.denkeeper`

Setting one value therefore relocates all of them. The Helm chart uses this: it sets `DENKEEPER_DATA_DIR=/data` and mounts a PVC there, so no per-path configuration is needed. Individual paths can still be overridden (e.g. `memory.db_path`) when you want one of them somewhere else.

## Sections overview

| Section | Purpose |
|---|---|
| `data_dir` *(top-level)* | Base directory all default paths derive from |
| `[telegram]` | Telegram bot token and user allowlist |
| `[discord]` | Discord bot token and user allowlist |
| `[llm]` | Default provider, model, cost limits |
| `[[llm.providers]]` | Named provider instances (multiple of the same type) |
| `[llm.openrouter]` / `[llm.anthropic]` / `[llm.ollama]` / `[llm.openai]` | Legacy single-slot provider syntax |
| `[[llm.fallback]]` | Fallback strategies (cost limit, rate limit, error) |
| `[session]` | Default permission tier, approval timeout and retries |
| `[[agents]]` | Multi-agent definitions |
| `[[channels]]` | Named routing endpoints decoupling sessions from adapters |
| `[agent]` | Default persona and skills directories |
| `[memory]` | Conversation storage, retention, persona size caps |
| `[log]` | Log level and format |
| `[voice]` | STT/TTS configuration |
| `[api]` | REST API server settings, timezone, WebSocket |
| `[[api.keys]]` | API key definitions |
| `[api.auth]` / `[api.auth.oidc]` | Dashboard password, sessions, OIDC SSO |
| `[api.mcp_server]` | Expose this instance *as* an MCP server |
| `[[schedules]]` | Recurring task schedules |
| `[plugins.*]` | Plugin definitions |
| `[security]` | Plugin signing (trusted keys, allow unsigned) |
| `[sandbox]` | Sandbox runtime for Docker-type plugins |
| `[mcp]` | Global MCP settings (timeout, auto-restart, SSE URL allowlist) |
| `[tools.*]` | MCP tool server definitions (stdio or SSE transport) |
| `[kv]` | Agent KV store limits |
| `[web]` | Built-in `web_search` / `web_fetch` tools |
| `[script]` | `run_javascript` sandbox bounds |
| `[skills]` | Per-file skill size cap |
| `[browser]` | Browser automation container and URL allowlist |
| `[costs]` | Model pricing overrides and fallback rate |
| `[audit]` | Audit log retention and buffering |
| `[otel]` | OpenTelemetry traces and metrics |
| `[eval]` | Dry-run / eval audit verbosity |

## Environment variable overrides

Secrets and select config fields can be set via `DENKEEPER_*` environment variables. These take precedence over values in the TOML file, enabling the standard Kubernetes pattern of a ConfigMap for config and a Secret for credentials. The list is an explicit allowlist — arbitrary config fields are *not* settable via the environment.

**Paths and adapters**

| Env Var | Config Field |
|---------|-------------|
| `DENKEEPER_DATA_DIR` | `data_dir` — base directory all default paths derive from. Set to `/data` by the Helm chart |
| `DENKEEPER_MEMORY_DB_PATH` | `memory.db_path` |
| `DENKEEPER_TELEGRAM_TOKEN` | `telegram.token` |
| `DENKEEPER_DISCORD_TOKEN` | `discord.token` |
| `DENKEEPER_TIMEZONE` | `api.timezone` |

**LLM**

| Env Var | Config Field |
|---------|-------------|
| `DENKEEPER_LLM_PROVIDER` | `llm.default_provider` |
| `DENKEEPER_LLM_MODEL` | `llm.default_model` |
| `DENKEEPER_LLM_OPENROUTER_API_KEY` | `llm.openrouter.api_key` |
| `DENKEEPER_LLM_ANTHROPIC_API_KEY` | `llm.anthropic.api_key` |
| `DENKEEPER_LLM_ANTHROPIC_BASE_URL` | `llm.anthropic.base_url` |
| `DENKEEPER_LLM_OLLAMA_BASE_URL` | `llm.ollama.base_url` |
| `DENKEEPER_LLM_OPENAI_API_KEY` | `llm.openai.api_key` |
| `DENKEEPER_LLM_OPENAI_BASE_URL` | `llm.openai.base_url` |
| `DENKEEPER_LLM_OPENROUTER_REASONING_ENABLED` | `llm.openrouter.reasoning.enabled` |
| `DENKEEPER_LLM_OPENROUTER_REASONING_EFFORT` | `llm.openrouter.reasoning.effort` |
| `DENKEEPER_LLM_OPENROUTER_REASONING_MAX_TOKENS` | `llm.openrouter.reasoning.max_tokens` |
| `DENKEEPER_VOICE_OPENAI_API_KEY` | `voice.openai.api_key` |
| `DENKEEPER_SEARCH_API_KEY` | `web.search.api_key` |

**API and auth**

| Env Var | Config Field |
|---------|-------------|
| `DENKEEPER_API_ENABLED` | `api.enabled` (accepts `"true"`/`"1"` to enable, `"false"`/`"0"` to disable) |
| `DENKEEPER_API_LISTEN` | `api.listen` |
| `DENKEEPER_API_EXTERNAL_URL` | `api.external_url` — needed for correct OAuth callbacks behind a proxy |
| `DENKEEPER_API_WEBSOCKET_ENABLED` | `api.websocket_enabled` |
| `DENKEEPER_API_AUTH_SESSION_SECRET` | `api.auth.session_secret` |
| `DENKEEPER_OIDC_CLIENT_ID` | `api.auth.oidc.client_id` |
| `DENKEEPER_OIDC_CLIENT_SECRET` | `api.auth.oidc.client_secret` |

**Sessions, memory, observability**

| Env Var | Config Field |
|---------|-------------|
| `DENKEEPER_SESSION_TIER` | `session.tier` |
| `DENKEEPER_APPROVAL_TIMEOUT` | `session.approval_timeout` |
| `DENKEEPER_APPROVAL_RETRIES` | `session.approval_retries` |
| `DENKEEPER_MEMORY_RETENTION_DAYS` | `memory.retention_days` |
| `DENKEEPER_MEMORY_MAX_CONVERSATIONS` | `memory.max_conversations` |
| `DENKEEPER_AUDIT_ENABLED` | `audit.enabled` |
| `DENKEEPER_OTEL_ENABLED` | `otel.enabled` |
| `DENKEEPER_OTEL_TRACES_ENDPOINT` | `otel.traces_endpoint` |
| `DENKEEPER_LOG_LEVEL` | `log.level` |
| `DENKEEPER_LOG_FORMAT` | `log.format` |

### In-value expansion

Additionally, string values in tool and plugin `env` maps support `$VAR` and `${VAR}` expansion:

```toml
[tools.my-tool]
env = { API_KEY = "$MY_SECRET" }
```

## Validation

Denkeeper validates the config on startup using a three-phase pattern: parse TOML, apply defaults, then validate. If validation fails, the process exits with a descriptive error message and a suggested fix.

See the [full configuration reference](/docs/reference/config/) for every option.
