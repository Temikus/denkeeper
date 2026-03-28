---
title: "Raspberry Pi Deployment"
description: "Deploy Denkeeper on a Raspberry Pi 5."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
weight: 20
toc: true
---

Denkeeper is designed to run on a Raspberry Pi 5 as its primary target. The static Go binary has a small memory footprint and starts instantly.

## Requirements

- Raspberry Pi 5 (primary target) or Pi 4 (best-effort)
- Raspberry Pi OS (64-bit) or any `linux/arm64` distribution
- Network access for LLM API calls and Telegram/Discord

## Install

Use the install script:

```bash
curl -fsSL https://get.denkeeper.io | sh
```

Or install the `.deb` package:

```bash
curl -LO https://github.com/Temikus/denkeeper/releases/latest/download/denkeeper_VERSION_linux_arm64.deb
sudo dpkg -i denkeeper_VERSION_linux_arm64.deb
```

## Configure

```bash
sudo cp /etc/denkeeper/denkeeper.toml.example /etc/denkeeper/denkeeper.toml
sudo nano /etc/denkeeper/denkeeper.toml
```

Fill in your Telegram token, user ID, and LLM API key.

## Run as a service

The `.deb` package installs a systemd service automatically:

```bash
sudo systemctl enable --now denkeeper
```

Check status:

```bash
sudo systemctl status denkeeper
journalctl -u denkeeper -f
```

## Resource usage

Denkeeper is a single static binary (~30 MB). Typical idle memory usage is under 50 MB. LLM calls are offloaded to cloud providers, so the Pi handles only message routing, conversation storage, and tool execution.

## Using Ollama locally

For fully offline operation, you can run [Ollama](https://ollama.ai/) on the Pi and configure Denkeeper to use it:

```toml
[llm]
default_provider = "ollama"
default_model = "llama3.2:3b"

[llm.ollama]
base_url = "http://localhost:11434"
```

Note: local models on a Pi 5 will be significantly slower than cloud providers. Consider using fallback strategies to balance speed and cost.
