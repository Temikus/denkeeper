<script>
  import { onDestroy, tick } from 'svelte'
  import { api } from '../api.js'
  import { CATEGORIES, categoryLabel } from '../evalCategories.js'
  import EvalCoverage from './EvalCoverage.svelte'

  // One test set's cases: what is in it, its mix of kinds, and the edits a
  // case needs after it was saved. Adding cases happens in the fill panels the
  // page owns; this view re-reads whenever `version` changes.
  let {
    sets = [],
    selected = $bindable(''),
    version = 0,
    // Called after a write that changes the set list (a count, or a deletion).
    onchanged = undefined,
    // Called with a category slug from a coverage gap prompt.
    onfill = undefined,
    // False when no agent exists to generate probes from.
    canProbe = true,
  } = $props()

  const PROMPT_PREVIEW = 140

  let detail = $state(null)
  let loading = $state(false)
  let loadError = $state('')

  let expanded = $state(new Set())

  let editingId = $state(null)
  let editKind = $state('')
  let editNotes = $state('')
  let editSaving = $state(false)
  let editError = $state('')
  let savedId = $state(null)
  let savedTimer = null
  // A live region that stays mounted: one inserted already filled is often
  // not announced.
  let statusMsg = $state('')

  let confirmTask = $state(null)
  let confirmSet = $state(false)
  let deleting = $state(false)
  let deleteError = $state('')
  // The control that opened the dialog, so closing it puts focus back.
  let returnFocus = null

  let exporting = $state(false)
  let exportError = $state('')

  let tasks = $derived(detail?.tasks || [])

  // Guards against a slow read for a set the operator has since left.
  let requestSeq = 0

  async function load(name) {
    const seq = ++requestSeq
    if (!name) {
      detail = null
      return
    }
    loading = true
    loadError = ''
    try {
      const d = await api.evalTaskSet(name)
      if (seq !== requestSeq) return
      detail = d
    } catch (e) {
      if (seq !== requestSeq) return
      detail = null
      loadError = e.message || 'Could not load the test set'
    } finally {
      if (seq === requestSeq) loading = false
    }
  }

  $effect(() => {
    void version
    const name = selected
    editingId = null
    load(name)
  })

  function preview(text) {
    const t = (text || '').trim()
    return t.length > PROMPT_PREVIEW ? `${t.slice(0, PROMPT_PREVIEW)}…` : t
  }

  function isLong(text) {
    return (text || '').trim().length > PROMPT_PREVIEW
  }

  function toggleExpanded(id) {
    const next = new Set(expanded)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    expanded = next
  }

  function shortLabel(t) {
    const s = (t.prompt || '').trim().replace(/\s+/g, ' ')
    return s.length > 60 ? `${s.slice(0, 60)}…` : s
  }

  // --- Editing ----------------------------------------------------------------

  function startEdit(t) {
    editingId = t.id
    editKind = t.category
    editNotes = t.notes || ''
    editError = ''
    statusMsg = ''
  }

  function cancelEdit() {
    const id = editingId
    editingId = null
    editError = ''
    focusTestId(`edit-case-${id}`)
  }

  /** Save and Cancel unmount the focused button; focus goes back to its row. */
  async function focusTestId(id) {
    await tick()
    document.querySelector('[data-testid="' + id + '"]')?.focus()
  }

  async function saveEdit(t) {
    editSaving = true
    editError = ''
    try {
      const updated = await api.updateEvalTask(selected, t.id, {
        category: editKind,
        notes: editNotes,
      })
      detail = {
        ...detail,
        tasks: tasks.map(x => (x.id === t.id ? { ...x, ...updated } : x)),
      }
      editingId = null
      savedId = t.id
      statusMsg = 'Case saved'
      clearTimeout(savedTimer)
      savedTimer = setTimeout(() => (savedId = null), 2500)
      focusTestId(`edit-case-${t.id}`)
    } catch (e) {
      editError = e.message || 'Could not save the case'
    } finally {
      editSaving = false
    }
  }

  // --- Deleting ---------------------------------------------------------------

  function askDeleteTask(t, e) {
    deleteError = ''
    returnFocus = e?.currentTarget || null
    confirmTask = t
  }

  function askDeleteSet(e) {
    deleteError = ''
    returnFocus = e?.currentTarget || null
    confirmSet = true
  }

  function closeConfirm() {
    if (deleting) return
    confirmTask = null
    confirmSet = false
    returnFocus?.focus()
  }

  async function doDeleteTask() {
    deleting = true
    deleteError = ''
    try {
      const t = confirmTask
      await api.deleteEvalTask(selected, t.id)
      // The deleted row's Delete button is gone; the next row's takes focus.
      const i = tasks.findIndex(x => x.id === t.id)
      const rest = tasks.filter(x => x.id !== t.id)
      const next = rest[Math.min(i, rest.length - 1)]
      detail = { ...detail, tasks: rest }
      confirmTask = null
      statusMsg = 'Case deleted'
      focusTestId(next ? `delete-case-${next.id}` : 'sets-select')
      onchanged?.()
    } catch (e) {
      deleteError = e.message || 'Could not delete the case'
    } finally {
      deleting = false
    }
  }

  async function doDeleteSet() {
    deleting = true
    deleteError = ''
    try {
      const name = selected
      await api.deleteEvalTaskSet(name)
      confirmSet = false
      statusMsg = `Deleted ${name}`
      onchanged?.(name)
      focusTestId('sets-select')
    } catch (e) {
      // Stays in the dialog: closing on failure would read as done.
      deleteError = e.status === 409
        ? 'Runs still use this set, so it cannot be deleted. Their results would lose the cases they were graded on.'
        : (e.message || 'Could not delete the test set')
    } finally {
      deleting = false
    }
  }

  // --- Export -----------------------------------------------------------------

  async function doExport() {
    exporting = true
    exportError = ''
    try {
      const text = await api.exportEvalTaskSet(selected)
      const url = URL.createObjectURL(new Blob([text], { type: 'application/jsonl' }))
      const a = document.createElement('a')
      a.href = url
      a.download = `${selected}.jsonl`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (e) {
      exportError = e.message || 'Could not export the test set'
    } finally {
      exporting = false
    }
  }

  onDestroy(() => clearTimeout(savedTimer))

  function focusOnMount(node) {
    node.focus()
  }

  function setOption(s) {
    const n = s.task_count ?? 0
    return `${s.name} (${n} case${n === 1 ? '' : 's'})`
  }
</script>

<section class="sets" data-testid="test-sets" aria-labelledby="sets-title">
  <h2 id="sets-title" class="sr-only">Test set cases</h2>
  <span class="sr-only" role="status">{statusMsg}</span>
  <div class="set-bar">
    <label class="field set-field">
      <span class="field-label">Test set</span>
      <select bind:value={selected} disabled={deleting} data-testid="sets-select">
        {#each sets as s (s.name)}
          <option value={s.name}>{setOption(s)}</option>
        {/each}
      </select>
    </label>
    <div class="set-actions">
      <button class="btn-ghost btn-sm" onclick={doExport} disabled={!selected || exporting}
        data-testid="export-set">{exporting ? 'Exporting…' : 'Export JSONL'}</button>
      <button class="btn-ghost btn-sm danger-text" onclick={(e) => askDeleteSet(e)} disabled={!selected}
        data-testid="delete-set">Delete set</button>
    </div>
  </div>
  {#if detail?.description}<p class="hint desc">{detail.description}</p>{/if}
  {#if exportError}<div class="inline-error" role="alert" data-testid="export-error">{exportError}</div>{/if}

  {#if loading && !detail}
    <p class="muted row" role="status" data-testid="sets-loading">
      <span class="spinner" aria-hidden="true"></span>Loading cases…
    </p>
  {:else if loadError}
    <div class="inline-error" role="alert" data-testid="sets-error">{loadError}</div>
    <button class="btn-ghost btn-sm" onclick={() => load(selected)}>Try again</button>
  {:else if detail && tasks.length === 0}
    <p class="muted" data-testid="sets-empty">
      This set has no cases yet. Add some with the buttons above: suggest them from past
      turns, generate probes from the agent's configuration, or import a JSONL file.
    </p>
  {:else if detail}
    <EvalCoverage {tasks} {onfill} {canProbe} />

    <div class="table-wrap" tabindex="0" role="region" aria-label="Test cases">
      <table class="table" data-testid="cases-table">
        <thead>
          <tr>
            <th scope="col">Case</th>
            <th scope="col">Kind</th>
            <th scope="col">Notes</th>
            <th scope="col"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each tasks as t (t.id)}
            {@const editing = editingId === t.id}
            <tr data-testid="case-{t.id}">
              <td class="prompt-cell">
                <span class="prompt">{expanded.has(t.id) ? t.prompt : preview(t.prompt)}</span>
                {#if isLong(t.prompt)}
                  <button class="btn-link" onclick={() => toggleExpanded(t.id)}
                    aria-expanded={expanded.has(t.id)}>
                    {expanded.has(t.id) ? 'Show less' : 'Show all'}
                  </button>
                {/if}
                {#if t.pinned_history?.length}
                  <span class="hint pinned">
                    {t.pinned_history.length} pinned turn{t.pinned_history.length === 1 ? '' : 's'} before it
                  </span>
                {/if}
              </td>
              {#if editing}
                <td>
                  <select bind:value={editKind} disabled={editSaving} use:focusOnMount
                    aria-label={`Kind for: ${shortLabel(t)}`} data-testid="edit-kind-{t.id}">
                    {#each CATEGORIES as c (c.value)}
                      <option value={c.value}>{c.label}</option>
                    {/each}
                  </select>
                </td>
                <td>
                  <textarea bind:value={editNotes} rows="2" disabled={editSaving}
                    aria-label={`Notes for: ${shortLabel(t)}`} data-testid="edit-notes-{t.id}"></textarea>
                  {#if editError}<div class="inline-error" role="alert" data-testid="edit-error">{editError}</div>{/if}
                </td>
                <td class="actions">
                  <button class="btn-primary btn-sm" onclick={() => saveEdit(t)} disabled={editSaving}
                    data-testid="save-case-{t.id}">{editSaving ? 'Saving…' : 'Save'}</button>
                  <button class="btn-ghost btn-sm" onclick={cancelEdit} disabled={editSaving}>Cancel</button>
                </td>
              {:else}
                <td class="kind">{categoryLabel(t.category)}</td>
                <td class="notes">
                  {#if t.notes}{t.notes}{:else}<span class="muted">—</span>{/if}
                  {#if savedId === t.id}<span class="save-ok"> Saved</span>{/if}
                </td>
                <td class="actions">
                  <button class="btn-ghost btn-sm" onclick={() => startEdit(t)}
                    disabled={editingId != null} aria-label={`Edit: ${shortLabel(t)}`}
                    data-testid="edit-case-{t.id}">Edit</button>
                  <button class="btn-ghost btn-sm danger-text" onclick={(e) => askDeleteTask(t, e)}
                    aria-label={`Delete: ${shortLabel(t)}`}
                    data-testid="delete-case-{t.id}">Delete</button>
                </td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

{#if confirmTask || confirmSet}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) closeConfirm() }}
    onkeydown={(e) => { if (e.key === 'Escape') closeConfirm() }}
    role="dialog" aria-modal="true" aria-labelledby="delete-confirm-title"
    tabindex="-1" use:focusOnMount>
    <div class="confirm-modal" data-testid="delete-confirm">
      {#if confirmSet}
        <h2 id="delete-confirm-title">Delete test set</h2>
        <p>
          Delete “{selected}” and its {tasks.length} case{tasks.length === 1 ? '' : 's'}? This
          cannot be undone. Export it first if you may want it back.
        </p>
      {:else}
        <h2 id="delete-confirm-title">Delete test case</h2>
        <p>Delete “{shortLabel(confirmTask)}” from {selected}? This cannot be undone.</p>
      {/if}
      {#if deleteError}
        <div class="inline-error" role="alert" data-testid="delete-error">{deleteError}</div>
      {/if}
      <div class="modal-actions">
        <button class="btn-danger" onclick={confirmSet ? doDeleteSet : doDeleteTask} disabled={deleting}
          data-testid="confirm-delete">
          {deleting ? 'Deleting…' : 'Delete'}
        </button>
        <button class="btn-ghost" onclick={closeConfirm} disabled={deleting}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .sets {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: var(--card-inset);
    margin-bottom: 24px;
    display: flex;
    flex-direction: column;
    gap: 14px;
    min-width: 0;
  }

  .set-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 12px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }

  .set-field { flex: 1 1 240px; }

  .field-label {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
  }

  .set-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  select,
  textarea {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 6px 10px;
    font-size: 13px;
    width: 100%;
    min-width: 0;
    font-family: inherit;
  }

  select:focus,
  textarea:focus { outline: none; border-color: var(--accent); }

  textarea { min-width: 160px; resize: vertical; }

  /* The longest kind label ("Behaviour probe") clips below this. */
  .table select { min-width: 150px; }

  p.hint, p.muted { margin: 0; }
  .desc { margin-top: -8px; }

  .muted { color: var(--text-muted); font-size: 13px; line-height: 1.6; }

  .row { display: flex; align-items: center; gap: 6px; }

  .table td { vertical-align: top; }

  .prompt-cell {
    min-width: 200px;
    max-width: 480px;
  }

  .prompt {
    display: block;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    line-height: 1.5;
  }

  .pinned { display: block; margin-top: 4px; }

  .kind { white-space: nowrap; }

  .notes {
    min-width: 120px;
    max-width: 280px;
    overflow-wrap: anywhere;
    color: var(--text);
  }

  .actions {
    white-space: nowrap;
    text-align: right;
  }

  .actions button + button { margin-left: 4px; }

  /* A destructive action that only opens the confirm keeps the ghost shape;
     the red is on the dialog's button. */
  .danger-text { color: var(--danger); }

  .btn-link {
    border: none;
    background: none;
    padding: 2px 0;
    color: var(--accent);
    font-size: 12px;
    cursor: pointer;
  }

  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .inline-error { margin: 4px 0 0; }

  .spinner {
    width: 12px;
    height: 12px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    display: inline-block;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  @media (prefers-reduced-motion: reduce) {
    .spinner { animation-duration: 2s; }
  }
</style>
