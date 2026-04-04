---
title: "First Run"
description: "Create your first Denkeeper configuration and connect to Telegram."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
weight: 20
toc: true
---

## Setup wizard

The easiest way to create your initial configuration is the interactive setup command:

```bash
denkeeper setup
```

This walks you through:

1. Choosing an LLM provider (OpenRouter, Anthropic, Ollama, or OpenAI)
2. Entering your API key
3. Configuring a Telegram bot token
4. Setting your Telegram user ID for the allowlist

The wizard writes `~/.denkeeper/denkeeper.toml` and creates the data directory.

## Manual configuration

If you prefer to configure manually, copy the example file:

```bash
mkdir -p ~/.denkeeper
cp denkeeper.toml.example ~/.denkeeper/denkeeper.toml
```

Edit the file and fill in at minimum:

```toml
[telegram]
token = "YOUR_TELEGRAM_BOT_TOKEN"
allowed_users = [YOUR_TELEGRAM_USER_ID]

[llm]
default_provider = "openrouter"
default_model = "anthropic/claude-sonnet-4-20250514"

[llm.openrouter]
api_key = "YOUR_OPENROUTER_API_KEY"
```

### Get a Telegram bot token

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow the prompts
3. Copy the token into your config

### Find your Telegram user ID

Message [@userinfobot](https://t.me/userinfobot) on Telegram. It replies with your numeric user ID.

## Start the agent

```bash
denkeeper serve
```

Send a message to your bot in Telegram. You should see a response within a few seconds.

## Web dashboard setup

The API and web dashboard are enabled by default. On first run, the web dashboard offers a streamlined setup flow.

When Denkeeper starts with no API keys and no password configured, it generates a **one-time setup PIN** and logs it to the console:

```
INFO FIRST-RUN SETUP PIN pin=482937
INFO Enter this PIN in the web dashboard to create your admin account.
```

Open the dashboard in your browser (default: `http://localhost:8080`) and you'll see two options:

1. **Create Account** (recommended) — enter the PIN from the logs and choose a password. This creates a password-based login for the dashboard and logs you in immediately.
2. **Create API Key** — creates a scoped API key for programmatic access. Useful for automation or headless setups.

The PIN is single-use and cleared after successful account creation. It is never exposed via any API endpoint — only in the server logs.

{{< callout type="info" >}}
The setup PIN protects against setup hijacking: an attacker with network access to the API port cannot create an account without also having access to the server logs.
{{< /callout >}}

## Logs

By default, Denkeeper logs to stderr at `info` level:

```bash
# Increase verbosity
denkeeper serve  # then edit denkeeper.toml: [log] level = "debug"
```

When running as a systemd service:

```bash
journalctl -u denkeeper -f
```

Next: [Configuration reference](/docs/reference/config/) for the full list of options.
