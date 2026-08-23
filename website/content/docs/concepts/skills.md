---
title: "Skills"
description: "Markdown-based instruction files that teach the agent how to behave."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-21T00:00:00+00:00
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

- **`command:name`** — activated when the user's message starts with `/name` or `!name` (case-insensitive), on any adapter or channel — Telegram, Discord, the web dashboard, or the REST chat API
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

## Other frontmatter fields

- **`requires.tools`** — names the MCP tools the skill depends on (`[requires] tools = [...]` in the example above). A skill naming a tool that isn't currently advertised is dropped from matching for that turn rather than included and left to fail; it reactivates automatically once the tool comes back.
- **`max_tool_rounds`** — caps how many tool-call rounds this skill's turn may use, on top of the agent's own round limit. It can only lower the effective budget, never raise it, and only applies when this skill is the one driving the turn (a schedule naming it, or the sole matching `command:` trigger) — an ambient skill matching every message does not cap unrelated turns.

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
