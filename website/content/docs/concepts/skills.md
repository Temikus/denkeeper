---
title: "Skills"
description: "Markdown-based instruction files that teach the agent how to behave."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-11-24T00:00:00+00:00
draft: false
weight: 20
toc: true
---

Skills are the simplest extension point. They are markdown files with TOML or YAML frontmatter that provide instructions to the agent. No code execution — skills teach the agent *how to behave* and *what steps to follow*.

## File format

```markdown
+++
name = "daily-briefing"
description = "Compile and deliver a daily briefing"
version = "1.0.0"
triggers = ["command:briefing", "schedule:morning-briefing"]

[requires]
tools = ["web-search", "calendar"]
+++

## Instructions

1. Check the user's calendar for today
2. Summarize top news from their preferred sources
3. List any pending tasks or reminders
4. Format as a concise morning briefing
```

### YAML frontmatter

Skills from other tools, such as skills.sh, often put YAML between `---` lines. Denkeeper reads that too:

```markdown
---
name: release-notes
description: >-
  Draft release notes from merged pull requests,
  grouped by area.
license: Apache-2.0
metadata:
  author: example
---

## Instructions

1. List the pull requests merged since the last tag
2. Group them by area and summarize each in one line
```

- Both forms use the same keys: `name`, `description`, `version`, `triggers`, `requires.tools`, `max_tool_rounds` and `max_output_tokens`. Denkeeper ignores other keys, such as `license` and `metadata` above, but keeps them when the skill is edited.
- A skill keeps the form it was written in. New skills are written as TOML.
- Anchors, aliases, merge keys, explicit tags (such as `!!binary`) and duplicate keys are refused. A refused file is skipped, and the server log has a warning that names the file and the reason, with the line.
- Any `.md` file in a skills directory that starts with `---` and has a `name` key loads as a skill.
- A skill with no `triggers` is ambient in either form, so its body goes into every prompt. Most skills.sh skills have no triggers: add a `command:` trigger to use one only on demand.

### What an edit keeps

An edit that changes no frontmatter field, such as a body edit, writes the frontmatter back byte for byte. An edit that changes a field rewrites the block:

- **YAML** keeps keys, their order, comments and quoting, but not exact formatting. Blank lines and indentation can change, and a folded `description` can be rewrapped onto one line. A newly set field goes at the end.
- **TOML** keeps every key and value, but not comments or key order.

If a rewrite would change a key Denkeeper does not read, the edit is refused and the file is left as it was. Edit that file by hand.

## Triggers

Skills are activated by triggers:

- **`command:name`** — activated when the user's message starts with `/name` or `!name` (case-insensitive), on any adapter or channel — Telegram, Discord, the web dashboard, or the REST chat API
- **`schedule:<name>`** — marks the skill as scheduler-driven
- **Ambient** — skills without triggers are always included in the system prompt

{{< callout context="danger" >}}
A `schedule:` trigger does **not** set a time. It only marks the skill as one the scheduler invokes, and the name after the colon must be an existing schedule: skill writes reject a newly added trigger naming anything else (triggers the skill already carries, and a bare `schedule:`, are accepted). The actual timing lives in a `[[schedules]]` entry that names the skill:

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

- **`requires.tools`** — names the MCP tools the skill depends on (`[requires] tools = [...]` in the example above). A skill naming a tool that isn't currently advertised is dropped from matching for that turn rather than included and left to fail; it reactivates automatically once the tool comes back. The declaration also gates what the model sees: once some active skill declares tools, only the union of those declarations is advertised for that turn (plus the agent's own management tools, which are never hidden). A turn where nothing declares anything advertises everything.
- **`max_tool_rounds`** — caps how many tool-call rounds this skill's turn may use, on top of the agent's own round limit. It can only lower the effective budget, never raise it, and only applies when this skill is the one driving the turn (a schedule naming it, or the sole matching `command:` trigger) — an ambient skill matching every message does not cap unrelated turns.
- **`max_output_tokens`** — caps output tokens per completion for turns this skill drives. It shares its name and unit with the agent's [`max_output_tokens`](/docs/reference/config/), and follows the same rule as `max_tool_rounds` (applies only when this skill drives the turn). 0 or omitted means no skill cap. When a cap is set, on the agent or in [`[llm]`](/docs/reference/config/#llm), the skill's value can only lower it, never raise it. When neither the agent nor `[llm]` sets `max_output_tokens`, the skill's value is sent as is, even above the provider default (4096 on Anthropic), and a value above the model's own output limit makes every request fail. A cap that cuts off a tool call, or a reply with no text, ends the turn early with the `output_limit` wrap-up instead of running the half-written call; a text reply cut off by the cap is sent as far as it got, and its `llm` audit event carries `output_limit: true`.

## Skill index

Every turn, the system prompt lists all of the agent's skills under `## Available Skills`, one line each: the name, how the skill is triggered (its commands, the schedules that run it, "always on", or "no active trigger"), and, for a skill that is not always on, the first line of its `description`. So when you ask "what does /briefing do?", the agent knows `briefing` exists even though the message did not trigger it, and is told to read the skill with `skill_get` before describing it.

A skill's instructions still load only on a turn that triggers it. Unless the skill is always on, the first line of `description` is what the agent sees on every turn, so keep it to one short line (longer text is cut at 120 characters). A skill whose `requires.tools` are missing is still listed. The list is capped at 8,000 bytes: past that, descriptions are left out, then any skill whose line does not fit.

The index is on by default. Turn it off for the whole instance with `[skills] index = false`, or for one agent with `skill_index = false` in its `[[agents]]` entry; see the [configuration reference](/docs/reference/config/).

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
