---
title: "CLI Reference"
description: "Denkeeper command-line interface reference."
slug: "cli"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-10-02T00:00:00+00:00
draft: false
weight: 20
toc: true
---

## Commands

| Command | Purpose |
|---|---|
| `denkeeper serve` | Start the agent |
| `denkeeper version` | Print version information |
| `denkeeper keys` | Manage REST API keys |
| `denkeeper sessions` | Inspect and prune conversation sessions |
| `denkeeper decide` | Calibrate decision models against recorded supervisor reviews |
| `denkeeper passwd` | Generate a bcrypt hash for dashboard login |
| `denkeeper plugin` | Ed25519 plugin signing |

## Flags

`--config` / `-c` is accepted by the commands that read the config file — `serve`, `keys`, `sessions`, and `decide`. It is not a root-level flag, so `denkeeper --config ... <command>` will not work; put it after the subcommand instead.

| Flag | Available on | Description |
|---|---|---|
| `--config PATH`, `-c` | `serve`, `keys`, `sessions`, `decide` | Path to config file (default: `~/.denkeeper/denkeeper.toml`) |
| `--help` | all commands | Print help |

There is no `--version` flag. Use the `denkeeper version` subcommand.

## `denkeeper serve`

Start the agent. Loads config, connects adapters, starts the scheduler, and optionally starts the REST API server.

```bash
denkeeper serve
denkeeper serve --config /etc/denkeeper/denkeeper.toml
```

## `denkeeper version`

Print the version, commit, build date, Go version, and platform.

```bash
denkeeper version
```

## `denkeeper keys`

Create and list API keys for the REST API and web dashboard.

```bash
denkeeper keys create dashboard --scopes admin,chat,sessions:read
denkeeper keys list
```

### `denkeeper keys create <name>`

The key name is a positional argument, not a flag.

| Argument / flag | Description |
|---|---|
| `<name>` | Key name (required, positional) |
| `--scopes`, `-s` | Comma-separated list of scopes (default: `admin`) |

The plaintext key is displayed once on creation and cannot be recovered.

### `denkeeper keys list`

Lists all API keys with their names, scopes, status, and creation dates. The key secret is never shown.

### Revoking a key

The CLI does not revoke keys — `keys` only has `create` and `list`. Revoke, permanently delete, and rotate through the REST API or the dashboard's API Keys page:

```bash
curl -X DELETE -H "Authorization: Bearer dk_..." \
  https://localhost:8080/api/v1/keys/{id}             # revoke
curl -X DELETE -H "Authorization: Bearer dk_..." \
  https://localhost:8080/api/v1/keys/{id}/permanent   # delete a revoked key
curl -X POST -H "Authorization: Bearer dk_..." \
  https://localhost:8080/api/v1/keys/{id}/rotate      # rotate
```

## `denkeeper sessions`

Manage conversation sessions stored in the memory database.

### `denkeeper sessions list`

List all sessions with metadata.

```bash
denkeeper sessions list
denkeeper sessions list --config /path/to/denkeeper.toml
```

Displays a table with session ID, adapter, external ID, message count, cost, and creation date.

### `denkeeper sessions show <session-id>`

Display all messages in a session.

```bash
denkeeper sessions show "telegram:12345"
```

Shows each message with timestamp, role, and content (truncated to 120 characters).

### `denkeeper sessions delete <session-id>`

Delete a session and all its messages.

```bash
denkeeper sessions delete "telegram:12345"
denkeeper sessions delete "telegram:12345" --yes
```

| Flag | Description |
|---|---|
| `--yes`, `-y` | Skip confirmation prompt |

### `denkeeper sessions export <session-id>`

Export a session transcript to stdout.

```bash
denkeeper sessions export "telegram:12345"
denkeeper sessions export "telegram:12345" --format json > session.json
```

| Flag | Description |
|---|---|
| `--format`, `-f` | Output format: `text` (default) or `json` |

### `denkeeper sessions prune`

Delete sessions older than a specified duration.

```bash
denkeeper sessions prune --older-than 720h
denkeeper sessions prune --older-than 720h --yes
```

| Flag | Description |
|---|---|
| `--older-than` | Duration threshold (e.g., `720h` for 30 days). Required. |
| `--yes`, `-y` | Skip confirmation prompt |

## `denkeeper decide`

Work with the decision models configured under `[[llm.deciders]]`.

### `denkeeper decide replay`

Re-score an agent's recorded supervisor reviews with a decider and report how often the two agree. Use it to pick `supervisor_decider_approve_at` and `supervisor_decider_deny_at` from existing audit history. It is read-only: nothing is written to the audit log and no approval outcome changes.

```bash
denkeeper decide replay --agent default
denkeeper decide replay --agent default --since 2026-09-01 --decider jev --limit 200
denkeeper decide replay --agent default --format json > replay.json
```

| Flag | Description |
|---|---|
| `--agent` | Agent whose supervisor reviews to replay. Required. |
| `--decider` | `[[llm.deciders]]` name (default: the agent's `supervisor_decider`) |
| `--since` | Replay reviews from this date, `2006-01-02` or RFC3339 (default: 30 days ago) |
| `--approve-at`, `--deny-at` | Thresholds for the agreement table (default: the agent's configured values) |
| `--limit` | Maximum reviews to replay, newest first (default: 500) |
| `--show` | Disagreements to list (default: 20) |
| `--concurrency` | Parallel decider calls (default: 4) |
| `--format`, `-f` | Output format: `text` (default) or `json` |

The report has four parts:

- **Agreement table:** the decider's verdict (approve, escalate, deny) against the supervisor's, at the chosen thresholds.
- **Threshold sweep:** for each `approve_at`, how many calls the decider would approve and how many of those the supervisor did not; for each `deny_at`, how many it would deny and how many of those the supervisor approved.
- **Disagreements:** decider approvals the supervisor denied or escalated come first, then decider denials the supervisor approved.
- **Latency and cost:** p50 and p95 per call, and the total provider-reported cost of the replay.

Two things to know before running it:

- **Egress:** each replayed review sends its tool arguments and the recent messages before it to the decider's provider, and each call is billed.
- **Thinner input than a live review:** the audit log does not record the tool description, server guidance, or skill context, and a conversation that was cleared, compacted, or pruned has no user request to recover. Treat replay as indicative. The `source = "decider:<name>"` audit events written by shadow mode are the authoritative comparison.

Only reviews that ended in a supervisor verdict are replayed. Failed reviews and calls that went straight to a human are not in the set.

## `denkeeper passwd`

Generate a bcrypt hash for dashboard password login.

```bash
denkeeper passwd
```

Reads the password interactively with confirmation. Also accepts piped stdin for scripted use:

```bash
echo "my-password" | denkeeper passwd
```

Outputs a bcrypt hash (cost 13) suitable for the `api.auth.password_hash` config field.

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
