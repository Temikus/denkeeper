<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let skills = []
  let filter = ''
  let error = ''

  onMount(async () => {
    try { skills = (await api.skills()) || [] }
    catch(e) { error = e.message }
  })

  $: filtered = filter.trim()
    ? skills.filter(s =>
        s.name.includes(filter) ||
        s.agent.includes(filter) ||
        (s.description || '').toLowerCase().includes(filter.toLowerCase())
      )
    : skills
</script>

<h1 class="page-title">Skills</h1>

<div class="toolbar">
  <input
    type="search"
    placeholder="Filter by name, agent, or description…"
    bind:value={filter}
  />
  <span class="count">{filtered.length} of {skills.length}</span>
</div>

<ErrorBanner message={error} />

{#if filtered.length === 0 && !error}
  <p class="empty">{filter ? 'No matching skills.' : 'No skills loaded.'}</p>
{:else}
  <table class="table">
    <thead>
      <tr><th>Name</th><th>Agent</th><th>Version</th><th>Triggers</th><th>Description</th></tr>
    </thead>
    <tbody>
      {#each filtered as s}
        <tr>
          <td class="name">{s.name}</td>
          <td>{s.agent}</td>
          <td>{s.version || '—'}</td>
          <td class="muted">{(s.triggers || []).join(', ') || '—'}</td>
          <td class="muted">{s.description || '—'}</td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 16px; }
  .toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
  input[type=search] {
    padding: 8px 12px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    font-size: 13px;
    outline: none;
    width: 300px;
  }
  input[type=search]:focus { border-color: var(--accent); }
  .count { font-size: 12px; color: var(--text-muted); }
  .table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .table th, .table td { padding: 9px 10px; border-bottom: 1px solid var(--border); text-align: left; }
  .table th { color: var(--text-muted); font-size: 11px; text-transform: uppercase; font-weight: 500; }
  .name { font-weight: 500; }
  .muted { color: var(--text-muted); max-width: 220px; }
  .empty { color: var(--text-muted); font-size: 13px; }
</style>
