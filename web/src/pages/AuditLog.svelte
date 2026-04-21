<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import AuditSession from '../components/AuditSession.svelte'
  import AuditRow from '../components/AuditRow.svelte'

  let events = $state([])
  let stats = $state(null)
  let error = $state('')
  let loading = $state(true)
  let total = $state(0)
  let offset = $state(0)
  const limit = 50

  let category = $state('')
  let status = $state('')
  let timeRange = $state('24h')
  let search = $state('')
  let view = $state('timeline')
  let follow = $state(false)
  let refreshTimer
  let refreshing = $state(false)
  let expandedRowId = $state(null)
  let expandedSessions = $state(new Set())

  const categories = [
    { value: '', label: 'All' },
    { value: 'tool_call', label: 'Tools' },
    { value: 'llm', label: 'LLM' },
    { value: 'approval', label: 'Approvals' },
    { value: 'config', label: 'Config' },
    { value: 'mcp', label: 'MCP' },
    { value: 'session', label: 'Sessions' },
    { value: 'skill', label: 'Skills' },
  ]
  const statuses = [
    { value: '', label: 'All' },
    { value: 'ok', label: 'OK' },
    { value: 'error', label: 'Error' },
    { value: 'denied', label: 'Denied' },
  ]
  const timeRanges = [
    { value: '1h', label: '1h' },
    { value: '24h', label: '24h' },
    { value: '7d', label: '7d' },
    { value: '30d', label: '30d' },
    { value: 'custom', label: 'Custom\u2026' },
  ]

  function sinceFromRange(range) {
    if (!range || range === 'custom') return undefined
    const now = new Date()
    const offsets = { '1h': 3600000, '24h': 86400000, '7d': 7*86400000, '30d': 30*86400000 }
    return new Date(now - (offsets[range] || 86400000)).toISOString()
  }

  async function load(append = false) {
    try {
      error = ''
      if (!append) refreshing = true
      const since = sinceFromRange(timeRange)
      const res = await api.auditEvents({ category, status, search, since, limit: String(limit), offset: String(append ? offset : 0) })
      if (append) { events = [...events, ...res.events] }
      else { events = res.events; offset = 0 }
      total = res.total
    } catch (e) { error = e.message }
    finally { loading = false; refreshing = false }
  }

  async function loadStats() {
    try { stats = await api.auditStats(sinceFromRange(timeRange)) } catch { /* non-critical */ }
  }

  function refresh() { load(); loadStats() }
  function loadMore() { offset += limit; load(true) }
  function setFilter(key, value) {
    if (key === 'category') category = value
    else if (key === 'status') status = value
    else if (key === 'timeRange') timeRange = value
    refresh()
  }
  function toggleFollow() {
    follow = !follow
    if (follow) refreshTimer = setInterval(refresh, 5000)
    else clearInterval(refreshTimer)
  }

  let searchTimeout
  function onSearchInput(e) {
    clearTimeout(searchTimeout)
    searchTimeout = setTimeout(() => { search = e.target.value; refresh() }, 300)
  }

  function toggleRow(id) { expandedRowId = expandedRowId === id ? null : id }
  function toggleSession(convId) {
    const next = new Set(expandedSessions)
    if (next.has(convId)) next.delete(convId); else next.add(convId)
    expandedSessions = next
  }

  let groupedItems = $derived(groupEvents(events))

  function groupEvents(evts) {
    const sessions = new Map()
    const standalone = []
    for (const ev of evts) {
      if (ev.conversation_id) {
        if (!sessions.has(ev.conversation_id)) sessions.set(ev.conversation_id, [])
        sessions.get(ev.conversation_id).push(ev)
      } else { standalone.push(ev) }
    }
    const items = []
    for (const [convId, sessEvents] of sessions) {
      sessEvents.sort((a, b) => new Date(a.timestamp) - new Date(b.timestamp))
      items.push({ type: 'session', conversation_id: convId, events: sessEvents,
        timestamp: sessEvents[0].timestamp, latest: sessEvents[sessEvents.length - 1].timestamp,
        expanded: expandedSessions.has(convId) })
    }
    for (const ev of standalone) items.push({ type: 'event', event: ev, timestamp: ev.timestamp })
    items.sort((a, b) => {
      const ta = a.type === 'session' ? new Date(a.latest) : new Date(a.timestamp)
      const tb = b.type === 'session' ? new Date(b.latest) : new Date(b.timestamp)
      return tb - ta
    })
    return items
  }

  function relativeTime(ts) {
    const diff = Date.now() - new Date(ts).getTime()
    if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
    return `${Math.floor(diff / 86400000)}d ago`
  }

  // Sparkline: hourly buckets from loaded events
  let sparkBars = $derived(() => {
    if (!events.length) return []
    const now = Date.now()
    const buckets = new Array(14).fill(0)
    const errorBuckets = new Array(14).fill(0)
    for (const ev of events) {
      const hoursAgo = Math.floor((now - new Date(ev.timestamp).getTime()) / 3600000)
      if (hoursAgo >= 0 && hoursAgo < 14) {
        buckets[13 - hoursAgo]++
        if (ev.status === 'error') errorBuckets[13 - hoursAgo]++
      }
    }
    const max = Math.max(...buckets, 1)
    return buckets.map((count, i) => ({
      pct: Math.max((count / max) * 100, count > 0 ? 5 : 0),
      hasError: errorBuckets[i] > 0,
    }))
  })

  onMount(() => { refresh() })
  onDestroy(() => { clearInterval(refreshTimer); clearTimeout(searchTimeout) })
</script>

<div class="audit-page">
  <!-- Header -->
  <div class="page-header">
    <div class="title-group">
      <span class="page-title">Audit log</span>
      <span class="title-meta">last {timeRange || '24h'}</span>
    </div>
    <div class="header-actions">
      <button class="btn-follow" class:active={follow} onclick={toggleFollow}>Follow</button>
      <button class="btn-export">Export</button>
      <div class="view-toggle" role="tablist" aria-label="View mode">
        <button class="view-btn" class:active={view === 'timeline'} role="tab" aria-selected={view === 'timeline'} onclick={() => view = 'timeline'}>Timeline</button>
        <button class="view-btn" class:active={view === 'table'} role="tab" aria-selected={view === 'table'} onclick={() => view = 'table'}>Table</button>
      </div>
    </div>
  </div>

  <ErrorBanner message={error} />

  <!-- Stats bar -->
  {#if stats}
    <div class="stats-card">
      <div class="stats-numbers">
        <span><span class="stat-num">{stats.total}</span> <span class="stat-label">events</span></span>
        <span><span class="stat-num">{stats.by_category?.tool_call || 0}</span> <span class="stat-label">tool</span></span>
        <span><span class="stat-num">{stats.by_category?.llm || 0}</span> <span class="stat-label">llm</span></span>
        <span><span class="stat-num" class:stat-error={stats.by_status?.error}>{stats.by_status?.error || 0}</span> <span class="stat-label">error</span></span>
      </div>
      {#if sparkBars().length > 0}
        <div class="sparkline">
          {#each sparkBars() as bar}
            <div class="spark-bar" class:spark-error={bar.hasError} style="height: {bar.pct}%"></div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Filters -->
  <div class="filters">
    <span class="filter-label">Type</span>
    <div class="filter-chips" role="radiogroup" aria-label="Category filter">
      {#each categories as c}
        <button class="chip" class:active={category === c.value} role="radio" aria-checked={category === c.value} onclick={() => setFilter('category', c.value)}>{c.label}</button>
      {/each}
    </div>
    <span class="filter-label">Status</span>
    <div class="filter-chips" role="radiogroup" aria-label="Status filter">
      {#each statuses as s}
        <button class="chip" class:active={status === s.value} role="radio" aria-checked={status === s.value} onclick={() => setFilter('status', s.value)}>{s.label}</button>
      {/each}
    </div>
    <span class="filter-label">Range</span>
    <div class="filter-chips" role="radiogroup" aria-label="Time range">
      {#each timeRanges as t}
        <button class="chip" class:active={timeRange === t.value} role="radio" aria-checked={timeRange === t.value} onclick={() => setFilter('timeRange', t.value)}>{t.label}</button>
      {/each}
    </div>
  </div>

  <!-- Search -->
  <div class="search-card">
    <span class="search-icon">{'\u2315'}</span>
    <input type="text" class="search-input" placeholder="Search events" aria-label="Search audit events" oninput={onSearchInput} />
    <span class="search-hint">try</span>
    <code class="search-example">tool:name</code>
    <code class="search-example">agent:planner</code>
  </div>

  <!-- Event list -->
  {#if loading}
    <p class="empty">Loading...</p>
  {:else if groupedItems.length === 0}
    <p class="empty">No audit events found.</p>
  {:else if view === 'timeline'}
    <div class="timeline">
      {#each groupedItems as item}
        {#if item.type === 'session'}
          <AuditSession session={item} expandedId={expandedRowId} onToggleRow={toggleRow} onToggleSession={toggleSession} />
        {:else}
          <div class="standalone-card" class:error-border={item.event.status === 'error'}>
            <AuditRow event={item.event} expanded={expandedRowId === item.event.id} ontoggle={() => toggleRow(item.event.id)} standalone={true} />
          </div>
        {/if}
      {/each}
    </div>
  {:else}
    <table class="table">
      <thead><tr><th>Time</th><th>Type</th><th>Summary</th><th>Status</th><th>Duration</th><th>Agent</th></tr></thead>
      <tbody>
        {#each events as event (event.id)}
          {@const isErr = event.status === 'error'}
          <tr class="row-clickable" class:row-expanded={expandedRowId === event.id} class:error-table-row={isErr} role="button" tabindex="0" aria-expanded={expandedRowId === event.id} onclick={() => toggleRow(event.id)} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleRow(event.id) }}}>
            <td class="date">{new Date(event.timestamp).toLocaleString()}</td>
            <td><span class="cat-badge-sm">{event.category}</span></td>
            <td class="summary-cell">{event.summary || event.action}{#if isErr} <span class="pill-failed-sm">FAILED</span>{/if}</td>
            <td><span class="status-text" class:status-err={isErr}>{event.status}</span></td>
            <td class="mono" class:dur-err={isErr}>{event.duration_ms > 0 ? `${event.duration_ms}ms` : '\u2014'}</td>
            <td class="muted">{event.agent || '\u2014'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  {#if events.length < total}
    <div class="load-more"><button class="btn-load-more" onclick={loadMore}>Load older events</button></div>
  {/if}
</div>

<style>
  .audit-page { padding: 18px; max-width: 1100px; }

  /* Header */
  .page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
  .title-group { display: flex; align-items: baseline; gap: 10px; }
  .page-title { font-size: 17px; font-weight: 500; }
  .title-meta { font-size: 11px; color: var(--text-muted); }
  .header-actions { display: flex; gap: 6px; align-items: center; }
  .btn-follow, .btn-export {
    font-size: 11px; padding: 4px 10px;
    border: 0.5px solid rgba(44,24,16,0.15); background: transparent;
    border-radius: var(--radius); color: var(--text); cursor: pointer;
  }
  .btn-follow.active { border-color: var(--accent); background: var(--accent); color: white; }
  .view-toggle { display: flex; border: 0.5px solid rgba(44,24,16,0.15); border-radius: var(--radius); overflow: hidden; font-size: 11px; }
  .view-btn { padding: 4px 10px; border: none; background: transparent; color: var(--text); cursor: pointer; }
  .view-btn.active { background: var(--accent); color: white; }

  /* Stats card */
  .stats-card {
    display: flex; align-items: center; gap: 20px;
    padding: 10px 14px; background: white; border: 0.5px solid rgba(44,24,16,0.1);
    border-radius: var(--radius); margin-bottom: 14px;
  }
  .stats-numbers { display: flex; gap: 16px; font-size: 12px; }
  .stat-num { font-weight: 500; font-size: 15px; }
  .stat-label { color: var(--text-muted); }
  .stat-error { color: var(--danger); }
  .sparkline { flex: 1; display: flex; align-items: flex-end; gap: 2px; height: 24px; }
  .spark-bar { flex: 1; background: rgba(139,115,85,0.3); border-radius: 1px; min-height: 1px; }
  .spark-error { background: var(--accent); }

  /* Filters */
  .filters {
    display: grid; grid-template-columns: 48px 1fr;
    gap: 6px 10px; margin-bottom: 8px; font-size: 11px; align-items: center;
  }
  .filter-label { color: var(--text-muted); font-weight: 500; }
  .filter-chips { display: flex; flex-wrap: wrap; gap: 4px; }
  .chip {
    padding: 3px 9px; background: transparent;
    border: 0.5px solid rgba(44,24,16,0.2); border-radius: 999px;
    font-size: 11px; color: var(--text); cursor: pointer; transition: all 0.1s;
  }
  .chip:hover { border-color: var(--accent); }
  .chip.active { background: var(--accent); color: white; border-color: var(--accent); }

  /* Search card */
  .search-card {
    display: flex; align-items: center; gap: 8px;
    margin: 10px 0 14px; padding: 6px 10px;
    background: white; border: 0.5px solid rgba(44,24,16,0.1);
    border-radius: var(--radius); font-size: 12px;
  }
  .search-icon { color: var(--text-muted); font-size: 13px; }
  .search-input {
    flex: 1; border: none; background: transparent;
    font-size: 12px; color: var(--text); outline: none;
  }
  .search-input::placeholder { color: var(--text-muted); }
  .search-hint { color: var(--text-muted); font-size: 11px; }
  .search-example {
    background: rgba(44,24,16,0.06); padding: 1px 6px;
    border-radius: 3px; font-size: 11px; color: #5F4A35;
  }

  /* Timeline */
  .timeline { display: flex; flex-direction: column; gap: 6px; }
  .standalone-card {
    background: white; border: 0.5px solid rgba(44,24,16,0.12);
    border-radius: var(--radius); overflow: hidden;
  }
  .error-border { border-color: rgba(226,75,74,0.35); }
  .empty { color: var(--text-muted); font-size: 13px; padding: 32px 0; text-align: center; }

  /* Table */
  .table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .table th { text-align: left; padding: 8px 10px; color: var(--text-muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 500; border-bottom: 1px solid var(--border); }
  .table td { padding: 8px 10px; border-bottom: 1px solid var(--border); }
  .row-clickable { cursor: pointer; }
  .row-clickable:hover { background: var(--hover-overlay); }
  .row-clickable:focus-visible { outline: 2px solid var(--accent); outline-offset: -1px; }
  .row-expanded { background: rgba(200, 78, 53, 0.08); }
  .error-table-row { background: rgba(196, 58, 58, 0.04); }
  .date { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
  .mono { font-family: monospace; font-size: 12px; }
  .muted { color: var(--text-muted); font-size: 12px; }
  .summary-cell { max-width: 350px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .cat-badge-sm { font-size: 10px; font-weight: 600; text-transform: uppercase; color: var(--text-muted); }
  .status-text { font-size: 12px; }
  .status-err { color: var(--danger); font-weight: 600; }
  .pill-failed-sm { font-size: 9px; font-weight: 700; padding: 1px 4px; border-radius: 2px; color: var(--danger); background: rgba(196,58,58,0.10); margin-left: 4px; }
  .dur-err { color: var(--danger); }

  .load-more { display: flex; justify-content: center; margin-top: 12px; }
  .btn-load-more {
    font-size: 11px; padding: 5px 14px;
    border: 0.5px solid rgba(44,24,16,0.2); background: transparent;
    border-radius: var(--radius); color: #5F4A35; cursor: pointer;
  }

  @media (max-width: 768px) {
    .page-header { flex-direction: column; align-items: flex-start; gap: 8px; }
    .filters { grid-template-columns: 40px 1fr; }
    .search-example { display: none; }
  }
</style>
