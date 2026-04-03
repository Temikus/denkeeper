<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import StatusBadge from '../components/StatusBadge.svelte'

  let filter = 'pending'
  let approvals = []
  let error = ''
  let timer

  async function load() {
    try {
      error = ''
      approvals = await api.approvals(filter)
    } catch(e) {
      error = e.message
    }
  }

  onMount(() => {
    load()
    timer = setInterval(load, 10000)
  })
  onDestroy(() => clearInterval(timer))

  async function resolve(id, approve) {
    try {
      if (approve) await api.approveApproval(id)
      else         await api.denyApproval(id)
      await load()
    } catch(e) {
      error = e.message
    }
  }

  function fmtDate(s) {
    if (!s) return '—'
    return new Date(s).toLocaleString()
  }

  const filters = ['pending', 'approved', 'denied', 'expired', '']
  const filterLabels = { '': 'all', pending: 'pending', approved: 'approved', denied: 'denied', expired: 'expired' }
</script>

<h1 class="page-title">Approvals</h1>

<div class="filters">
  {#each filters as f}
    <button
      class="filter-btn"
      class:active={filter === f}
      onclick={() => { filter = f; load() }}
    >{filterLabels[f]}</button>
  {/each}
</div>

<ErrorBanner message={error} />

{#if approvals.length === 0 && !error}
  <p class="empty">No approvals{filter ? ` with status "${filter}"` : ''}.</p>
{:else}
  <table class="table">
    <thead>
      <tr>
        <th>ID</th><th>Kind</th><th>Summary</th><th>Agent</th>
        <th>Status</th><th>Requested</th><th>Expires</th><th>Actions</th>
      </tr>
    </thead>
    <tbody>
      {#each approvals as a}
        <tr>
          <td class="id">{a.id.slice(0, 8)}…</td>
          <td>{a.kind}</td>
          <td class="summary">{a.summary}</td>
          <td>{a.agent_name || '—'}</td>
          <td><StatusBadge status={a.status} /></td>
          <td class="date">{fmtDate(a.created_at)}</td>
          <td class="date">{fmtDate(a.expires_at)}</td>
          <td class="actions">
            {#if a.status === 'pending'}
              <button class="btn-ok"  onclick={() => resolve(a.id, true)}>Approve</button>
              <button class="btn-bad" onclick={() => resolve(a.id, false)}>Deny</button>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 16px; }
  .filters { display: flex; gap: 8px; margin-bottom: 16px; flex-wrap: wrap; }
  .filter-btn {
    padding: 6px 14px; border: 1px solid var(--border);
    border-radius: var(--radius); background: none;
    color: var(--text-muted); cursor: pointer; font-size: 13px;
    text-transform: capitalize;
  }
  .filter-btn:hover  { color: var(--text); border-color: var(--text-muted); }
  .filter-btn.active { color: var(--accent); border-color: var(--accent); background: rgba(79,142,247,0.1); }
  .id { font-family: monospace; color: var(--text-muted); white-space: nowrap; }
  .summary { max-width: 280px; }
  .date { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
  .actions { display: flex; gap: 6px; white-space: nowrap; }
  .btn-ok  { padding: 4px 10px; border: none; border-radius: var(--radius); background: var(--success); color: #fff; cursor: pointer; font-size: 12px; font-weight: 600; }
  .btn-bad { padding: 4px 10px; border: none; border-radius: var(--radius); background: var(--danger);  color: #fff; cursor: pointer; font-size: 12px; font-weight: 600; }
  .btn-ok:hover  { opacity: 0.85; }
  .btn-bad:hover { opacity: 0.85; }
  .empty { color: var(--text-muted); font-size: 13px; }
</style>
