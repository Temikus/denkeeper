<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let data = $state(null)
  let error = $state('')
  let loading = $state(true)
  let agentFilter = $state('')
  let expandedAgent = $state(null)
  let sortBy = $state('cost')
  let sortAsc = $state(false)
  let sessionSortBy = $state('cost')
  let sessionSortAsc = $state(false)

  let sortedAgents = $derived(
    data?.by_agent
      ? [...data.by_agent].sort((a, b) => {
          if (sortBy === 'cost') return sortAsc ? a.cost - b.cost : b.cost - a.cost
          if (sortBy === 'messages') return sortAsc ? a.messages - b.messages : b.messages - a.messages
          if (sortBy === 'sessions') return sortAsc ? a.sessions - b.sessions : b.sessions - a.sessions
          if (sortBy === 'input') return sortAsc ? a.input_tokens - b.input_tokens : b.input_tokens - a.input_tokens
          if (sortBy === 'output') return sortAsc ? a.output_tokens - b.output_tokens : b.output_tokens - a.output_tokens
          return sortAsc ? a.agent.localeCompare(b.agent) : b.agent.localeCompare(a.agent)
        })
      : []
  )

  let agentSessions = $derived(
    expandedAgent && data?.session_stats
      ? Object.entries(data.session_stats)
          .filter(([id]) => id.startsWith(expandedAgent + ':'))
          .map(([id, s]) => ({ id, ...s }))
          .sort((a, b) => {
            if (sessionSortBy === 'cost') return sessionSortAsc ? a.cost - b.cost : b.cost - a.cost
            return sessionSortAsc ? a.id.localeCompare(b.id) : b.id.localeCompare(a.id)
          })
      : []
  )

  let totalInputTokens = $derived(
    data?.by_agent ? data.by_agent.reduce((sum, a) => sum + a.input_tokens, 0) : 0
  )
  let totalOutputTokens = $derived(
    data?.by_agent ? data.by_agent.reduce((sum, a) => sum + a.output_tokens, 0) : 0
  )
  let totalMessages = $derived(
    data?.by_agent ? data.by_agent.reduce((sum, a) => sum + a.messages, 0) : 0
  )

  function toggleSort(col) {
    if (sortBy === col) { sortAsc = !sortAsc } else { sortBy = col; sortAsc = false }
  }

  function toggleSessionSort(col) {
    if (sessionSortBy === col) { sessionSortAsc = !sessionSortAsc } else { sessionSortBy = col; sessionSortAsc = false }
  }

  function toggleAgent(name) {
    expandedAgent = expandedAgent === name ? null : name
  }

  async function fetchData() {
    loading = true
    error = ''
    try {
      data = await api.costs(agentFilter || undefined)
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function formatTokens(n) {
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
    return String(n)
  }

  onMount(fetchData)
</script>

<h1 class="page-title">Costs</h1>
<ErrorBanner message={error} />

{#if loading && !data}
  <p class="loading">Loading...</p>
{:else if data}
  <div class="grid">
    <div class="card">
      <div class="label">Total Cost</div>
      <div class="value">${data.global_cost.toFixed(4)}</div>
    </div>
    <div class="card">
      <div class="label">Session Budget</div>
      <div class="value">${data.max_per_session.toFixed(4)}</div>
    </div>
    <div class="card">
      <div class="label">Sessions</div>
      <div class="value">{data.session_count}</div>
    </div>
    <div class="card">
      <div class="label">Messages</div>
      <div class="value">{totalMessages}</div>
    </div>
    <div class="card">
      <div class="label">Input Tokens</div>
      <div class="value">{formatTokens(totalInputTokens)}</div>
    </div>
    <div class="card">
      <div class="label">Output Tokens</div>
      <div class="value">{formatTokens(totalOutputTokens)}</div>
    </div>
  </div>

  <h2 class="section-title">Per-Agent Breakdown</h2>

  {#if sortedAgents.length === 0}
    <p class="empty">No cost data recorded yet.</p>
  {:else}
    <div class="table-wrapper">
      <table class="data-table">
        <thead>
          <tr>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable" onclick={() => toggleSort('agent')} role="columnheader" tabindex="0">
              Agent {#if sortBy === 'agent'}<span class="arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable num" onclick={() => toggleSort('sessions')} role="columnheader" tabindex="0">
              Sessions {#if sortBy === 'sessions'}<span class="arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable num" onclick={() => toggleSort('messages')} role="columnheader" tabindex="0">
              Messages {#if sortBy === 'messages'}<span class="arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable num" onclick={() => toggleSort('input')} role="columnheader" tabindex="0">
              Input Tokens {#if sortBy === 'input'}<span class="arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable num" onclick={() => toggleSort('output')} role="columnheader" tabindex="0">
              Output Tokens {#if sortBy === 'output'}<span class="arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable num" onclick={() => toggleSort('cost')} role="columnheader" tabindex="0">
              Cost {#if sortBy === 'cost'}<span class="arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
          </tr>
        </thead>
        <tbody>
          {#each sortedAgents as a}
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <tr class="clickable" class:expanded={expandedAgent === a.agent} onclick={() => toggleAgent(a.agent)} role="button" tabindex="0">
              <td class="agent-name">{a.agent}</td>
              <td class="num mono">{a.sessions}</td>
              <td class="num mono">{a.messages}</td>
              <td class="num mono">{formatTokens(a.input_tokens)}</td>
              <td class="num mono">{formatTokens(a.output_tokens)}</td>
              <td class="num mono">${a.cost.toFixed(6)}</td>
            </tr>
            {#if expandedAgent === a.agent && agentSessions.length > 0}
              <tr class="sub-header">
                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <th colspan="2" class="sortable" onclick={() => toggleSessionSort('id')} role="columnheader" tabindex="0">
                  Session ID {#if sessionSortBy === 'id'}<span class="arrow">{sessionSortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
                </th>
                <th class="num">Messages</th>
                <th class="num">Input</th>
                <th class="num">Output</th>
                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <th class="sortable num" onclick={() => toggleSessionSort('cost')} role="columnheader" tabindex="0">
                  Cost {#if sessionSortBy === 'cost'}<span class="arrow">{sessionSortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
                </th>
              </tr>
              {#each agentSessions as s}
                <tr class="sub-row">
                  <td colspan="2" class="mono session-id">{s.id}</td>
                  <td class="num mono">{s.messages}</td>
                  <td class="num mono">{formatTokens(s.input_tokens)}</td>
                  <td class="num mono">{formatTokens(s.output_tokens)}</td>
                  <td class="num mono">${s.cost.toFixed(6)}</td>
                </tr>
              {/each}
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .section-title { font-size: 16px; font-weight: 600; margin: 28px 0 12px; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 14px;
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 20px 16px;
  }
  .label { font-size: 11px; color: var(--text-muted); margin-bottom: 8px; text-transform: uppercase; letter-spacing: 0.05em; }
  .value { font-size: 24px; font-weight: 700; }
  .table-wrapper {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow-x: auto;
  }
  .data-table { width: 100%; border-collapse: collapse; }
  .data-table th {
    text-align: left; padding: 10px 14px; font-size: 11px;
    color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border); user-select: none;
  }
  .data-table th.sortable { cursor: pointer; }
  .data-table th.sortable:hover { color: var(--text); }
  .data-table td { padding: 8px 14px; border-bottom: 1px solid var(--border); font-size: 13px; }
  .data-table tr:last-child td { border-bottom: none; }
  .data-table tbody tr:hover { background: rgba(255,255,255,0.02); }
  .num { text-align: right; }
  .mono { font-family: monospace; }
  .clickable { cursor: pointer; }
  .clickable:hover { background: rgba(255,255,255,0.04); }
  .expanded { background: rgba(79, 142, 247, 0.06); }
  .agent-name { font-weight: 600; }
  .sub-header th {
    background: rgba(255,255,255,0.02);
    padding: 6px 14px;
    font-size: 10px;
    border-bottom: 1px solid var(--border);
  }
  .sub-row td {
    background: rgba(255,255,255,0.01);
    padding: 6px 14px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .session-id { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .arrow { margin-left: 4px; font-size: 10px; }
  .empty { color: var(--text-muted); padding: 20px 0; }
  .loading { color: var(--text-muted); }
</style>
