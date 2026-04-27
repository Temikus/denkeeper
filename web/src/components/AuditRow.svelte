<script>
  import { navigate } from '../router.js'
  import AuditDetail from './AuditDetail.svelte'

  let { event, expanded = false, ontoggle, compact = false, standalone = false } = $props()

  const catLabels = { tool_call: 'TOOL', llm: 'LLM', approval: 'APPROVE', schedule: 'SCHED', config: 'CONFIG', skill: 'SKILL', mcp: 'MCP', session: 'SESSION', channel: 'CHAN', supervisor: 'SUPER' }
  const catClasses = { tool_call: 'cat-tool', llm: 'cat-llm', approval: 'cat-approval', schedule: 'cat-schedule', config: 'cat-config', skill: 'cat-skill', mcp: 'cat-mcp', session: 'cat-session', channel: 'cat-session', supervisor: 'cat-supervisor' }

  function parseDetail(d) { if (!d) return {}; try { return JSON.parse(d) } catch { return {} } }
  function relativeTime(ts) {
    const diff = Date.now() - new Date(ts).getTime()
    if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
    return `${Math.floor(diff / 86400000)}d ago`
  }
  function formatDuration(ms) {
    if (!ms || ms <= 0) return ''
    if (ms < 1000) return `${ms}ms`
    return `${(ms / 1000).toFixed(1)}s`
  }
  function handleKeydown(e) { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); ontoggle?.() } }

  let detail = $derived(parseDetail(event.detail))
  let isError = $derived(event.status === 'error')
  let isDenied = $derived(event.status === 'denied')
  let label = $derived(catLabels[event.category] || event.category.toUpperCase())
  let catClass = $derived(catClasses[event.category] || 'cat-mcp')

  // LLM: model . tokens . cost
  let llmMeta = $derived(event.category === 'llm' && detail.model
    ? `${detail.model} \u00b7 ${(detail.tokens || 0).toLocaleString()} tok \u00b7 $${(detail.cost || 0).toFixed(3)}`
    : '')
  // Tool: server . duration
  let toolMeta = $derived(event.category === 'tool_call' && detail.server ? detail.server : '')

  // Tier 1 shape chip for tool_call results (collapsed row)
  let resultChip = $derived.by(() => {
    if (event.category !== 'tool_call' || event.status === 'error' || !detail.result) return ''
    const raw = detail.result
    try {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed)) return `${parsed.length} item${parsed.length !== 1 ? 's' : ''}`
      if (parsed && typeof parsed === 'object') {
        const keys = Object.keys(parsed)
        return `object \u00b7 ${keys.length} key${keys.length !== 1 ? 's' : ''}`
      }
      if (typeof parsed === 'boolean') return String(parsed)
      if (typeof parsed === 'number') return String(parsed)
      if (parsed === null) return 'null'
      return ''
    } catch {
      // Non-JSON string result — show byte size
      const kb = raw.length / 1024
      return kb >= 1 ? `${kb.toFixed(1)} kb` : `${raw.length} B`
    }
  })
  // Tier 1 shape chip for LLM responses (collapsed row)
  let llmChip = $derived.by(() => {
    if (event.category !== 'llm' || event.status === 'error') return ''
    if (detail.response_text) {
      const len = detail.response_text.length
      const kb = len / 1024
      return kb >= 1 ? `${kb.toFixed(1)} kb` : `${len} chars`
    }
    return ''
  })

  // LLM thinking badge
  let hasThinking = $derived(event.category === 'llm' && !!detail.thinking_content)

  // Supervisor decision pill
  let supervisorDecision = $derived(event.category === 'supervisor' ? (detail.decision || '') : '')
  let supervisorPillClass = $derived.by(() => {
    if (supervisorDecision === 'APPROVE') return 'pill-decision-approve'
    if (supervisorDecision === 'DENY') return 'pill-decision-deny'
    if (supervisorDecision === 'ESCALATE') return 'pill-decision-escalate'
    return ''
  })

  // Standalone dot class
  let dotClass = $derived(isError ? 'dot-error' : isDenied ? 'dot-muted' : 'dot-ok')
</script>

<div class="audit-row" class:error-bg={isError && !compact}>
  <div
    class="row-body"
    role="button" tabindex="0" aria-expanded={expanded}
    onclick={() => ontoggle?.()}
    onkeydown={handleKeydown}
  >
    <div class="row-main">
      {#if standalone}
        <span class="status-dot {dotClass}"></span>
      {/if}

      <span class="cat-badge {catClass}">{label}</span>

      {#if event.category === 'tool_call'}
        <span class="row-summary mono">{event.summary || event.action}</span>
      {:else if event.category === 'approval'}
        <span class="row-summary">{event.action}</span>
        {#if event.summary && event.summary !== event.action}
          <span class="row-summary-detail mono">{event.summary}</span>
        {/if}
      {:else}
        <span class="row-summary">{event.summary || event.action}</span>
      {/if}

      {#if resultChip}
        <span class="pill-shape">{resultChip}</span>
      {/if}
      {#if hasThinking}
        <span class="pill-thinking">+ thinking</span>
      {/if}
      {#if llmChip}
        <span class="pill-shape">{llmChip}</span>
      {/if}
      {#if supervisorDecision}
        <span class="pill-decision {supervisorPillClass}">{supervisorDecision}</span>
      {/if}

      {#if isError}
        <span class="pill-failed">FAILED</span>
      {/if}

      <span class="spacer"></span>

      {#if llmMeta}
        <span class="row-meta">{llmMeta}</span>
        {#if event.conversation_id}
          <a
            class="session-link"
            href="#/sessions/{event.conversation_id}"
            title="View session {event.conversation_id}"
            onclick={(e) => { e.stopPropagation(); navigate('sessions/' + event.conversation_id) }}
          >{'\u2197'}</a>
        {/if}
      {/if}
      {#if toolMeta}
        <span class="row-meta">{toolMeta} {event.duration_ms > 0 ? '\u00b7 ' + formatDuration(event.duration_ms) : ''}</span>
      {:else if event.duration_ms > 0}
        <span class="row-duration" class:dur-error={isError}>{formatDuration(event.duration_ms)}</span>
      {/if}

      {#if standalone}
        <span class="row-time">{relativeTime(event.timestamp)}</span>
      {/if}

      <span class="chevron">{expanded ? '\u25be' : '\u25b8'}</span>
    </div>

    {#if isError && detail.error && !expanded}
      <div class="inline-error">{detail.error}</div>
    {/if}
  </div>

  <div class="detail-panel" class:open={expanded} aria-hidden={!expanded}>
    <div class="detail-panel-inner">
      {#if expanded}
        <AuditDetail {event} />
      {/if}
    </div>
  </div>
</div>

<style>
  .audit-row { display: flex; flex-direction: column; }

  .row-body {
    padding: 5px 10px;
    cursor: pointer;
    transition: background 0.1s;
    border-radius: 3px;
  }
  .row-body:hover { background: var(--hover-overlay); }
  .row-body:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .error-bg .row-body { background: rgba(226,75,74,0.04); }
  .error-bg .row-body:hover { background: rgba(226,75,74,0.07); }

  .row-main {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    min-width: 0;
  }

  .status-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
  .dot-ok { background: #3B6D11; }
  .dot-error { background: var(--danger); }
  .dot-muted { background: var(--text-muted); }

  .cat-badge {
    font-size: 9px; padding: 1px 5px;
    border-radius: 3px; font-weight: 500;
    letter-spacing: 0.3px; white-space: nowrap; flex-shrink: 0;
  }
  .cat-llm { color: #185FA5; background: rgba(55,138,221,0.1); }
  .cat-tool { color: #993C1D; background: rgba(192,68,44,0.1); }
  .cat-approval { color: #854F0B; background: rgba(186,117,23,0.12); }
  .cat-schedule { color: var(--success); background: rgba(61,143,98,0.08); }
  .cat-config { color: #8b5cf6; background: rgba(139,92,246,0.08); }
  .cat-skill { color: #06b6d4; background: rgba(6,182,212,0.08); }
  .cat-mcp { color: var(--accent); background: rgba(192,68,44,0.08); }
  .cat-session { color: #10b981; background: rgba(16,185,129,0.08); }
  .cat-supervisor { color: #534AB7; background: rgba(127,119,221,0.12); }

  .row-summary {
    color: var(--text); white-space: nowrap;
    overflow: hidden; text-overflow: ellipsis; min-width: 0;
  }
  .row-summary-detail {
    color: #5F4A35; font-size: 11px; white-space: nowrap;
  }
  .mono { font-family: monospace; font-size: 11px; }

  .pill-thinking {
    font-size: 9px; font-weight: 500; padding: 1px 5px;
    border-radius: 3px; color: #534AB7;
    background: rgba(127,119,221,0.12);
    letter-spacing: 0.3px;
    white-space: nowrap; flex-shrink: 0;
  }

  .pill-shape {
    font-size: 10px; padding: 1px 6px;
    border-radius: 999px; color: var(--text-muted);
    background: var(--hover-overlay);
    white-space: nowrap; flex-shrink: 0;
  }

  .pill-failed {
    font-size: 9px; font-weight: 700; padding: 1px 5px;
    border-radius: 3px; color: var(--danger); background: rgba(226,75,74,0.10);
    white-space: nowrap; flex-shrink: 0;
  }

  .pill-decision {
    font-size: 9px; font-weight: 600; padding: 1px 5px;
    border-radius: 3px; letter-spacing: 0.3px;
    white-space: nowrap; flex-shrink: 0;
  }
  .pill-decision-approve { color: #3B6D11; background: rgba(61,143,98,0.10); }
  .pill-decision-deny { color: var(--danger); background: rgba(226,75,74,0.10); }
  .pill-decision-escalate { color: #854F0B; background: rgba(186,117,23,0.12); }

  .spacer { flex: 1; }

  .row-meta { font-size: 10px; color: var(--text-muted); white-space: nowrap; flex-shrink: 0; }
  .session-link {
    font-size: 11px; color: var(--accent); text-decoration: none;
    flex-shrink: 0; opacity: 0.6; transition: opacity 0.1s;
  }
  .session-link:hover { opacity: 1; }
  .row-duration { font-size: 10px; color: var(--text-muted); white-space: nowrap; flex-shrink: 0; }
  .dur-error { color: var(--danger); }
  .row-time { font-size: 11px; color: var(--text-muted); white-space: nowrap; flex-shrink: 0; }
  .chevron { font-size: 12px; color: var(--text-muted); flex-shrink: 0; }

  .inline-error {
    margin-top: 2px; padding-left: 48px;
    font-size: 11px; font-family: monospace; color: var(--danger);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }

  .detail-panel { display: grid; grid-template-rows: 0fr; transition: grid-template-rows 0.2s ease; }
  .detail-panel.open { grid-template-rows: 1fr; }
  .detail-panel-inner { overflow: hidden; padding-left: 48px; }
</style>
