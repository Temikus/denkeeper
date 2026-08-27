# CLAUDE.md

Guidance for Claude Code (claude.ai/code) when working in this repository.

## Build & Development Commands

Tooling: `just` (user-facing) → Task (`Taskfile.yml`, per-target fingerprint caching, cache in gitignored `.task/`) → mise for tool pinning (`.mise.toml`: Go 1.26.5, swag 1.16.6 — swag pinned because its output is a committed, CI-gated artifact). Bust one step: `mise x -- task <name> --force`; bust the whole hook chain: `JUST_HOOK_FORCE=1 just hook`.

```bash
just build                    # Build binary → pkg/bin/denkeeper
just serve                    # Build UI, then serve dashboard + API on :8080 (live reload)
just dev                      # Backend :8080 + Vite dev server :5173 (hot reload)
just test                     # All Go tests with -race
just test-v                   # Verbose test output
just test-pkg internal/agent  # Single package
just test-ui                  # Web UI tests (vitest)
just lint                     # golangci-lint
just fmt                      # gofmt -w .
just check                    # CI equivalent: fmt-check + vet + lint + test + test-ui + openapi-check
just hook                     # `just check`, minimal output, cached — prefer this for full-suite runs
just scan                     # gosec + govulncheck
just build-ui                 # Build web UI (auto-run by build/test/vet/lint when web/dist missing)
just build-full               # Build web then binary
just openapi                  # Generate OpenAPI spec (requires swag CLI)
just openapi-check            # Fail if the committed spec is stale (CI gate)
just dev-ui                   # Vite dev server only (hot reload)
just test-integration         # E2E integration tests
```

## Architecture

Denkeeper is a single-binary personal AI agent with multi-agent routing:

```
Adapter (Telegram/Discord) ─┐
Web Dashboard (WS/SSE) ─────┼→ Dispatcher → Engine (per agent) → LLM Router → Provider (Anthropic/OpenRouter/OpenAI/Ollama)
REST API (/api/v1/chat) ────┘                    ↕                    ↕
                                             MemoryStore          CostTracker
                                             (SQLite)              + Pricing Registry
```

- **Dispatcher** (`internal/agent/dispatcher.go`): routes messages to Engines via channel bindings (or legacy adapter bindings), falling back to the `"default"` agent. Sends inline-keyboard approval messages for `tool_approval` ChatEvents; intercepts `/session` for runtime channel switching.
- **Channels** (`internal/agent/channel.go`): named routing endpoints decoupling sessions from 1:1 agent–adapter binding. A channel points to one agent, can bind multiple adapters (cross-adapter session sharing). Config `[[channels]]` (`name`, `agent`, `adapters`); auto-synthesized from agent `adapters` when absent. Conversation ID `"chan:{name}"`. `/session <name>` switches; selections persist in SQLite `active_channels`.
- **Engine** (`internal/agent/engine.go`): per-agent orchestrator. Pipeline: permissions → get/create conversation → store user msg → load history → system prompt (persona + skills) → `Router.Complete()` → tool-call loop (with approval if supervised) → usage event → memory extraction → store assistant msg → return.
- **Three key interfaces**: `adapter.Adapter` (platforms), `llm.Provider` (LLM backends), `agent.MemoryStore` (SQLite persistence).
- **Multi-agent config**: `[[agents]]` with `name`, `persona_dir`, `adapters`, `llm_provider` (overrides global `default_provider`), `llm_model`, `session_tier`. With no `[[agents]]`, a single `"default"` agent is synthesized when an adapter token is set (headless, bound to those adapters) **or** `api.enabled` is explicitly true (API-only, no bindings). Nothing configured → no agent, so the web setup wizard can guide creation; synthesis runs before `api.enabled` defaults to true so "explicit" is distinguishable.
- **Named provider instances**: `[[llm.providers]]` allows multiple instances of one provider type (`name`, `type`, `api_key`, `base_url`, `organization`). Legacy `[llm.openai]` single-slot syntax auto-converts. Per-agent `llm_provider` references by name.
- **Data directory**: all default paths (db/persona/skills) derive from one base dir — `DENKEEPER_DATA_DIR` env > `data_dir` TOML > `~/.denkeeper`. Helm sets `DENKEEPER_DATA_DIR=/data`.
- **Wiring** is in `cmd/denkeeper/main.go`; all behavior should be TOML-configurable, not hardcoded.

## Conventions

- **Error wrapping**: always `fmt.Errorf("context: %w", err)`.
- **Logging**: `log/slog` with contextual fields.
- **Context**: all I/O functions accept `context.Context`.
- **Concurrency**: channels for message passing; `sync.Mutex` for shared state.
- **Config**: three-phase — parse TOML → apply defaults (incl. env) → validate. `applyEnvOverrides()` reads an explicit allowlist of `DENKEEPER_*` vars.
- **Cyclomatic complexity**: gocyclo threshold 15 for all non-test functions.

## Testing Patterns

- **Coverage thresholds are quality gates** — never lower to make CI pass; add tests instead. Only the project owner may approve lowering one.
- Hand-written mocks (no codegen); in-memory SQLite via `NewInMemoryStore()`; individual `TestName_Scenario` functions (not table-driven); always `-race`.
- Web UI must be built before Go steps that embed `internal/web/dist/` (`ensure-web-dist` recipe handles it; CI uses an explicit `build-ui` job).
- **Web UI**: Vitest + jsdom + MSW; tests in `web/src/__tests__/` and `web/src/components/__tests__/`.
- **E2E** (`internal/integration/`): full in-process API server, mock LLM, in-memory stores via `NewHarness(t, &HarnessOpts{...})` (`ConfigPath`, `WithLifecycleMgr`, `Responses`). WebSocket tests need `httptest.NewServer`; the rest use `httptest.NewRecorder`.

## Permission Tiers

`autonomous` (all actions), `supervised` (chat + tools with approval), `restricted` (chat + read-only tools). Approval workflow, auto-approve scopes, and supervisor agents: `.claude/rules/approval.md`.

## UI/UX Standards

Every user-facing feature gets thoughtful UX:

- **Web (Svelte)**: loading spinners, empty states with CTAs, inline errors, confirm destructive actions, success feedback, disabled buttons in-flight, responsive ≥320px, existing CSS variables (`--accent`, `--surface`, `--border`, `--text-muted`, `--danger`).
- **Confirm pattern**: overlay (`.overlay` + `.confirm-modal`) for **irreversible** actions only; **inline** confirm rendered in place inside the card for reversible config writes (`Providers.svelte` is the reference). An overlay over the evidence for the decision is the failure mode. Full rule in `web/src/shared.css` above `.overlay`.
- **Shared classes before local ones**: `.sr-only`, `.table-wrap`, `.inline-error`, `.btn-*`, `.table`, `.hint`, `.pill` and friends live in `web/src/shared.css` (imported globally by `App.svelte`). Redefine one locally only to add a margin or a genuinely different shape, and say why. A `.table-wrap` needs `tabindex="0"`, `role="region"` and an `aria-label` on the wrapper, or its off-screen columns are pointer-only.
- **CLI (Cobra)**: progress feedback for >500ms ops, `tabwriter` tables, actionable errors, non-zero exits via `RunE`.
- **Adapters**: typing indicators before LLM calls, platform-native formatting, inline keyboards for approvals. Telegram registers built-in commands (`/start`, `/help`, `/stop`, `/panic`, `/resume`, `/debug`, `/clear`, `/compact`) plus skill `command:` triggers via `setMyCommands` (`RegisterSkillCommands`).

## Web Dashboard & WebSocket Transport

`internal/web/` embeds a Svelte SPA (`//go:embed dist`). 18 pages, roughly one per subsystem (routes in `web/src`).

**WebSocket** (`internal/api/websocket.go`): `GET /api/v1/ws` upgrades to bidirectional WS; dashboard auto-connects and falls back to SSE after 3 failed reconnects. `WSHub` keeps a per-connection replay buffer. Config: `api.websocket_enabled` (true), `api.websocket_max_connections`, `api.websocket_replay_buffer_ttl` (5m). Frame types in `wsframes.go`.

## Key Subsystems

| Subsystem | Package | Config Section |
|-----------|---------|----------------|
| Scheduler | `internal/scheduler/` | `[[schedules]]` |
| Config MCP | `internal/configmcp/` | (in-process, per-agent) |
| Web MCP | `internal/webmcp/` | `[web]` |
| Browser | `internal/browser/` | `[browser]` |
| KV Store | `internal/kv/` | `[kv]` |
| Plugins | `internal/plugin/` | `[plugins.*]` |
| Sandbox | `internal/sandbox/` | `[sandbox]` |
| OTel | `internal/otel/` | `[otel]` |
| Pricing | `internal/llm/pricing/` | `[costs]` |
| Auth | `internal/api/session.go`, `oidc.go` | `[api.auth]` |
| MCP OAuth | `internal/tool/oauth/` | `[tools.*.auth]` |
| Telemetry | `internal/agent/memory.go` | `[memory]` |
| Audit Log | `internal/audit/` | `[audit]` |
| Channels | `internal/agent/channel.go`, `dispatcher.go` | `[[channels]]` |
| MCP Server | `internal/mcpserver/` | `[api.mcp_server]` |
| Script MCP | `internal/scriptmcp/` | `[script]` |
| Dry run / evals | `internal/agent/execpolicy.go`, `internal/api/dryrun.go` | `[eval]` |
| Eval task sets & runner | `internal/eval/` | `[eval]` |
| Eval pairing, judging, verdict | `internal/eval/pairing.go`, `judging.go`, `verdict.go`, `internal/mcpserver/tools_eval.go` | `[eval]` |
| Internal judge (`judge_model`) | `internal/eval/judge.go`, `rubric.go` | `[eval]` |
| Turn traces & inspector | `internal/agent/trace.go`, `internal/eval/trace.go`, `internal/api/traces.go` | `[eval]` (`capture`, `max_trace_bytes`, `retention_days`) |
| Skill undo journal | `internal/skilleffect/` | (none — on whenever a SQLite store is wired) |

## Detailed Invariants

Subsystem-specific invariants live in `.claude/rules/*.md`, each scoped with `paths:` frontmatter so it loads only when you touch matching files. Read the relevant file **before** editing that subsystem — these are things you cannot infer from the code at a glance.

| Rule file | Covers | Loads on |
|---|---|---|
| `agent-engine.md` | Engine knobs, tool loop, stop reasons & wrap-up, reply guard, telemetry attribution, channels, memoization, date injection | `internal/agent/**`, `internal/scheduler/**`, `main.go` |
| `eval-dryrun.md` | ExecPolicy isolation, router overlays, eval runs, pairing, blinding, judging, verdicts | `internal/eval/**`, `execpolicy.go`, `api/dryrun.go` |
| `mcp-tools.md` | MCP health/drain teardown, tool-name collisions, OAuth, stdio env scoping, `run_javascript`, `web_fetch`, `kv_list` | `internal/tool/**`, `scriptmcp/**`, `webmcp/**`, `kv/**` |
| `skills.md` | Skill frontmatter writer, config-MCP dep gating, undo journal, skill-file IO hardening | `internal/configmcp/**`, `skilleffect/**` |
| `rest-api.md` | Endpoint map, scopes, streaming events, OpenAPI generation gate | `internal/api/**` |
| `approval.md` | Tiers, approval flow, auto-approve scopes, supervisor agents | `internal/approval/**` |
| `mcp-server.md` | External MCP agent & audit tool surfaces | `internal/mcpserver/**` |
| `pricing.md` | Cost tracking, pricing registry, lookup priority | `internal/llm/**` |
| `config.md` | TOML writer semantics, shared validators | `internal/config/**` |
| `adapters.md` | Telegram activity-log size budgets | `internal/adapter/**` |
| `release.md` | Release pipeline, released-PR stamping | `.github/**` |

CI/CD: golangci-lint, gosec, govulncheck, Grype, Gitleaks, GoReleaser, Homebrew tap, Docker (ghcr.io) with cosign + SLSA, GitHub Pages docs, released-PR stamping.

See `design/denkeeper-prd.md` for the full roadmap.
