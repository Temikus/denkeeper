---
title: "Skills"
description: "Markdown-based instruction files that teach the agent how to behave."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
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
triggers = ["schedule:daily:08:00", "command:briefing"]

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
- **`schedule:pattern`** — activated by the scheduler on matching schedules
- **Always-on** — skills without triggers are always included in the system prompt

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
