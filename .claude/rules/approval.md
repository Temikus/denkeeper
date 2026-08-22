---
name: approval
description: Permission tiers, approval workflow, auto-approve rule scopes, supervisor agents, supervisor config validation.
paths:
  - internal/approval/**
  - internal/agent/engine.go
  - internal/agent/dispatcher.go
---

## Permission Tiers & Approval Workflows

Tiers: `autonomous` (all actions), `supervised` (chat + tools with approval), `restricted` (chat + read-only tools).

`internal/approval/` manages human sign-off. Flow: Engine submits → Manager persists + registers closure → inline Approve/Deny keyboard → callback resolves → closure invoked. Eleven action kinds: `user_update`, `soul_update`, `identity_update`, `create_skill`, `update_skill`, `delete_skill`, `modify_schedule`, `install_tool`, `modify_config`, `browser_profile`, `tool_call`.

**Supervised tool calls**: each MCP call needs approval. Engine checks `Manager.ShouldAutoApprove()` first (match → immediate execution, `tool_approval` event with `auto_approved`), else blocks on `WaitForResolution`. Dispatcher sends four buttons: Approve, Deny, Auto (session), Auto (always). Denied calls feed "Tool call was denied by the operator." to the LLM. Within one turn, a name+args exact match against an already-denied call is auto-denied (status `auto_denied`); dedup map resets on next user message.

**Auto-approve rules**: three scopes, checked `config` → `session` → `permanent`. `session` (in-memory, conversation-scoped, 15m TTL) and `permanent` (SQLite, agent-scoped) are created at runtime from Telegram buttons (`:approve_session`/`:approve_always`), web UI Always Approve, or REST; `approval.ExtractToolName()` keys them from the approval summary. `config` is declared per agent in TOML (`[[agents]] auto_approve_tools = [...]`), held in memory on the Manager, and **replaced wholesale** by `SetConfigRules` — the only writer, called from the config-load path (startup + `POST /server/reload`). It cannot be weakened at runtime: `POST /auto-approve` 400s on `scope: "config"`, config rules are listed with an empty `id` so DELETE has nothing to address, and `RemoveAutoApproveRule`/`ClearSessionRules` touch different state. Ordering cannot change an approve/deny outcome (all three answer the same question) — it fixes **attribution** (`scope=config` stays stable across sessions) and skips the SQLite lookup for the hottest calls. Newly-blessed pairs auto-resolve queued approvals, same as the other two scopes. Validation is syntax-only at load (non-empty, no whitespace, unique — `ValidResourceName` deliberately not applied, MCP names have hyphens); names not matching an advertised tool are **warned about at wiring/reload and kept**, never dropped (a late-connecting remote server must not silently weaken policy). Stage-1 auto-approvals emit an `audit.CategoryApproval` / `auto_approve` event with `detail.scope` for **all** scopes.

**Supervisor agents**: `supervisor = "agent-name"` on a supervised agent inserts an LLM reviewer between auto-approve and human approval: APPROVE / DENY (reason fed to LLM) / ESCALATE (→ human). One-shot LLM call via the supervisor's Router with tool details + recent history — no storage/skills/tool loops. Emits `audit.CategorySupervisor` events and `tool_approval` statuses `supervisor_approved`/`_denied`/`_escalated`/`_error` (error falls through to human). Web UI: supervisor controls in Agents, statuses in Chat, filter chip in AuditLog.


## Invariants

- **Supervisor config validation**: supervisor must exist, must not itself be supervised (no chaining), must not use `supervised` tier (deadlock), and `supervisor` is only valid on supervised agents. Delete guard rejects removing a referenced supervisor. `supervisor_timeout` is a Go duration string; `supervisor_context_messages` int (0 = default 5).
