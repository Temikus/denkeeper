<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import { navigate } from '../router.js'

  let data = $state(null)
  let error = $state('')
  let sortBy = $state('cost')
  let sortAsc = $state(false)

  let sortedSessions = $derived(
    data && data.costs.session_costs
      ? Object.entries(data.costs.session_costs).sort((a, b) =>
          sortBy === 'cost'
            ? (sortAsc ? a[1] - b[1] : b[1] - a[1])
            : (sortAsc ? a[0].localeCompare(b[0]) : b[0].localeCompare(a[0]))
        )
      : []
  )

  function toggleSort(col) {
    if (sortBy === col) {
      sortAsc = !sortAsc
    } else {
      sortBy = col
      sortAsc = col === 'id'
    }
  }

  onMount(async () => {
    try {
      const [health, agents, costs, approvals] = await Promise.all([
        api.health(),
        api.agents(),
        api.costs(),
        api.approvals('pending').catch(() => []),
      ])
      data = { health, agents, costs, pendingCount: approvals.length }
    } catch (e) {
      error = e.message
    }
  })
</script>

<h1 class="page-title">Overview</h1>
<ErrorBanner message={error} />

{#if data}
  <div class="grid">
    <div class="card">
      <div class="label">Status</div>
      <div class="value" class:ok={data.health.status === 'ok'}>{data.health.status}</div>
    </div>
    <div class="card">
      <div class="label">Agents</div>
      <div class="value">{data.agents.length}</div>
    </div>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
      class="card clickable"
      class:alert={data.pendingCount > 0}
      onclick={() => navigate('approvals')}
      role="button"
      tabindex="0"
    >
      <div class="label">Pending Approvals</div>
      <div class="value" class:warn={data.pendingCount > 0}>{data.pendingCount}</div>
    </div>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="card clickable" onclick={() => navigate('costs')} role="button" tabindex="0">
      <div class="label">Total Cost</div>
      <div class="value">${data.costs.global_cost.toFixed(4)}</div>
    </div>
    <div class="card">
      <div class="label">Session Budget</div>
      <div class="value">${data.costs.max_per_session.toFixed(4)}</div>
    </div>
    <div class="card">
      <div class="label">Active Sessions</div>
      <div class="value">{data.costs.session_count}</div>
    </div>
  </div>

  {#if sortedSessions.length > 0}
    <h2 class="section-title">Cost Breakdown</h2>
    <div class="cost-table-wrapper">
      <table class="cost-table">
        <thead>
          <tr>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable" onclick={() => toggleSort('id')} role="columnheader" tabindex="0">
              Session ID
              {#if sortBy === 'id'}<span class="sort-arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <th class="sortable" onclick={() => toggleSort('cost')} role="columnheader" tabindex="0">
              Cost
              {#if sortBy === 'cost'}<span class="sort-arrow">{sortAsc ? '\u25B2' : '\u25BC'}</span>{/if}
            </th>
          </tr>
        </thead>
        <tbody>
          {#each sortedSessions as [id, cost]}
            <tr>
              <td class="mono session-id">{id}</td>
              <td class="mono">${cost.toFixed(6)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  {#if data.agents.length > 0}
    <h2 class="section-title">Agents</h2>
    <div class="agent-grid">
      {#each data.agents as a}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div class="agent-card" onclick={() => navigate('agents')} role="button" tabindex="0">
          <div class="agent-name">{a.name}</div>
          <div class="agent-meta">
            <span class="tier">{a.permission_tier}</span>
            <span>{a.skill_count} skills</span>
            {#if a.has_tools}<span>has tools</span>{/if}
          </div>
          <div class="agent-model">{a.model}</div>
        </div>
      {/each}
    </div>
  {/if}
{:else if !error}
  <p class="loading">Loading…</p>
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .section-title { font-size: 16px; font-weight: 600; margin: 28px 0 12px; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 14px;
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 20px 16px;
  }
  .card.clickable { cursor: pointer; }
  .card.clickable:hover, .card.alert { border-color: var(--warn); }
  .label { font-size: 11px; color: var(--text-muted); margin-bottom: 8px; text-transform: uppercase; letter-spacing: 0.05em; }
  .value { font-size: 28px; font-weight: 700; }
  .value.ok   { color: var(--success); }
  .value.warn { color: var(--warn); }
  .cost-table-wrapper {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow-x: auto;
  }
  .cost-table { width: 100%; border-collapse: collapse; }
  .cost-table th {
    text-align: left; padding: 10px 14px; font-size: 11px;
    color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border); user-select: none;
  }
  .cost-table th.sortable { cursor: pointer; }
  .cost-table th.sortable:hover { color: var(--text); }
  .cost-table td { padding: 8px 14px; border-bottom: 1px solid var(--border); font-size: 13px; }
  .cost-table tr:last-child td { border-bottom: none; }
  .cost-table tbody tr:hover { background: var(--hover-overlay); }
  .mono { font-family: monospace; }
  .session-id { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sort-arrow { margin-left: 4px; font-size: 10px; }
  .agent-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 12px;
  }
  .agent-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 14px 16px;
    cursor: pointer;
  }
  .agent-card:hover { border-color: var(--accent); }
  .agent-name { font-weight: 600; margin-bottom: 6px; }
  .agent-meta { display: flex; gap: 10px; font-size: 12px; color: var(--text-muted); margin-bottom: 4px; }
  .tier { background: var(--border); padding: 1px 6px; border-radius: 10px; }
  .agent-model { font-size: 11px; color: var(--text-muted); font-family: monospace; }
  .loading { color: var(--text-muted); }
</style>
