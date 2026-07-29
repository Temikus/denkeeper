---
title: "Telegram Setup"
description: "Set up the Telegram adapter with BotFather, allowlists, and commands."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
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

Denkeeper registers commands with Telegram's command picker:

- `/start` — welcome message
- `/help` — list available commands

Skills with `command:` triggers (e.g., `triggers = ["command:briefing"]`) are automatically registered as `/briefing` in Telegram's command menu.

### Message formatting

Agent replies are written as Markdown and delivered with Telegram's `parse_mode=HTML`. Denkeeper parses the reply as CommonMark — the same dialect the web dashboard renders — and re-emits it using the tags Telegram supports:

| Markdown | Telegram |
|---|---|
| `**bold**`, `*italic*`, `~~strike~~` | bold, italic, strikethrough |
| `` `code` ``, fenced blocks | monospace, syntax-highlighted block |
| `[label](url)`, bare URLs | clickable links |
| `# Heading` | bold line |
| `- item`, `1. item` | `•` / numbered lines (Telegram has no list markup) |
| `> quote` | block quote |
| tables | left as plain pipe-delimited text |

Every character that is not markup is HTML-escaped before sending, so underscores in URLs, `snake_case` identifiers, asterisks and brackets reach the reader exactly as the agent wrote them. Earlier releases used Telegram's legacy `Markdown` parse mode, which silently deleted such characters and mis-applied italics across a message ([#243](https://github.com/Temikus/denkeeper/issues/243)).

If Telegram rejects the formatted message for any reason, the adapter automatically resends the reply as unformatted plain text rather than dropping it.

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
