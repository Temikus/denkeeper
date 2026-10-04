<script>
  import { onMount, tick } from 'svelte'
  import { api } from '../api.js'

  // deciders and providers come from GET /llm/providers. openAdd and addFor
  // come from the #/providers?add=decider&for=<agent> deep link.
  let { deciders = [], providers = [], onChange = () => {}, onAddProvider = () => {}, openAdd = false, addFor = '' } = $props()

  const DEFAULT_TIMEOUT = '5s'
  const DEFAULT_MAX_TOKENS = 30000
  const NAME_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/

  let section = $state(null)

  let decisionProviders = $derived(providers.filter(p => p.serves_decisions && p.enabled))

  // A cleared number input binds to undefined (or null); both mean "use the default".
  function tokenLimit(v) {
    return v === '' || v === null || v === undefined ? 0 : Number(v)
  }

  // --- Add ---
  let showAdd = $state(false)
  let form = $state(emptyForm())
  let formError = $state('')
  let saving = $state(false)
  let created = $state('')
  let formTest = $state(null)
  let formTesting = $state(false)

  // The open form follows the provider list: one added while it is open
  // (the "Add an OpenRouter provider" path) becomes the selection.
  $effect(() => {
    if (showAdd && !decisionProviders.some(p => p.name === form.provider)) {
      form.provider = decisionProviders[0]?.name || ''
    }
  })

  function emptyForm() {
    return { name: '', provider: '', model: '', timeout: '', maxTokens: '' }
  }

  function openAddForm() {
    form = emptyForm()
    form.provider = decisionProviders[0]?.name || ''
    formError = ''
    formTest = null
    created = ''
    showAdd = true
  }

  function optionalFields(f) {
    const body = {}
    if (f.timeout.trim()) body.timeout = f.timeout.trim()
    if (tokenLimit(f.maxTokens)) body.max_input_tokens = tokenLimit(f.maxTokens)
    return body
  }

  async function saveNew() {
    formError = ''
    const name = form.name.trim()
    if (!NAME_RE.test(name) || name.length > 64) {
      formError = 'Name must be lowercase letters, digits and single hyphens (e.g. jev-strict)'
      return
    }
    if (!form.model.trim()) {
      formError = 'Model is required'
      return
    }
    saving = true
    try {
      await api.createDecider({ name, provider: form.provider, model: form.model.trim(), ...optionalFields(form) })
      created = name
      showAdd = false
      await onChange()
    } catch (e) {
      formError = e.message
    } finally {
      saving = false
    }
  }

  // A result is shown only while the form still holds the inputs it tested.
  const formKey = () => [form.provider, form.model.trim(), form.timeout.trim()].join('\n')
  let shownFormTest = $derived(formTest?.key === formKey() ? formTest : null)

  async function testForm() {
    const key = formKey()
    formTesting = true
    formTest = null
    try {
      formTest = { ...await api.testDecider({ provider: form.provider, model: form.model.trim(), timeout: form.timeout.trim() }), key }
    } catch (e) {
      formTest = { status: 'error', message: e.message, key }
    } finally {
      formTesting = false
    }
  }

  // --- Card test ---
  let cardTest = $state({})
  let cardTesting = $state({})

  async function testCard(name) {
    cardTesting[name] = true
    try {
      cardTest[name] = await api.testDecider({ name })
    } catch (e) {
      cardTest[name] = { status: 'error', message: e.message }
    } finally {
      delete cardTesting[name]
    }
  }

  // --- Edit ---
  let editing = $state('')
  let draft = $state({})
  let editError = $state('')
  let editSaving = $state(false)

  function startEdit(d) {
    confirmDelete = ''
    editing = d.name
    editError = ''
    draft = { provider: d.provider, model: d.model, timeout: d.timeout === DEFAULT_TIMEOUT ? '' : d.timeout,
      maxTokens: d.max_input_tokens === DEFAULT_MAX_TOKENS ? '' : String(d.max_input_tokens) }
  }

  // Agents whose thresholds were tuned against the model being replaced.
  function staleCalibration(d) {
    if (draft.model.trim() === d.model && draft.provider === d.provider) return []
    return agentUsers(d)
  }

  async function saveEdit(d) {
    editError = ''
    if (!draft.model.trim()) {
      editError = 'Model is required'
      return
    }
    editSaving = true
    try {
      await api.updateDecider(d.name, {
        provider: draft.provider,
        model: draft.model.trim(),
        timeout: draft.timeout.trim(),
        max_input_tokens: tokenLimit(draft.maxTokens),
      })
      editing = ''
      delete cardTest[d.name]
      await onChange()
    } catch (e) {
      editError = e.message
    } finally {
      editSaving = false
    }
  }

  // --- Delete ---
  let confirmDelete = $state('')
  let deleting = $state(false)
  let deleteError = $state('')

  async function performDelete(name) {
    deleting = true
    deleteError = ''
    try {
      await api.deleteDecider(name)
      confirmDelete = ''
      await onChange()
    } catch (e) {
      deleteError = e.message
    } finally {
      deleting = false
    }
  }

  // --- Used by ---
  function agentUsers(d) {
    return (d.used_by || []).filter(u => u.startsWith('agent:')).map(u => u.slice('agent:'.length))
  }

  function usedByLabel(u) {
    if (u.startsWith('agent:')) return `${u.slice('agent:'.length)} · supervisor stage`
    if (u === 'eval.judge_decider') return 'Eval judge'
    if (u === 'decide.decider') return 'decide tool'
    return u
  }

  function usedByHref(u) {
    if (u.startsWith('agent:')) return `#/agents/${encodeURIComponent(u.slice('agent:'.length))}?card=permission`
    if (u === 'eval.judge_decider') return '#/evals'
    return null
  }

  function testSummary(t) {
    if (!t) return ''
    if (t.status === 'ok') return `Test passed · ${t.latency_ms} ms · $${(t.cost_usd || 0).toFixed(5)}`
    return `Test failed: ${t.message}`
  }

  onMount(async () => {
    if (!openAdd) return
    openAddForm()
    await tick()
    section?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
  })
</script>

<section class="decision-models" bind:this={section} data-testid="decision-models">
  <div class="dm-header">
    <div>
      <h2 class="dm-title">Decision Models</h2>
      <p class="hint dm-sub">Classifiers that score instead of chat. Used by supervisor stages, the eval judge and the decide tool.</p>
    </div>
    <button class="btn-ghost" onclick={openAddForm} disabled={showAdd} data-testid="add-decider-btn">+ Add Decision Model</button>
  </div>

  {#if created && addFor}
    <div class="banner success dm-created" role="status" data-testid="decider-created">
      <span><strong>{created}</strong> is ready.</span>
      <a href="#/agents/{encodeURIComponent(addFor)}?card=permission&decider={encodeURIComponent(created)}" data-testid="use-for-agent">Use it for {addFor} →</a>
    </div>
  {/if}

  {#if showAdd}
    <div class="dm-card dm-form" data-testid="decider-form">
      <h3 class="dm-form-title">New decision model</h3>
      {#if decisionProviders.length === 0}
        <p class="dm-note">Decision models need an OpenRouter provider with an API key, and none is set up yet.</p>
        <div class="dm-actions">
          <button class="btn-primary" onclick={onAddProvider} data-testid="add-openrouter-btn">Add an OpenRouter provider</button>
          <button class="btn-ghost" onclick={() => { showAdd = false }}>Cancel</button>
        </div>
      {:else}
        {#if formError}
          <div class="inline-error" role="alert">{formError}</div>
        {/if}
        <div class="dm-grid">
          <label class="dm-field">
            <span class="dm-label">Name</span>
            <input class="dm-input mono" type="text" bind:value={form.name} disabled={saving} placeholder="e.g. jev" data-testid="decider-name-input" />
          </label>
          <label class="dm-field">
            <span class="dm-label">Provider</span>
            <select class="dm-input" bind:value={form.provider} disabled={saving} data-testid="decider-provider-select">
              {#each decisionProviders as p}
                <option value={p.name}>{p.name}</option>
              {/each}
            </select>
          </label>
          <label class="dm-field dm-wide">
            <span class="dm-label">Model</span>
            <input class="dm-input mono" type="text" bind:value={form.model} disabled={saving} placeholder="typesafe/jev-1.13" data-testid="decider-model-input" />
          </label>
          <label class="dm-field">
            <span class="dm-label">Timeout</span>
            <input class="dm-input mono" type="text" bind:value={form.timeout} disabled={saving} placeholder={DEFAULT_TIMEOUT} />
          </label>
          <label class="dm-field">
            <span class="dm-label">Max input tokens</span>
            <input class="dm-input mono" type="number" min="1" bind:value={form.maxTokens} disabled={saving} placeholder={String(DEFAULT_MAX_TOKENS)} />
          </label>
          <p class="hint dm-wide dm-explain">Only providers that serve decisions are listed. Inputs over the token limit are skipped, never cut short. Tool arguments and recent messages are sent to this provider.</p>
        </div>
        <div class="dm-actions">
          <button class="btn-primary" onclick={saveNew} disabled={saving || !form.name.trim() || !form.model.trim()} data-testid="decider-save-btn">
            {saving ? 'Adding…' : 'Add'}
          </button>
          <button class="btn-ghost" onclick={testForm} disabled={formTesting || saving || !form.model.trim()} data-testid="decider-test-btn">
            {formTesting ? 'Testing…' : 'Test first'}
          </button>
          <button class="btn-ghost" onclick={() => { showAdd = false }} disabled={saving}>Cancel</button>
          <span class="dm-test" class:ok={shownFormTest?.status === 'ok'} class:fail={shownFormTest && shownFormTest.status !== 'ok'} role="status">{testSummary(shownFormTest)}</span>
        </div>
      {/if}
    </div>
  {/if}

  {#if deciders.length === 0 && !showAdd}
    <div class="dm-card dm-empty">
      <p>No decision models yet. A decision model scores each supervised tool call for about $0.0001, so clear calls skip the supervisor.</p>
      <button class="btn-primary" onclick={openAddForm} data-testid="empty-add-decider-btn">Add Decision Model</button>
    </div>
  {/if}

  {#each deciders as d (d.name)}
    <div class="dm-card" data-testid="decider-card">
      <div class="dm-card-head">
        <div>
          <div class="dm-name-row"><span class="dm-name">{d.name}</span><span class="mono dm-model">{d.model}</span></div>
          <div class="hint dm-meta">via {d.provider} · timeout {d.timeout} · max input {d.max_input_tokens.toLocaleString()} tokens</div>
        </div>
        {#if editing !== d.name && confirmDelete !== d.name}
          <div class="dm-card-actions">
            <button class="btn-ghost dm-compact" onclick={() => testCard(d.name)} disabled={cardTesting[d.name]}>{cardTesting[d.name] ? 'Testing…' : 'Test'}</button>
            <button class="btn-ghost dm-compact" onclick={() => startEdit(d)}>Edit</button>
            <button class="btn-ghost dm-compact dm-danger-text" onclick={() => { confirmDelete = d.name; deleteError = '' }} disabled={(d.used_by || []).length > 0} data-testid="delete-decider-btn">Delete</button>
          </div>
        {/if}
      </div>

      <div class="dm-test" class:ok={cardTest[d.name]?.status === 'ok'} class:fail={cardTest[d.name] && cardTest[d.name].status !== 'ok'} role="status">{testSummary(cardTest[d.name])}</div>

      {#if editing === d.name}
        <div class="dm-edit">
          {#if editError}
            <div class="inline-error" role="alert">{editError}</div>
          {/if}
          <div class="dm-grid">
            <label class="dm-field">
              <span class="dm-label">Provider</span>
              <select class="dm-input" bind:value={draft.provider} disabled={editSaving}>
                {#each decisionProviders as p}
                  <option value={p.name}>{p.name}</option>
                {/each}
              </select>
            </label>
            <label class="dm-field dm-wide">
              <span class="dm-label">Model</span>
              <input class="dm-input mono" type="text" bind:value={draft.model} disabled={editSaving} data-testid="decider-edit-model" />
            </label>
            <label class="dm-field">
              <span class="dm-label">Timeout</span>
              <input class="dm-input mono" type="text" bind:value={draft.timeout} disabled={editSaving} placeholder={DEFAULT_TIMEOUT} />
            </label>
            <label class="dm-field">
              <span class="dm-label">Max input tokens</span>
              <input class="dm-input mono" type="number" min="1" bind:value={draft.maxTokens} disabled={editSaving} placeholder={String(DEFAULT_MAX_TOKENS)} />
            </label>
          </div>
          {#if staleCalibration(d).length}
            <div class="banner warning dm-stale" data-testid="decider-stale-warning">
              Thresholds for {staleCalibration(d).join(', ')} were calibrated on <span class="mono">{d.model}</span>. Check them on the agent's Permission card after saving.
            </div>
          {/if}
          <div class="dm-actions">
            <button class="btn-primary" onclick={() => saveEdit(d)} disabled={editSaving} data-testid="decider-edit-save">{editSaving ? 'Saving…' : 'Save'}</button>
            <button class="btn-ghost" onclick={() => { editing = '' }} disabled={editSaving}>Cancel</button>
          </div>
        </div>
      {/if}

      {#if confirmDelete === d.name}
        <div class="dm-delete" data-testid="decider-delete-confirm">
          <span>Delete decision model <strong>{d.name}</strong>?</span>
          {#if deleteError}
            <div class="inline-error" role="alert">{deleteError}</div>
          {/if}
          <div class="dm-actions">
            <button class="btn-danger" onclick={() => performDelete(d.name)} disabled={deleting} data-testid="decider-delete-confirm-btn">{deleting ? 'Deleting…' : 'Delete'}</button>
            <button class="btn-ghost" onclick={() => { confirmDelete = '' }} disabled={deleting}>Cancel</button>
          </div>
        </div>
      {/if}

      <div class="dm-used">
        <span class="dm-label">Used by</span>
        {#if (d.used_by || []).length === 0}
          <span class="hint">Nothing yet. Pick it on an agent's Permission card.</span>
        {:else}
          <div class="dm-chips">
            {#each d.used_by as u}
              {#if usedByHref(u)}
                <a class="dm-chip" href={usedByHref(u)}>{usedByLabel(u)}</a>
              {:else}
                <span class="dm-chip">{usedByLabel(u)}</span>
              {/if}
            {/each}
          </div>
          <span class="hint">Remove these uses before deleting {d.name}.</span>
        {/if}
      </div>
    </div>
  {/each}
</section>

<style>
  .decision-models { margin-top: 28px; }
  .dm-header { display: flex; justify-content: space-between; align-items: flex-end; gap: 12px; margin-bottom: 12px; flex-wrap: wrap; }
  .dm-title { font-size: 16px; font-weight: 600; margin: 0 0 4px; }
  .dm-sub { margin: 0; font-size: 12px; }
  .dm-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 16px 18px;
    margin-bottom: 12px;
  }
  .dm-form { border-color: var(--accent); }
  .dm-form-title { font-size: 14px; font-weight: 600; margin: 0 0 12px; }
  .dm-note { font-size: 13px; margin: 0 0 12px; }
  .dm-empty { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
  .dm-empty p { margin: 0; font-size: 13px; color: var(--text-muted); flex: 1; min-width: 220px; }
  .dm-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 12px; margin-bottom: 12px; }
  .dm-wide { grid-column: 1 / -1; }
  .dm-field { display: flex; flex-direction: column; gap: 4px; }
  .dm-label { font-size: 12px; color: var(--text-muted); }
  /* Same as Providers' .input: nothing styles inputs globally. */
  .dm-input {
    width: 100%; padding: 8px 10px; font-size: 13px;
    border: 1px solid var(--border); border-radius: var(--radius);
    background: var(--bg); color: var(--text);
  }
  .dm-input:focus { outline: none; border-color: var(--accent); }
  .dm-explain { margin: 0; line-height: 1.5; }
  .dm-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .dm-test { font-size: 12px; margin-top: 8px; }
  .dm-test:empty { display: none; }
  .dm-actions .dm-test { margin: 0 0 0 auto; }
  .dm-test.ok { color: var(--success); }
  .dm-test.fail { color: var(--danger); }
  .dm-card-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; flex-wrap: wrap; }
  .dm-name-row { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
  .dm-name { font-weight: 600; font-size: 15px; }
  .dm-model { font-size: 12px; color: var(--text-muted); }
  .dm-meta { margin-top: 4px; font-size: 12px; }
  .dm-card-actions { display: flex; gap: 6px; }
  /* Card-sized btn-ghost, matching the Edit/Delete buttons on provider cards. */
  .dm-compact { padding: 4px 10px; font-size: 12px; }
  .dm-danger-text { color: var(--danger); border-color: transparent; }
  .dm-danger-text:hover:not(:disabled) { border-color: var(--danger); }
  .dm-card-actions button:disabled { opacity: 0.5; cursor: not-allowed; }
  .dm-edit { margin-top: 14px; }
  .dm-stale { margin-bottom: 12px; font-size: 12px; }
  .dm-delete {
    margin-top: 12px;
    padding: 10px;
    background: color-mix(in srgb, var(--danger) 5%, transparent);
    border: 1px solid color-mix(in srgb, var(--danger) 20%, transparent);
    border-radius: var(--radius);
    font-size: 13px;
  }
  .dm-delete .dm-actions { margin-top: 8px; }
  .dm-used { display: flex; flex-direction: column; gap: 6px; margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--border); }
  .dm-chips { display: flex; flex-wrap: wrap; gap: 6px; }
  .dm-chip {
    font-size: 12px;
    padding: 3px 10px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--bg);
    color: var(--text);
    text-decoration: none;
  }
  a.dm-chip:hover { border-color: var(--accent); }
  /* .banner.success plus a row layout for the follow-up link. */
  .dm-created { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
  .dm-created a { color: var(--accent); font-weight: 600; }
  .inline-error { margin-bottom: 8px; }
</style>
