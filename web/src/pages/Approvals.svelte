<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import StatusBadge from '../components/StatusBadge.svelte'

  let filter = 'pending'
  let approvals = []
  let error = ''
  let timer
  let resolvingId = ''

  // Auto-approve rules management
  let autoRules = []
  let autoError = ''
  let showAddRule = false
  let newRule = { agent: '', tool: '', scope: 'permanent' }
  let addingRule = false
  let agents = []

  async function load() {
    try {
      error = ''
      approvals = await api.approvals(filter)
    } catch(e) {
      error = e.message
    }
  }

  async function loadAutoRules() {
    try {
      autoError = ''
      autoRules = await api.listAutoApprove() || []
    } catch(e) {
      autoError = e.message
    }
  }

  async function loadAgents() {
    try { agents = await api.agents() || [] } catch(_) {}
  }

  async function addAutoRule() {
    if (!newRule.agent || !newRule.tool) return
    addingRule = true
    try {
      await api.createAutoApprove(newRule)
      newRule = { agent: '', tool: '', scope: 'permanent' }
      showAddRule = false
      await loadAutoRules()
    } catch(e) {
      autoError = e.message
    } finally {
      addingRule = false
    }
  }

  async function revokeRule(id) {
    try {
      await api.deleteAutoApprove(id)
      await loadAutoRules()
    } catch(e) {
      autoError = e.message
    }
  }

  onMount(() => {
    load()
    loadAutoRules()
    loadAgents()
    timer = setInterval(load, 10000)
  })
  onDestroy(() => clearInterval(timer))

  async function resolve(id, approve) {
    resolvingId = id
    try {
      if (approve) await api.approveApproval(id)
      else         await api.denyApproval(id)
      await load()
    } catch(e) {
      error = e.message
    } finally {
      resolvingId = ''
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
          <td class="summary">
            {a.summary}
            {#if a.payload}
              <details class="payload-details">
                <summary>Show payload</summary>
                <pre>{a.payload}</pre>
              </details>
            {/if}
          </td>
          <td>{a.agent_name || '—'}</td>
          <td><StatusBadge status={a.status} /></td>
          <td class="date">{fmtDate(a.created_at)}</td>
          <td class="date">{fmtDate(a.expires_at)}</td>
          <td class="actions">
            {#if a.status === 'pending'}
              <button class="btn-ok" onclick={() => resolve(a.id, true)} disabled={resolvingId === a.id}>
                {resolvingId === a.id ? '...' : 'Approve'}
              </button>
              <button class="btn-bad" onclick={() => resolve(a.id, false)} disabled={resolvingId === a.id}>
                {resolvingId === a.id ? '...' : 'Deny'}
              </button>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<!-- Auto-Approve Rules Section -->
<h2 class="section-title">Auto-Approve Rules</h2>
<p class="section-desc">Rules that automatically approve tool calls without prompting. Timed rules expire after 15 minutes; permanent rules survive restarts.</p>

<ErrorBanner message={autoError} />

{#if autoRules.length === 0 && !autoError}
  <p class="empty">No auto-approve rules. Rules are created from chat approval prompts or added here.</p>
{:else}
  <table class="table">
    <thead>
      <tr>
        <th>Tool</th><th>Agent</th><th>Scope</th><th>Created</th><th>Source</th><th>Actions</th>
      </tr>
    </thead>
    <tbody>
      {#each autoRules as rule}
        <tr>
          <td class="tool-name-cell"><code>{rule.tool_name}</code></td>
          <td>{rule.agent_name}</td>
          <td><span class="scope-badge" class:session={rule.scope === 'session'}>{rule.scope}</span></td>
          <td class="date">{fmtDate(rule.created_at)}</td>
          <td class="muted">{rule.created_by || '—'}</td>
          <td class="actions">
            {#if rule.scope === 'permanent'}
              <button class="btn-bad" onclick={() => revokeRule(rule.id)}>Revoke</button>
            {:else}
              <span class="muted">session only</span>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<div class="add-rule-area">
  {#if showAddRule}
    <div class="add-rule-form">
      <select bind:value={newRule.agent}>
        <option value="">Select agent…</option>
        {#each agents as a}
          <option value={a.name}>{a.name}</option>
        {/each}
      </select>
      <input type="text" bind:value={newRule.tool} placeholder="Tool name" />
      <select bind:value={newRule.scope}>
        <option value="permanent">Permanent</option>
      </select>
      <button class="btn-ok" onclick={addAutoRule} disabled={addingRule || !newRule.agent || !newRule.tool}>
        {addingRule ? '...' : 'Add'}
      </button>
      <button class="btn-ghost" onclick={() => { showAddRule = false }}>Cancel</button>
    </div>
  {:else}
    <button class="btn-ghost" onclick={() => { showAddRule = true }}>+ Add Rule</button>
  {/if}
</div>

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
  .filter-btn.active { color: var(--accent); border-color: var(--accent); background: rgba(200, 78, 53, 0.1); }
  .id { font-family: monospace; color: var(--text-muted); white-space: nowrap; }
  .summary { max-width: 380px; }
  .payload-details {
    margin-top: 4px;
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 12px;
  }
  .payload-details summary {
    padding: 2px 6px;
    cursor: pointer;
    color: var(--text-muted);
    user-select: none;
    font-size: 11px;
  }
  .payload-details pre {
    padding: 6px 8px;
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 200px;
    overflow-y: auto;
    border-top: 1px solid var(--border);
    font-size: 12px;
    background: var(--surface);
  }
  .date { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
  .actions { display: flex; gap: 6px; white-space: nowrap; }
  .btn-ok  { padding: 4px 10px; border: none; border-radius: var(--radius); background: var(--success); color: #fff; cursor: pointer; font-size: 12px; font-weight: 600; }
  .btn-bad { padding: 4px 10px; border: none; border-radius: var(--radius); background: var(--danger);  color: #fff; cursor: pointer; font-size: 12px; font-weight: 600; }
  .btn-ok:hover  { opacity: 0.85; }
  .btn-bad:hover { opacity: 0.85; }
  .empty { color: var(--text-muted); font-size: 13px; }

  .section-title { font-size: 17px; font-weight: 600; margin-top: 40px; margin-bottom: 6px; }
  .section-desc { font-size: 13px; color: var(--text-muted); margin-bottom: 16px; }
  .tool-name-cell code { font-size: 12px; }
  .scope-badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 3px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    background: rgba(99,102,241,0.1);
    color: var(--accent);
  }
  .scope-badge.session { background: rgba(100,100,100,0.1); color: var(--text-muted); }
  .muted { color: var(--text-muted); font-size: 12px; }

  .add-rule-area { margin-top: 12px; }
  .add-rule-form {
    display: flex;
    gap: 8px;
    align-items: center;
    flex-wrap: wrap;
  }
  .add-rule-form select,
  .add-rule-form input {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 6px 10px;
    font-size: 13px;
  }
  .add-rule-form input { min-width: 180px; }
  .btn-ghost {
    background: none;
    border: 1px solid var(--border);
    color: var(--text-muted);
    padding: 5px 12px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
  }
  .btn-ghost:hover { border-color: var(--text-muted); color: var(--text); }
</style>
