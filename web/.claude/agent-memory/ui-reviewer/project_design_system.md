---
name: project-design-system
description: Denkeeper Svelte dashboard design-system conventions and known shared a11y gaps, for consistent UI reviews
metadata:
  type: project
---

Denkeeper web dashboard (`web/src`) is a Svelte 5 SPA with a shared design system.

**Compliant config pattern (inline, not modal):** the `.inline-panel` in `shared.css`
uses `display:grid; grid-template-rows:0fr → 1fr` with `transition: grid-template-rows 0.2s ease`
and an `.inline-panel-inner{overflow:hidden}` child. This is the approved add/edit
pattern — do NOT flag it as a modal anti-pattern. Center-screen `.overlay`/`.confirm-modal`
is used ONLY for destructive delete confirms (allowed by the philosophy).

**Why:** pages (Schedules, Tools, Agents) intentionally standardized on this. Flagging it
as non-compliant creates churn.

**How to apply:** on config pages, verify add/edit uses `.inline-panel`; only flag if a
form was put in a center modal or portalled.

**Tokens:** all colors come from CSS vars defined in `App.svelte` (light + dark blocks):
`--accent --accent-hover --danger --success --warn --border --surface --bg --text
--text-muted --radius --hover-overlay --overlay-bg`. Tier-badge tints use hardcoded
semi-transparent rgba backgrounds with a var() text color (e.g. `rgba(76,175,125,0.15)` +
`color:var(--success)`) — this exact form is duplicated in Agents.svelte and Schedules.svelte
and is theme-safe on both. `agentColor()` hashes name → `hsl(h 55% 45%)`, theme-safe.

**Known shared a11y gaps (present across Tools/AuditLog/Schedules — flag as project-wide,
not per-page novel):**
- Pill toggle `.switch` hides the checkbox at 0×0 opacity:0 with NO visible focus indicator
  on `.switch-slider` — keyboard focus is invisible. Same for `.chip` and `.icon-btn`
  (no `:focus-visible`).
- Filter `.chip` groups use `role="radiogroup"` over plain `<button role="radio">` — no
  roving tabindex / arrow-key nav (each chip is its own tab stop). Established AuditLog convention.
- `.inline-panel` when collapsed (`0fr`, overflow hidden) leaves form fields in the DOM and
  still tab-focusable; trigger buttons lack `aria-expanded`/`aria-controls` and the inner has
  no `aria-hidden`/`inert`.

**How to apply:** report these once as project-wide with a shared-fix suggestion rather than
re-deriving them on every page. Related: [[feedback-ui-review]] parent-repo memory says run
this agent after web changes.
