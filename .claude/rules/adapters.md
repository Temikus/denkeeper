---
name: adapters
description: Telegram activity log size budgets and approval keyboard rendering.
paths:
  - internal/adapter/**
---

# Adapter invariants

- **Telegram activity log**: tool approvals render inline in the activity-log message (collapsible blockquote) with buttons, not as separate messages. Every size budget in `dispatcher.go` counts **rendered, HTML-escaped** bytes, never source text (`&` → 5 bytes, `<`/`>` fourfold). Budgets sum-bounded by construction: blockquote gets `activityChunkMaxBytes` (3000) alone or `activityChunkWithApprovalMaxBytes` when sharing with an approval; approval section gets `approvalSectionMaxBytes`; `truncateEscaped` enforces shares; `setPending` starts a fresh chunk if no room. Over-cap sends are rejected by Telegram — with an approval attached that silently loses the keyboard, so those failures log Error (`activityLog.logSendFailure`, `sendDebugApprovalPrompt`, `sendStandaloneApprovalPrompt`); cosmetic activity-log failures stay Debug.
