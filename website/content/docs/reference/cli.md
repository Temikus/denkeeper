---
title: "CLI Reference"
description: "Denkeeper command-line interface reference."
slug: "cli"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-11-23T00:00:00+00:00
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
| `denkeeper kv` | Maintain the agents' key-value store |
| `denkeeper decide` | Calibrate decision models against recorded supervisor reviews |
| `denkeeper audit` | Check the audit log's hash chain |
| `denkeeper passwd` | Generate a bcrypt hash for dashboard login |
| `denkeeper plugin` | Ed25519 plugin signing |

## Flags

`--config` / `-c` is accepted by the commands that read the config file — `serve`, `keys`, `sessions`, `kv`, `decide`, and `audit`. It is not a root-level flag, so `denkeeper --config ... <command>` will not work; put it after the subcommand instead.

| Flag | Available on | Description |
|---|---|---|
| `--config PATH`, `-c` | `serve`, `keys`, `sessions`, `kv`, `decide`, `audit` | Path to config file (default: `~/.denkeeper/denkeeper.toml`) |
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

## `denkeeper kv`

Maintain the key-value store the agents write with `kv_set`. It lives in the memory database, and `--config`/`-c` picks the config file as for `sessions`.

### `denkeeper kv backfill-ttl`

Give existing keys the expiry [`[kv] default_ttl`](/docs/reference/config/) gives new writes. Every key that has no expiry and matches a `default_ttl` prefix gets `expires_at = updated_at + ttl`, whoever wrote it: the agent's `kv_set`, `kv_set_nx` locks, the dashboard, the REST API or the external MCP server. The store does not record which writer stored a key, so keys deliberately stored without an expiry get one too, such as a `kv_set` with `ttl: "0s"` or a value you saved from the dashboard. Prefixes match the way `kv_set` matches them: longest prefix wins, case-sensitive. Keys that already have an expiry are left alone, as are keys under a prefix with no default. Values, `created_at` and `updated_at` do not change.

```bash
denkeeper kv backfill-ttl --dry-run
denkeeper kv backfill-ttl
denkeeper kv backfill-ttl --yes
```

A dry run prints one row per agent and namespace and writes nothing:

```
AGENT   NAMESPACE  TTL   KEYS  ALREADY EXPIRED
pamela  cache:     168h  12    3
pamela  log:       720h  214   40
Dry run: nothing was written.
```

`ALREADY EXPIRED` counts keys whose new expiry is in the past. Those keys disappear from reads as soon as the backfill runs, and the next cleanup deletes them, so this cannot be undone. Without `--dry-run` the command prints the same table, warns how many keys will disappear when any are already past their new expiry, and asks for confirmation before writing. `--yes` skips only the question; the warning still prints. If stdin closes with no answer, as in a cron job, it writes nothing; a piped `y` or `yes` counts as confirmation. Running it again prints `No keys need a TTL.` It is safe to run while `denkeeper serve` is up: the backfill takes the database write lock before it reads, so a server write in the meantime waits for it, for up to 5 seconds, instead of failing it. The lock is held for the whole write pass, so with a very large key set or slow storage a server write can still time out; run it when the server is quiet.

| Flag | Description |
|---|---|
| `--dry-run` | Show what would change and write nothing |
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

## `denkeeper audit`

Inspect the audit log (`audit.db`, next to the memory database).

### `denkeeper audit verify`

Walks the audit log in id order and checks every event against its hash and the hash of the event before it. Read-only, and safe while `denkeeper serve` is running.

```bash
denkeeper audit verify
denkeeper audit verify --config /etc/denkeeper/denkeeper.toml
```

An intact log:

```
Audit log: /home/me/.denkeeper/data/audit.db
OK: 48211 events verified (ids 1-48211).
Chain start: event 1 is the first chained event.
Head: id 48211 hash 9f2c…
```

A broken one (an event was changed after it was written), which names the first event that fails. Its chain starts at id 16342 because retention pruned the events before it, which is normal:

```
Audit log: /home/me/.denkeeper/data/audit.db
BROKEN at event 20417 (2026-11-20T08:14:03.512Z tool_call/execute): the event does not match its stored hash. It was changed after it was written.
  expected hash: 5a5e…
  stored hash:   b8a1…
Verified before the break: 4075 events (ids 16342-20416).
Not checked: 27794 events after id 20417.
```

The other breaks are an event whose `prev_hash` does not match the event before it (events removed, added or reordered), an event with no hash after chained ones, and a first chained event that points at earlier events no prune checkpoint accounts for (events removed outside retention).

Events written before this version have no hash. They are reported as not covered and age out with `[audit] retention_days`. If no event has a hash at all, the output starts `NOT COVERED`: that is either a log no hashing version has written to, or one whose hashes were all removed, and the two look the same.

| Exit code | Meaning |
|---|---|
| `0` | The chain verified, or the log has no events |
| `1` | The chain is broken, or the command failed (for example, no `audit.db`) |
| `2` | No event has a hash, so nothing was verified |

Exit code `1` covers both a broken chain and a command that could not run. To tell them apart in a script, check whether the output contains `BROKEN`.

An empty log also exits `0`. If the log has ever had events, an empty table means it was emptied outside denkeeper.

What the chain cannot catch, as the [Observability](/docs/concepts/observability/#integrity) page explains: removing the newest events, a full rewrite of the file with recomputed hashes, and a forged prune checkpoint. A saved `Head` line covers the first two, but only for events up to the saved one: events written after it need a later saved line.

#### Checking a saved Head line

Copy the `Head` line somewhere other than this machine, then check it on a later run:

1. Run `denkeeper audit verify`. The new `Head` id must be at least the saved id.
2. The saved event must still exist with the same hash:

   ```bash
   sqlite3 ~/.denkeeper/data/audit.db "SELECT hash FROM audit_events WHERE id = <saved id>"
   ```

   The output must equal the saved hash. A different hash, or a smaller `Head` id, means events were rewritten or removed after you saved the line.

Once retention has pruned the saved event, this check no longer works. A prune checkpoint records only the last event it removed (`pruned_through_id` and `pruned_through_hash` in its detail), so it matches your saved line only if that was the same event. Save a new `Head` line more often than `[audit] retention_days` (weekly, with the default 30 days).

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
| `--output`, `-o` | Output directory for key files (default: current directory) |

Creates two files: `<name>.pub` (public key, PEM) and `<name>.key` (private key, PEM, mode 0600).

### `denkeeper plugin sign <binary>`

Sign a plugin binary with an Ed25519 private key.

```bash
denkeeper plugin sign ./my-plugin --key my-plugin.key
```

| Flag | Description |
|---|---|
| `--key`, `-k` | Path to private key file (required) |

Creates a detached signature file `<binary>.sig`.

### `denkeeper plugin verify <binary>`

Verify a plugin binary's signature against one or more public keys.

```bash
denkeeper plugin verify ./my-plugin --key my-plugin.pub
denkeeper plugin verify ./my-plugin --key key1.pub --key key2.pub
```

| Flag | Description |
|---|---|
| `--key`, `-k` | Path to public key file (required, repeatable) |

Exits with code 0 if the signature is valid for any of the provided keys.
