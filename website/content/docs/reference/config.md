---
title: "Configuration Reference"
description: "Complete reference for denkeeper.toml options."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-29T00:00:00+00:00
draft: false
weight: 10
toc: true
---

All configuration lives in a single TOML file. Default location: `~/.denkeeper/denkeeper.toml`.

## `[telegram]`

| Key | Type | Default | Description |
|---|---|---|---|
| `token` | string | *required* | Bot token from @BotFather |
| `allowed_users` | int[] | *required* | Telegram user IDs allowed to interact |

## `[discord]`

| Key | Type | Default | Description |
|---|---|---|---|
| `token` | string | *required* | Discord bot token |
| `allowed_users` | string[] | *required* | Discord user snowflake IDs |

## `[llm]`

| Key | Type | Default | Description |
|---|---|---|---|
| `default_provider` | string | `"openrouter"` | `"openrouter"`, `"anthropic"`, or `"ollama"` |
| `default_model` | string | — | Model identifier (format depends on provider) |
| `max_cost_per_session` | float | `0` (unlimited) | Maximum estimated cost per session in USD |

## `[llm.openrouter]`

| Key | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *required* | OpenRouter API key |

## `[llm.anthropic]`

| Key | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *required* | Anthropic API key (`sk-ant-...`) |
| `base_url` | string | `"https://api.anthropic.com"` | API endpoint override |

## `[llm.ollama]`

| Key | Type | Default | Description |
|---|---|---|---|
| `base_url` | string | `"http://localhost:11434"` | Ollama server URL |

## `[[llm.fallback]]`

| Key | Type | Description |
|---|---|---|
| `trigger` | string | `"low_funds"`, `"rate_limit"`, or `"error"` |
| `action` | string | `"switch_provider"`, `"switch_model"`, or `"wait_and_retry"` |
| `provider` | string | Target provider (for `switch_provider`) |
| `model` | string | Target model (for `switch_model`) |
| `threshold` | float | USD threshold (for `low_funds`) |
| `max_retries` | int | Max retry count (for `wait_and_retry`) |
| `backoff` | string | `"exponential"` (default) or `"constant"` |

## `[session]`

| Key | Type | Default | Description |
|---|---|---|---|
| `tier` | string | `"supervised"` | Default permission tier: `"autonomous"`, `"supervised"`, `"restricted"` |

## `[[agents]]`

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | *required* | Unique agent name (one must be `"default"`) |
| `description` | string | — | Agent description |
| `persona_dir` | string | — | Path to persona files |
| `adapters` | string[] | — | Adapter bindings (e.g., `["telegram"]`, `["telegram:12345"]`) |
| `llm_model` | string | — | Override default model |
| `session_tier` | string | — | Override default permission tier |

## `[memory]`

| Key | Type | Default | Description |
|---|---|---|---|
| `db_path` | string | `"~/.denkeeper/data/memory.db"` | SQLite database path |

## `[log]`

| Key | Type | Default | Description |
|---|---|---|---|
| `level` | string | `"info"` | `"debug"`, `"info"`, `"warn"`, `"error"` |
| `format` | string | `"text"` | `"text"` or `"json"` |

## `[voice]`

| Key | Type | Default | Description |
|---|---|---|---|
| `stt_provider` | string | — | Speech-to-text provider (`"openai"`) |
| `tts_provider` | string | — | Text-to-speech provider (`"openai"`) |
| `tts_voice` | string | `"alloy"` | Voice name |
| `auto_voice_reply` | bool | `false` | Reply with voice when user sends voice |

## `[voice.openai]`

| Key | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *required* | OpenAI API key for STT/TTS |

## `[api]`

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable the REST API server |
| `listen` | string | `":8080"` | Bind address |
| `tls` | bool | `false` | Enable HTTPS |
| `cert_file` | string | — | TLS certificate path |
| `key_file` | string | — | TLS private key path |
| `cors_origins` | string[] | — | Allowed CORS origins |
| `rate_limit` | float | `0` | Max requests/sec per API key |

## `[[schedules]]`

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | *required* | Unique schedule name |
| `type` | string | *required* | `"system"` or `"agent"` |
| `schedule` | string | *required* | Cron expression, interval, or named schedule |
| `skill` | string | — | Skill to invoke |
| `agent` | string | `"default"` | Target agent |
| `session_tier` | string | `"supervised"` | Permission tier for this schedule |
| `channel` | string | — | Delivery channel (e.g., `"telegram:12345"`) |
| `tags` | string[] | — | Freeform labels |
| `enabled` | bool | `true` | Enable/disable without removing |

## `[plugins.*]`

| Key | Type | Default | Description |
|---|---|---|---|
| `type` | string | *required* | `"subprocess"` or `"docker"` |
| `command` | string | *required* | Plugin binary path (subprocess) or Docker image (docker) |
| `args` | string[] | — | Command-line arguments |
| `env` | map | — | Environment variable overrides |
| `capabilities` | string[] | *required* | `["tools"]` |
| `memory_limit` | string | — | Docker container memory limit (e.g., `"256m"`) |
| `cpu_limit` | string | — | Docker container CPU limit (e.g., `"0.5"`) |
| `network` | string | `"none"` | Docker network mode (`"none"`, `"bridge"`, etc.) |
| `volumes` | string[] | — | Docker bind mounts |

Subprocess plugins run as child processes with direct MCP stdio. Docker plugins run in hardened containers with `--cap-drop ALL`, `--read-only`, `--security-opt no-new-privileges`, and `--network none` by default.

## `[security]`

| Key | Type | Default | Description |
|---|---|---|---|
| `trusted_keys` | string[] | — | Paths to PEM-encoded Ed25519 public key files |
| `allow_unsigned` | bool | `true` | Allow unsigned subprocess plugin binaries |

When `allow_unsigned = false`, all subprocess plugin binaries must have a valid Ed25519 signature from one of the trusted keys.

## `[kv]`

| Key | Type | Default | Description |
|---|---|---|---|
| `max_keys_per_agent` | int | `1000` | Maximum keys per agent |
| `max_value_bytes` | int | `65536` | Maximum value size in bytes (64 KB) |
| `cleanup_interval` | string | `"1h"` | Background cleanup interval for expired keys |

Per-agent key-value storage with optional TTL. Exposed as Config MCP tools (`kv_get`, `kv_set`, `kv_delete`, `kv_list`, `kv_set_nx`). Useful for locks, counters, caches, and cross-session coordination.

## `[sandbox]`

| Key | Type | Default | Description |
|---|---|---|---|
| `runtime` | string | `"docker"` | Sandbox backend: `"docker"` or `"kubernetes"` |

Selects the runtime backend for sandboxed (Docker-type) plugins.

## `[sandbox.kubernetes]`

| Key | Type | Default | Description |
|---|---|---|---|
| `namespace` | string | `"denkeeper-sandboxes"` | Kubernetes namespace for sandbox Pods |
| `kubeconfig` | string | — | Path to kubeconfig file (empty uses in-cluster config) |
| `runtime_class` | string | — | RuntimeClassName for gVisor or Kata Containers |

The Kubernetes backend creates ephemeral Pods with init-container network isolation, dropped capabilities, read-only root filesystem, and Pod Security Admission labels. Supports both in-cluster (ServiceAccount) and out-of-cluster (kubeconfig) authentication.

## `[tools.*]`

| Key | Type | Default | Description |
|---|---|---|---|
| `command` | string | *required* | MCP server command |
| `args` | string[] | — | Command arguments |
| `env` | map | — | Environment variables |

Tools can also be added and removed at runtime via the REST API (`tools:write` scope) or the Config MCP server (`tool_add`/`tool_remove`). Runtime changes are persisted to the TOML config file.
