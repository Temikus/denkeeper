---
title: "Security"
description: "Denkeeper's security model: threat model, permissions, and sandboxing."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-14T00:00:00+00:00
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
| Plugin escape | Subprocess isolation and sandboxed execution via Docker (`--cap-drop ALL`, `--read-only`, `--network none`) or Kubernetes (ephemeral Pods with init-container network isolation, Pod Security Admission, optional gVisor/Kata); Ed25519 signature verification |
| Config file secrets | File permissions (`0o600`), environment variable expansion for secrets |
| SSRF via remote MCP tool servers or the browser tool | Connect-time IP blocking of link-local, loopback, and cloud-metadata addresses, plus an optional hostname allowlist |
| Secret leakage into stdio MCP subprocesses | Children do not inherit the parent environment — they get a fixed non-secret allowlist; any `DENKEEPER_*` or otherwise denylisted name is blocked even if explicitly passed through |
| Skill file writes escaping the skills directory | Skill writes are confined to the agent's skills directory at the OS level (Go's `os.Root`), backstopping name validation against `..` traversal and symlink escapes |

Every consequential decision — tool calls, approvals, supervisor verdicts, config changes — is recorded in the audit log. See [Observability](/docs/concepts/observability/) for what is captured and how to query it.

## Adapter security

Both adapters enforce user allowlists:

```toml
[telegram]
allowed_users = [123456789]                # bare integers

[discord]
allowed_users = ["YOUR_DISCORD_USER_ID"]   # snowflake IDs, quoted as strings
```

Messages from unlisted users are silently dropped.

See the [Telegram](/docs/guides/telegram-setup/) and [Discord](/docs/guides/discord-setup/) guides for how to find your own ID on each platform.

## API security

The REST API uses scoped bearer tokens with constant-time comparison:

- Each key has a name and list of scopes (e.g., `chat`, `sessions:read`, `approvals:write`)
- Per-key rate limiting via token bucket
- Optional TLS with configurable cert/key
- CORS origin allowlist

## MCP tool security

Denkeeper connects outward to MCP servers to give agents external tools (see [Tools](/docs/concepts/tools/)). A few isolation properties apply specifically to that boundary:

**OAuth 2.1 for remote servers** — a tool can be configured with `auth = "oauth"` (plus optional `client_id`/`client_secret`/`scopes`) to authenticate against a remote SSE MCP server using the standard Authorization Code flow. Tokens are stored in SQLite, not the config file, and the callback routes live at `/api/v1/tools/{name}/oauth/...`.

**Stdio subprocess environment scoping** — a stdio MCP server does not inherit Denkeeper's process environment. It receives only a built-in non-secret allowlist (`PATH`, `HOME`, language-runtime variables, etc.) plus the tool's own configured `env`. `env_passthrough` on `[mcp]` or a specific `[tools.*]` entry can forward additional names, but a hard denylist — every `DENKEEPER_*` variable plus a fixed list of other secret-shaped names — is enforced even on explicit passthrough entries, so a misconfigured tool cannot pull API keys or tokens out of the parent process.

**Tool name collisions never silently merge** — if two connected servers advertise the same tool name, neither keeps the bare name. Both are re-advertised as `<server>__<tool>` and the bare name becomes unroutable, so a call can never be silently misrouted to the wrong server's tool.

**Skill file writes are hardened** — every skill create/update/delete goes through an OS-level `os.Root` confined to the agent's skills directory (blocking `..` traversal and symlink escapes), writes land via a randomized, exclusively-created temp file and an atomic rename, and a configurable per-file byte cap (`[skills] max_bytes`, 1 MiB by default) bounds how much a single skill write can grow the file.

## Dashboard authentication

The web dashboard and REST API support two authentication mechanisms that can be used independently or together.

### Bearer tokens

Existing API key authentication (`Authorization: Bearer dk_...`). Keys are scoped and managed via `denkeeper keys`. See [API security](#api-security) above.

### Session cookies

Session-based authentication for the web dashboard. Cookies are AES-256-GCM encrypted with `HttpOnly`, `Secure`, and `SameSite=Lax` attributes. The encryption key is configured via `api.auth.session_secret` (a 64-character hex string).

### Password login

Local password authentication using bcrypt (cost 13). Generate the hash with `denkeeper passwd` and set it in `api.auth.password_hash`. Login attempts are rate limited to 5 per 15 minutes per IP address. CSRF protection is enforced via Origin header validation on `POST /auth/login`.

### OIDC single sign-on

OpenID Connect authentication using Authorization Code flow with PKCE (S256 challenge method). Configure under `[api.auth.oidc]`. Requirements:

- The OIDC provider must return an `email_verified: true` claim.
- The user's email must appear in the `allowed_emails` list (case-insensitive matching).
- A nonce is included in the authorization request and verified in the ID token.

Supported providers include any standard OIDC-compliant identity provider (Google, Okta, Auth0, Keycloak, etc.).

## systemd hardening

The `.deb`/`.rpm` packages include a hardened systemd service unit:

- `ProtectSystem=strict` — read-only filesystem except `/var/lib/denkeeper`
- `NoNewPrivileges=true` — no privilege escalation
- `CapabilityBoundingSet=` — no Linux capabilities
- `SystemCallFilter=@system-service` — restricted syscalls
- `MemoryDenyWriteExecute=true` — W^X enforcement
- `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX` — no raw sockets
