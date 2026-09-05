---
title: "Configuration Reference"
description: "Complete reference for denkeeper.toml options."
slug: "config"
date: 2025-01-01T00:00:00+00:00
lastmod: 2026-08-28T00:00:00+00:00
draft: false
weight: 10
toc: true
---

All configuration lives in a single TOML file. Default location: `~/.denkeeper/denkeeper.toml`.

## Top-level keys

| Key | Type | Default | Description |
|---|---|---|---|
| `data_dir` | string | `"~/.denkeeper"` | Base directory all default paths derive from. Overridden by `DENKEEPER_DATA_DIR` |
| `max_tools` | int | `50` | Combined ceiling on tools + plugins |

## `[telegram]`

| Key | Type | Default | Description |
|---|---|---|---|
| `token` | string | *required* | Bot token from @BotFather |
| `allowed_users` | int[] | *required* | Telegram user IDs allowed to interact |

## `[discord]`

| Key | Type | Default | Description |
|---|---|---|---|
| `token` | string | *required* | Discord bot token |
| `allowed_users` | string[] | *required* | Discord user snowflake IDs |

## `[llm]`

| Key | Type | Default | Description |
|---|---|---|---|
| `default_provider` | string | `"openrouter"` | Name of the provider instance to use by default (must match a configured instance name) |
| `default_model` | string | `"anthropic/claude-sonnet-4-20250514"` | Model identifier (format depends on provider) |
| `cost_limit_soft` | float | `0` | Soft cost limit per session in USD (warns but continues) |
| `cost_limit_hard` | float | `1.0` | Hard cost limit per session in USD (stops generation) |
| `stream_idle_timeout_secs` | int | `120` | Seconds a streaming LLM response may sit idle before the request is aborted |

## `[[llm.providers]]`

Named provider instances. Multiple entries of the same `type` are allowed, enabling e.g. OpenAI + a local LM Studio endpoint simultaneously. Each instance is addressable by its unique `name`.

| Key | Type | Description |
|---|---|---|
| `name` | string | Unique instance name (used in `default_provider` and per-agent `llm_provider`) |
| `type` | string | Provider type: `"anthropic"`, `"openai"`, `"openrouter"`, or `"ollama"` |
| `api_key` | string | API key (required for all types except `ollama`) |
| `base_url` | string | API endpoint override (useful for Azure, vLLM, LM Studio, etc.) |
| `organization` | string | OpenAI organization ID (openai type only) |

```toml
[[llm.providers]]
name = "openai"
type = "openai"
api_key = "sk-..."

[[llm.providers]]
name = "lmstudio"
type = "openai"
base_url = "http://localhost:1234/v1"
api_key = "lm-studio"
```

**Legacy single-slot syntax** (`[llm.openai]`, `[llm.anthropic]`, etc.) is still supported and auto-converted at startup. The two styles can coexist; an explicit `[[llm.providers]]` entry with the same name takes precedence.

## `[llm.openrouter]` *(legacy)*

| Key | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *required* | OpenRouter API key |
| `provider_order` | []string | none | Explicit preference list of upstream provider slugs (e.g. `"moonshotai"`) that overrides sticky routing when set — usually unnecessary |
| `provider_allow_fallbacks` | bool | unset (OpenRouter default: allowed) | Whether OpenRouter may fall back to providers outside `provider_order` |
| `provider_sticky` | bool | `true` | Prefer the last-served upstream provider for `provider_sticky_ttl` after a successful response, so upstream prompt caching keeps hitting. Reset by upstream errors (429/5xx/network), not by client cancellation or 4xx |
| `provider_sticky_ttl` | duration string | `"1h"` | How long to keep the sticky provider preference |

### `[llm.openrouter.reasoning]`

Controls the `reasoning` parameter sent to OpenRouter.

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | inferred `true` if `effort` or `max_tokens` is set | Activate reasoning with model defaults |
| `effort` | string | none | `"xhigh"`, `"high"`, `"medium"`, `"low"`, `"minimal"`, or `"none"`. Mutually exclusive with `max_tokens` |
| `max_tokens` | int | `0` | Reasoning token budget. Mutually exclusive with `effort` |
| `exclude` | bool | `false` | Omit reasoning from the response (tokens are still billed) |

## `[llm.anthropic]` *(legacy)*

| Key | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *required* | Anthropic API key (`sk-ant-...`) |
| `base_url` | string | `"https://api.anthropic.com"` | API endpoint override |

## `[llm.ollama]` *(legacy)*

| Key | Type | Default | Description |
|---|---|---|---|
| `base_url` | string | `"http://localhost:11434"` | Ollama server URL |

## `[llm.openai]` *(legacy)*

| Key | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *required* | OpenAI API key |
| `base_url` | string | `"https://api.openai.com/v1"` | API endpoint override (for Azure OpenAI, vLLM, LiteLLM, etc.) |
| `organization` | string | — | OpenAI organization ID (optional) |

Compatible with any endpoint that speaks the OpenAI Chat Completions API format.

## `[[llm.fallback]]`

| Key | Type | Description |
|---|---|---|
| `trigger` | string | `"cost_limit"`, `"rate_limit"`, or `"error"` |
| `action` | string | `"switch_provider"`, `"switch_model"`, or `"wait_and_retry"` |
| `provider` | string | Target provider (for `switch_provider`) |
| `model` | string | Target model (for `switch_model`) |
| `scope` | string | `"soft"` or `"hard"` (for `cost_limit`) — which agent cost limit triggers the swap |
| `max_retries` | int | Max retry count (for `wait_and_retry`) |
| `backoff` | string | `"exponential"` (default) or `"constant"` |

`cost_limit` rules consume the agent's `cost_limit_soft` / `cost_limit_hard` (resolved via `[[agents]]` overrides or the global `[llm]` defaults). Legacy `low_funds` rules with a `threshold` field auto-migrate to `cost_limit` + `scope = "soft"` on load.

## `[session]`

| Key | Type | Default | Description |
|---|---|---|---|
| `tier` | string | `"supervised"` | Default permission tier: `"autonomous"`, `"supervised"`, `"restricted"` |
| `approval_timeout` | string | `"5m"` | How long to wait for operator approval before timing out. Go duration string |
| `approval_retries` | int | `0` | Times to re-submit a timed-out approval before reporting failure to the LLM |

Note that `approval_timeout` is the *engine's* wait, distinct from the 24-hour TTL after which an unresolved approval request is expired and cleaned up.

## `[agent]`

Defaults for agents that do not set their own directories.

| Key | Type | Default | Description |
|---|---|---|---|
| `persona_dir` | string | `<data_dir>/agents/<name>` | Base persona directory |
| `skills_dir` | string | `<data_dir>/skills` | Global skills directory |

## `[[agents]]`

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | *required* | Unique agent name (one must be `"default"`) |
| `description` | string | — | Agent description |
| `persona_dir` | string | — | Path to persona files |
| `skills_dir` | string | — | Override the global skills directory. Agent-specific skills in `<persona_dir>/skills/` are always loaded regardless, and override global skills by name |
| `adapters` | string[] | — | Adapter bindings (e.g., `["telegram"]`, `["telegram:12345"]`) |
| `llm_provider` | string | — | Override default provider (must match a configured provider instance name) |
| `llm_model` | string | — | Override default model |
| `session_tier` | string | — | Override default permission tier |
| `timezone` | string | — | IANA name, e.g. `"Australia/Sydney"`. Precedence: agent > `api.timezone` > UTC. Does **not** affect cron evaluation, which stays on `api.timezone` and is restart-only |
| `cost_limit_soft` | float | — | Per-agent soft cost limit in USD (overrides global) |
| `cost_limit_hard` | float | — | Per-agent hard cost limit in USD (overrides global) |
| `max_context_messages` | int | `50` | Most recent messages sent to the LLM. Older history is dropped from the request, not deleted |
| `max_tool_rounds` | int | `50` | Tool-call **rounds** per turn, not calls — one round may fan out to many parallel calls |
| `browser_url_allowlist` | string[] | — | Overrides the global browser allowlist for this agent. Supports `*.example.com` |
| `auto_approve_tools` | string[] | — | Tool names auto-approved for this agent without human sign-off (`config` scope). Declared in TOML only — cannot be created or removed at runtime; re-applied wholesale on config reload. Names not matching an advertised tool are warned about and kept |
| `[[agents.fallback]]` | table array | — | Per-agent fallback rules. When non-empty these **replace** the global `[[llm.fallback]]` rules rather than merging with them |

### Supervisor

| Key | Type | Default | Description |
|---|---|---|---|
| `supervisor` | string | — | Name of another agent that auto-reviews tool calls before they reach you (supervised tier only; supervisor must be autonomous or restricted, not itself supervised) |
| `supervisor_timeout` | string | `"30s"` | Max wait for the supervisor's LLM review. Go duration format (`30s`, `1m`, `90s`). On timeout, falls through to human approval |
| `supervisor_context_messages` | int | `5` | Number of recent conversation messages passed to the supervisor as context |
| `supervisor_body_excerpt_len` | int | `500` | Max characters of skill body included in the review prompt |
| `supervisor_tool_desc_len` | int | `200` | Max characters of tool description included in the review prompt |

### Post-turn reviewer

A headless per-agent engine that reviews after a turn and can append persona memory or report skill improvements. Distinct from the supervisor above — it reviews *after* the fact rather than gating tool calls, and is capability-reduced rather than approval-gated.

| Key | Type | Default | Description |
|---|---|---|---|
| `reviewer_model` | string | — | Model used for post-turn review. **Empty disables post-turn review for this agent** |
| `reviewer_provider` | string | — | Provider for the reviewer; inherits the agent's `llm_provider` when empty |
| `review_max_iterations` | int | `6` | Tool-call rounds allowed to the reviewer |
| `review_timeout` | string | `"2m"` | Max duration of a review pass |
| `nudge_memory_interval` | int | `0` | User turns between memory-review nudges. 0 = disabled |
| `nudge_skill_interval` | int | `0` | Tool-call rounds between skill-review nudges. 0 = disabled |

## `[[channels]]`

Named routing endpoints that decouple sessions from a 1:1 agent–adapter binding. A channel points at one agent and may bind several adapters, which lets one conversation be shared across them. When no `[[channels]]` are declared, Denkeeper synthesizes them from each agent's `adapters` list.

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | *required* | Unique channel name. Conversation ID is `chan:{name}` |
| `agent` | string | *required* | Agent that handles messages on this channel |
| `adapters` | string[] | — | Adapter bindings, same format as `[[agents]] adapters`. Empty means the channel is reachable only via `/session` or the API |
| `delivery` | string | `"single"` | How scheduled messages are delivered: `"single"` (first specific binding) or `"broadcast"` (all specific bindings) |
| `session_mode` | string | `"persistent"` | `"persistent"` keeps one conversation per channel; `"ephemeral"` starts a fresh one per interaction (conversation ID `chan:{name}:{unix_nano}`). Cross-adapter ephemeral channels are rejected at validation |

Users switch channels at runtime with `/session <name>`; the selection is persisted. Resolution priority is: active `/session` override > specific binding > wildcard binding > legacy agent-adapter fallback.

## `[memory]`

| Key | Type | Default | Description |
|---|---|---|---|
| `db_path` | string | `"<data_dir>/data/memory.db"` | SQLite database path |
| `retention_days` | int | `90` | How long conversations are kept. 0 = unlimited |
| `max_conversations` | int | `10000` | Cap on stored conversations. 0 = unlimited |
| `cleanup_interval` | string | `"1h"` | How often retention is enforced |
| `persona_memory_char_limit` | int | `2200` | Cap on `MEMORY.md` size in characters. `0` in TOML falls back to this default — there is currently no way to request an unlimited cap |
| `persona_user_char_limit` | int | `1375` | Cap on `USER.md` size in characters. `0` in TOML falls back to this default — there is currently no way to request an unlimited cap |

## `[log]`

| Key | Type | Default | Description |
|---|---|---|---|
| `level` | string | `"info"` | `"debug"`, `"info"`, `"warn"`, `"error"` |
| `format` | string | `"text"` | `"text"` or `"json"` |

## `[voice]`

| Key | Type | Default | Description |
|---|---|---|---|
| `stt_provider` | string | — | Speech-to-text provider (`"openai"`) |
| `tts_provider` | string | — | Text-to-speech provider (`"openai"`) |
| `tts_voice` | string | `"alloy"` | Voice name |
| `auto_voice_reply` | bool | `false` | Reply with voice when user sends voice |

## `[voice.openai]`

| Key | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *required* | OpenAI API key for STT/TTS |

## `[web]`

Built-in `web_search` and `web_fetch` tools. Restart-only — `[web]` settings are not hot-reloaded.

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable web search/fetch tools for agents |

## `[web.search]`

| Key | Type | Default | Description |
|---|---|---|---|
| `provider` | string | `"duckduckgo"` | Search backend: `"duckduckgo"` or `"tavily"` |
| `api_key` | string | — | Provider API key (required for Tavily) |
| `max_results` | int | `5` | Number of search results to return |

## `[web.fetch]`

| Key | Type | Default | Description |
|---|---|---|---|
| `timeout` | string | `"30s"` | HTTP request timeout |
| `max_size_bytes` | int | `5242880` | Raw response body size limit (5 MB) |
| `max_response_chars` | int | `8000` | Characters of converted Markdown returned per `web_fetch` call (max `100000`); longer pages paginate via `start_index`. Each pagination round re-reads the full conversation context, so 24000–32000 usually serves a whole article in one call. Also editable in the Server Config dashboard page |
| `user_agent` | string | `"Denkeeper/1.0 (+https://denkeeper.io)"` | HTTP User-Agent header |
| `respect_robots_txt` | bool | `false` | Check robots.txt before fetching |
| `respect_agents_txt` | bool | `false` | Check agents.txt before fetching |

## `[web.fetch.jina]`

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable Jina Reader as a fallback fetcher for JS-heavy pages |

## `[script]`

Bounds for the in-process `run_javascript` tool, which runs short ES5.1 snippets against a JSON `input` in a fresh sandboxed VM per call. There is no network, no filesystem, and no `require`. Disabled entirely in the `restricted` tier.

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable `run_javascript` |
| `timeout` | string | `"2s"` | Per-call wall-clock limit |
| `max_output_chars` | int | `16000` | Result length cap (truncates) |
| `max_input_bytes` | int | `262144` | Accepted input payload cap, 256 KiB (rejects) |
| `max_concurrent` | int | `4` | Simultaneous VM executions across **all** agents. Negative = unlimited |
| `max_concurrent_per_agent` | int | `0` | Additional per-agent cap so one agent cannot monopolize the global pool. 0 = off |

{{< callout context="danger" >}}
There is no per-VM heap cap. `max_concurrent` bounds the memory multiplier but is not a hard ceiling — lower it if you run on constrained hardware.
{{< /callout >}}

## `[skills]`

| Key | Type | Default | Description |
|---|---|---|---|
| `max_bytes` | int | `1048576` | Cap on a single persisted skill file, frontmatter + body (1 MiB). Negative = unlimited |

Skill content is written verbatim, so without a bound an authorized caller could exhaust disk. The cap is enforced on every write surface (REST, config MCP, external MCP).

## `[browser]`

Containerized browser automation. Off by default.

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable browser automation |
| `image` | string | `"ghcr.io/temikus/denkeeper-browser:latest"` | Browser plugin container image |
| `memory_limit` | string | `"512m"` | Container memory limit |
| `cpu_limit` | string | `"1"` | Container CPU limit |
| `profile_dir` | string | `"data/browser-profiles"` | Per-agent profile directory, relative to `data_dir` |
| `session_ttl` | string | `"10m"` | Idle session close timeout |
| `max_pages` | int | `5` | Concurrent pages per agent |

## `[browser.url_allowlist]`

| Key | Type | Default | Description |
|---|---|---|---|
| `domains` | string[] | — | Domains the browser may navigate to. Empty = unrestricted. Supports `*.example.com` |

Individual agents can narrow this further with `browser_url_allowlist` on `[[agents]]`.

## `[costs]`

| Key | Type | Default | Description |
|---|---|---|---|
| `default_rate_per_1k_tokens` | float | `0` | Fallback rate (USD per 1K tokens) when a model is in neither the bundled registry nor your overrides. 0 records $0.00 and logs a warning |

Denkeeper ships a pricing registry covering roughly 70 models. Lookup priority is: provider-reported cost > registry exact match > registry longest-prefix match > `default_rate_per_1k_tokens` > $0 with a warning. The winning source is recorded as the `pricing_source` telemetry attribute, so you can tell a real price from a fallback.

## `[costs.model_prices.<model>]`

Override or add pricing for a model. Rates are **USD per million tokens**.

| Key | Type | Default | Description |
|---|---|---|---|
| `input` | float | — | Input token rate |
| `output` | float | — | Output token rate |
| `cached_input` | float | `0` | Cached-input rate. 0 means "same as `input`" |

```toml
[costs.model_prices."my-org/custom-model"]
input = 3.0
output = 15.0
cached_input = 0.3
```

## `[audit]`

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable audit logging |
| `retention_days` | int | `30` | How long audit events are kept. 0 = unlimited |
| `cleanup_interval` | string | `"1h"` | How often retention is enforced |
| `buffer_size` | int | `1000` | Capacity of the in-memory event buffer. Emission never blocks an agent turn: events past a full buffer are dropped with a warning log |

Events are queryable via `GET /api/v1/audit` and the dashboard's Audit Log page.

## `[eval]`

| Key | Type | Default | Description |
|---|---|---|---|
| `audit` | string | `"full"` | How much of a dry-run or eval turn reaches the audit log. `"full"` records it like a live turn; `"summary"` keeps only lifecycle events and errors |
| `max_concurrent` | int | `2` | Eval samples running at once, process-wide across all runs |
| `max_cost_per_run` | float | `2.0` | Default USD ceiling for one eval run, overridable per run. There is no uncapped value |
| `default_k` | int | `3` | Samples per (task, variant) pair, giving the objective metrics something to average over |
| `completeness_floor` | float | `0.8` | Fraction of expected samples that must succeed before a run's scorecard is called conclusive |
| `win_threshold` | float | `0.55` | Blinded-pair judge win rate a candidate must reach to be called an upgrade |
| `gate_rejected_rate_pp` | float | `2.0` | Largest tolerated rise in the rejected tool-call rate, in percentage points |
| `gate_rounds_pct` | float | `20` | Largest tolerated rise in mean tool-call rounds per task, in percent |
| `gate_cost_pct` | float | `25` | Largest tolerated rise in mean cost per task, in percent |
| `judge_model` | string | — | Model the internal judge grades blinded pairs with. Empty means there is no internal judge and the MCP path is the only one |
| `judge_provider` | string | — | `[[llm.providers]]` instance serving the judge. Empty uses the base agent's own provider |
| `judge_max_cost_per_run` | float | `max_cost_per_run` | USD ceiling for one judging pass, a separate budget from the run's sample cap |
| `capture` | bool | `false` | Record live turns as turn traces. Off by default — a trace holds everything the model saw |
| `max_trace_bytes` | int | `262144` | Per-trace payload cap (256 KiB). Over it, the oldest tool-call rounds are dropped first, then the oldest history messages |
| `retention_days` | int | `30` | How long captured traces are kept, matching the audit log |

`win_threshold` and the three `gate_*` keys are the decision rule, and it is deliberately asymmetric: the three gates can declare a *downgrade* on their own (a failed gate needs no judge to reject a candidate) or report that nothing regressed, but calling a candidate an *upgrade* also requires the judge win-rate to reach `win_threshold`. A judge's preference can never override an objective regression. `GET /api/v1/eval/runs/{id}/summary` returns the gate table with each value, delta, threshold and pass/fail alongside a one-line reason, plus a per-category breakdown so a candidate that wins on chat while regressing on tool-heavy tasks is visible rather than averaged away.

An eval run is bounded twice, by spend (`max_cost_per_run`) and by rate (`max_concurrent`); both are always in force. Writing `0` for any of these keys is indistinguishable from omitting it and yields the default — set a real value to change one. A run that hits its cost cap stops dispatching new samples, lets the in-flight ones finish, and keeps its partial results rather than discarding them.

Setting `judge_model` turns on the **internal judge**: `POST /api/v1/eval/runs/{id}/judge` works the same pending queue server-side, so a run can be judged unattended instead of only from Claude Code over MCP. It is capability-reduced by construction — one completion per item with no tool definitions in the request, reading only the same blinded payload `eval_get_pair` returns — so it can no more unblind its own queue than the MCP judge can. Its verdicts are ordinary verdicts under `judge_ident` `judge_model`, stamped with the rubric version, and they feed the same win rate. Judging spend is capped by `judge_max_cost_per_run` and recorded on the run as `judge_cost`, kept apart from the sample spend in `cost_spent` because the two are separate budgets. `judge_model`, `judge_provider` and `judge_max_cost_per_run` are re-read on config reload, so turning the judge on or moving its cap takes effect on the next pass; `max_concurrent` bounds judging process-wide and is fixed at start-up. Judging spend is attributed to the pseudo-agent `{agent}#eval:judge`, so it never lands in a real agent's totals.

`capture`, `max_trace_bytes` and `retention_days` are the turn-trace knobs. A trace records what a turn actually saw: the system prompt as it was assembled after skill injection, the history window as it went on the wire, every tool call with its arguments and the result the model read, the final response, timings and usage. The dashboard's **Turn inspector** renders them, which is what answers "why did it do that" — the audit log carries rounds and outcomes but never the payloads.

Live capture is off by default and should stay off unless you want that record: a trace is the most sensitive data Denkeeper stores. Eval samples are traced regardless of the switch, because the judge reads the trace and a verdict has to stay re-checkable; those turns never touch a live conversation. Traces get their own `retention_days` rather than riding on `[memory]`'s for the same reason.

Dry-run turns persist nothing — no messages, telemetry, or memory — and execute only idempotent tools; everything else returns a suppressed marker. `"full"` is the default because a preview that is audited like a live turn is easier to trust; the resulting noise is handled by *marking* rather than by recording less. Preview events are attributed to a pseudo-agent (`{name}#dryrun` / `{name}#eval:{variant}`) and carry `source` = `dryrun`/`eval`, so the Audit Log page's "Previews" toggle can filter them out of both the event list and the statistics.

## `[api]`

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable the REST API server and web dashboard |
| `listen` | string | `":8080"` | Bind address |
| `tls` | bool | `false` | Enable HTTPS |
| `cert_file` | string | — | TLS certificate path |
| `key_file` | string | — | TLS private key path |
| `cors_origins` | string[] | — | Allowed CORS origins |
| `rate_limit` | float | `0` | Max requests/sec per API key |
| `websocket_enabled` | bool | `true` | Enable the WebSocket endpoint (`GET /api/v1/ws`) |
| `websocket_max_connections` | int | `0` | Maximum concurrent WebSocket connections (0 = unlimited) |
| `websocket_replay_buffer_ttl` | string | `"5m"` | How long to buffer events for replay after a client disconnects |
| `external_url` | string | — | Publicly-reachable base URL (used for OAuth callback URLs; defaults to `http(s)://<listen>`) |
| `timezone` | string | `"UTC"` | IANA timezone used for cron evaluation and as the fallback for agents without their own `timezone`. Cron evaluation is restart-only |
| `login_rate_limit` | int | `5` | Failed password logins allowed per window per IP |
| `login_rate_window` | string | `"15m"` | Window for `login_rate_limit` |
| `onboarding_dismissed` | bool | `false` | Set by the dashboard when the onboarding checklist is dismissed |
| `wizard_completed` | bool | `false` | Set by the dashboard when the setup wizard finishes |

## `[[api.keys]]`

Static API keys, in addition to any created at runtime via the Keys CLI, the `keys` REST endpoints, or first-run setup. All keys — static and runtime — are stored and checked the same way.

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | *required* | Unique, human-readable key name |
| `key` | string | *required* | The token clients send as `Authorization: Bearer <key>` |
| `scopes` | string[] | *required* | Permissions this key grants; `["admin"]` grants everything |

```toml
[[api.keys]]
name = "ci"
key = "dk_..."
scopes = ["chat", "sessions:read"]
```

**Valid scopes:** `admin`, `chat`, `health`, `sessions:read`, `sessions:write`, `costs:read`, `agents:read`, `agents:write`, `skills:read`, `skills:write`, `schedules:read`, `schedules:write`, `approvals:read`, `approvals:write`, `tools:read`, `tools:write`, `browser:read`, `browser:write`, `kv:read`, `kv:write`, `audit:read`, `channels:read`, `channels:write`, `eval:read`, `eval:write`. `admin` implies every other scope.

## `[api.mcp_server]`

Exposes this Denkeeper instance *as* an MCP server, so an external MCP client (another agent, an IDE) can drive its agents, skills, schedules, and audit log. Opt-in.

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable the MCP server endpoint |
| `transport` | string | `"streamable"` | `"streamable"` or `"sse"` (legacy) |
| `session_timeout` | string | `"30m"` | Idle session cleanup duration |
| `chat_timeout` | string | `"2m"` | Maximum time for a single chat tool call |
| `stateless` | bool | `false` | Disable session tracking |

Note the direction: `[tools.*]` is Denkeeper connecting *outward* to MCP servers; this section is Denkeeper *being* one.

## `[[schedules]]`

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | *required* | Unique schedule name |
| `type` | string | *required* | `"system"` or `"agent"` |
| `schedule` | string | *required* | Cron expression, interval, or named schedule |
| `skill` | string | — | Skill to invoke |
| `agent` | string | `"default"` | Target agent |
| `session_tier` | string | `"supervised"` | Permission tier for this schedule |
| `channel` | string | — | Delivery channel (e.g., `"telegram:12345"`) |
| `tags` | string[] | — | Freeform labels |
| `enabled` | bool | `true` | Enable/disable without removing |
| `session_mode` | string | `"shared"` | `"shared"` reuses the target channel's existing conversation history; `"isolated"` starts a fresh conversation with no prior context for each run. Note: schedules created via `POST /schedules` default to `"isolated"` instead — the default differs by creation path |

## `[plugins.*]`

| Key | Type | Default | Description |
|---|---|---|---|
| `type` | string | *required* | `"subprocess"` or `"docker"` |
| `command` | string | *required* | Plugin binary path (subprocess) or Docker image (docker) |
| `args` | string[] | — | Command-line arguments |
| `env` | map | — | Environment variable overrides |
| `capabilities` | string[] | *required* | `["tools"]` |
| `memory_limit` | string | — | Docker container memory limit (e.g., `"256m"`) |
| `cpu_limit` | string | — | Docker container CPU limit (e.g., `"0.5"`) |
| `network` | string | `"none"` | Docker network mode (`"none"`, `"bridge"`, etc.) |
| `volumes` | string[] | — | Docker bind mounts |

Subprocess plugins run as child processes with direct MCP stdio. Docker plugins run in hardened containers with `--cap-drop ALL`, `--read-only`, `--security-opt no-new-privileges`, and `--network none` by default.

## `[security]`

| Key | Type | Default | Description |
|---|---|---|---|
| `trusted_keys` | string[] | — | Paths to PEM-encoded Ed25519 public key files |
| `allow_unsigned` | bool | `true` | Allow unsigned subprocess plugin binaries |

When `allow_unsigned = false`, all subprocess plugin binaries must have a valid Ed25519 signature from one of the trusted keys.

## `[safety.reply_guard]`

Runtime guardrail that holds back an obviously broken final reply on schedule-driven turns — a live user reacts to a bad reply, a schedule fires unattended. Distinct from `[security]`, which covers plugin signing. The turn is still stored in full and an audit event lands under category `safety`, action `reply_guard`; what changes is the delivered text, replaced by a one-line notice. Dry runs and evals evaluate the guard and report the verdict on the transcript, but never substitute the text.

Each signal takes `"withhold"`, `"warn"` (audit but deliver anyway), or `"off"`.

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Master switch |
| `on_role_markup` | string | `"withhold"` | Reply carries role/tool-call scaffolding as plain text (`<rs_tool_calls>`, `<\|im_start\|>`, `"\n\nHuman:"`, ...) instead of calling a tool |
| `on_oversized` | string | `"withhold"` | Reply exceeds `max_reply_bytes` or `max_completion_tokens` |
| `on_no_tool_calls` | string | `"warn"` | A schedule named a skill and the turn made no tool calls at all; only flags, since some skills legitimately finish without tools |
| `max_reply_bytes` | int | `16000` | Caps the final reply in bytes (~4 Telegram chunks, under half that adapter's own render limit). Negative disables |
| `max_completion_tokens` | int | `0` | Caps provider-reported completion tokens. `0` disables — it measures the same thing as `max_reply_bytes` |
| `excerpt_bytes` | int | `200` | How much of the held reply reaches the audit detail. Negative disables |

## `[kv]`

| Key | Type | Default | Description |
|---|---|---|---|
| `max_keys_per_agent` | int | `1000` | Maximum keys per agent |
| `max_value_bytes` | int | `65536` | Maximum value size in bytes (64 KB) |
| `list_max_bytes` | int | `16384` | Total value bytes a single `kv_list` response may carry |
| `list_value_head_bytes` | int | `1024` | Per-value cap inside a `kv_list` response |
| `cleanup_interval` | string | `"1h"` | Background cleanup interval for expired keys |
| `default_ttl` | table | none | Expiry applied by `kv_set` when the call passes no `ttl`, keyed by namespace prefix (must end in `:`). Longest matching prefix wins; an explicit `ttl` always wins. Example: `default_ttl = { "log:" = "720h", "cache:" = "168h" }` |

Per-agent key-value storage with optional TTL. Exposed as Config MCP tools (`kv_get`, `kv_set`, `kv_delete`, `kv_list`, `kv_set_nx`). Useful for locks, counters, caches, and cross-session coordination.

`list_max_bytes` and `list_value_head_bytes` are sized for model context, not disk: a `kv_list` over a busy namespace can otherwise return tens of thousands of tokens in one tool result. Keys are always returned in full; values are what gets dropped once the budget is spent. Both must be positive, and `list_value_head_bytes` must not exceed `list_max_bytes`.

## `[sandbox]`

| Key | Type | Default | Description |
|---|---|---|---|
| `runtime` | string | `"docker"` | Sandbox backend: `"docker"` or `"kubernetes"` |

Selects the runtime backend for sandboxed (Docker-type) plugins.

## `[sandbox.kubernetes]`

| Key | Type | Default | Description |
|---|---|---|---|
| `namespace` | string | `"denkeeper-sandboxes"` | Kubernetes namespace for sandbox Pods |
| `kubeconfig` | string | — | Path to kubeconfig file (empty uses in-cluster config) |
| `runtime_class` | string | — | RuntimeClassName for gVisor or Kata Containers |

The Kubernetes backend creates ephemeral Pods with init-container network isolation, dropped capabilities, read-only root filesystem, and Pod Security Admission labels. Supports both in-cluster (ServiceAccount) and out-of-cluster (kubeconfig) authentication.

## `[mcp]`

Global settings that apply to all MCP tool servers.

| Key | Type | Default | Description |
|---|---|---|---|
| `request_timeout_secs` | int | `30` | Per-request timeout for MCP calls (0 = no timeout) |
| `auto_restart` | bool | `true` | Automatically restart crashed stdio servers |
| `max_restart_attempts` | int | `3` | Consecutive failures before disabling a server |
| `restart_cooldown` | string | `"5m"` | Duration a server must stay connected to reset the failure counter |
| `drain_timeout` | string | `"35s"` | How long teardown waits for in-flight tool calls before forcing the transport closed |
| `url_allowlist` | string[] | — | Allowed hostnames/wildcards for SSE tool server URLs (empty = all non-blocked hosts) |
| `health_fail_threshold` | int | `3` | Consecutive health-probe failures before a `health_fail` audit event fires for remote (sse/http) servers. Stdio servers always emit on the first failure |
| `init_retry_attempts` | int | `5` | Retries for a server's initial connection attempt |
| `init_retry_backoff` | string | `"2s"` | Base backoff duration between initial-connection retries |
| `sse_keep_alive_secs` | int | `15` | TCP keepalive interval for SSE connections (overridable per server) |
| `env_passthrough` | string[] | — | Extra parent-process environment variable names forwarded to stdio server subprocesses, on top of the built-in non-secret allowlist. `DENKEEPER_*` and other denylisted names are always blocked, even here |

Removing, disabling, restarting or reconfiguring a tool server tears it down in
two phases. The server stops being offered immediately — its tools leave the
advertised set and new calls are refused — and only then does Denkeeper wait for
the calls already running to finish, up to `drain_timeout`, before closing the
transport. The server reports status `draining` while it waits. A window that
expires forces the close and records a `forced_close` event in the audit log with
the number of calls that were still running. The default sits just above the 30s
per-tool-call timeout, so a call that was going to succeed gets to.

## `[tools.*]`

| Key | Type | Default | Description |
|---|---|---|---|
| `transport` | string | `"stdio"` | Transport type: `"stdio"` (subprocess) or `"sse"` (remote HTTP/SSE) |
| `command` | string | *required for stdio* | MCP server command (stdio only) |
| `args` | string[] | — | Command arguments (stdio only) |
| `env` | map | — | Environment variables; supports `${NAME}` placeholder expansion (stdio only) |
| `url` | string | *required for sse* | Remote server URL (SSE only, must be http/https) |
| `headers` | map | — | HTTP headers sent with SSE requests (SSE only) |
| `request_timeout_secs` | int | `0` | Per-server timeout override (0 = use global `[mcp]` value) |
| `sse_keep_alive_secs` | int | `0` | Per-server keep-alive override (0 = use global `[mcp]` value, SSE only) |
| `auth` | string | `""` | Authentication method: `""` (none) or `"oauth"` (OAuth 2.1, SSE only) |
| `client_id` | string | — | OAuth2 client ID (optional; some servers use dynamic registration) |
| `client_secret` | string | — | OAuth2 client secret (optional; must be set together with `client_id`) |
| `scopes` | string[] | — | OAuth2 scopes to request (optional) |
| `disabled_tools` | string[] | — | Tool names on this server to exclude from the LLM's tool payload |
| `idempotent` | bool | `false` | Memoize identical calls to this server's tools within one message turn (identical name+args returns the cached result instead of re-executing). Only set on servers whose tools are **all** read-only — a cached write is a silently dropped side effect |
| `idempotent_tools` | string[] | — | Per-tool memoization opt-in for servers that mix read and write tools; union with `idempotent` |
| `trust_annotations` | bool | `false` | Also memoize tools this server marks read-only via the MCP `readOnlyHint` annotation. Annotations are self-declared by the server — enable only for servers you trust to describe their tools honestly |
| `env_passthrough` | string[] | — | Additional parent-process env vars to forward into this stdio server's subprocess, on top of the built-in allowlist and the global `[mcp] env_passthrough`. `DENKEEPER_*` and other secret-matching names are always filtered out |
| `allow_loopback` | bool | `false` | Bypass SSRF protection against localhost/127.x/::1 for this server's `url` (SSE only) — unsafe, only for a server you control |

**SSE security**: SSRF protection blocks localhost, link-local (169.254.x.x), and cloud metadata endpoints. `${NAME}` placeholders in `url` and `headers` are resolved from environment but secrets matching `DENKEEPER_*_SECRET`, `DENKEEPER_*_PASSWORD*`, and related patterns are denied. URL and header values are redacted in API responses.

Tools can also be added and removed at runtime via the REST API (`tools:write` scope) or the Config MCP server (`tool_add`/`tool_remove`). Runtime changes are persisted to the TOML config file.

## `[otel]`

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable OpenTelemetry instrumentation |
| `traces_endpoint` | string | — | OTLP HTTP endpoint for trace export (e.g. `"http://localhost:4318"`) |
| `service_name` | string | `"denkeeper"` | Service name for the OTel resource |

Env overrides: `DENKEEPER_OTEL_ENABLED` sets `enabled`, `DENKEEPER_OTEL_TRACES_ENDPOINT` sets `traces_endpoint`.

When enabled, Prometheus metrics are exposed at `GET /metrics` (no auth required). Traces are only exported when `traces_endpoint` is set.

## `[api.auth]`

| Key | Type | Default | Description |
|---|---|---|---|
| `password_hash` | string | — | bcrypt hash from `denkeeper passwd` CLI |
| `session_secret` | string | — | Hex-encoded AES-256 key (64 hex chars). Generate with `openssl rand -hex 32` |
| `session_max_age` | string | `"24h"` | Session cookie lifetime |
| `preferred_login_method` | string | `"auto"` | Which login method the login page shows first: `"auto"`, `"password"`, or `"apikey"` |
| `session_record_retention` | string | `"720h"` | How long to keep session records after they expire |

Env override: `DENKEEPER_API_AUTH_SESSION_SECRET` sets `session_secret`.

## `[api.auth.oidc]`

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable OIDC SSO |
| `issuer` | string | — | OIDC provider issuer URL (e.g. `"https://accounts.google.com"`) |
| `client_id` | string | — | OAuth2 client ID |
| `client_secret` | string | — | OAuth2 client secret |
| `redirect_url` | string | — | Callback URL (e.g. `"https://denkeeper.example.com/auth/callback"`) |
| `scopes` | string[] | `["openid","email","profile"]` | OAuth2 scopes |
| `allowed_emails` | string[] | — | Email allowlist. Required non-empty when enabled. Case-insensitive. |

Env overrides: `DENKEEPER_OIDC_CLIENT_ID` sets `client_id`, `DENKEEPER_OIDC_CLIENT_SECRET` sets `client_secret`.

Requires `email_verified: true` claim from the OIDC provider. Uses Authorization Code flow with PKCE (S256).
