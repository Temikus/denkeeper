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

## `denkeeper plugin`

Manage Ed25519 plugin signing. These commands help you sign and verify plugin binaries for secure distribution.

### `denkeeper plugin keygen <name>`

Generate an Ed25519 key pair for plugin signing.

```bash
denkeeper plugin keygen my-plugin
denkeeper plugin keygen my-plugin --output /path/to/keys
```

| Flag | Description |
|---|---|
| `--output` | Output directory for key files (default: current directory) |

Creates two files: `<name>.pub` (public key, PEM) and `<name>.key` (private key, PEM, mode 0600).

### `denkeeper plugin sign <binary>`

Sign a plugin binary with an Ed25519 private key.

```bash
denkeeper plugin sign ./my-plugin --key my-plugin.key
```

| Flag | Description |
|---|---|
| `--key` | Path to private key file (required) |

Creates a detached signature file `<binary>.sig`.

### `denkeeper plugin verify <binary>`

Verify a plugin binary's signature against one or more public keys.

```bash
denkeeper plugin verify ./my-plugin --key my-plugin.pub
denkeeper plugin verify ./my-plugin --key key1.pub --key key2.pub
```

| Flag | Description |
|---|---|
| `--key` | Path to public key file (required, repeatable) |

Exits with code 0 if the signature is valid for any of the provided keys.
