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

1. `--config` flag (explicit path)
2. `~/.denkeeper/denkeeper.toml`

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

## Environment variable expansion

String values support `$VAR` and `${VAR}` expansion. This is useful for keeping secrets out of the config file:

```toml
[llm.openrouter]
api_key = "$OPENROUTER_API_KEY"
```

## Validation

Denkeeper validates the config on startup using a three-phase pattern: parse TOML, apply defaults, then validate. If validation fails, the process exits with a descriptive error message and a suggested fix.

See the [full configuration reference](/docs/reference/config/) for every option.
