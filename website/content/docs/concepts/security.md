---
title: "Security"
description: "Denkeeper's security model: threat model, permissions, and sandboxing."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
weight: 50
toc: true
---

## Design philosophy

Every capability in Denkeeper is opt-in. The agent starts with zero permissions and gains them through explicit configuration. This is a fundamental difference from most AI agent frameworks.

## Threat model

| Threat | Mitigation |
|---|---|
| Prompt injection via incoming messages | Tiered permissions limit what the agent can do; supervised mode requires approval for sensitive actions |
| Unauthorized access to the bot | Telegram: `allowed_users` allowlist. Discord: `allowed_users` allowlist. API: scoped bearer tokens |
| Tool abuse | Permission tiers control tool access; restricted mode limits to read-only tools |
| Cost runaway | Per-session budget caps, global cost tracking, automatic fallback to cheaper models |
| Plugin escape | Subprocess isolation now, Docker sandboxing planned |
| Config file secrets | File permissions (`0o600`), environment variable expansion for secrets |

## Adapter security

Both adapters enforce user allowlists:

```toml
[telegram]
allowed_users = [123456789]   # numeric Telegram user IDs

[discord]
allowed_users = ["123456789012345678"]  # Discord snowflake IDs
```

Messages from unlisted users are silently dropped.

## API security

The REST API uses scoped bearer tokens with constant-time comparison:

- Each key has a name and list of scopes (e.g., `chat`, `sessions:read`, `approvals:write`)
- Per-key rate limiting via token bucket
- Optional TLS with configurable cert/key
- CORS origin allowlist

## systemd hardening

The `.deb`/`.rpm` packages include a hardened systemd service unit:

- `ProtectSystem=strict` — read-only filesystem except `/var/lib/denkeeper`
- `NoNewPrivileges=true` — no privilege escalation
- `CapabilityBoundingSet=` — no Linux capabilities
- `SystemCallFilter=@system-service` — restricted syscalls
- `MemoryDenyWriteExecute=true` — W^X enforcement
- `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX` — no raw sockets
