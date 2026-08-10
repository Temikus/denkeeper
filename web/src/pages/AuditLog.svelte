<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from '../api.js'
  import { parseAuditSearch } from '../auditSearch.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import AuditSession from '../components/AuditSession.svelte'
  import AuditRow from '../components/AuditRow.svelte'
  import FilterChips from '../components/FilterChips.svelte'

  let events = $state([])
  let stats = $state(null)
  let error = $state('')
  let loading = $state(true)
  let total = $state(0)
  let offset = $state(0)
  const limit = 50

  // Type and Status are unions (multi-select chips); an empty array is "All".
  let categories = $state([])
  let statuses = $state([])
  let timeRange = $state('24h')
  let search = $state('')
  let agent = $state('')
  let view = $state('timeline')
  let follow = $state(false)
  let refreshTimer
  let refreshing = $state(false)
  let expandedRowId = $state(null)
  let expandedSessions = $state(new Set())

  const categoryItems = [
    { value: '', label: 'All' },
    { value: 'tool_call', label: 'Tools' },
    { value: 'llm', label: 'LLM' },
    { value: 'approval', label: 'Approvals' },
    { value: 'config', label: 'Config' },
    { value: 'mcp', label: 'MCP' },
    { value: 'session', label: 'Sessions' },
    { value: 'skill', label: 'Skills' },
    { value: 'supervisor', label: 'Supervisor' },
  ]
  const statusItems = [
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

  // Guards against out-of-order responses: filters can change faster than the
  // API answers (arrow-keying across the chips), and a stale reply would
  // otherwise repaint the list with a filter the user has already left.
  let loadSeq = 0

  // `quiet` suppresses the in-flight marker for background work (the Follow
  // poll): dimming the list every 5s unprompted is worse than showing nothing.
  async function load(append = false, quiet = false) {
    const seq = ++loadSeq
    try {
      error = ''
      if (!append && !quiet) refreshing = true
      const since = sinceFromRange(timeRange)
      const res = await api.auditEvents({ category: categories, status: statuses, agent, search, since, limit: String(limit), offset: String(append ? offset : 0) })
      if (seq !== loadSeq) return
      if (append) { events = [...events, ...res.events] }
      else { events = res.events; offset = 0 }
      total = res.total
    } catch (e) {
      if (seq === loadSeq) error = e.message
    }
    finally { if (seq === loadSeq) { loading = false; refreshing = false } }
  }

  async function loadStats() {
    try { stats = await api.auditStats(sinceFromRange(timeRange)) } catch { /* non-critical */ }
  }

  function refresh(quiet = false) { load(false, quiet); loadStats() }
  function loadMore() { offset += limit; load(true) }

  // Range chips select on arrow-key focus, so a keyboard user sweeping the bar
  // would fire one request per chip; toggling several Type chips is the same
  // shape. Coalesce them the way the search box does.
  let filterTimeout
  function setFilter(key, value) {
    if (key === 'categories') categories = value
    else if (key === 'statuses') statuses = value
    else if (key === 'timeRange') timeRange = value
    refreshing = true
    clearTimeout(filterTimeout)
    filterTimeout = setTimeout(refresh, 120)
  }
  function toggleFollow() {
    follow = !follow
    if (follow) refreshTimer = setInterval(() => refresh(true), 5000)
    else clearInterval(refreshTimer)
  }

  // `tool:`/`agent:` tokens are resolved here rather than sent verbatim: the
  // API has an exact-match agent filter but no token syntax of its own.
  let searchTimeout
  function onSearchInput(e) {
    // Marked on the keystroke rather than when the request finally goes out:
    // the debounce is part of the wait the user is sitting through, and the
    // filter chips below still describe the *previous* query until it fires.
    refreshing = true
    const raw = e.target.value
    clearTimeout(searchTimeout)
    searchTimeout = setTimeout(() => {
      const parsed = parseAuditSearch(raw)
      search = parsed.search
      agent = parsed.agent
      refresh()
    }, 300)
  }

  // Chips describe the request that was sent, not the tokens that were typed:
  // `tool:a tool:b` is one phrase match on the wire, so it reads as one chip,
  // and untokenized text gets a chip too. The two filters read differently
  // server-side — agent is exact, search is a summary substring — so they say
  // so, which is the mapping the token syntax exists to expose.
  let activeFilters = $derived([
    ...(agent ? [`agent = ${agent}`] : []),
    ...(search ? [`summary contains "${search}"`] : []),
  ])

  // Two audiences for one flag. `aria-busy` tracks any in-flight load, first
  // paint included. The visible dim and the "Searching…" marker are reserved
  // for refetches that replace results already on screen — the first paint has
  // its own "Loading..." placeholder, and doubling it only flashes.
  let busy = $derived(refreshing && !loading)

  // Distinguishes "nothing happened yet" from "your filter matched nothing".
  function filterSuffix() {
    const parts = []
    if (agent) parts.push(`for agent "${agent}"`)
    if (search) parts.push(`matching "${search}"`)
    return parts.length ? ` ${parts.join(' ')}` : ''
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

  // Sparkline: bucket count + width adapt to the selected time range so the
  // bars actually cover the window the user filtered on.
  const sparkConfig = {
    '1h':  { count: 12, ms: 5 * 60_000 },
    '24h': { count: 24, ms: 60 * 60_000 },
    '7d':  { count: 14, ms: 12 * 60 * 60_000 },
    '30d': { count: 30, ms: 24 * 60 * 60_000 },
  }

  function fmtBucket(start, end) {
    const span = end.getTime() - start.getTime()
    if (span <= 60 * 60_000) {
      const t = (d) => d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
      return `${t(start)}\u2013${t(end)}`
    }
    if (span <= 24 * 60 * 60_000) {
      return `${start.toLocaleString([], { weekday: 'short', hour: '2-digit', minute: '2-digit' })} \u2192 ${end.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
    }
    const d = (x) => x.toLocaleDateString([], { month: 'short', day: 'numeric' })
    return `${d(start)}\u2013${d(end)}`
  }

  let sparkBars = $derived(() => {
    if (!events.length) return []
    const { count, ms } = sparkConfig[timeRange] || sparkConfig['24h']
    const now = Date.now()
    const start = now - count * ms
    const buckets = new Array(count).fill(0)
    const errorBuckets = new Array(count).fill(0)
    for (const ev of events) {
      const t = new Date(ev.timestamp).getTime()
      if (t < start || t > now) continue
      const idx = Math.min(count - 1, Math.floor((t - start) / ms))
      buckets[idx]++
      if (ev.status === 'error') errorBuckets[idx]++
    }
    const max = Math.max(...buckets, 1)
    return buckets.map((c, i) => {
      const bs = new Date(start + i * ms)
      const be = new Date(start + (i + 1) * ms)
      const errs = errorBuckets[i]
      const evtLabel = `${c} event${c === 1 ? '' : 's'}`
      const errLabel = errs > 0 ? ` (${errs} error${errs === 1 ? '' : 's'})` : ''
      return {
        pct: Math.max((c / max) * 100, c > 0 ? 5 : 0),
        hasError: errs > 0,
        title: `${fmtBucket(bs, be)} \u2022 ${evtLabel}${errLabel}`,
      }
    })
  })

  function sparkRangeLabel() {
    const r = timeRange === 'custom' ? '24h' : (timeRange || '24h')
    return `${r} ago`
  }

  onMount(() => { refresh() })
  onDestroy(() => { clearInterval(refreshTimer); clearTimeout(searchTimeout); clearTimeout(filterTimeout) })
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
        <div class="sparkline-wrap">
          <div class="sparkline">
            {#each sparkBars() as bar}
              <div class="spark-bar" class:spark-error={bar.hasError} style="height: {bar.pct}%" title={bar.title}></div>
            {/each}
          </div>
          <div class="sparkline-axis">
            <span>{sparkRangeLabel()}</span>
            <span>now</span>
          </div>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Filters -->
  <div class="filters">
    <span class="filter-label">Type</span>
    <FilterChips items={categoryItems} value={categories} label="Category filter" size="sm" multiple
      onselect={(v) => setFilter('categories', v)} />
    <span class="filter-label">Status</span>
    <FilterChips items={statusItems} value={statuses} label="Status filter" size="sm" multiple
      onselect={(v) => setFilter('statuses', v)} />
    <span class="filter-label">Range</span>
    <FilterChips items={timeRanges} value={timeRange} label="Time range" size="sm"
      onselect={(v) => setFilter('timeRange', v)} />
  </div>

  <!-- Search -->
  <div class="search-card">
    <span class="search-icon">{'\u2315'}</span>
    <input type="text" class="search-input" placeholder="Search events" aria-label="Search audit events" oninput={onSearchInput} />
    <!-- Mounted even when empty: a live region inserted together with its
         content is not reliably announced, and the first filter is the one
         that matters. The in-flight marker shares the region rather than
         adding a second one, for the same reason \u2014 and because "a query is
         running" and "this is what it filtered on" are one status, read in
         that order. -->
    <span class="search-filters" class:has-filters={activeFilters.length > 0} role="status">
      {#if busy}
        <span class="search-status">Searching{'\u2026'}</span>
      {/if}
      {#if activeFilters.length > 0}
        <span class="search-hint">filtering</span>
        {#each activeFilters as f}
          <code class="search-example is-active">{f}</code>
        {/each}
      {/if}
    </span>
    {#if activeFilters.length === 0 && !busy}
      <span class="search-hint is-hint">try</span>
      <code class="search-example is-hint">tool:name</code>
      <code class="search-example is-hint">agent:planner</code>
    {/if}
  </div>

  <!-- Event list -->
  <div class="results" class:is-refreshing={busy} aria-busy={refreshing}>
    {#if loading}
      <p class="empty">Loading...</p>
    {:else if groupedItems.length === 0}
      <p class="empty">No audit events found{filterSuffix()}.</p>
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
  </div>

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
  .sparkline-wrap { flex: 1; display: flex; flex-direction: column; gap: 3px; }
  .sparkline { display: flex; align-items: flex-end; gap: 2px; height: 24px; }
  .spark-bar { flex: 1; background: rgba(139,115,85,0.3); border-radius: 1px; min-height: 1px; cursor: help; }
  .spark-bar:hover { background: rgba(139,115,85,0.55); }
  .spark-error { background: var(--accent); }
  .spark-error:hover { background: var(--accent); opacity: 0.85; }
  .sparkline-axis { display: flex; justify-content: space-between; font-size: 10px; color: var(--text-muted); }

  /* Filters */
  .filters {
    display: grid; grid-template-columns: 48px 1fr;
    gap: 6px 10px; margin-bottom: 8px; font-size: 11px; align-items: center;
  }
  .filter-label { color: var(--text-muted); font-weight: 500; }

  /* Search card */
  .search-card {
    display: flex; align-items: center; gap: 8px;
    margin: 10px 0 14px; padding: 6px 10px;
    background: white; border: 0.5px solid rgba(44,24,16,0.1);
    border-radius: var(--radius); font-size: 12px;
  }
  .search-icon { color: var(--text-muted); font-size: 13px; }
  .search-input {
    flex: 1; min-width: 0; border: none; background: transparent;
    font-size: 12px; color: var(--text); outline: none;
  }
  .search-input::placeholder { color: var(--text-muted); }
  .search-hint { color: var(--text-muted); font-size: 11px; }
  .search-example {
    background: rgba(44,24,16,0.06); padding: 1px 6px;
    border-radius: 3px; font-size: 11px; color: #5F4A35;
  }
  /* Live filters read as state, not as the muted examples they replace —
     same accent tint the FAILED pill uses for its own state below. */
  .search-example.is-active {
    background: rgba(200,78,53,0.10); color: var(--accent);
    max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .search-filters { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }

  /* In-flight marker. Every keystroke-debounce and filter chip refetches, so a
     full-page "Loading..." swap would flicker; the current results dim instead
     and the live region names the state. The filter chips above describe the
     *last* request that went out, so without this the window before a new one
     lands reads as a settled result. */
  .search-status { color: var(--text-muted); font-size: 11px; white-space: nowrap; }
  .results { transition: opacity 0.12s ease; }
  .results.is-refreshing { opacity: 0.5; }

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
    /* Examples are optional prompting; active filters must stay visible, and
       wrap to their own line rather than crushing the input. */
    .is-hint { display: none; }
    .search-card { flex-wrap: wrap; }
    .search-filters.has-filters { flex-basis: 100%; }
  }
</style>
