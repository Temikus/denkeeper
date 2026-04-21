<script>
  import AuditRow from './AuditRow.svelte'

  let { session, expandedId = null, onToggleRow, onToggleSession } = $props()

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

  function handleHeaderKeydown(e) {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onToggleSession?.(session.conversation_id) }
  }

  let events = $derived(session.events)
  let stepCount = $derived(events.length)
  let errorCount = $derived(events.filter(e => e.status === 'error').length)
  let hasErrors = $derived(errorCount > 0)
  let allFailed = $derived(events.every(e => e.status === 'error'))
  let isExpanded = $derived(session.expanded)

  let title = $derived(() => {
    const first = events[0]
    if (first?.summary) {
      const text = first.summary.replace(/\n/g, ' ').trim()
      if (text.length > 60) return text.slice(0, 57) + '...'
      if (text) return text
    }
    const cid = session.conversation_id || ''
    return cid.replace(/^chan:/, '').replace(/^default:/, '') || 'Session'
  })

  let totalDuration = $derived(() => {
    if (events.length < 2) return events[0]?.duration_ms || 0
    const first = new Date(events[0].timestamp).getTime()
    const last = new Date(events[events.length - 1].timestamp).getTime()
    return last - first + (events[events.length - 1].duration_ms || 0)
  })

  let statusChip = $derived(() => {
    if (!hasErrors) return null
    if (allFailed) return { text: `${errorCount} error${errorCount > 1 ? 's' : ''}`, cls: 'chip-error' }
    return { text: `recovered \u00b7 ${errorCount} error${errorCount > 1 ? 's' : ''}`, cls: 'chip-warn' }
  })

  let dotClass = $derived(allFailed ? 'dot-error' : 'dot-ok')
</script>

<div class="session-card">
  <div
    class="session-header"
    role="button" tabindex="0" aria-expanded={isExpanded}
    onclick={() => onToggleSession?.(session.conversation_id)}
    onkeydown={handleHeaderKeydown}
  >
    <span class="session-dot {dotClass}"></span>
    <span class="session-type">SESSION</span>
    <span class="session-title">{title()}</span>
    <span class="session-steps">{stepCount} step{stepCount !== 1 ? 's' : ''}</span>
    {#if statusChip()}
      <span class="session-chip {statusChip().cls}">{statusChip().text}</span>
    {/if}
    <span class="spacer"></span>
    <span class="session-duration">{formatDuration(totalDuration())}</span>
    <span class="session-time">{relativeTime(session.latest)}</span>
    <span class="session-chevron" class:open={isExpanded}>{isExpanded ? '\u25be' : '\u25b8'}</span>
  </div>

  {#if isExpanded}
    <div class="session-children">
      <div class="tree-line"></div>
      {#each events as event, i (event.id)}
        <div class="child-row">
          <span class="tree-branch">{i === events.length - 1 ? '\u2514' : '\u251c'}</span>
          <div class="child-content">
            <AuditRow
              {event}
              expanded={expandedId === event.id}
              ontoggle={() => onToggleRow?.(event.id)}
              compact={true}
            />
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .session-card {
    background: white;
    border: 0.5px solid rgba(44,24,16,0.12);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .session-header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 12px;
    background: rgba(232,130,94,0.08);
    cursor: pointer;
    transition: background 0.1s;
  }
  .session-header:hover { background: rgba(232,130,94,0.12); }
  .session-header:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

  .session-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
  .dot-ok { background: #3B6D11; }
  .dot-error { background: var(--danger); }

  .session-type { font-size: 10px; font-weight: 500; color: var(--accent); letter-spacing: 0.5px; flex-shrink: 0; }
  .session-title { font-size: 13px; font-weight: 500; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }

  .session-steps {
    font-size: 11px; color: var(--text-muted);
    background: rgba(44,24,16,0.05); padding: 1px 7px;
    border-radius: 999px; white-space: nowrap; flex-shrink: 0;
  }

  .session-chip { font-size: 10px; font-weight: 600; padding: 1px 7px; border-radius: 999px; white-space: nowrap; flex-shrink: 0; }
  .chip-warn { color: var(--warn); background: rgba(186,117,23,0.12); }
  .chip-error { color: var(--danger); background: rgba(226,75,74,0.10); }

  .spacer { flex: 1; }
  .session-duration { font-size: 11px; color: #5F4A35; flex-shrink: 0; }
  .session-time { font-size: 11px; color: var(--text-muted); flex-shrink: 0; }
  .session-chevron { font-size: 12px; color: var(--text-muted); flex-shrink: 0; }

  .session-children {
    border-top: 0.5px solid rgba(44,24,16,0.08);
    padding: 6px 12px 10px 28px;
    position: relative;
  }
  .tree-line {
    position: absolute;
    left: 22px;
    top: 12px;
    bottom: 18px;
    width: 1px;
    background: rgba(44,24,16,0.12);
  }

  .child-row {
    display: flex;
    align-items: flex-start;
    gap: 4px;
  }
  .tree-branch {
    color: rgba(44,24,16,0.25);
    font-size: 11px;
    margin-left: -10px;
    margin-right: 4px;
    margin-top: 6px;
    flex-shrink: 0;
    line-height: 1;
    font-family: monospace;
  }
  .child-content { flex: 1; min-width: 0; }
</style>
