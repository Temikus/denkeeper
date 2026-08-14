---
title: "Skills"
description: "Markdown-based instruction files that teach the agent how to behave."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-11T00:00:00+00:00
draft: false
weight: 20
toc: true
---

Skills are the simplest extension point. They are markdown files with TOML frontmatter that provide instructions to the agent. No code execution — skills teach the agent *how to behave* and *what steps to follow*.

## File format

```markdown
+++
name = "daily-briefing"
description = "Compile and deliver a daily briefing"
version = "1.0.0"
triggers = ["command:briefing", "schedule:daily"]

[requires]
tools = ["web-search", "calendar"]
+++

## Instructions

1. Check the user's calendar for today
2. Summarize top news from their preferred sources
3. List any pending tasks or reminders
4. Format as a concise morning briefing
```

## Triggers

Skills are activated by triggers:

- **`command:name`** — activated when the user sends `/name` in Telegram
- **`schedule:...`** — marks the skill as scheduler-driven
- **Ambient** — skills without triggers are always included in the system prompt

{{< callout context="danger" >}}
A `schedule:` trigger does **not** set a time. Everything after the colon is ignored by the parser — the trigger only marks the skill as one the scheduler invokes. The actual timing lives in a `[[schedules]]` entry that names the skill:

```toml
[[schedules]]
name = "morning-briefing"
type = "agent"
schedule = "0 8 * * *"
skill = "daily-briefing"
```

A skill with a `schedule:` trigger and no matching `[[schedules]]` entry will never fire on its own.
{{< /callout >}}

## Directory structure

```
~/.denkeeper/
├── skills/                  # Global skills (shared across agents)
│   ├── daily-briefing.md
│   └── expense-tracker.md
└── agents/
    └── default/
        └── skills/          # Agent-specific skills (override global)
            └── custom.md
```

Agent-specific skills are merged with global skills. If both define a skill with the same name, the agent-specific version wins.

## Creating skills at runtime

In `supervised` or `autonomous` tiers, the agent can create new skills via the Config MCP server. In supervised mode, this requires human approval via the approval workflow.

## Undoing a skill change

Every runtime skill change — from the agent's own Config MCP tools, the REST API, or the external MCP server — is recorded in an undo journal *before* it is applied. Each entry holds the file's raw prior bytes, so a revert restores exactly what was there rather than a re-rendered approximation: a frontmatter field this build does not recognise still survives the round trip.

The `skill_revert` tool on the [MCP server](/docs/guides/mcp-server/) applies the inverse — deleting a created skill, restoring an updated or deleted one, renaming a renamed one back. Each journal entry can be spent only once, and the claim is persisted, so a revert cannot be applied twice even across a restart.

Reverting restores the skill file. It does not undo what the skill did while it was live: messages already sent, tool calls already made and KV keys already written stay as they are.
