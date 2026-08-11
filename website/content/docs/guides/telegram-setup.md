---
title: "Telegram Setup"
description: "Set up the Telegram adapter with BotFather, allowlists, and commands."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-11T00:00:00+00:00
draft: false
weight: 10
toc: true
---

## Create a Telegram bot

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot`
3. Choose a display name (e.g., "My Denkeeper")
4. Choose a username (must end in `bot`, e.g., `my_denkeeper_bot`)
5. Copy the token BotFather gives you

## Configure Denkeeper

```toml
[telegram]
token = "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
allowed_users = [YOUR_USER_ID]
```

Find your user ID by messaging [@userinfobot](https://t.me/userinfobot).

## Features

### Typing indicator

While the LLM is processing, the bot shows a "typing..." indicator in the chat. This is sent immediately after receiving your message and refreshed every 4 seconds until the response is ready.

### Slash commands

Denkeeper registers these built-in commands with Telegram's command picker:

| Command | Description |
|---|---|
| `/start` | Start a conversation |
| `/help` | Show help and available commands |
| `/debug` | Toggle verbose approval messages |
| `/stop` | Cancel the current request |
| `/panic` | Emergency stop — cancels all in-flight requests and pauses the scheduler |
| `/resume` | Resume after an emergency stop |
| `/clear` | Clear session history (the session itself survives) |
| `/compact` | Replace history with a single summary message |

`/session` is also handled, though it is not registered in the command picker. Send `/session` to list the channels available to you, or `/session <name>` to switch the current chat to another channel. The selection persists across restarts.

Skills with `command:` triggers (e.g., `triggers = ["command:briefing"]`) are automatically registered as `/briefing` in Telegram's command menu alongside the built-ins.

### Voice messages

With the `[voice]` section configured, the bot transcribes incoming voice messages to text using OpenAI's Whisper API, and can optionally reply with synthesized speech.

### Inline keyboards

Approval requests are delivered as messages with Approve/Deny inline buttons. The user taps a button to resolve the request without typing.

## Multiple agents

You can bind different agents to different Telegram chats:

```toml
[[agents]]
name = "default"
adapters = ["telegram"]            # all other chats

[[agents]]
name = "work"
adapters = ["telegram:987654321"]  # this specific chat only
```

A specific binding beats a wildcard, so the two above coexist.

To share one conversation between Telegram and [Discord](/docs/guides/discord-setup/), or to switch a chat between agents at runtime with `/session`, see [Channels](/docs/concepts/channels/).
