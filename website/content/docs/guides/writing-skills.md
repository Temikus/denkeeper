---
title: "Writing Skills"
description: "How to create custom skills for your Denkeeper agent."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-09-04T00:00:00+00:00
draft: false
weight: 30
toc: true
---

Skills are markdown files that teach the agent how to handle specific tasks. They are the simplest way to extend Denkeeper.

## Anatomy of a skill

Create a file in `~/.denkeeper/skills/`:

```markdown
+++
name = "expense-tracker"
description = "Track and categorize expenses"
version = "1.0.0"
triggers = ["command:expense"]

[requires]
tools = []
+++

## Instructions

When the user reports an expense:

1. Extract the amount, currency, and category
2. Confirm the details with the user
3. Store it in your memory for the weekly summary

## Categories

- Food & dining
- Transport
- Entertainment
- Utilities
- Other

## Response format

Always acknowledge with: "Logged: $AMOUNT for CATEGORY"
```

## Frontmatter fields

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Unique identifier |
| `description` | Yes | What the skill does (shown in Telegram command menu for command triggers) |
| `version` | No | Semantic version |
| `triggers` | No | When to activate (see below) |
| `requires.tools` | No | MCP tools this skill needs. The skill is skipped while any of them is unavailable (see below) |
| `max_tool_rounds` | No | Cap on tool-call rounds for turns this skill drives (command or schedule trigger). Only lowers the agent's limit, never raises it; must be ≥ 0 |

## Trigger types

- **`command:name`** — activates when the user's message starts with `/name` or `!name` (case-insensitive), on any adapter or channel — Telegram, Discord, the web dashboard, or the REST chat API
- **`schedule:...`** — marks the skill as scheduler-driven. The text after the colon is ignored; timing comes from a `[[schedules]]` entry whose `skill` field names this skill. Without one, the skill never fires
- **No triggers** — *ambient*: always included in the system prompt, and matched on every turn

The distinction between ambient and scheduled matters for `max_tool_rounds`: the cap applies only when a single skill explicitly drives the turn (a command match, or a schedule naming it). An ambient skill matches every message, so capping on it would throttle unrelated conversation — it is deliberately exempt.

## Tool requirements

A skill that names tools in `requires.tools` is only injected while **every** one of them is registered:

```toml
requires.tools = ["gmail_list", "gmail_send"]
```

If the MCP server hosting `gmail_list` is disconnected or disabled, the skill goes quiet — it is left out of the system prompt entirely rather than instructing the agent to call a tool that isn't there. Availability is re-checked on every message, so the skill comes back on its own the moment the server reconnects. No restart or config reload is involved.

Notes:

- Names are matched exactly as the MCP server advertises them (same as auto-approve rules). A typo means the skill never activates.
- This applies to scheduled skills too: a run whose tools are missing does nothing and logs a warning, rather than half-executing.
- Deactivation and reactivation are logged once per change, not once per message.
- A skill with no `requires.tools` is always active — the field is opt-in.

## Agent-specific skills

Place skills in an agent's persona directory to scope them:

```
~/.denkeeper/agents/work-assistant/skills/standup.md
```

Agent-specific skills are merged with global skills. Same-name agent skills override global ones.

## Testing a skill

The fastest loop is a **dry run** — it executes the turn now and shows you the transcript without storing anything:

```bash
curl -X POST -H "Authorization: Bearer dk_..." \
  -H "Content-Type: application/json" \
  -d '{"message": "coffee, $12"}' \
  https://localhost:8080/api/v1/skills/default/expense-tracker/dry-run
```

The dashboard's Skills page exposes the same thing with a transcript panel. This matters most for scheduled skills: without it, testing a 7am briefing means waiting until 7am.

A dry run persists nothing and suppresses non-read-only tools, but it does spend real tokens and does let read-only tools hit the network. See [Dry Runs & Previews](/docs/concepts/dry-runs/) for exactly what is and is not isolated.

To test the real path end to end:

1. Create the skill file
2. Restart Denkeeper (skills are loaded at startup)
3. If the skill has a `command:` trigger, send the command in Telegram
4. Check that the agent follows the instructions
