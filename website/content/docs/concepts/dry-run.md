---
title: "Dry Runs & Previews"
description: "Seeing what a skill or schedule would do before it does it."
slug: "dry-runs"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-25T00:00:00+00:00
draft: false
weight: 55
toc: true
---

A scheduled skill that fires at 7am is hard to iterate on: you change the wording, then wait a day to see whether it helped. A dry run executes the turn now, shows you the transcript, and leaves no trace.

## What a preview does and does not do

A dry run is a real LLM turn. It spends real tokens and returns the model's actual response — that is the point, since a simulated turn would not tell you anything.

What changes is everything around it:

| Aspect | Live turn | Dry run |
|---|---|---|
| Conversation | Created and stored | Supplied verbatim, no row created |
| History | The session's messages | `history_from`, read-only and strictly *before* this turn; empty means a fresh turn |
| The message being previewed | Stored, then read back as the last history entry | Sent directly — there is nothing stored to read back |
| Messages, telemetry, memory | Persisted | Nothing written |
| Post-turn reviewer, nudges | Run | Skipped |
| Approvals | Supervised agents prompt | Forced off |
| Idempotent tools | Execute | Execute |
| Every other tool | Executes | Returns a suppressed marker |
| Current date | Now | `as_of`, if given |

Isolation is **structural, not filtered** — the persistence step returns immediately rather than writing and later hiding. There is no dry-run data to leak, because none is created.

Unknown tools are suppressed too. The rule fails closed: a tool has to be positively known idempotent to run.

{{< callout context="danger" >}}
"Idempotent tools execute" means a preview really does hit the network — `web_fetch` fetches, `web_search` searches. A dry run is safe with respect to *your data*, not with respect to *other people's servers*.

Both dry-run endpoints therefore sit behind their parent's **write** scope despite persisting nothing.
{{< /callout >}}

## Previewing a skill

```bash
curl -X POST -H "Authorization: Bearer dk_..." \
  -H "Content-Type: application/json" \
  -d '{"message": "log $12 for coffee"}' \
  https://localhost:8080/api/v1/skills/default/expense-tracker/dry-run
```

Every field is optional. The response is a transcript: the prompt, the response, the round count, the model that answered, token counts, cost, and a `tool_calls` array in which each entry marks whether it was suppressed.

### Invocation modes

A skill can be reached three ways, and they are not interchangeable — so a preview has to pick one.

| Mode | Behaviour |
|---|---|
| `schedule` | Sets `SkillName` and the scheduled flag, which forces the skill body into the prompt |
| `command` | Leaves `SkillName` empty, so ordinary trigger matching runs |
| `message` | Same as `command`, with the user's text as the message |

The distinction matters most for `command`: because trigger matching actually runs, a command preview exercises the trigger rather than bypassing it. If your trigger is wrong, the preview shows you that.

Omit `mode` and it is inferred — an explicit `message` implies message semantics, otherwise a `command:` trigger or a `[[schedules]]` entry naming the skill decides, defaulting to `message`.

{{< callout context="note" >}}
The inference consults the **scheduler**, not the skill's frontmatter, because frontmatter cannot distinguish the two cases. A skill with no triggers is *ambient* — it matches every turn and reads your own text. It is scheduled only if a `[[schedules]]` entry names it.

`GET /api/v1/skills` reports `scheduled_by` for exactly this reason; it is the only signal the dashboard has for preselecting the right mode.
{{< /callout >}}

## Previewing a schedule

```bash
curl -X POST -H "Authorization: Bearer dk_..." \
  https://localhost:8080/api/v1/schedules/morning-briefing/dry-run
```

The preview message is built by the same code the live scheduler uses, so the header, skill, cron expression, and tier match a real firing rather than being a reconstruction that can drift.

## Comparing models

Pass `model` to run the turn against a different model without touching the agent's configuration:

```json
{ "model": "claude-haiku-4-5" }
```

This is the cheap way to answer "would a smaller model do?" for a scheduled job that runs every day.

The override clones the router rather than mutating it. That matters: the router is shared by every turn on an agent, so mutating it would retarget a live turn already in flight, and a turn that started on one model must not finish on another. The transcript reports both `requested_model` and the `model` that actually answered, so it is self-describing.

## Previews in the audit log

Preview turns are audited like live turns by default, on the grounds that a record which genuinely represents what happened is easier to trust than a filtered one. The resulting noise is handled by marking rather than by recording less.

Every event from a preview carries `source` = `dryrun` or `eval`, and is attributed to a pseudo-agent named `{agent}#dryrun` or `{agent}#eval:{variant}`. The `#` is rejected by the resource-name validator, so an exact-match `?agent=` query excludes previews automatically. Preview spend is tracked against the pseudo-agent too, so it never lands in a real agent's totals.

To filter them out, pass `?exclude_source=eval,dryrun` — the Audit Log page's **Previews** toggle does this on both the event list and the statistics.

Set `[eval] audit = "summary"` to record only lifecycle events and errors instead.

## Where to find it

The dashboard exposes previews from the Skills and Schedules pages; the transcript panel shows suppressed calls inline. See the [REST API reference](/docs/reference/rest-api/) for the full request and response shapes.

A preview answers one question once. To ask it across a set of saved turns and compare two configurations, see [Evals](/docs/concepts/evals/) — the eval runner executes its samples under this same policy.
