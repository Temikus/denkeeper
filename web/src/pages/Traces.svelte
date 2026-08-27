<script>
  // Turn inspector: what a turn actually saw. The audit log has rounds and
  // outcomes; this has the built system prompt, the history window as it went
  // on the wire, and every tool call with its arguments and result.
  //
  // Rows expand inline (house rule: no modal), and the transcript half is the
  // same DryRunTranscript the previews and eval results use.
  import { onMount } from 'svelte'
  import { api, traceTranscript } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import FilterChips from '../components/FilterChips.svelte'
  import Collapsible from '../components/Collapsible.svelte'
  import DryRunTranscript from '../components/DryRunTranscript.svelte'
  import { relativeTime } from '../relativeTime.js'

  const limit = 50

  let traces = $state([])
  let total = $state(0)
  let offset = $state(0)
  let capture = $state(false)
  let retentionDays = $state(0)
  // loading is the first read only; a refetch dims the list instead, so the
  // chip the operator just arrow-keyed onto is never unmounted under them.
  let loading = $state(true)
  let refreshing = $state(false)
  let loaded = $state(false)
  let loadingMore = $state(false)
  let error = $state('')

  let source = $state('')
  let agentFilter = $state('')
  let agents = $state([])

  // One expanded row at a time: two open transcripts do not fit on a screen
  // and the comparison they invite is what the eval results view is for.
  let expandedId = $state(null)
  let detail = $state(null)
  let detailError = $state('')
  let loadingDetail = $state(false)

  const sourceItems = [
    { value: '',       label: 'All',      testid: 'source-all' },
    { value: 'live',   label: 'Live',     testid: 'source-live' },
    { value: 'eval',   label: 'Eval',     testid: 'source-eval' },
  ]
  // No dry-run chip: a preview's trace rides out on its response and is never
  // stored, so the filter would always come back empty.

  let agentItems = $derived([
    { value: '', label: 'All' },
    ...agents.map(a => ({ value: a, label: a })),
  ])

  // Every list read carries a sequence number: a slow refresh landing after a
  // newer one would otherwise overwrite the list the operator is looking at.
  let listSeq = 0

  onMount(async () => {
    await refresh()
    try {
      const res = await api.agents()
      agents = (res.agents || []).map(a => a.name).filter(Boolean)
    } catch { /* the agent chips are a convenience, not the page */ }
  })

  async function refresh() {
    const seq = ++listSeq
    refreshing = true
    error = ''
    offset = 0
    collapse()
    try {
      const res = await api.traces({ limit, source, agent: agentFilter })
      if (seq !== listSeq) return
      traces = res.traces || []
      total = res.total || 0
      capture = !!res.capture
      retentionDays = res.retention_days || 0
      loaded = true
    } catch (e) {
      if (seq === listSeq) error = e.message
    } finally {
      if (seq === listSeq) {
        loading = false
        refreshing = false
      }
    }
  }

  async function loadMore() {
    const seq = ++listSeq
    loadingMore = true
    error = ''
    // The offset only advances once the page it asked for is in hand: a failed
    // read that moved it would silently skip a page on the next press.
    const next = offset + limit
    try {
      const res = await api.traces({ limit, offset: next, source, agent: agentFilter })
      if (seq !== listSeq) return
      traces = appendNewRows(traces, res.traces || [])
      total = res.total || 0
      offset = next
    } catch (e) {
      if (seq === listSeq) error = e.message
    } finally {
      if (seq === listSeq) loadingMore = false
    }
  }

  // Offset paging over a table that is still being written to shifts rows down
  // a page, so a row already on screen can come back in the next one. The each
  // block is keyed by id, and a duplicate key is a hard Svelte error, so the
  // overlap is dropped here.
  function appendNewRows(current, incoming) {
    const seen = new Set(current.map(r => r.id))
    return [...current, ...incoming.filter(r => !seen.has(r.id))]
  }

  function setSource(v) {
    source = v
    refresh()
  }

  function setAgent(v) {
    agentFilter = v
    refresh()
  }

  function collapse() {
    expandedId = null
    detail = null
    detailError = ''
  }

  async function toggle(row) {
    if (expandedId === row.id) {
      collapse()
      return
    }
    const id = row.id
    expandedId = id
    detail = null
    detailError = ''
    loadingDetail = true
    try {
      const loadedDetail = await api.trace(id)
      // Opening B while A is still in flight must not drop A's trace into B's
      // panel: a mismatched prompt is worse than a slow one.
      if (expandedId !== id) return
      detail = loadedDetail
    } catch (e) {
      if (expandedId === id) detailError = e.message
    } finally {
      if (expandedId === id) loadingDetail = false
    }
  }

  function fmtCost(v) {
    return `$${(v || 0).toFixed(4)}`
  }

  function fmtDuration(ms) {
    if (typeof ms !== 'number' || ms < 0) return '—'
    return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms} ms`
  }

  function fmtAbsolute(t) {
    return t ? new Date(t).toLocaleString() : ''
  }

  let transcript = $derived(detail ? traceTranscript(detail) : null)
</script>

<div class="traces-page">
  <div class="page-header">
    <h1 class="page-title">Turn inspector</h1>
    {#if loaded}
      <!-- Always shown once the page has read: an operator looking at eval
           traces would otherwise have no hint that live turns are not being
           recorded. -->
      {#if capture}
        <span class="header-note" data-testid="capture-on">
          Capturing live turns{retentionDays > 0 ? `, kept ${retentionDays} days` : ''}
        </span>
      {:else}
        <span class="header-note" data-testid="capture-off-note">Live capture off — eval samples only</span>
      {/if}
    {/if}
  </div>

  <ErrorBanner message={error} />

  {#if loaded && !error && (traces.length > 0 || source || agentFilter)}
    <!-- Mounted across a refetch: FilterChips owns a roving-tabindex
         radiogroup, and unmounting it mid-interaction throws focus to body. -->
    <div class="filters">
      <span class="filter-label">Source</span>
      <FilterChips items={sourceItems} value={source} label="Source filter" size="sm"
        testid="source-filter" onselect={setSource} />
      {#if agents.length > 1}
        <span class="filter-label">Agent</span>
        <FilterChips items={agentItems} value={agentFilter} label="Agent filter" size="sm"
          testid="agent-filter" onselect={setAgent} />
      {/if}
    </div>
  {/if}

  {#if loading}
    <p class="muted" data-testid="traces-loading">Loading{'…'}</p>
  {:else if error}
    <!-- A failed read leaves traces empty and capture false; telling the
         operator to edit their config would be a guess, not a diagnosis. -->
  {:else if traces.length === 0 && !capture && !source && !agentFilter}
    <!-- "Nothing recorded" and "recording is off" are different problems, and
         only one of them is fixed by waiting. -->
    <div class="empty-state" data-testid="traces-capture-off">
      <p class="empty-lead">
        Live turn capture is off. Turn it on to record what each turn actually saw —
        the system prompt as it was assembled, the history window as it was sent, and
        every tool call with its arguments and result.
      </p>
      <p class="empty-note">
        A trace holds everything the model saw, which is the most sensitive data
        Denkeeper stores, so this stays an explicit choice. Set it in
        <code>denkeeper.toml</code> and reload the server:
      </p>
      <pre class="empty-code">[eval]
capture = true</pre>
      <p class="empty-note">
        Eval samples are traced either way — they never touch a live conversation.
      </p>
    </div>
  {:else if traces.length === 0}
    <div class="empty-state" data-testid="traces-empty">
      <p class="empty-lead">
        {#if source || agentFilter}
          No turns recorded match this filter.
        {:else}
          No turns recorded yet. The next turn this agent takes will show up here.
        {/if}
      </p>
      {#if source || agentFilter}
        <div class="empty-actions">
          <button class="btn-ghost" onclick={() => { source = ''; agentFilter = ''; refresh() }}
            data-testid="clear-filters">Clear filters</button>
        </div>
      {/if}
    </div>
  {/if}

  {#if traces.length > 0}
    <div class="rows" class:refreshing aria-busy={refreshing} data-testid="trace-rows">
      {#each traces as row (row.id)}
        <button
          class="row"
          class:expanded={expandedId === row.id}
          onclick={() => toggle(row)}
          aria-expanded={expandedId === row.id}
          aria-controls="trace-{row.id}"
          data-testid="trace-row"
        >
          <span class="lane-time" title={fmtAbsolute(row.created_at)}>{relativeTime(row.created_at)}</span>
          <span class="badge badge-{row.source}">{row.source}</span>
          <span class="lane-agent">{row.agent || '—'}</span>
          <span class="lane-model">{row.model || '—'}</span>
          <span class="lane-meta">
            {row.rounds} round{row.rounds === 1 ? '' : 's'}
            {#if row.truncated}<span class="badge badge-trimmed">TRIMMED</span>{/if}
          </span>
          <span class="lane-numbers">{row.tokens_total} tok · {fmtCost(row.cost_usd)} · {fmtDuration(row.latency_ms)}</span>
          <span class="chevron-toggle" class:open={expandedId === row.id} aria-hidden="true">&#x25B6;</span>
        </button>

        {#if expandedId === row.id}
          <div class="detail" id="trace-{row.id}" role="region"
            aria-label="Trace of {row.agent || 'agent'} at {fmtAbsolute(row.created_at)}"
            data-testid="trace-detail">
            {#if loadingDetail}
              <p class="muted" role="status" data-testid="detail-loading">Loading trace{'…'}</p>
            {:else if detailError}
              <div class="banner error" role="alert" data-testid="detail-error">{detailError}</div>
            {:else if detail}
              <p class="conv" title={detail.conversation_id}>
                <span class="conv-label">Conversation</span>
                <code>{detail.conversation_id || '—'}</code>
              </p>

              {#if detail.truncation}
                <div class="banner warning" data-testid="trace-truncation">{detail.truncation.note}</div>
              {/if}

              <div class="prompt-block">
                <div class="block-label">Message that started the turn</div>
                <pre class="block-body">{detail.prompt || '(empty)'}</pre>
              </div>

              <Collapsible title="System prompt as sent" id="trace-{row.id}-system" open={false}>
                <pre class="block-body tall" data-testid="trace-system-prompt">{detail.system_prompt || '(empty)'}</pre>
              </Collapsible>

              <Collapsible title="History window as sent ({detail.history.length})" id="trace-{row.id}-history" open={false}>
                {#if detail.history.length === 0}
                  <p class="muted" data-testid="trace-history-empty">No preceding context — the model saw the system prompt and this turn.</p>
                {:else}
                  <div class="history" data-testid="trace-history">
                    {#each detail.history as m, i (i)}
                      <div class="history-msg">
                        <span class="role">{m.role}</span>
                        <pre class="block-body">{m.content}</pre>
                      </div>
                    {/each}
                  </div>
                {/if}
              </Collapsible>

              <div class="transcript">
                <DryRunTranscript transcript={transcript} label={detail.source} />
              </div>
            {/if}
          </div>
        {/if}
      {/each}
    </div>
  {/if}

  {#if traces.length > 0 && traces.length < total}
    <div class="load-more">
      <button class="btn-ghost" onclick={loadMore} disabled={loadingMore} data-testid="load-more">
        {loadingMore ? 'Loading…' : 'Load older turns'}
      </button>
    </div>
  {/if}
</div>

<style>
  .traces-page { padding: 18px; max-width: 1100px; }

  .header-note { font-size: 11px; color: var(--text-muted); }

  .filters {
    display: grid;
    grid-template-columns: 48px 1fr;
    align-items: center;
    gap: 8px 10px;
    margin-bottom: 14px;
  }
  .filter-label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .empty-state {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 24px;
    max-width: 620px;
  }
  .empty-lead { font-size: 14px; color: var(--text); margin-bottom: 12px; line-height: 1.6; }
  .empty-note { font-size: 12px; color: var(--text-muted); margin-bottom: 10px; line-height: 1.6; }
  .empty-code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 10px 12px;
    margin-bottom: 12px;
    white-space: pre;
  }
  .empty-actions { display: flex; gap: 8px; flex-wrap: wrap; }

  .rows.refreshing { opacity: 0.55; transition: opacity 0.1s; }
  .rows {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 9px 12px;
    background: var(--bg);
    border: none;
    border-top: 1px solid var(--border);
    width: 100%;
    text-align: left;
    cursor: pointer;
    font-family: inherit;
  }
  .row:first-child { border-top: none; }
  .row:hover { background: var(--hover-overlay); }
  /* The house expanded tint, not --surface: the detail panel below already
     uses --surface, so the two would merge into one undifferentiated block. */
  .row.expanded { background: rgba(var(--accent-rgb), 0.08); }
  .row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

  .lane-time { width: 96px; flex-shrink: 0; font-size: 11px; color: var(--text-muted); }
  .lane-agent { width: 110px; flex-shrink: 0; font-size: 12px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .lane-model {
    width: 170px; flex-shrink: 0; font-size: 11px; color: var(--text-muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .lane-meta { flex: 1; min-width: 0; font-size: 11px; color: var(--text-muted); display: flex; align-items: center; gap: 6px; }
  .lane-numbers { flex-shrink: 0; font-size: 11px; color: var(--text-muted); }
  /* .chevron-toggle comes from shared.css — same glyph, same rotation. */

  .badge {
    display: inline-block; font-size: 10px; font-weight: 600; letter-spacing: 0.05em;
    border: 1px solid var(--border); border-radius: 4px; padding: 1px 5px;
    color: var(--text-muted); text-transform: uppercase; flex-shrink: 0;
  }
  .badge-live { color: var(--accent); border-color: var(--accent); }
  .badge-trimmed { color: var(--warn); border-color: var(--warn); }

  .detail {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 14px 12px 18px;
    background: var(--surface);
    border-top: 1px solid var(--border);
  }

  .conv { font-size: 11px; color: var(--text-muted); display: flex; gap: 8px; align-items: baseline; }
  .conv-label { text-transform: uppercase; letter-spacing: 0.06em; font-weight: 600; }
  .conv code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

  .block-label {
    font-size: 11px; font-weight: 600; letter-spacing: 0.06em;
    text-transform: uppercase; color: var(--text-muted); margin-bottom: 6px;
  }
  .block-body {
    font-size: 12px; line-height: 18px; color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    background: var(--bg); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 10px 12px; margin: 0;
    max-height: 240px; overflow: auto; white-space: pre-wrap; word-break: break-word;
  }
  .block-body.tall { max-height: 420px; }

  .history { display: flex; flex-direction: column; gap: 8px; }
  .history-msg { display: flex; flex-direction: column; gap: 4px; }
  .role {
    font-size: 10px; font-weight: 600; letter-spacing: 0.05em;
    text-transform: uppercase; color: var(--text-muted);
  }

  .transcript { margin-top: 4px; }

  .load-more { display: flex; justify-content: center; margin-top: 14px; }

  @media (max-width: 720px) {
    .lane-model, .lane-numbers { display: none; }
    .lane-agent { width: 90px; }
  }

  /* At 320px the fixed lanes plus a TRIMMED badge overflow, so the row wraps
     rather than scrolling the page sideways. */
  @media (max-width: 420px) {
    .row { flex-wrap: wrap; gap: 8px; }
    .lane-time { width: auto; }
    .lane-agent { width: auto; max-width: 45%; }
  }
</style>
