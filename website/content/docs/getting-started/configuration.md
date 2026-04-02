---
title: "Configuration"
description: "Overview of Denkeeper's TOML configuration file."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
weight: 30
toc: true
---

Denkeeper is configured via a single TOML file, typically at `~/.denkeeper/denkeeper.toml`. The `denkeeper serve` command reads this file on startup. All behavior is driven by config — nothing is hardcoded.

## Config file location

Denkeeper searches for the config in this order:

1. `--config` / `-c` flag (explicit path)
2. `DENKEEPER_CONFIG` environment variable
3. `~/.denkeeper/denkeeper.toml`

When installed via `.deb`/`.rpm`, the systemd service uses `/etc/denkeeper/denkeeper.toml`.

## Sections overview

| Section | Purpose |
|---|---|
| `[telegram]` | Telegram bot token and user allowlist |
| `[discord]` | Discord bot token and user allowlist |
| `[llm]` | Default provider, model, cost limits |
| `[llm.openrouter]` | OpenRouter API key |
| `[llm.anthropic]` | Direct Anthropic API key |
| `[llm.ollama]` | Ollama base URL |
| `[llm.openai]` | OpenAI-compatible API key and endpoint |
| `[[llm.fallback]]` | Fallback strategies (low funds, rate limit, error) |
| `[session]` | Default permission tier |
| `[[agents]]` | Multi-agent definitions |
| `[memory]` | SQLite database path |
| `[log]` | Log level and format |
| `[voice]` | STT/TTS configuration |
| `[api]` | REST API server settings |
| `[[api.keys]]` | API key definitions |
| `[[schedules]]` | Recurring task schedules |
| `[plugins.*]` | Plugin definitions |
| `[security]` | Plugin signing (trusted keys, allow unsigned) |
| `[tools.*]` | MCP tool server definitions |
| `[kv]` | Agent KV store limits |

## Environment variable overrides

Secrets and select config fields can be set via `DENKEEPER_*` environment variables. These take precedence over values in the TOML file, enabling the standard Kubernetes pattern of a ConfigMap for config and a Secret for credentials.

| Env Var | Config Field |
|---------|-------------|
| `DENKEEPER_TELEGRAM_TOKEN` | `telegram.token` |
| `DENKEEPER_DISCORD_TOKEN` | `discord.token` |
| `DENKEEPER_LLM_PROVIDER` | `llm.default_provider` |
| `DENKEEPER_LLM_MODEL` | `llm.default_model` |
| `DENKEEPER_LLM_OPENROUTER_API_KEY` | `llm.openrouter.api_key` |
| `DENKEEPER_LLM_ANTHROPIC_API_KEY` | `llm.anthropic.api_key` |
| `DENKEEPER_LLM_ANTHROPIC_BASE_URL` | `llm.anthropic.base_url` |
| `DENKEEPER_LLM_OLLAMA_BASE_URL` | `llm.ollama.base_url` |
| `DENKEEPER_LLM_OPENAI_API_KEY` | `llm.openai.api_key` |
| `DENKEEPER_LLM_OPENAI_BASE_URL` | `llm.openai.base_url` |
| `DENKEEPER_VOICE_OPENAI_API_KEY` | `voice.openai.api_key` |
| `DENKEEPER_LOG_LEVEL` | `log.level` |
| `DENKEEPER_LOG_FORMAT` | `log.format` |
| `DENKEEPER_MEMORY_DB_PATH` | `memory.db_path` |
| `DENKEEPER_API_ENABLED` | `api.enabled` (accepts `"true"` or `"1"`) |
| `DENKEEPER_API_LISTEN` | `api.listen` |
| `DENKEEPER_SESSION_TIER` | `session.tier` |

### In-value expansion

Additionally, string values in tool and plugin `env` maps support `$VAR` and `${VAR}` expansion:

```toml
[tools.my-tool]
env = { API_KEY = "$MY_SECRET" }
```

## Validation

Denkeeper validates the config on startup using a three-phase pattern: parse TOML, apply defaults, then validate. If validation fails, the process exits with a descriptive error message and a suggested fix.

See the [full configuration reference](/docs/reference/config/) for every option.
