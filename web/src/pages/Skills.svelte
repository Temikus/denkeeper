<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let skills = $state([])
  let agents = $state([])
  let filter = $state('')
  let error = $state('')
  let loading = $state(true)

  // Add/Edit modal
  let showModal = $state(false)
  let editingSkill = $state(null) // null = add mode, {agent, name} = edit mode
  let formAgent = $state('default')
  let formName = $state('')
  let formDescription = $state('')
  let formVersion = $state('1.0.0')
  let formTriggers = $state('')
  let formBody = $state('')
  let saving = $state(false)
  let loadingSkill = $state(false)

  // Delete confirmation
  let confirmDelete = $state(null) // { agent, name }
  let deleting = $state(false)

  async function loadData() {
    loading = true
    error = ''
    try {
      const [skillsRes, agentsRes] = await Promise.all([
        api.skills().catch(() => []),
        api.agents().catch(() => []),
      ])
      skills = skillsRes || []
      agents = agentsRes || []
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function openAdd() {
    editingSkill = null
    formAgent = agents.length > 0 ? agents[0].name : 'default'
    formName = ''
    formDescription = ''
    formVersion = '1.0.0'
    formTriggers = ''
    formBody = ''
    showModal = true
  }

  async function openEdit(s) {
    editingSkill = { agent: s.agent, name: s.name }
    formAgent = s.agent
    formName = s.name
    formDescription = s.description || ''
    formVersion = s.version || '1.0.0'
    formTriggers = (s.triggers || []).join(', ')
    formBody = ''
    showModal = true
    // Fetch the full skill (including body) from the API.
    loadingSkill = true
    try {
      const full = await api.getSkill(s.agent, s.name)
      formBody = full.body || ''
      formDescription = full.description || formDescription
      formVersion = full.version || formVersion
      formTriggers = (full.triggers || []).join(', ') || formTriggers
    } catch (e) {
      error = e.message
    } finally {
      loadingSkill = false
    }
  }

  async function saveSkill() {
    saving = true
    error = ''
    try {
      const triggers = formTriggers.trim() ? formTriggers.split(',').map(t => t.trim()).filter(Boolean) : []
      if (editingSkill) {
        await api.updateSkill(editingSkill.agent, editingSkill.name, {
          description: formDescription || undefined,
          version: formVersion || undefined,
          triggers: triggers.length ? triggers : undefined,
          body: formBody,
        })
      } else {
        await api.addSkill(formAgent, {
          name: formName.trim(),
          description: formDescription || undefined,
          version: formVersion || undefined,
          triggers: triggers.length ? triggers : undefined,
          body: formBody,
        })
      }
      showModal = false
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  async function doDelete() {
    if (!confirmDelete) return
    deleting = true
    error = ''
    try {
      await api.deleteSkill(confirmDelete.agent, confirmDelete.name)
      confirmDelete = null
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      deleting = false
    }
  }

  let filtered = $derived(filter.trim()
    ? skills.filter(s =>
        s.name.includes(filter) ||
        s.agent.includes(filter) ||
        (s.description || '').toLowerCase().includes(filter.toLowerCase())
      )
    : skills)

  onMount(loadData)
</script>

<div class="page">
  <div class="header">
    <h1 class="page-title">Skills</h1>
    <button class="btn-primary" onclick={openAdd}>+ Add Skill</button>
  </div>

  <div class="toolbar">
    <input
      type="search"
      placeholder="Filter by name, agent, or description..."
      bind:value={filter}
    />
    <span class="count">{filtered.length} of {skills.length}</span>
  </div>

  <ErrorBanner message={error} />

  {#if loading}
    <p class="muted">Loading...</p>
  {:else if filtered.length === 0 && !error}
    <p class="muted">{filter ? 'No matching skills.' : 'No skills loaded. Add one to give your agent new capabilities.'}</p>
  {:else}
    <table class="table">
      <thead>
        <tr><th>Name</th><th>Agent</th><th>Version</th><th>Triggers</th><th>Description</th><th>Actions</th></tr>
      </thead>
      <tbody>
        {#each filtered as s}
          <tr>
            <td class="name">{s.name}</td>
            <td>{s.agent}</td>
            <td>{s.version || '—'}</td>
            <td class="muted">{(s.triggers || []).join(', ') || '—'}</td>
            <td class="muted desc">{s.description || '—'}</td>
            <td class="actions">
              <button class="btn-sm" onclick={() => openEdit(s)}>Edit</button>
              <button class="btn-sm danger" onclick={() => { confirmDelete = { agent: s.agent, name: s.name } }}>Delete</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<!-- Add/Edit Skill Modal -->
{#if showModal}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) showModal = false }} role="dialog" aria-modal="true">
    <div class="modal wide">
      <h2>{editingSkill ? 'Edit Skill' : 'Add Skill'}</h2>
      {#if !editingSkill}
        <label>
          Agent
          <select bind:value={formAgent}>
            {#each agents as a}
              <option value={a.name}>{a.name}</option>
            {/each}
            {#if agents.length === 0}
              <option value="default">default</option>
            {/if}
          </select>
        </label>
      {:else}
        <label>
          Agent
          <input type="text" value={editingSkill.agent} disabled />
        </label>
      {/if}
      <label>
        Name
        <input type="text" bind:value={formName} placeholder="e.g. daily-report" disabled={!!editingSkill} />
      </label>
      <label>
        Description <span class="hint">(optional)</span>
        <input type="text" bind:value={formDescription} placeholder="One-line description" />
      </label>
      <div class="row">
        <label>
          Version <span class="hint">(optional)</span>
          <input type="text" bind:value={formVersion} placeholder="1.0.0" />
        </label>
        <label>
          Triggers <span class="hint">(comma-separated)</span>
          <input type="text" bind:value={formTriggers} placeholder="command:name, keyword:hello" />
        </label>
      </div>
      <label>
        Body <span class="hint">(markdown skill instructions)</span>
        {#if loadingSkill}
          <div class="body-loading">Loading skill content...</div>
        {:else}
          <textarea bind:value={formBody} rows="12" placeholder="Markdown content for the skill..."></textarea>
        {/if}
      </label>
      <div class="modal-actions">
        <button class="btn-primary" onclick={saveSkill}
          disabled={saving || loadingSkill || !formName.trim() || !formBody.trim()}>
          {saving ? 'Saving...' : (editingSkill ? 'Update' : 'Add Skill')}
        </button>
        <button class="btn-ghost" onclick={() => showModal = false}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- Delete Confirmation -->
{#if confirmDelete}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmDelete = null }} role="dialog" aria-modal="true">
    <div class="modal">
      <h2>Delete Skill</h2>
      <p>Delete <strong>{confirmDelete.name}</strong> from agent <strong>{confirmDelete.agent}</strong>? This will remove the skill file.</p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={doDelete} disabled={deleting}>
          {deleting ? 'Deleting...' : 'Delete'}
        </button>
        <button class="btn-ghost" onclick={() => confirmDelete = null}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page { max-width: 1100px; }
  .header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
  .page-title { font-size: 20px; font-weight: 700; }
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
  .muted { color: var(--text-muted); }
  .desc { max-width: 220px; }
  .actions { white-space: nowrap; }

  .btn-primary { background: var(--accent); color: #fff; border: none; padding: 8px 16px; border-radius: var(--radius); cursor: pointer; font-size: 13px; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-ghost { background: none; border: 1px solid var(--border); color: var(--text); padding: 8px 16px; border-radius: var(--radius); cursor: pointer; font-size: 13px; }
  .btn-ghost:hover { border-color: var(--text-muted); }
  .btn-sm { background: var(--border); border: none; color: var(--text); padding: 4px 10px; border-radius: var(--radius); cursor: pointer; font-size: 12px; margin-right: 4px; }
  .btn-sm:hover { background: var(--accent); color: #fff; }
  .btn-sm.danger:hover { background: var(--danger); }
  .btn-danger { background: var(--danger); color: #fff; border: none; padding: 8px 16px; border-radius: var(--radius); cursor: pointer; font-size: 13px; }
  .btn-danger:hover:not(:disabled) { opacity: 0.85; }
  .btn-danger:disabled { opacity: 0.5; cursor: not-allowed; }

  .overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 100; }
  .modal { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 28px; width: 460px; max-width: 90vw; }
  .modal.wide { width: 600px; }
  .modal h2 { font-size: 16px; font-weight: 600; margin-bottom: 16px; }
  .modal p { color: var(--text-muted); margin-bottom: 20px; line-height: 1.6; }
  .modal label { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; font-size: 13px; color: var(--text-muted); }
  .modal input[type="text"], .modal select { background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); color: var(--text); padding: 8px 12px; font-size: 14px; }
  .modal input[type="text"]:focus, .modal select:focus { outline: none; border-color: var(--accent); }
  .modal input[type="text"]:disabled { opacity: 0.5; cursor: not-allowed; }
  .modal textarea {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 8px 12px;
    font-size: 13px;
    font-family: monospace;
    resize: vertical;
    min-height: 120px;
  }
  .modal textarea:focus { outline: none; border-color: var(--accent); }
  .modal-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 8px; }
  .hint { font-size: 11px; color: var(--text-muted); }
  .row { display: flex; gap: 16px; }
  .row label { flex: 1; }
  .body-loading { color: var(--text-muted); font-style: italic; padding: 12px 0; }
</style>
