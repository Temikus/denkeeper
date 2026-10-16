<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import { memberGroups, assignHint } from '../tasks.js'

  let { members = [], onfiled = () => {}, oncancel = () => {} } = $props()

  let title = $state('')
  let body = $state('')
  // Unassigned is the default: a task can be a plain to-do.
  let assignee = $state('')
  let review = $state(true)
  let saving = $state(false)
  let error = $state('')
  let titleEl

  const groups = $derived(memberGroups(members))
  const hint = $derived(assignHint(assignee, members))

  onMount(() => titleEl?.focus())

  async function file() {
    if (!title.trim() || saving) return
    saving = true
    error = ''
    try {
      const t = await api.createTask({ title: title.trim(), body, assignee, no_review: !review })
      onfiled(t)
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  function onkeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault()
      oncancel()
    }
  }

  function titleKey(e) {
    if (e.key === 'Enter') {
      e.preventDefault()
      file()
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions (Esc anywhere in the form cancels) -->
<form class="composer" aria-label="New task" onsubmit={(e) => { e.preventDefault(); file() }} {onkeydown}>
  <label class="sr-only" for="task-title">Title</label>
  <input
    id="task-title"
    class="field title"
    type="text"
    placeholder="What needs doing?"
    maxlength="200"
    bind:value={title}
    bind:this={titleEl}
    onkeydown={titleKey}
    disabled={saving}
  />
  <label class="sr-only" for="task-body">Details</label>
  <textarea
    id="task-body"
    class="field"
    rows="3"
    placeholder="Details, links, what done looks like (optional)"
    bind:value={body}
    disabled={saving}
  ></textarea>

  <div class="controls">
    <label class="assign">
      <span class="assign-label">Assign</span>
      <select bind:value={assignee} disabled={saving} data-testid="task-assignee">
        {#each groups as g}
          <optgroup label={g.label}>
            {#each g.members as m}
              <option value={m.id}>{m.name}</option>
            {/each}
          </optgroup>
        {/each}
        <optgroup label="People">
          <option value="human">You</option>
          <option value="">Unassigned</option>
        </optgroup>
      </select>
    </label>
    <label class="review">
      <span class="switch switch-sm">
        <input type="checkbox" bind:checked={review} disabled={saving} />
        <span class="switch-slider"></span>
      </span>
      I review before it's done
    </label>
  </div>

  {#if error}<p class="inline-error" role="alert">{error}</p>{/if}

  <div class="actions">
    <button type="submit" class="btn-primary" disabled={!title.trim() || saving}>
      {saving ? 'Filing…' : 'File task'}
    </button>
    <button type="button" class="btn-ghost" onclick={oncancel} disabled={saving}>Cancel</button>
    <span class="hint">{hint}</span>
  </div>
</form>

<style>
  .composer {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 16px;
    margin-bottom: 20px;
    border: 1px solid var(--accent);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .field {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 8px 12px;
    font: inherit;
    font-size: 14px;
  }
  .field:focus { outline: none; border-color: var(--accent); }
  .title { font-weight: 600; }
  textarea.field { resize: vertical; font-size: 13px; }
  .controls { display: flex; flex-wrap: wrap; gap: 16px; align-items: center; }
  .assign { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; min-width: 0; max-width: 100%; }
  .assign-label { color: var(--text-muted); }
  .assign select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 5px 8px;
    font-size: 13px;
    max-width: 100%;
    min-width: 0;
  }
  .review { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; cursor: pointer; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
</style>
