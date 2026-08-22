---
name: config
description: TOML config writer semantics and shared validators.
paths:
  - internal/config/**
---

# Config invariants

- **TOML config writer** (`config.AddX/UpdateX/RemoveX`): atomic read-modify-write under a file mutex, `.bak` backup before each save. Comments/formatting are NOT preserved — everything round-trips through the parser.
- **Shared validators** (`internal/config`): `ValidResourceName`, `ValidProviderType`, `IsProviderReferenced` — use for new CRUD endpoints.
