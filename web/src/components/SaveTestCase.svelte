<script>
  import { tick } from 'svelte'
  import { api } from '../api.js'

  // Saves one chat turn as an eval test case. Rendered in place under the
  // message bubble (never a modal — house rule), because the thing being saved
  // is right above it and the choice only makes sense next to it.
  //
  // `prompt` is the user text. `precedingTurns` are the turns above it, newest
  // last; the operator picks how many to pin. Pinned history is captured *now*
  // and replayed verbatim at run time: the source conversation drifts, so its
  // latest window would not be the window that preceded this message.
  //
  // `sourceMessageId` is the stored message row id, present only for turns read
  // back from GET /sessions/{id}/messages — a message still streaming in this
  // session has no id yet, and provenance stays partial rather than wrong.
  let {
    prompt = '',
    precedingTurns = [],
    conversationId = '',
    sourceMessageId = null,
    onclose = undefined,
  } = $props()

  const CATEGORIES = [
    { value: 'chat', label: 'Chat / persona' },
    { value: 'skill_command', label: 'Skill command' },
    { value: 'scheduled', label: 'Scheduled' },
    { value: 'tool_heavy', label: 'Tool-heavy' },
    { value: 'probe', label: 'Behaviour probe' },
  ]

  let sets = $state([])
  let loading = $state(true)
  let loadError = $state('')

  let selectedSet = $state('')
  let creatingNew = $state(false)
  let newSetName = $state('')
  let category = $state('chat')
  let historyCount = $state(0)
  let notes = $state('')

  let saving = $state(false)
  let error = $state('')
  let saved = $state(false)
  let firstControl = $state(null)

  const maxHistory = $derived(precedingTurns.length)
  const canSave = $derived(
    !saving && !saved && prompt.trim() !== '' &&
    (creatingNew ? newSetName.trim() !== '' : selectedSet !== '')
  )

  $effect(() => { loadSets() })

  let requested = false
  async function loadSets() {
    if (requested) return
    requested = true
    try {
      const res = await api.evalTaskSets()
      sets = res || []
      if (sets.length > 0) selectedSet = sets[0].name
      else creatingNew = true
    } catch (e) {
      loadError = e.message || 'Could not load test sets'
      creatingNew = true
    } finally {
      loading = false
      // The panel is a focused decision opened from a menu, so keyboard focus
      // follows it in rather than being left behind on the message bubble.
      await tick()
      firstControl?.focus()
    }
  }

  async function startNewSet() {
    creatingNew = true
    newSetName = ''
    await tick()
    firstControl?.focus()
  }

  async function cancelNewSet() {
    creatingNew = false
    if (sets.length > 0) selectedSet = sets[0].name
    await tick()
    firstControl?.focus()
  }

  function handleKeydown(e) {
    if (e.key === 'Escape' && !saving) {
      e.stopPropagation()
      onclose?.()
    }
  }

  async function save() {
    if (!canSave) return
    saving = true
    error = ''
    try {
      let setName = selectedSet
      if (creatingNew) {
        const created = await api.createEvalTaskSet({ name: newSetName.trim() })
        setName = created.name
        sets = [...sets, created]
      }
      const pinned = historyCount > 0
        ? precedingTurns.slice(-historyCount).map(m => ({ role: m.role, content: m.text || '' }))
        : undefined
      await api.addEvalTask(setName, {
        prompt,
        category,
        notes: notes.trim() || undefined,
        pinned_history: pinned,
        source_conversation_id: conversationId || undefined,
        source_message_id: sourceMessageId ?? null,
      })
      saved = true
      selectedSet = setName
      creatingNew = false
      setTimeout(() => onclose?.(), 1200)
    } catch (e) {
      error = e.message || 'Could not save the test case'
    } finally {
      saving = false
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div class="save-panel" role="group" aria-label="Save this turn as an eval test case" onkeydown={handleKeydown}>
  {#if loading}
    <div class="row muted">
      <span class="spinner" aria-hidden="true"></span>
      <span>Loading test sets…</span>
    </div>
  {:else if saved}
    <div class="row success" role="status">Saved to “{selectedSet}”</div>
  {:else}
    {#if loadError}
      <div class="inline-error" role="alert">{loadError}</div>
    {/if}

    <div class="field">
      <label for="stc-set">Test set</label>
      {#if creatingNew}
        <div class="row">
          <input
            id="stc-set"
            type="text"
            bind:this={firstControl}
            bind:value={newSetName}
            placeholder="e.g. regression"
            disabled={saving}
            maxlength="80"
          />
          {#if sets.length > 0}
            <button class="btn-link" onclick={cancelNewSet} disabled={saving}>Use existing</button>
          {/if}
        </div>
      {:else}
        <div class="row">
          <select id="stc-set" bind:this={firstControl} bind:value={selectedSet} disabled={saving}>
            {#each sets as s}
              <option value={s.name}>{s.name} ({s.task_count})</option>
            {/each}
          </select>
          <button class="btn-link" onclick={startNewSet} disabled={saving}>New set…</button>
        </div>
      {/if}
    </div>

    <div class="field">
      <label for="stc-category">Category</label>
      <select id="stc-category" bind:value={category} disabled={saving}>
        {#each CATEGORIES as c}
          <option value={c.value}>{c.label}</option>
        {/each}
      </select>
    </div>

    {#if maxHistory > 0}
      <div class="field">
        <label for="stc-history">Include preceding turns</label>
        <div class="row">
          <input
            id="stc-history"
            type="number"
            min="0"
            max={maxHistory}
            bind:value={historyCount}
            disabled={saving}
          />
          <span class="muted small">of {maxHistory} above</span>
        </div>
      </div>
    {/if}

    <div class="field">
      <label for="stc-notes">Notes <span class="muted small">(what good looks like)</span></label>
      <input
        id="stc-notes"
        type="text"
        bind:value={notes}
        placeholder="Optional — shown to the judge as context, never parsed as an assertion"
        disabled={saving}
        maxlength="500"
      />
    </div>

    {#if error}
      <div class="inline-error" role="alert">{error}</div>
    {/if}

    <div class="actions">
      <button class="btn-primary" onclick={save} disabled={!canSave}>
        {#if saving}<span class="spinner" aria-hidden="true"></span>Saving…{:else}Save{/if}
      </button>
      <button class="btn-ghost" onclick={() => onclose?.()} disabled={saving}>Cancel</button>
    </div>
  {/if}
</div>

<style>
  /* Colour is set explicitly rather than inherited: the panel renders inside a
     user bubble, which is an accent background with white text, and inheriting
     that would put white type on the panel's own light surface. */
  .save-panel {
    margin-top: 0.5rem;
    padding: 0.75rem 14px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    font-size: 0.85rem;
    text-align: left;
    animation: panel-in 200ms ease;
  }

  @keyframes panel-in {
    from { opacity: 0; transform: translateY(-8px); }
    to   { opacity: 1; transform: translateY(0); }
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  label {
    font-size: 0.75rem;
    color: var(--text-muted);
  }

  .row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }

  /* Recessed relative to the panel so editable areas read as editable. */
  select,
  input[type='text'],
  input[type='number'] {
    flex: 1 1 auto;
    min-width: 0;
    padding: 0.35rem 0.5rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--text);
    font-size: 0.85rem;
  }

  select:focus-visible,
  input:focus-visible {
    border-color: var(--accent);
    outline: none;
  }

  input[type='number'] {
    flex: 0 0 5rem;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 0.25rem;
  }

  .btn-primary,
  .btn-ghost,
  .btn-link {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.35rem 0.75rem;
    border-radius: 6px;
    border: 1px solid var(--border);
    background: transparent;
    color: inherit;
    font-size: 0.8rem;
    cursor: pointer;
  }

  .btn-primary {
    background: var(--accent);
    border-color: var(--accent);
    color: #fff;
  }

  .btn-primary:disabled,
  .btn-ghost:disabled,
  .btn-link:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn-link {
    border: none;
    padding: 0.35rem 0.25rem;
    color: var(--accent);
    flex: 0 0 auto;
  }

  .muted {
    color: var(--text-muted);
  }

  .small {
    font-size: 0.75rem;
  }

  .success {
    color: var(--accent);
  }

  .inline-error {
    color: var(--danger);
    font-size: 0.8rem;
  }

  .spinner {
    width: 12px;
    height: 12px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    display: inline-block;
  }

  .btn-primary:focus-visible,
  .btn-ghost:focus-visible,
  .btn-link:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  @media (prefers-reduced-motion: reduce) {
    .save-panel { animation: none; }
    .spinner { animation-duration: 2s; }
  }
</style>
