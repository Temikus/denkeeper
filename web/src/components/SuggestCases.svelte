<script>
  import { tick } from 'svelte'
  import { api } from '../api.js'

  // Offers past turns worth keeping as test cases, mined by GET /eval/suggest.
  // Cold-start fill: an operator with no test set has nothing to compare on,
  // and picking turns out of Chat one at a time is the slow way there.
  //
  // Accepting writes the same shape SaveTestCase does — including the pinned
  // history, captured now and replayed verbatim at run time, because the source
  // conversation drifts. Rejecting hides the candidate for the session only:
  // nothing is written, so a reload offers it again.
  let {
    agent = '',
    sets = [],
    defaultSet = '',
    onaccepted = undefined,
    onclose = undefined,
  } = $props()

  const CATEGORY_LABEL = {
    chat: 'Chat / persona',
    skill_command: 'Skill command',
    scheduled: 'Scheduled',
    tool_heavy: 'Tool-heavy',
    probe: 'Behaviour probe',
  }

  // Why this turn is worth keeping, in the operator's words. The API's signal
  // names are the store's vocabulary, not a reason anyone can read.
  const SIGNAL_LABEL = {
    tool_fault: 'a tool call was rejected or failed',
    many_rounds: 'took three or more tool rounds',
    high_cost: 'cost in the top 10% of turns',
    command_skill: 'triggered by a skill command',
  }

  let loading = $state(true)
  let error = $state('')
  let candidates = $state([])

  // Keys hidden for this session — rejected, or already accepted.
  let hiddenKeys = $state(new Set())
  let selectedKeys = $state(new Set())
  let busyKeys = $state(new Set())
  let batching = $state(false)
  // Batch progress, so a long serial add says where it is rather than sitting
  // on one "Adding…" for the whole run.
  let batchDone = $state(0)
  let batchTotal = $state(0)
  let acceptError = $state('')
  let savedMsg = $state('')

  // The set control, focused once the panel has something to act on: it is
  // opened from a button elsewhere on the page, so focus has to follow it in.
  let setControl = $state(null)
  let targetSet = $state('')
  let creatingNew = $state(false)
  let newSetName = $state('')
  // Sets created from here, so the picker shows them before the page reloads.
  let created = $state([])

  const allSets = $derived([
    ...sets,
    ...created.filter(c => !sets.some(s => s.name === c.name)),
  ])
  const visible = $derived(candidates.filter(c => !hiddenKeys.has(keyOf(c))))
  const selectedCount = $derived(visible.filter(c => selectedKeys.has(keyOf(c))).length)
  const busy = $derived(batching || busyKeys.size > 0)
  const setChosen = $derived(creatingNew ? newSetName.trim() !== '' : targetSet !== '')

  function keyOf(c) {
    return `${c.conversation_id}:${c.message_id}`
  }

  function categoryLabel(c) {
    return CATEGORY_LABEL[c] || c
  }

  /** The "why" line: the signals that made this turn interesting. */
  function whyLine(c) {
    const parts = (c.signals || []).map(s => SIGNAL_LABEL[s] || s)
    if (parts.length === 0) return ''
    return `Why: ${parts.join(' · ')}`
  }

  /** Names a card's controls, so 60 buttons are not all called "Accept". */
  function shortLabel(c) {
    const t = (c.prompt || '').trim().replace(/\s+/g, ' ')
    return t.length > 60 ? `${t.slice(0, 60)}…` : t
  }

  function preview(text) {
    const t = (text || '').trim()
    return t.length > 400 ? `${t.slice(0, 400)}…` : t
  }

  // --- Loading --------------------------------------------------------------

  // Guards against a slow response for an agent the operator has since changed
  // away from overwriting a newer one.
  let requestSeq = 0

  async function load() {
    const seq = ++requestSeq
    loading = true
    error = ''
    acceptError = ''
    savedMsg = ''
    try {
      const res = await api.evalSuggest({ agent: agent || undefined, limit: 20 })
      if (seq !== requestSeq) return
      // The endpoint answers {candidates: [...]}; tolerate a bare array so an
      // older server does not render as an error.
      candidates = Array.isArray(res) ? res : (res?.candidates || [])
      selectedKeys = new Set()
      // A fresh pass is a fresh offer: rejections were only for the last list.
      // Accepted turns do not come back — the endpoint excludes saved sources.
      hiddenKeys = new Set()
    } catch (e) {
      if (seq !== requestSeq) return
      error = e.message || 'Could not load suggestions'
    } finally {
      if (seq === requestSeq) loading = false
    }
    if (seq === requestSeq && candidates.length > 0) {
      await tick()
      setControl?.focus()
    }
  }

  function handleKeydown(e) {
    if (e.key === 'Escape' && !busy && onclose) {
      e.stopPropagation()
      onclose()
    }
  }

  /** "golden-set (4 cases)" — a bare count reads as runs. */
  function setOption(s) {
    const n = s.task_count ?? 0
    return `${s.name} (${n} case${n === 1 ? '' : 's'})`
  }

  // Refetch when the agent changes: suggestions are that agent's history.
  $effect(() => {
    void agent
    load()
  })

  // Pick a target set once one is known, defaulting to the launcher's. With no
  // sets at all the only path is creating one, so open that directly.
  let setInitialised = false
  $effect(() => {
    if (setInitialised) return
    if (allSets.length > 0) {
      targetSet = allSets.some(s => s.name === defaultSet) ? defaultSet : allSets[0].name
      setInitialised = true
    } else if (!loading) {
      creatingNew = true
      setInitialised = true
    }
  })

  // --- Selection ------------------------------------------------------------

  function toggleSelected(c) {
    const k = keyOf(c)
    const next = new Set(selectedKeys)
    if (next.has(k)) next.delete(k)
    else next.add(k)
    selectedKeys = next
  }

  function selectAll() {
    selectedKeys = new Set(visible.map(keyOf))
  }

  function clearSelection() {
    selectedKeys = new Set()
  }

  function reject(c) {
    const k = keyOf(c)
    hiddenKeys = new Set(hiddenKeys).add(k)
    const next = new Set(selectedKeys)
    next.delete(k)
    selectedKeys = next
  }

  // --- Accepting ------------------------------------------------------------

  function startNewSet() {
    creatingNew = true
    newSetName = ''
  }

  function cancelNewSet() {
    creatingNew = false
    if (allSets.length > 0) targetSet = allSets[0].name
  }

  /** Resolves the target set name, creating the set on the fly when new. */
  async function ensureSet() {
    if (!creatingNew) return targetSet
    const name = newSetName.trim()
    if (!name) throw new Error('Name the test set first')
    if (!allSets.some(s => s.name === name)) {
      const set = await api.createEvalTaskSet({ name })
      created = [...created, set]
    }
    creatingNew = false
    targetSet = name
    return name
  }

  async function addOne(setName, c) {
    const pinned = (c.preceding || []).map(m => ({ role: m.role, content: m.content }))
    await api.addEvalTask(setName, {
      prompt: c.prompt,
      category: c.category,
      pinned_history: pinned.length ? pinned : undefined,
      source_conversation_id: c.conversation_id || undefined,
      source_message_id: c.message_id ?? null,
    })
  }

  async function accept(c) {
    if (busy) return
    acceptError = ''
    savedMsg = ''
    const k = keyOf(c)
    busyKeys = new Set(busyKeys).add(k)
    try {
      const setName = await ensureSet()
      await addOne(setName, c)
      hiddenKeys = new Set(hiddenKeys).add(k)
      const sel = new Set(selectedKeys)
      sel.delete(k)
      selectedKeys = sel
      savedMsg = `Added 1 test case to “${setName}”`
      onaccepted?.(setName)
    } catch (e) {
      acceptError = e.message || 'Could not add the test case'
    } finally {
      const next = new Set(busyKeys)
      next.delete(k)
      busyKeys = next
    }
  }

  async function acceptSelected() {
    if (busy || selectedCount === 0) return
    acceptError = ''
    savedMsg = ''
    batching = true
    const chosen = visible.filter(c => selectedKeys.has(keyOf(c)))
    batchTotal = chosen.length
    let added = 0
    try {
      const setName = await ensureSet()
      const done = new Set(hiddenKeys)
      for (const c of chosen) {
        // One failure stops the batch rather than silently skipping cases: a
        // partial add the operator cannot see is worse than a short one.
        await addOne(setName, c)
        done.add(keyOf(c))
        hiddenKeys = new Set(done)
        added++
        batchDone = added
      }
      savedMsg = `Added ${added} test case${added === 1 ? '' : 's'} to “${setName}”`
      onaccepted?.(setName)
    } catch (e) {
      acceptError = added > 0
        ? `Added ${added} of ${chosen.length}, then failed: ${e.message}`
        : (e.message || 'Could not add the test cases')
      if (added > 0) onaccepted?.(targetSet)
    } finally {
      selectedKeys = new Set()
      batching = false
      batchDone = 0
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="suggest" data-testid="suggest-panel" aria-label="Suggested test cases"
  aria-busy={busy} onkeydown={handleKeydown}>
  <div class="head">
    <h2 class="section-title">Suggest from history</h2>
    <div class="head-actions">
      <button class="btn-ghost btn-sm" onclick={load} disabled={loading || busy}
        data-testid="suggest-refresh">Refresh</button>
      {#if onclose}
        <button class="btn-ghost btn-sm" onclick={() => onclose?.()} disabled={busy}
          data-testid="suggest-close">Close</button>
      {/if}
    </div>
  </div>

  {#if acceptError}<div class="inline-error" role="alert" data-testid="suggest-accept-error">{acceptError}</div>{/if}
  {#if savedMsg}<div class="save-ok" role="status" data-testid="suggest-saved">{savedMsg}</div>{/if}

  {#if loading}
    <p class="muted row" role="status" data-testid="suggest-loading">
      <span class="spinner" aria-hidden="true"></span>
      Looking through recent turns…
    </p>
  {:else if error}
    <div class="inline-error" role="alert" data-testid="suggest-error">{error}</div>
    <button class="btn-ghost btn-sm" onclick={load}>Try again</button>
  {:else if visible.length === 0}
    <p class="muted" data-testid="suggest-empty">
      {#if candidates.length === 0}
        Nothing to suggest yet. Turns become candidates once they show something worth
        testing — a failed tool call, several tool rounds, an unusually expensive reply,
        or a skill command.
      {:else}
        All suggestions handled. Refresh to look again.
      {/if}
    </p>
  {:else}
    <div class="controls">
      <div class="field set-field">
        <label class="field-label" for="suggest-set">Add to test set</label>
        {#if creatingNew}
          <div class="row">
            <input
              id="suggest-set"
              type="text"
              bind:this={setControl}
              bind:value={newSetName}
              placeholder="e.g. golden-set"
              maxlength="80"
              disabled={busy}
              data-testid="suggest-new-set"
            />
            {#if allSets.length > 0}
              <button class="btn-link" onclick={cancelNewSet} disabled={busy}>Use existing</button>
            {/if}
          </div>
          <span class="hint">Created when you accept the first case.</span>
        {:else}
          <div class="row">
            <select id="suggest-set" bind:this={setControl} bind:value={targetSet}
              disabled={busy} data-testid="suggest-set-select">
              {#each allSets as s}
                <option value={s.name}>{setOption(s)}</option>
              {/each}
            </select>
            <button class="btn-link" onclick={startNewSet} disabled={busy}>New set…</button>
          </div>
        {/if}
      </div>

      <div class="batch">
        <button class="btn-primary" onclick={acceptSelected}
          disabled={busy || selectedCount === 0 || !setChosen}
          data-testid="accept-selected">
          {#if batching}<span class="spinner" aria-hidden="true"></span>Adding {Math.min(batchDone + 1, batchTotal)} of {batchTotal}…{:else}Accept selected ({selectedCount}){/if}
        </button>
        <button class="btn-ghost btn-sm" onclick={selectAll} disabled={busy}>Select all</button>
        <button class="btn-ghost btn-sm" onclick={clearSelection}
          disabled={busy || selectedCount === 0}>Clear</button>
      </div>
    </div>

    {#if !setChosen}
      <p class="hint" data-testid="suggest-blocker">Pick or name a test set to accept into.</p>
    {/if}
    <p class="hint">Rejecting writes nothing — a rejected suggestion comes back on refresh.</p>
    <ul class="cards" data-testid="suggest-cards">
      {#each visible as c (keyOf(c))}
        {@const k = keyOf(c)}
        <li class="card">
          <div class="card-head">
            <label class="pick">
              <input type="checkbox" checked={selectedKeys.has(k)}
                onchange={() => toggleSelected(c)} disabled={busy}
                aria-label={`Select: ${shortLabel(c)}`} />
            </label>
            <span class="cat-chip">{categoryLabel(c.category)}</span>
          </div>
          <p class="prompt">{preview(c.prompt)}</p>
          {#if whyLine(c)}<p class="why">{whyLine(c)}</p>{/if}
          {#if c.preceding?.length}
            <p class="hint">Pins {c.preceding.length} preceding turn{c.preceding.length === 1 ? '' : 's'} as history.</p>
          {/if}
          <div class="card-actions">
            <button class="btn-primary" onclick={() => accept(c)}
              disabled={busy || !setChosen} aria-label={`Accept: ${shortLabel(c)}`}
              data-testid={`accept-${k}`}>
              {#if busyKeys.has(k)}<span class="spinner" aria-hidden="true"></span>Adding…{:else}Accept{/if}
            </button>
            <button class="btn-ghost btn-sm" onclick={() => reject(c)} disabled={busy}
              aria-label={`Reject: ${shortLabel(c)}`}
              data-testid={`reject-${k}`}>Reject</button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .suggest {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: var(--card-inset);
    margin-bottom: 24px;
  }

  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
  }

  .head-actions {
    display: flex;
    gap: 6px;
  }

  /* Matches the page's own section headings and field labels rather than the
     shared sheet, which has neither. */
  .section-title {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin: 0 0 12px;
  }

  .field-label {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
  }

  .inline-error { margin: 0 0 10px; }

  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }

  .set-field {
    flex: 1 1 240px;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .batch {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }

  select,
  input[type='text'] {
    flex: 1 1 auto;
    min-width: 0;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 6px 10px;
    font-size: 13px;
  }

  select:focus,
  input:focus { outline: none; border-color: var(--accent); }

  .cards {
    list-style: none;
    margin: 12px 0 0;
    padding: 0;
    display: grid;
    /* min() keeps a single column from overflowing at 320px, where the page
       and panel padding leave under 260px of track. */
    grid-template-columns: repeat(auto-fit, minmax(min(240px, 100%), 1fr));
    gap: 12px;
  }

  .card {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    padding: 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }

  .card-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .pick {
    display: inline-flex;
    align-items: center;
    cursor: pointer;
  }

  /* A static label, so it takes the page's .status-chip shape rather than the
     shared .chip, which is an interactive filter control. */
  .cat-chip {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-muted);
    white-space: nowrap;
  }

  .prompt {
    font-size: 13px;
    line-height: 1.5;
    color: var(--text);
    margin: 0;
    /* Prompts are prose with unbreakable ids in them; preview() caps the
       length, so the card grows rather than clipping into a scroll box a
       keyboard user could not reach. */
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }

  .why {
    font-size: 12px;
    color: var(--text-muted);
    margin: 0;
    line-height: 1.5;
  }

  .card-actions {
    display: flex;
    gap: 6px;
    margin-top: auto;
  }

  .muted {
    color: var(--text-muted);
    font-size: 13px;
    line-height: 1.6;
  }

  p.hint { margin: 0; }

  /* The shared sheet styles the buttons; it has no disabled treatment for the
     ghost/small pair, and an in-flight control has to read as unavailable. */
  button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn-link {
    border: none;
    background: none;
    padding: 4px 2px;
    color: var(--accent);
    font-size: 13px;
    cursor: pointer;
    flex: 0 0 auto;
  }

  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  .spinner {
    width: 12px;
    height: 12px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    display: inline-block;
    margin-right: 4px;
    vertical-align: -1px;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  @media (prefers-reduced-motion: reduce) {
    .spinner { animation-duration: 2s; }
  }
</style>
