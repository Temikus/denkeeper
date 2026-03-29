---
title: "Installation"
description: "How to install the Denkeeper binary on Linux and macOS."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
weight: 10
toc: true
---

## Quick install

The fastest way to install Denkeeper is the one-liner script. It detects your OS and architecture, downloads the latest release, verifies the SHA256 checksum, and installs to `/usr/local/bin`:

```bash
curl -fsSL https://get.denkeeper.io | sh
```

To pin a specific version:

```bash
curl -fsSL https://get.denkeeper.io | sh -s -- --version v0.0.1
```

## Homebrew (macOS)

```bash
brew install Temikus/tap/denkeeper
```

## Linux packages

`.deb` and `.rpm` packages are published with each release. They include a systemd service unit and create a `denkeeper` system user automatically.

### Debian / Ubuntu

```bash
# Download the .deb for your architecture from the latest release
curl -LO https://github.com/Temikus/denkeeper/releases/latest/download/denkeeper_VERSION_linux_amd64.deb
sudo dpkg -i denkeeper_VERSION_linux_amd64.deb
```

### RHEL / Fedora

```bash
curl -LO https://github.com/Temikus/denkeeper/releases/latest/download/denkeeper_VERSION_linux_amd64.rpm
sudo rpm -i denkeeper_VERSION_linux_amd64.rpm
```

After installing via `.deb` or `.rpm`, follow the post-install instructions printed to your terminal to configure and start the service.

## Docker

Multi-architecture images are published to GitHub Container Registry:

```bash
docker pull ghcr.io/temikus/denkeeper:latest
```

Run with a bind-mounted config:

```bash
docker run -d \
  --name denkeeper \
  -v ~/.denkeeper:/data \
  ghcr.io/temikus/denkeeper:latest
```

The container reads config from the `DENKEEPER_CONFIG` env var (default `/data/denkeeper.toml`). Override with `-e DENKEEPER_CONFIG=/path/to/config.toml`.

## Helm (Kubernetes)

A Helm chart is available in the repository:

```bash
helm install denkeeper deploy/helm/denkeeper/ \
  --set secrets.llmAnthropicApiKey=sk-ant-... \
  --set secrets.telegramToken=123456:ABC...
```

The chart supports Ingress, PVC persistence, secrets management (or `existingSecret` for external secret managers), and security-hardened pod defaults. See [`deploy/helm/denkeeper/values.yaml`](https://github.com/Temikus/denkeeper/blob/main/deploy/helm/denkeeper/values.yaml) for all options.

## From source

Requires Go 1.25+ and Node.js 24+ (for the web dashboard build):

```bash
git clone https://github.com/Temikus/denkeeper.git
cd denkeeper
just build-full   # builds web UI then the binary
./pkg/bin/denkeeper --version
```

## Verify your installation

```bash
denkeeper --version
```

Next: [First run](/docs/getting-started/first-run/) to create your configuration.
