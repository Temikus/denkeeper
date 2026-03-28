---
title: "Denkeeper"
description: "Your AI agent. Your rules. Your hardware."
lead: "A security-first, single-binary personal AI agent that runs on a Raspberry Pi."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
seo:
  title: "Denkeeper — Security-first personal AI agent"
  description: "Single-binary personal AI agent with multi-agent routing, cost tracking, and tiered permissions. Connects to Telegram and Discord. Runs on a Raspberry Pi."
---

## Your AI agent. Your rules. Your hardware.

Denkeeper is a single-binary personal AI agent designed for people who want full control over their AI assistant. It connects to Telegram and Discord, routes messages through LLM providers (Anthropic, OpenRouter, Ollama), and runs comfortably on a Raspberry Pi 5.

### Why Denkeeper?

- **Security first** — Tiered permissions (autonomous, supervised, restricted). Every capability is opt-in. Approval workflows for sensitive actions.
- **Cost aware** — Per-session and global budget tracking. Automatic fallback to cheaper models when funds are low.
- **Single binary** — One file, one TOML config, SQLite storage. No Docker required to run (but supported).
- **Multi-agent** — Run multiple agents with distinct personas, skills, and adapter bindings in a single instance.
- **Extensible** — Three extension tiers: skills (markdown), tools (MCP servers), and plugins (subprocess/Docker).

### Get Started

Install with a single command:

```bash
curl -fsSL https://get.denkeeper.io | sh
```

Or via Homebrew:

```bash
brew install Temikus/tap/denkeeper
```

Or grab a `.deb`/`.rpm` package from the [releases page](https://github.com/Temikus/denkeeper/releases).

Then [follow the getting started guide](/docs/getting-started/installation/) to configure your first agent.
