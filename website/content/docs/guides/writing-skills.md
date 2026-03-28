---
title: "Writing Skills"
description: "How to create custom skills for your Denkeeper agent."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
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
| `requires.tools` | No | MCP tools this skill needs |

## Trigger types

- **`command:name`** — activates on `/name` Telegram command
- **`schedule:pattern`** — activates on matching scheduler runs
- **No triggers** — always included in the system prompt

## Agent-specific skills

Place skills in an agent's persona directory to scope them:

```
~/.denkeeper/agents/work-assistant/skills/standup.md
```

Agent-specific skills are merged with global skills. Same-name agent skills override global ones.

## Testing a skill

1. Create the skill file
2. Restart Denkeeper (skills are loaded at startup)
3. If the skill has a `command:` trigger, send the command in Telegram
4. Check that the agent follows the instructions
