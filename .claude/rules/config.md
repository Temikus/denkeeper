---
name: config
description: TOML config writer semantics and shared validators.
paths:
  - internal/config/**
---

# Config invariants

- **TOML config writer** (`config.AddX/UpdateX/RemoveX`): atomic read-modify-write under a file mutex, `.bak` backup before each save. Comments/formatting are NOT preserved — everything round-trips through the parser.
- **`config.Holder` owns the live config**: the process-wide `*config.Config` is never mutated in place after startup. `api.Deps.Config` and `mcpserver.Deps.Config` are `*config.Holder`; a hot reload calls `Store` with a config freshly read from disk, so a request sees one whole config or the other. Handlers take a snapshot once (`s.appConfig()`) and read every field off it; in-memory writes go through `Holder.Update`, which deep-clones under a mutex. Never retain a `*config.Config` (or a pointer into one) past the request that fetched it.

- **Shared validators** (`internal/config`): `ValidResourceName`, `ValidProviderType`, `IsProviderReferenced` — use for new CRUD endpoints.
