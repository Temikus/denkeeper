---
title: "Built-in Tools"
description: "Web search and fetch, JavaScript execution, the KV store, and browser automation."
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-14T00:00:00+00:00
draft: false
weight: 35
toc: true
---

Alongside external [MCP tool servers](/docs/concepts/tools-mcp/), Denkeeper ships tools that run in-process. They need no subprocess, no container, and no separate install — just a config section.

All of them respect the agent's permission tier.

## Web search and fetch

Enabled by default. Gives agents `web_search` and `web_fetch`.

```toml
[web]
enabled = true

[web.search]
provider = "duckduckgo"   # or "tavily"
max_results = 5

[web.fetch]
max_response_chars = 24000
respect_robots_txt = true
```

`web_fetch` converts a page to Markdown and returns at most `max_response_chars` of it, paginating through `start_index` for longer pages. The default of 8000 is conservative; because each pagination round re-reads the whole conversation context, raising it to 24000–32000 is usually *cheaper* than fetching an article in four calls.

The live value is formatted into the tool description, so the model can plan its pagination rather than discovering the limit by hitting it.

For JavaScript-heavy pages that return an empty shell, Jina Reader can be enabled as a fallback fetcher under `[web.fetch.jina]`.

{{< callout context="note" >}}
`[web]` settings are **restart-only**. The web tools are constructed once per agent at startup, and config hot-reload re-applies only per-agent engine knobs. The one exception is `max_response_chars`, which is also editable from the dashboard's Server Config page.
{{< /callout >}}

## Running JavaScript

`run_javascript` executes a short ES5.1 snippet against a JSON `input` and returns the result. It is useful for the arithmetic and data-reshaping that language models are unreliable at — filtering a list, summing a column, reformatting a payload.

```toml
[script]
enabled = true
timeout = "2s"
max_output_chars = 16000
max_input_bytes = 262144
max_concurrent = 4
```

Each call gets a **fresh VM with no host globals** — no network, no filesystem, no `require`, no access to previous calls. The interpreter is pure Go, so there is no Node.js dependency.

The tool is disabled entirely in the `restricted` tier.

{{< callout context="danger" >}}
There is no per-VM heap cap. `max_concurrent` bounds how many snippets can allocate simultaneously — roughly `max_concurrent × rate × timeout` — but it is not a hard memory ceiling. On constrained hardware such as a Raspberry Pi, lower it.

`max_concurrent` is process-global, shared across every agent. Add `max_concurrent_per_agent` if you want to stop one agent monopolizing the pool.
{{< /callout >}}

## The KV store

Per-agent key-value storage with optional TTL, exposed as `kv_get`, `kv_set`, `kv_set_nx`, `kv_delete`, and `kv_list`.

```toml
[kv]
max_keys_per_agent = 1000
max_value_bytes = 65536
list_max_bytes = 16384
list_value_head_bytes = 1024
cleanup_interval = "1h"
```

`kv_list` is size-capped so one call over a busy namespace cannot flood the model's context. Every key comes back, but values are budgeted: a value longer than `list_value_head_bytes` is cut to its head and marked `value_truncated`, and once `list_max_bytes` of values is spent the remaining entries arrive with `value_omitted: true` plus a top-level `truncated` and `omitted_values` count, so the agent knows to `kv_get` them individually. Pass `keys_only: true` when you only need to see what is in the namespace.

It exists because an agent's memory is prose, and prose is a bad place to keep state that has to be *exact*. Typical uses:

- **Locks** — `kv_set_nx` succeeds only if the key is absent, so an agent can claim a task it must not start twice
- **Counters** — how many times something has happened
- **Caches** — an API result with a TTL, so a daily lookup happens once
- **Cross-session coordination** — checking whether a routine already ran today

Reads are allowed at every tier; writes are denied in `restricted`.

The namespaces an agent chooses are its own. Treat any convention you suggest as a starting point rather than a schema it must obey.

## Browser automation

Off by default. When enabled, the agent drives a real browser running in a container.

```toml
[browser]
enabled = true
memory_limit = "512m"
session_ttl = "10m"
max_pages = 5

[browser.url_allowlist]
domains = ["*.wikipedia.org", "news.ycombinator.com"]
```

An empty allowlist means unrestricted. Individual agents can narrow it further with `browser_url_allowlist`, and private, link-local, and cloud-metadata addresses are blocked regardless of what the allowlist says.

Each agent gets its own persistent profile directory, so a login survives between sessions. Profiles are managed through the `browser_profile_list`, `browser_profile_info`, `browser_profile_clear`, and `browser_profile_delete` Config MCP tools, the `/api/v1/browser/*` endpoints, or the dashboard's Browser page.

{{< callout context="danger" >}}
A browser with a persistent profile is the most powerful capability an agent can hold — it inherits every session you have logged it into, and web pages are attacker-controlled input. Set a URL allowlist, and prefer a dedicated profile over one carrying credentials that matter.
{{< /callout >}}

## Memoization

Identical calls to *idempotent* tools are memoized within a single turn — see [Tools (MCP)](/docs/concepts/tools-mcp/). Of the built-ins, `web_search`, `web_fetch`, `kv_get`, and `kv_list` are cache-eligible. `run_javascript` deliberately is not.

See the [configuration reference](/docs/reference/config/) for every option in `[web]`, `[script]`, `[kv]`, and `[browser]`.
