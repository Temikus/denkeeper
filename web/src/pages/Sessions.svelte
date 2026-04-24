<script>
  import { onMount } from 'svelte'
  import { currentRoute } from '../router.js'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let sessions = $state([])
  let selected = $state(null)
  let messages = $state([])
  let error = $state('')
  let loadingMsgs = $state(false)
  let confirmDeleteId = $state(null)
  let deletingSession = $state(false)

  // Pagination state
  let total = $state(0)
  let offset = $state(0)
  const limit = 50
  let loadingMore = $state(false)

  // Telemetry state
  let stats = $state(null)
  let toolCalls = $state([])
  let skillUsages = $state([])

  // Clear/Compact state
  let confirmClearId = $state(null)
  let clearingSession = $state(false)
  let compactingSession = $state(null)

  onMount(async () => {
    try {
      const res = await api.sessions({ limit })
      sessions = res.sessions || []
      total = res.total || 0
      // Auto-select session from URL (e.g. #/sessions/abc123)
      const parts = $currentRoute.split('/')
      if (parts.length > 1 && parts[1]) {
        const target = sessions.find(s => s.id === parts[1])
        if (target) selectSession(target)
      }
    } catch(e) { error = e.message }
  })

  async function loadMore() {
    loadingMore = true
    offset += limit
    try {
      const res = await api.sessions({ limit, offset })
      sessions = [...sessions, ...(res.sessions || [])]
      total = res.total || 0
    } catch(e) { error = e.message }
    finally { loadingMore = false }
  }

  async function selectSession(s) {
    selected = s
    messages = []
    stats = null
    toolCalls = []
    skillUsages = []
    loadingMsgs = true
    error = ''
    try {
      const [msgs, st, tc, sk] = await Promise.allSettled([
        api.sessionMessages(s.id),
        api.sessionStats(s.id),
        api.sessionToolCalls(s.id),
        api.sessionSkills(s.id),
      ])
      messages = msgs.status === 'fulfilled' ? msgs.value : []
      stats = st.status === 'fulfilled' ? st.value : null
      toolCalls = tc.status === 'fulfilled' ? tc.value : []
      skillUsages = sk.status === 'fulfilled' ? sk.value : []
      if (msgs.status === 'rejected') error = msgs.reason.message
    } finally {
      loadingMsgs = false
    }
  }

  async function deleteSession() {
    if (!confirmDeleteId) return
    deletingSession = true
    try {
      await api.deleteSession(confirmDeleteId)
      sessions = sessions.filter(s => s.id !== confirmDeleteId)
      if (selected?.id === confirmDeleteId) { selected = null; messages = []; stats = null; toolCalls = []; skillUsages = [] }
      confirmDeleteId = null
    } catch(e) { error = e.message }
    finally { deletingSession = false }
  }

  async function clearSession() {
    if (!confirmClearId) return
    clearingSession = true
    try {
      await api.clearSession(confirmClearId)
      if (selected?.id === confirmClearId) { messages = []; stats = null; toolCalls = []; skillUsages = [] }
      confirmClearId = null
    } catch(e) { error = e.message }
    finally { clearingSession = false }
  }

  async function compactSession(id) {
    compactingSession = id
    try {
      await api.compactSession(id)
      if (selected?.id === id) {
        messages = await api.sessionMessages(id)
        try { stats = await api.sessionStats(id) } catch { /* non-critical */ }
      }
    } catch(e) { error = e.message }
    finally { compactingSession = null }
  }

  function fmtDate(s) {
    if (!s) return '\u2014'
    return new Date(s).toLocaleString()
  }

  function fmtCost(v) {
    if (!v || v === 0) return ''
    return `$${v.toFixed(4)}`
  }

  function fmtTokens(n) {
    if (!n) return '0'
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
    return String(n)
  }

  function fmtDuration(ms) {
    if (ms < 1000) return ms + 'ms'
    return (ms / 1000).toFixed(1) + 's'
  }
</script>

<h1 class="page-title">Sessions</h1>
<ErrorBanner message={error} />

<div class="layout">
  <aside class="list">
    {#each sessions as s}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="item"
        class:active={selected?.id === s.id}
        onclick={() => selectSession(s)}
        role="button"
        tabindex="0"
      >
        <div class="sid">{s.id.slice(0, 14)}</div>
        <div class="smeta">{fmtDate(s.updated_at || s.created_at)}</div>
        {#if s.channel}
          <div class="schannel">{s.channel}</div>
        {/if}
        {#if s.last_model}
          <div class="smodel">{s.last_model}</div>
        {/if}
        {#if s.total_cost > 0}
          <div class="scost">{fmtCost(s.total_cost)}</div>
        {/if}
        <button class="del" onclick={(e) => { e.stopPropagation(); confirmDeleteId = s.id }} title="Delete session">&#x2715;</button>
      </div>
    {/each}
    {#if sessions.length === 0 && !error}
      <p class="empty">No sessions.</p>
    {/if}
    {#if sessions.length > 0 && sessions.length < total}
      <button class="btn-ghost btn-sm load-more" onclick={loadMore} disabled={loadingMore}>
        {loadingMore ? 'Loading\u2026' : 'Load more'}
      </button>
    {/if}
  </aside>

  <section class="thread">
    {#if loadingMsgs}
      <p class="loading">Loading messages&#x2026;</p>
    {:else if selected}
      {#if selected.channel}
        <div class="channel-badge">Channel: {selected.channel}</div>
      {/if}

      <div class="session-actions">
        <button class="btn-ghost btn-sm" onclick={() => confirmClearId = selected.id}
          disabled={clearingSession || compactingSession}>Clear</button>
        <button class="btn-ghost btn-sm" onclick={() => compactSession(selected.id)}
          disabled={compactingSession || clearingSession}>
          {compactingSession === selected.id ? 'Compacting\u2026' : 'Compact'}
        </button>
      </div>

      {#if stats}
        <div class="stats-grid">
          <div class="stat-card">
            <div class="stat-label">Cost</div>
            <div class="stat-value">{fmtCost(stats.total_cost) || '$0'}</div>
          </div>
          <div class="stat-card">
            <div class="stat-label">Prompt</div>
            <div class="stat-value">{fmtTokens(stats.total_tokens_prompt)}</div>
          </div>
          <div class="stat-card">
            <div class="stat-label">Completion</div>
            <div class="stat-value">{fmtTokens(stats.total_tokens_completion)}</div>
          </div>
          {#if stats.total_tokens_cached > 0}
            <div class="stat-card">
              <div class="stat-label">Cached</div>
              <div class="stat-value">{fmtTokens(stats.total_tokens_cached)}</div>
            </div>
          {/if}
          <div class="stat-card">
            <div class="stat-label">Tool Calls</div>
            <div class="stat-value">{stats.total_tool_calls}{stats.total_tool_errors > 0 ? ` (${stats.total_tool_errors} err)` : ''}</div>
          </div>
          <div class="stat-card">
            <div class="stat-label">Model</div>
            <div class="stat-value stat-value-sm">{stats.last_model || '\u2014'}</div>
          </div>
          <div class="stat-card">
            <div class="stat-label">Provider</div>
            <div class="stat-value stat-value-sm">{stats.last_provider || '\u2014'}</div>
          </div>
        </div>
      {/if}

      {#if toolCalls.length > 0}
        <details class="detail-section">
          <summary>Tool Calls ({toolCalls.length})</summary>
          <table class="table">
            <thead>
              <tr>
                <th>Tool</th>
                <th>Server</th>
                <th>Duration</th>
                <th>Status</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {#each toolCalls as tc}
                <tr>
                  <td class="mono">{tc.tool_name}</td>
                  <td class="mono">{tc.server_name}</td>
                  <td>{fmtDuration(tc.duration_ms)}</td>
                  <td class={tc.success ? 'status-ok' : 'status-err'}>{tc.success ? 'ok' : tc.error_msg || 'error'}</td>
                  <td class="ts-cell">{fmtDate(tc.created_at)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </details>
      {/if}

      {#if skillUsages.length > 0}
        <details class="detail-section">
          <summary>Skills ({skillUsages.length})</summary>
          <table class="table">
            <thead>
              <tr>
                <th>Skill</th>
                <th>Match</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {#each skillUsages as su}
                <tr>
                  <td class="mono">{su.skill_name}</td>
                  <td>{su.match_type}</td>
                  <td class="ts-cell">{fmtDate(su.created_at)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </details>
      {/if}

      {#each messages as m}
        <div class="msg" class:user={m.role === 'user'} class:assistant={m.role === 'assistant'}>
          <div class="role">{m.role}{m.tokens_used ? ` \u00B7 ${m.tokens_used} tokens` : ''}</div>
          <div class="body">{m.content}</div>
          <div class="ts">{fmtDate(m.created_at)}</div>
        </div>
      {/each}
      {#if messages.length === 0}
        <p class="empty">No messages in this session.</p>
      {/if}
    {:else}
      <p class="empty">Select a session to view messages.</p>
    {/if}
  </section>
</div>

{#if confirmDeleteId}
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmDeleteId = null }} onkeydown={(e) => { if (e.key === 'Escape') confirmDeleteId = null }} role="dialog" aria-modal="true" tabindex="-1">
    <div class="confirm-modal">
      <h2>Delete Session</h2>
      <p>Delete session <strong>{confirmDeleteId.slice(0, 12)}</strong>? This cannot be undone.</p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={deleteSession} disabled={deletingSession}>
          {deletingSession ? 'Deleting\u2026' : 'Delete'}
        </button>
        <button class="btn-ghost" onclick={() => confirmDeleteId = null}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

{#if confirmClearId}
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmClearId = null }} onkeydown={(e) => { if (e.key === 'Escape') confirmClearId = null }} role="dialog" aria-modal="true" tabindex="-1">
    <div class="confirm-modal">
      <h2>Clear Session</h2>
      <p>Remove all messages from session <strong>{confirmClearId.slice(0, 12)}</strong>? The session will remain but its history will be lost.</p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={clearSession} disabled={clearingSession}>
          {clearingSession ? 'Clearing\u2026' : 'Clear'}
        </button>
        <button class="btn-ghost" onclick={() => confirmClearId = null}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .layout { display: flex; gap: 16px; height: calc(100vh - 110px); }
  .list {
    width: 240px; flex-shrink: 0;
    overflow-y: auto;
    display: flex; flex-direction: column; gap: 4px;
  }
  .item {
    position: relative;
    padding: 10px 34px 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    cursor: pointer;
  }
  .item:hover, .item.active { border-color: var(--accent); }
  .sid { font-family: monospace; font-size: 12px; }
  .smeta { font-size: 11px; color: var(--text-muted); margin-top: 3px; }
  .schannel { font-size: 11px; color: var(--accent); margin-top: 2px; font-family: monospace; }
  .smodel { font-size: 10px; color: var(--text-muted); margin-top: 2px; font-family: monospace; }
  .scost { font-size: 10px; color: var(--text-muted); margin-top: 1px; font-family: monospace; }
  .del {
    position: absolute; top: 8px; right: 8px;
    background: none; border: none; color: var(--text-muted);
    cursor: pointer; font-size: 13px; padding: 2px 5px;
    border-radius: 3px;
  }
  .del:hover { color: var(--danger); background: rgba(224,92,110,0.1); }
  .load-more { width: 100%; margin-top: 4px; }

  .session-actions {
    display: flex;
    gap: 8px;
    padding: 0 0 12px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 12px;
  }

  .thread { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 10px; padding-bottom: 16px; }

  /* Stats grid */
  .stats-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));
    gap: 8px;
    margin-bottom: 12px;
  }
  .stat-card {
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .stat-label {
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 4px;
  }
  .stat-value { font-size: 16px; font-weight: 700; }
  .stat-value-sm { font-size: 12px; font-family: monospace; }

  /* Detail sections (tool calls, skills) */
  .detail-section {
    margin-bottom: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .detail-section summary {
    padding: 8px 12px;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
    color: var(--text-muted);
  }
  .detail-section summary:hover { color: var(--accent); }

  .table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .table th {
    text-align: left;
    padding: 6px 10px;
    font-size: 10px;
    text-transform: uppercase;
    color: var(--text-muted);
    letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border);
  }
  .table td {
    padding: 5px 10px;
    border-bottom: 1px solid var(--border);
  }
  .table tr:last-child td { border-bottom: none; }
  .mono { font-family: monospace; }
  .ts-cell { font-size: 10px; color: var(--text-muted); white-space: nowrap; }
  .status-ok { color: var(--accent); }
  .status-err { color: var(--danger); }

  .msg {
    padding: 10px 14px;
    border-radius: var(--radius);
    border: 1px solid var(--border);
    background: var(--surface);
    max-width: 75%;
  }
  .msg.user      { align-self: flex-end; border-color: var(--accent); }
  .msg.assistant { align-self: flex-start; }
  .role { font-size: 10px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 5px; }
  .body { white-space: pre-wrap; word-break: break-word; font-size: 13px; line-height: 1.5; }
  .ts   { font-size: 10px; color: var(--text-muted); margin-top: 6px; }
  .channel-badge { font-size: 12px; color: var(--text-muted); padding: 4px 0 8px; font-family: monospace; }
  .empty, .loading { color: var(--text-muted); font-size: 13px; padding: 8px 0; }
</style>
