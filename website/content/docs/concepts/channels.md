---
title: "Channels"
description: "Named routing endpoints that decouple conversations from adapter bindings."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-14T00:00:00+00:00
draft: false
weight: 15
toc: true
---

A channel is a named routing endpoint. It points at one agent, and it may bind several adapters at once.

Without channels, a conversation is pinned to a single agent–adapter pair: this Telegram chat talks to that agent, permanently. Channels break that coupling, which buys two things — one conversation can be reached from more than one adapter, and you can switch which agent a chat is talking to without editing config and restarting.

## Configuration

```toml
[[channels]]
name = "work"
agent = "work-assistant"
adapters = ["telegram:987654321", "discord:YOUR_CHANNEL_ID"]
```

Both bindings above address the *same* conversation. A message sent from Telegram and a message sent from Discord land in one shared history, so you can start something on your phone and continue it from your desktop.

If you declare no `[[channels]]` at all, Denkeeper synthesizes one per agent from that agent's `adapters` list. Existing configs therefore keep working untouched — synthesized channels are marked implicit and hidden from `/session` listings.

## Conversation IDs

A channel's conversation ID is `chan:{name}` — so the `work` channel above stores its history under `chan:work`.

With `session_mode = "ephemeral"`, each interaction gets a fresh ID of the form `chan:{name}:{unix_nano}` instead, so nothing is remembered between interactions:

```toml
[[channels]]
name = "kiosk"
agent = "default"
adapters = ["telegram"]
session_mode = "ephemeral"
```

Ephemeral channels cannot bind multiple adapters — there would be no shared conversation to share. That combination is rejected at config validation rather than at runtime.

## Switching channels at runtime

`/session` is handled by the dispatcher itself, before routing. It never reaches an agent, so it costs no tokens.

| Command | Effect |
|---|---|
| `/session` | List the channels reachable from this chat |
| `/session <name>` | Switch this chat to that channel |
| `/session reset` | Clear the override and fall back to normal routing |

Switching is persisted, so a restart does not silently move a chat back to a different agent. Each switch also emits a `session` audit event recording the channel, adapter, and agent.

## Resolution priority

When a message arrives, the dispatcher picks a channel in this order:

1. **Active override** — a `/session` selection for this adapter and chat
2. **Specific binding** — a channel bound to `adapter:externalID`
3. **Wildcard binding** — a channel bound to the bare `adapter`
4. **Legacy fallback** — the pre-channels agent-adapter resolution, ending at the `"default"` agent

A specific binding always beats a wildcard, which is what makes the common pattern work: a wildcard channel catches everything, and one specific channel peels off a single chat.

## Delivery for scheduled messages

When a schedule fires on a channel with several bindings, `delivery` decides where the output goes:

| Value | Behaviour |
|---|---|
| `"single"` *(default)* | Deliver through the first specific binding |
| `"broadcast"` | Deliver through every specific binding |

Wildcard bindings are not delivery targets — there is no single chat to send to.

## Managing channels

Channels are reachable from every surface:

- **REST** — `GET/POST/PATCH/DELETE /api/v1/channels`, plus `POST/DELETE /api/v1/channels/{name}/activate` to set the active override for an adapter key. See the [REST API reference](/docs/reference/rest-api-reference/).
- **Config MCP** — agents can call `channel_list`, `channel_info`, and `channel_switch`.
- **Dashboard** — the Channels page.

See the [configuration reference](/docs/reference/config/) for the full `[[channels]]` schema.
