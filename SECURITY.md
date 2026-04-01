# Security Policy

## Supported Versions

Denkeeper is pre-1.0 software. Security fixes are applied to the **latest release only**.

| Version | Supported          |
|---------|--------------------|
| latest  | :white_check_mark: |
| < latest| :x:                |

Upgrade to the latest release to receive all security patches.

## Reporting a Vulnerability

**Please do not open a public issue for security vulnerabilities.**

Use [GitHub Private Vulnerability Reporting](https://github.com/Temikus/denkeeper/security/advisories/new) to submit a report. This keeps the details confidential until a fix is available.

### What to include

- Description of the vulnerability and its impact.
- Steps to reproduce or a proof-of-concept.
- Affected version(s) and component (e.g., API server, plugin sandbox, adapter).
- Suggested fix, if you have one.

### What to expect

- **Acknowledgement** within 48 hours.
- **Status update** within 7 days with an assessment and expected timeline.
- A coordinated disclosure after the fix is released. Credit is given unless you prefer to remain anonymous.

If you do not receive a response within 48 hours, please email the maintainer directly (see the commit log for contact details).

## Security Architecture

Denkeeper has several layers of security controls:

### Permission Tiers

Every agent runs in one of three tiers:

- **Restricted** — read-only, no tool use, no mutations.
- **Supervised** — mutations require human approval via inline keyboard buttons (Telegram/Discord) or the REST API.
- **Autonomous** — full access (use with caution).

### Plugin Sandboxing

Plugins execute in isolated environments via the `sandbox.Runtime` interface:

- **Docker** — `--cap-drop ALL`, `--read-only`, `--security-opt no-new-privileges`, `--network none` by default.
- **Kubernetes** — ephemeral pods with init-container iptables isolation, PSA baseline/restricted labels, optional gVisor/Kata RuntimeClass.

### Plugin Signing

Ed25519 signature verification for subprocess plugin binaries. Configurable via `[security]` with `trusted_keys` and `allow_unsigned`.

### API Server

- Bearer token authentication with scoped API keys and constant-time comparison.
- Per-key token-bucket rate limiting.
- Configurable CORS origins.
- Optional TLS termination.

### Supply Chain

- Release binaries are signed with [cosign](https://github.com/sigstore/cosign) (keyless OIDC).
- Docker images include SLSA build provenance attestations.
- SBOMs are generated for every release.
- Dependabot monitors Go modules, npm packages, Docker base images, and GitHub Actions.

### CI Security Scanning

- **gosec** (SAST) — results uploaded to GitHub Security tab via SARIF.
- **Gitleaks** — secret detection on push and PR.
- **Grype** (Anchore) — filesystem and container image vulnerability scanning.
- **govulncheck** — Go vulnerability database checks.
- **Secret scanning** and **push protection** are enabled on this repository.
