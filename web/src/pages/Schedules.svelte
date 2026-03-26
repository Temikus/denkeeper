<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let schedules = []
  let error = ''

  onMount(async () => {
    try { schedules = (await api.schedules()) || [] }
    catch(e) { error = e.message }
  })

  function fmtDate(s) { return s ? new Date(s).toLocaleString() : '—' }
</script>

<h1 class="page-title">Schedules</h1>
<ErrorBanner message={error} />

{#if schedules.length === 0 && !error}
  <p class="empty">No schedules configured.</p>
{:else}
  <table class="table">
    <thead>
      <tr>
        <th>Name</th><th>Expression</th><th>Type</th><th>Skill</th>
        <th>Agent</th><th>Tier</th><th>Enabled</th><th>Last Run</th><th>Next Run</th>
      </tr>
    </thead>
    <tbody>
      {#each schedules as s}
        <tr>
          <td class="name">{s.name}</td>
          <td class="expr">{s.expression}</td>
          <td>{s.type}</td>
          <td>{s.skill || '—'}</td>
          <td>{s.agent || '—'}</td>
          <td>{s.session_tier || '—'}</td>
          <td>
            <span class="dot" class:on={s.enabled} title={s.enabled ? 'Enabled' : 'Disabled'}></span>
            {s.enabled ? 'yes' : 'no'}
          </td>
          <td class="date">{fmtDate(s.last_run)}</td>
          <td class="date">{fmtDate(s.next_run)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .table th, .table td { padding: 9px 10px; border-bottom: 1px solid var(--border); text-align: left; }
  .table th { color: var(--text-muted); font-size: 11px; text-transform: uppercase; font-weight: 500; white-space: nowrap; }
  .name { font-weight: 600; }
  .expr { font-family: monospace; font-size: 12px; white-space: nowrap; }
  .date { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
  .dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; background: var(--border); margin-right: 4px; vertical-align: middle; }
  .dot.on { background: var(--success); }
  .empty { color: var(--text-muted); font-size: 13px; }
</style>
