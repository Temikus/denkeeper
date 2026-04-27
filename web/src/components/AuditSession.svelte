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

  function parseDetail(d) { if (!d) return {}; try { return JSON.parse(d) } catch { return {} } }

  function handleHeaderKeydown(e) {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onToggleSession?.(session.conversation_id) }
  }

  let events = $derived(session.events)

  // Separate trigger event from step events
  let triggerEvent = $derived.by(() => {
    const ev = events.find(e => e.category === 'session' && e.action === 'trigger')
    return ev ? { ...ev, detail: parseDetail(ev.detail) } : null
  })

  // Step events: everything except the first trigger (shown as session header).
  // Subsequent triggers stay inline so follow-up user messages remain visible.
  let stepEvents = $derived.by(() => {
    let firstTriggerSkipped = false
    return events.filter(e => {
      if (e.category === 'session' && e.action === 'trigger' && !firstTriggerSkipped) {
        firstTriggerSkipped = true
        return false
      }
      return true
    })
  })

  let errorCount = $derived(stepEvents.filter(e => e.status === 'error').length)
  let hasErrors = $derived(errorCount > 0)
  let allFailed = $derived(stepEvents.length > 0 && stepEvents.every(e => e.status === 'error'))
  let isExpanded = $derived(session.expanded)

  let title = $derived.by(() => {
    // Use first non-trigger event's summary for title
    const first = stepEvents[0]
    if (first?.summary) {
      const text = first.summary.replace(/\n/g, ' ').trim()
      if (text.length > 60) return text.slice(0, 57) + '...'
      if (text) return text
    }
    const cid = session.conversation_id || ''
    return cid.replace(/^chan:/, '').replace(/^default:/, '') || 'Session'
  })

  let totalDuration = $derived.by(() => {
    if (events.length < 2) return events[0]?.duration_ms || 0
    const first = new Date(events[0].timestamp).getTime()
    const last = new Date(events[events.length - 1].timestamp).getTime()
    return last - first + (events[events.length - 1].duration_ms || 0)
  })

  let totalCost = $derived.by(() => {
    let sum = 0
    for (const e of events) {
      if (e.category !== 'llm') continue
      const d = parseDetail(e.detail)
      if (typeof d.cost === 'number') sum += d.cost
    }
    return sum
  })

  function formatCost(c) {
    if (!c || c <= 0) return ''
    if (c < 0.01) return `$${c.toFixed(4)}`
    return `$${c.toFixed(3)}`
  }

  let statusChip = $derived.by(() => {
    if (!hasErrors) return null
    if (allFailed) return { text: `${errorCount} error${errorCount > 1 ? 's' : ''}`, cls: 'chip-error' }
    return { text: `recovered \u00b7 ${errorCount} error${errorCount > 1 ? 's' : ''}`, cls: 'chip-warn' }
  })

  // Composition chip excludes trigger events
  let compositionChip = $derived.by(() => {
    const counts = {}
    for (const e of stepEvents) {
      const cat = e.category === 'tool_call' ? 'tool' : e.category
      counts[cat] = (counts[cat] || 0) + 1
    }
    return Object.entries(counts).map(([k, v]) => `${v} ${k}`).join(' \u00b7 ')
  })

  let dotClass = $derived(allFailed ? 'dot-error' : 'dot-ok')

  // Trigger display helpers
  let triggerType = $derived(triggerEvent?.detail?.trigger_type || null)
  let triggerPrompt = $derived(triggerEvent?.detail?.prompt || '')
  let triggerUserName = $derived(triggerEvent?.detail?.user_name || '')
  let triggerAdapter = $derived(triggerEvent?.detail?.adapter || triggerEvent?.source || '')
  let triggerScheduleName = $derived(triggerEvent?.detail?.schedule_name || '')
  let triggerScheduleCron = $derived(triggerEvent?.detail?.schedule_cron || '')
  let triggerSkillName = $derived(triggerEvent?.detail?.skill_name || '')
  let triggerUserInitial = $derived(triggerUserName ? triggerUserName.charAt(0).toUpperCase() : '?')
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
    <span class="session-title">{title}</span>
    {#if compositionChip}
      <span class="session-steps">{compositionChip}</span>
    {/if}
    {#if statusChip}
      <span class="session-chip {statusChip.cls}">{statusChip.text}</span>
    {/if}
    <span class="spacer"></span>
    <span class="session-duration">{formatDuration(totalDuration)}</span>
    {#if totalCost > 0}
      <span class="session-cost">{formatCost(totalCost)}</span>
    {/if}
    <span class="session-time">{relativeTime(session.latest)}</span>
    <span class="session-chevron" class:open={isExpanded}>{isExpanded ? '\u25be' : '\u25b8'}</span>
  </div>

  {#if isExpanded}
    <div class="session-children">
      <div class="tree-line"></div>

      <!-- Trigger block: sits above the tree -->
      {#if triggerType === 'user'}
        <div class="trigger-block trigger-user">
          <div class="trigger-header">
            <span class="trigger-avatar">{triggerUserInitial}</span>
            <span class="trigger-label trigger-label-user">USER{#if triggerUserName} &middot; {triggerUserName}{/if}</span>
            {#if triggerAdapter}
              <span class="trigger-meta">{triggerAdapter}</span>
            {/if}
            {#if triggerEvent?.timestamp}
              <span class="trigger-meta">{relativeTime(triggerEvent.timestamp)}</span>
            {/if}
          </div>
          {#if triggerPrompt}
            <div class="trigger-prompt">{triggerPrompt}</div>
          {/if}
        </div>
      {:else if triggerType === 'schedule'}
        <div class="trigger-block trigger-schedule">
          <div class="trigger-header">
            <span class="trigger-icon trigger-icon-schedule">{'\u23F1'}</span>
            <span class="trigger-label trigger-label-schedule">SCHEDULE</span>
            {#if triggerScheduleCron}
              <span class="trigger-cron">{triggerScheduleCron}</span>
            {/if}
            <span class="trigger-schedule-name">{triggerScheduleName || triggerSkillName || 'scheduled task'}</span>
            {#if triggerEvent?.timestamp}
              <span class="spacer"></span>
              <span class="trigger-meta">triggered {new Date(triggerEvent.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
            {/if}
          </div>
        </div>
      {/if}

      <!-- Step rows -->
      {#each stepEvents as event, i (event.id)}
        {#if event.category === 'session' && event.action === 'trigger'}
          {@const d = parseDetail(event.detail) || {}}
          <div
            class="inline-trigger {d.trigger_type === 'schedule' ? 'inline-trigger-schedule' : ''}"
            role="button" tabindex="0"
            aria-expanded={expandedId === event.id}
            onclick={() => onToggleRow?.(event.id)}
            onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onToggleRow?.(event.id) } }}
          >
            {#if d.trigger_type === 'schedule'}
              <span class="trigger-icon trigger-icon-schedule">{'\u23F1'}</span>
              <span class="trigger-label trigger-label-schedule">SCHEDULE</span>
              {#if d.schedule_cron}
                <span class="trigger-cron">{d.schedule_cron}</span>
              {/if}
              <span class="trigger-schedule-name">{d.schedule_name || d.skill_name || 'scheduled task'}</span>
            {:else}
              <span class="trigger-avatar">{(d.user_name || '?').charAt(0).toUpperCase()}</span>
              <span class="trigger-label trigger-label-user">USER</span>
              {#if d.prompt}
                <span class="inline-trigger-text" class:truncated={expandedId !== event.id}>{d.prompt}</span>
              {/if}
            {/if}
            <span class="spacer"></span>
            <span class="trigger-meta">{relativeTime(event.timestamp)}</span>
          </div>
        {:else}
          <div class="child-row">
            <span class="tree-branch">{i === stepEvents.length - 1 ? '\u2514' : '\u251c'}</span>
            <div class="child-content">
              <AuditRow
                {event}
                expanded={expandedId === event.id}
                ontoggle={() => onToggleRow?.(event.id)}
                compact={true}
              />
            </div>
          </div>
        {/if}
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
  .session-cost { font-size: 11px; color: #5F4A35; flex-shrink: 0; font-variant-numeric: tabular-nums; }
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

  /* ─── Trigger blocks ─── */
  .trigger-block {
    margin: 0 -6px 8px -22px;
    padding: 10px 12px 10px 22px;
    border-radius: 0 4px 4px 0;
  }
  .trigger-user {
    background: rgba(127,119,221,0.05);
    border-left: 2px solid #7F77DD;
  }
  .trigger-schedule {
    background: rgba(55,138,221,0.04);
    border-left: 2px solid rgba(55,138,221,0.5);
  }

  .trigger-header {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .trigger-avatar {
    width: 18px; height: 18px;
    border-radius: 50%;
    background: #7F77DD;
    color: #fff;
    font-size: 9px;
    font-weight: 500;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }
  .trigger-icon {
    width: 18px; height: 18px;
    border-radius: 4px;
    font-size: 11px;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }
  .trigger-icon-schedule {
    background: rgba(55,138,221,0.15);
    color: #185FA5;
  }

  .trigger-label {
    font-size: 10px;
    font-weight: 500;
    letter-spacing: 0.4px;
    flex-shrink: 0;
  }
  .trigger-label-user { color: #3C3489; }
  .trigger-label-schedule { color: #185FA5; }

  .trigger-meta {
    font-size: 10px;
    color: var(--text-muted);
  }
  .trigger-cron {
    font-family: monospace;
    font-size: 10px;
    color: var(--text-muted);
    background: rgba(44,24,16,0.05);
    padding: 1px 6px;
    border-radius: 3px;
  }
  .trigger-schedule-name {
    font-size: 11px;
    color: var(--text);
  }

  .trigger-prompt {
    font-size: 12px;
    color: var(--text);
    line-height: 1.55;
    padding-left: 26px;
    margin-top: 4px;
  }

  /* ─── Inline follow-up trigger ─── */
  .inline-trigger {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 6px -6px 6px -22px;
    padding: 8px 12px 8px 22px;
    background: rgba(127,119,221,0.05);
    border-left: 2px solid #7F77DD;
    border-radius: 0 4px 4px 0;
    cursor: pointer;
    transition: background 0.1s;
  }
  .inline-trigger:hover { background: rgba(127,119,221,0.09); }
  .inline-trigger-schedule {
    background: rgba(55,138,221,0.04);
    border-left-color: rgba(55,138,221,0.5);
  }
  .inline-trigger-schedule:hover { background: rgba(55,138,221,0.08); }
  .inline-trigger:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .inline-trigger-text {
    font-size: 12px;
    color: var(--text);
    line-height: 1.45;
    min-width: 0;
  }
  .inline-trigger-text.truncated {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* ─── Step rows ─── */
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
