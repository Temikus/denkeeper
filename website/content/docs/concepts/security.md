---
title: "Security"
description: "Denkeeper's security model: threat model, permissions, and sandboxing."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-04-03T00:00:00+00:00
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
