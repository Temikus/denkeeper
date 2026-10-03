---
title: "First Run"
description: "Create your first Denkeeper configuration and connect to Telegram."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-10-03T00:00:00+00:00
draft: false
weight: 20
toc: true
---

There are two ways to get started: write a minimal config by hand and start the agent, or start with an empty config and finish setup in the web dashboard's guided wizard.

## Configuration

Copy the example file:

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

{{< callout context="note" >}}
The setup PIN protects against setup hijacking: an attacker with network access to the API port cannot create an account without also having access to the server logs.
{{< /callout >}}

See the [Web Dashboard guide](/docs/guides/web-dashboard/) for what each page does.

Once you're logged in, the dashboard's setup wizard takes over. An empty config file is enough: Denkeeper starts with no provider and no agent so the wizard can create them. It has four steps, and writes each one to your TOML file as you go:

1. **Connect a provider.** Pick Anthropic, OpenAI, OpenRouter or Ollama. The key is checked when you paste it.
2. **Create an agent.** Choose a name, a model from the provider's list, and a permission tier. Supervised agents get a supervisor that checks tool calls first.
3. **Give it a personality.** Set a display name, an emoji, a tone, and the house rules it follows.
4. **Connect a chat app** (optional). Paste a Telegram bot token, then send your bot any message: the wizard reads your user ID from it, so you don't have to look it up. Chat apps start after a restart.

Everything except the chat app works straight away; you can chat in the dashboard as soon as the agent exists. Progress is saved on the server, so you can close the tab and resume later, from any browser.

If you choose **Set up later**, the Overview page shows what's left and a minimal `denkeeper.toml` you can use instead. After editing the file, press **Reload** on the Server page.

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
