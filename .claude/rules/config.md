---
name: config
description: TOML config writer semantics and shared validators.
paths:
  - internal/config/**
---

# Config invariants

- **TOML config writer** (`config.AddX/UpdateX/RemoveX`): atomic read-modify-write under a file mutex, `.bak` backup before each save. Comments/formatting are NOT preserved — everything round-trips through the parser.
- **`config.Holder` owns the live config**: the process-wide `*config.Config` is never mutated in place after startup. `api.Deps.Config` and `mcpserver.Deps.Config` are `*config.Holder`; a hot reload calls `Store` with a config freshly read from disk, so a request sees one whole config or the other. Handlers take a snapshot once (`s.appConfig()`) and read every field off it; in-memory writes go through `Holder.Update`, which deep-clones under a mutex. Never retain a `*config.Config` (or a pointer into one) past the request that fetched it. A helper that derives one response field from another (`mcpServerEndpoint`) takes the caller's snapshot as an argument rather than re-reading the holder — a second read is a second snapshot and can mix two configs into one response.

- **Validate before write when a write spans sections**: `SaveChatAdapter` (token + `allowed_users` + agent binding) marshals the candidate and runs `Parse` on it before `WriteRawConfig`, returning `ErrInvalidCandidate` and leaving the file untouched — validation rejects a token without users, so the pieces can only be written together. It never synthesizes an `[[agents]]` entry (the legacy path would bind every adapter with a token).
- **`default_provider` defaulting** (`applyDefaultProvider`, after env overrides): a config with no LLM or adapter setup keeps it empty, so a blank file loads for the setup wizard; otherwise `openrouter` when configured or nothing else is, else the first `[[llm.providers]]` instance.

- **Shared validators** (`internal/config`): `ValidResourceName`, `ValidProviderType`, `IsProviderReferenced`/`ProviderReferrers` (the one list of provider references: a new `*_provider` field must be added there or the DELETE guard misses it), `ValidateDecider`, `DeciderReferrers`, `ServesDecisions` — use for new CRUD endpoints.
