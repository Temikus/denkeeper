<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let agents = []
  let selected = null
  let detail = null
  let error = ''

  onMount(async () => {
    try {
      agents = (await api.agents()) || []
      if (agents.length) selectAgent(agents[0])
    } catch(e) {
      error = e.message
    }
  })

  async function selectAgent(a) {
    if (!a) return
    selected = a
    detail = null
    try {
      detail = await api.agent(a.name)
    } catch(e) {
      error = e.message
    }
  }
</script>

<h1 class="page-title">Agents</h1>
<ErrorBanner message={error} />

<div class="layout">
  <aside class="list">
    {#each agents as a}
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <div
        class="item"
        class:active={selected?.name === a.name}
        on:click={() => selectAgent(a)}
        role="button"
        tabindex="0"
      >
        <div class="name">{a.name}</div>
        <div class="meta">{a.permission_tier} · {a.skill_count} skills</div>
      </div>
    {/each}
    {#if agents.length === 0 && !error}
      <p class="empty">No agents.</p>
    {/if}
  </aside>

  {#if detail}
    <section class="detail">
      <h2>{detail.name}</h2>
      <dl class="props">
        <dt>Model</dt>        <dd class="mono">{detail.model}</dd>
        <dt>Permission</dt>   <dd>{detail.permission_tier}</dd>
        <dt>Has Tools</dt>    <dd>{detail.has_tools ? 'Yes' : 'No'}</dd>
        <dt>Adapters</dt>     <dd>{(detail.adapters || []).join(', ') || '—'}</dd>
      </dl>
      <h3>Skills ({detail.skills.length})</h3>
      {#if detail.skills.length > 0}
        <table class="table">
          <thead>
            <tr><th>Name</th><th>Version</th><th>Triggers</th><th>Description</th></tr>
          </thead>
          <tbody>
            {#each detail.skills as sk}
              <tr>
                <td class="skill-name">{sk.name}</td>
                <td>{sk.version || '—'}</td>
                <td class="muted">{(sk.triggers || []).join(', ') || '—'}</td>
                <td class="muted">{sk.description || '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <p class="empty">No skills loaded.</p>
      {/if}
    </section>
  {:else if !error && agents.length > 0}
    <p class="empty">Select an agent.</p>
  {/if}
</div>

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .layout { display: flex; gap: 20px; }
  .list { width: 200px; flex-shrink: 0; display: flex; flex-direction: column; gap: 6px; }
  .item {
    padding: 10px 12px;
    border-radius: var(--radius);
    cursor: pointer;
    border: 1px solid var(--border);
    background: var(--surface);
  }
  .item:hover, .item.active { border-color: var(--accent); }
  .name { font-weight: 600; }
  .meta { font-size: 12px; color: var(--text-muted); margin-top: 2px; }
  .detail { flex: 1; min-width: 0; }
  h2 { font-size: 18px; margin-bottom: 14px; }
  h3 { font-size: 14px; font-weight: 600; margin: 18px 0 10px; }
  .props { display: grid; grid-template-columns: auto 1fr; gap: 6px 20px; margin-bottom: 8px; }
  dt { color: var(--text-muted); font-size: 12px; }
  dd { font-size: 13px; }
  .mono { font-family: monospace; font-size: 12px; }
  .table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .table th, .table td { padding: 8px 10px; text-align: left; border-bottom: 1px solid var(--border); }
  .table th { color: var(--text-muted); font-weight: 500; font-size: 11px; text-transform: uppercase; }
  .skill-name { font-weight: 500; }
  .muted { color: var(--text-muted); max-width: 240px; }
  .empty { color: var(--text-muted); font-size: 13px; padding: 4px 0; }
</style>
