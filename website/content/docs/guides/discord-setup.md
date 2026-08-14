---
title: "Discord Setup"
description: "Connect Denkeeper to Discord with a bot application and user allowlist."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-14T00:00:00+00:00
draft: false
weight: 15
toc: true
---

## Create a Discord bot

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) and click **New Application**
2. Name it, then open the **Bot** tab and click **Add Bot**
3. Under **Privileged Gateway Intents**, enable **Message Content Intent** — without it the bot receives events but no message text
4. Click **Reset Token** and copy the token

## Invite the bot

From **OAuth2 → URL Generator**, select the `bot` scope and, at minimum, the **Send Messages**, **Read Message History**, and **View Channels** permissions. Open the generated URL to add the bot to your server.

For a personal agent, a private server with a single channel is usually the right shape. The adapter also subscribes to direct messages, so DMing the bot works without any server at all.

## Configure Denkeeper

```toml
[discord]
token = "YOUR_DISCORD_BOT_TOKEN"
allowed_users = ["YOUR_DISCORD_USER_ID"]
```

`allowed_users` holds Discord snowflake IDs — 17 to 19 digit numbers — **as strings**. Note the quotes: Telegram's equivalent takes bare integers, which is an easy mix-up.

Messages from anyone not on the list are silently dropped.

### Find your user ID

Enable **Settings → Advanced → Developer Mode**, then right-click your name and choose **Copy User ID**.

### Keep the token out of the config file

```bash
export DENKEEPER_DISCORD_TOKEN="..."
```

The environment variable takes precedence over the TOML value, which is the usual pattern for containers and Kubernetes.

## Features

**Typing indicator** — shown while the LLM is working, so a slow model does not look like a hung bot.

**Approval buttons** — approval requests arrive as messages with Approve/Deny action-row buttons; resolving one is a click, not a reply.

{{< callout context="note" >}}
Voice transcription is Telegram-only — the Discord adapter has no voice/STT/TTS support.
{{< /callout >}}

{{< callout context="note" >}}
The Discord adapter does **not** register slash commands. Telegram publishes `/start`, `/help`, `/clear` and the rest into its command picker, and adds skill `command:` triggers to it; on Discord those commands are not advertised.

Skills with `command:` triggers still work — send the command as an ordinary message.
{{< /callout >}}

## Routing

Bind an agent to Discord the same way as any other adapter:

```toml
[[agents]]
name = "default"
adapters = ["discord"]                    # every allowed Discord chat

[[agents]]
name = "work"
adapters = ["discord:YOUR_CHANNEL_ID"]    # one specific channel
```

A specific binding beats a wildcard, so the two above coexist.

To share one conversation between Discord and Telegram — start something on your phone, continue at your desk — bind both to a single channel:

```toml
[[channels]]
name = "main"
agent = "default"
adapters = ["telegram:987654321", "discord:YOUR_CHANNEL_ID"]
```

See [Channels](/docs/concepts/channels/) for how routing resolves.
