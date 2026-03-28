---
title: "CLI Reference"
description: "Denkeeper command-line interface reference."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-29T00:00:00+00:00
draft: false
weight: 20
toc: true
---

## Global flags

| Flag | Description |
|---|---|
| `--config PATH` | Path to config file (default: `~/.denkeeper/denkeeper.toml`) |
| `--version` | Print version and exit |
| `--help` | Print help |

## `denkeeper serve`

Start the agent. Loads config, connects adapters, starts the scheduler, and optionally starts the REST API server.

```bash
denkeeper serve
denkeeper serve --config /etc/denkeeper/denkeeper.toml
```

## `denkeeper setup`

Interactive first-run wizard. Creates `~/.denkeeper/denkeeper.toml` with your LLM provider, API keys, and Telegram configuration.

```bash
denkeeper setup
```

## `denkeeper keys`

Manage API keys for the REST API.

```bash
denkeeper keys create --name dashboard --scopes admin,chat,sessions:read
denkeeper keys list
denkeeper keys revoke --name dashboard
```

### `denkeeper keys create`

| Flag | Description |
|---|---|
| `--name` | Key name (required) |
| `--scopes` | Comma-separated list of scopes |

The plaintext key is displayed once on creation and cannot be recovered.

### `denkeeper keys list`

Lists all API keys with their names, scopes, and creation dates. The key secret is never shown.

### `denkeeper keys revoke`

| Flag | Description |
|---|---|
| `--name` | Key name to revoke (required) |

Revocation is immediate — the key stops working as soon as the command completes.

### `denkeeper keys delete`

| Flag | Description |
|---|---|
| `--name` | Key name to permanently delete (required) |

Permanently removes a revoked key from the database. Only revoked keys can be deleted.
