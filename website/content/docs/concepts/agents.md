---
title: "Agents"
description: "Multi-agent routing, personas, and adapter bindings."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
weight: 10
toc: true
---

Denkeeper supports running multiple named agents within a single instance. Each agent has its own persona, skills, LLM model, and permission tier.

## Persona system

Each agent's identity is defined by persona files in its `persona_dir`:

| File | Purpose | Who updates it |
|---|---|---|
| `SOUL.md` | Core identity — personality, values, communication style | Agent (supervised requires approval; autonomous writes directly) |
| `USER.md` | What the agent knows about its user | Agent (user can edit directly) |
| `MEMORY.md` | Working memory — updated automatically each session | Agent |

These files are injected into the system prompt at the start of every conversation.

## Adapter bindings

Each agent declares which adapters it listens on:

```toml
[[agents]]
name = "default"
adapters = ["telegram"]           # all Telegram messages

[[agents]]
name = "work-assistant"
adapters = ["telegram:987654321"] # only this specific chat
```

The Dispatcher routes incoming messages to the correct agent based on these bindings. If no specific binding matches, messages go to the `"default"` agent.

## Per-agent configuration

Each agent can override the global LLM model and permission tier:

```toml
[[agents]]
name = "home-automation"
persona_dir = "~/.denkeeper/agents/home-automation"
adapters = ["discord"]
llm_model = "meta-llama/llama-3-70b"
session_tier = "restricted"
```

## Self-modification rules

Agents can modify their own persona files within their permission tier:

- **MEMORY.md** — freely writable (working memory)
- **USER.md** — writable in `supervised` and `autonomous` tiers
- **SOUL.md** — writable in `supervised` (requires approval) and `autonomous` tiers
