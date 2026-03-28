---
title: "Sessions & Permissions"
description: "Permission tiers, session management, and approval workflows."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-03-28T00:00:00+00:00
draft: false
weight: 40
toc: true
---

## Permission tiers

Every agent session operates in one of three tiers:

| Tier | Capabilities |
|---|---|
| **Autonomous** | All capabilities — intended for sandboxed environments |
| **Supervised** | Chat, memory, tools. Self-modification actions (create skills, modify schedules, update USER.md) require human approval |
| **Restricted** | Chat and read-only tools only. No memory writes beyond MEMORY.md |

Set the default tier globally:

```toml
[session]
tier = "supervised"
```

Or per-agent:

```toml
[[agents]]
name = "home-automation"
session_tier = "restricted"
```

## Approval workflows

In supervised mode, actions like skill creation or schedule modification produce an approval request. The user is notified via Telegram (inline keyboard with Approve/Deny buttons) or the REST API.

Approval requests have a 24-hour TTL. Unapproved requests expire automatically.

### Approval via Telegram

When the agent wants to create a skill, the user sees a message with inline buttons:

> **Approval required:** Create skill "weather-check"
>
> [Approve] [Deny]

### Approval via API

```bash
# List pending approvals
curl -H "Authorization: Bearer dk_..." https://localhost:8080/api/v1/approvals

# Approve
curl -X POST -H "Authorization: Bearer dk_..." \
  https://localhost:8080/api/v1/approvals/{id}/approve
```

## Cost tracking

Denkeeper tracks LLM costs per session and globally:

```toml
[llm]
max_cost_per_session = 1.0  # USD
```

When the budget is exhausted, the agent refuses further LLM calls for that session. Fallback strategies can automatically switch to cheaper models before the limit is reached.
