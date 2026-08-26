<script>
  import { tick, untrack } from 'svelte'
  import { api } from '../api.js'

  // Offers behaviour probes generated from the agent's own configuration,
  // via GET /eval/probes. The top-down counterpart to SuggestCases: history
  // can only show what has already happened, so a set drawn from it has
  // nothing to say about whether a candidate respects a denial, stays inside
  // its permission tier, or honours "one sentence, no tools".
  //
  // Accepting writes the same shape SuggestCases does, plus the probe's notes
  // (free-text "what good looks like", read by the judge as context) and its
  // family as a tag. Rejecting hides the card for the session only.
  let {
    agent = '',
    sets = [],
    defaultSet = '',
    onaccepted = undefined,
    onclose = undefined,
  } = $props()

  const KIND_LABEL = {
    denial_compliance: 'Denial compliance',
    tier_boundary: 'Permission tier',
    budget_hint: 'Budget hints',
    approval_policy: 'Approval policy',
    skill_instruction: 'Skill instructions',
    persona_fidelity: 'Persona fidelity',
  }

  // What each family is actually checking, in the operator's words. The API's
  // kind slugs name the family, not the question it asks.
  const KIND_BLURB = {
    denial_compliance: 'does it accept a refusal instead of retrying?',
    tier_boundary: 'does it act within the tier it is actually on?',
    budget_hint: 'does it honour an explicit bound on the answer?',
    approval_policy: 'does it treat a request as standing consent?',
    skill_instruction: 'does it follow the skill you wrote — and leave it alone otherwise?',
    persona_fidelity: 'does it hold the persona you wrote?',
  }

  let loading = $state(true)
  let error = $state('')
  let probes = $state([])
  let tier = $state('')

  let hiddenKeys = $state(new Set())
  let selectedKeys = $state(new Set())
  let busyKeys = $state(new Set())
  let batching = $state(false)
  let batchDone = $state(0)
  let batchTotal = $state(0)
  let acceptError = $state('')
  let savedMsg = $state('')

  let setControl = $state(null)
  // Resolved from the props at construction rather than in an effect: the pass
  // sends the target set so the server can exclude what it already holds, and
  // settling it later would cost a second request on every mount.
  // svelte-ignore state_referenced_locally
  let targetSet = $state(sets.some(s => s.name === defaultSet) ? defaultSet : (sets[0]?.name || ''))
  // svelte-ignore state_referenced_locally
  let creatingNew = $state(sets.length === 0)
  let newSetName = $state('')
  let created = $state([])

  const allSets = $derived([
    ...sets,
    ...created.filter(c => !sets.some(s => s.name === c.name)),
  ])
  const visible = $derived(probes.filter(p => !hiddenKeys.has(keyOf(p))))
  const selectedCount = $derived(visible.filter(p => selectedKeys.has(keyOf(p))).length)
  const busy = $derived(batching || busyKeys.size > 0)
  const setChosen = $derived(creatingNew ? newSetName.trim() !== '' : targetSet !== '')

  // Generation is deterministic, so the prompt is a stable identity — the same
  // probe from a later pass is the same card.
  function keyOf(p) {
    return `${p.kind}:${p.prompt}`
  }

  function kindLabel(k) {
    return KIND_LABEL[k] || k
  }

  function whyLine(p) {
    const blurb = KIND_BLURB[p.kind]
    return blurb ? `Checks: ${blurb}` : ''
  }

  function shortLabel(p) {
    const t = (p.prompt || '').trim().replace(/\s+/g, ' ')
    return t.length > 60 ? `${t.slice(0, 60)}…` : t
  }

  function preview(text) {
    const t = (text || '').trim()
    return t.length > 400 ? `${t.slice(0, 400)}…` : t
  }

  // --- Loading --------------------------------------------------------------

  let requestSeq = 0

  async function load() {
    const seq = ++requestSeq
    loading = true
    error = ''
    acceptError = ''
    savedMsg = ''
    try {
      // Passing the target set is what keeps a second pass quiet: the server
      // drops probes that set already carries.
      const res = await api.evalProbes({
        agent: agent || undefined,
        set: targetSet || undefined,
      })
      if (seq !== requestSeq) return
      probes = Array.isArray(res) ? res : (res?.probes || [])
      tier = res?.permission_tier || ''
      selectedKeys = new Set()
      hiddenKeys = new Set()
    } catch (e) {
      if (seq !== requestSeq) return
      error = e.message || 'Could not generate probes'
    } finally {
      if (seq === requestSeq) loading = false
    }
    if (seq === requestSeq && probes.length > 0) {
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

  function setOption(s) {
    const n = s.task_count ?? 0
    return `${s.name} (${n} case${n === 1 ? '' : 's'})`
  }

  // Probes are that agent's configuration, so a change of agent is a new pass.
  // load() reads targetSet too, but untracked: ensureSet assigns it mid-batch
  // when a set is created on the fly, and a reload there would wipe the
  // progress and the saved message. The set picker reloads explicitly instead.
  $effect(() => {
    void agent
    untrack(() => load())
  })

  // --- Selection ------------------------------------------------------------

  function toggleSelected(p) {
    const k = keyOf(p)
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

  function reject(p) {
    const k = keyOf(p)
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

  async function addOne(setName, p) {
    const pinned = (p.preceding || []).map(m => ({ role: m.role, content: m.content }))
    await api.addEvalTask(setName, {
      prompt: p.prompt,
      category: p.category,
      // Judge context, never parsed as an assertion.
      notes: p.notes || undefined,
      tags: p.tags?.length ? p.tags : undefined,
      pinned_history: pinned.length ? pinned : undefined,
    })
  }

  async function accept(p) {
    if (busy) return
    acceptError = ''
    savedMsg = ''
    const k = keyOf(p)
    busyKeys = new Set(busyKeys).add(k)
    try {
      const setName = await ensureSet()
      await addOne(setName, p)
      hiddenKeys = new Set(hiddenKeys).add(k)
      const sel = new Set(selectedKeys)
      sel.delete(k)
      selectedKeys = sel
      savedMsg = `Added 1 probe to “${setName}”`
      onaccepted?.(setName)
    } catch (e) {
      acceptError = e.message || 'Could not add the probe'
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
    const chosen = visible.filter(p => selectedKeys.has(keyOf(p)))
    batchTotal = chosen.length
    let added = 0
    try {
      const setName = await ensureSet()
      const done = new Set(hiddenKeys)
      for (const p of chosen) {
        // One failure stops the batch: a partial add the operator cannot see is
        // worse than a short one.
        await addOne(setName, p)
        done.add(keyOf(p))
        added++
        batchDone = added
      }
      hiddenKeys = done
      savedMsg = `Added ${added} probe${added === 1 ? '' : 's'} to “${setName}”`
      onaccepted?.(setName)
    } catch (e) {
      acceptError = added > 0
        ? `Added ${added} of ${chosen.length}, then failed: ${e.message}`
        : (e.message || 'Could not add the probes')
      if (added > 0) onaccepted?.(targetSet)
    } finally {
      selectedKeys = new Set()
      batching = false
      batchDone = 0
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="probes" data-testid="probes-panel" aria-label="Generated behaviour probes"
  aria-busy={busy} onkeydown={handleKeydown}>
  <div class="head">
    <h2 class="section-title">Generate behaviour probes</h2>
    <div class="head-actions">
      <button class="btn-ghost btn-sm" onclick={load} disabled={loading || busy}
        data-testid="probes-refresh">Refresh</button>
      {#if onclose}
        <button class="btn-ghost btn-sm" onclick={() => onclose?.()} disabled={busy}
          data-testid="probes-close">Close</button>
      {/if}
    </div>
  </div>

  {#if acceptError}<div class="inline-error" role="alert" data-testid="probes-accept-error">{acceptError}</div>{/if}
  {#if savedMsg}<div class="save-ok" role="status" data-testid="probes-saved">{savedMsg}</div>{/if}

  {#if loading}
    <p class="muted row" role="status" data-testid="probes-loading">
      <span class="spinner" aria-hidden="true"></span>
      Reading this agent's configuration…
    </p>
  {:else if error}
    <div class="inline-error" role="alert" data-testid="probes-error">{error}</div>
    <button class="btn-ghost btn-sm" onclick={load}>Try again</button>
  {:else if visible.length === 0}
    <p class="muted" data-testid="probes-empty">
      {#if probes.length === 0}
        Nothing new to generate. Probes come from this agent's permission tier, auto-approve
        policy, persona and skills — the set you picked already covers what its configuration
        describes.
      {:else}
        All probes handled. Refresh to look again.
      {/if}
    </p>
  {:else}
    <p class="muted" data-testid="probes-lead">
      Generated from this agent's own configuration{#if tier} — permission tier
      <strong>{tier}</strong>{/if}. These cover behaviour history cannot: a well-behaved
      current model never retried a denied call, so no past turn shows one being respected.
    </p>

    <div class="controls">
      <div class="field set-field">
        <label class="field-label" for="probes-set">Add to test set</label>
        {#if creatingNew}
          <div class="row">
            <input
              id="probes-set"
              type="text"
              bind:this={setControl}
              bind:value={newSetName}
              placeholder="e.g. probes"
              maxlength="80"
              disabled={busy}
              data-testid="probes-new-set"
            />
            {#if allSets.length > 0}
              <button class="btn-link" onclick={cancelNewSet} disabled={busy}>Use existing</button>
            {/if}
          </div>
          <span class="hint">Created when you accept the first probe.</span>
        {:else}
          <div class="row">
            <!-- Not bind:value — the pass has to be re-run against the newly
                 chosen set, and the assignment and the reload must be ordered. -->
            <select id="probes-set" bind:this={setControl} value={targetSet}
              onchange={(e) => { targetSet = e.currentTarget.value; load() }}
              disabled={busy} data-testid="probes-set-select">
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
          data-testid="probes-accept-selected">
          {#if batching}<span class="spinner" aria-hidden="true"></span>Adding {Math.min(batchDone + 1, batchTotal)} of {batchTotal}…{:else}Accept selected ({selectedCount}){/if}
        </button>
        <button class="btn-ghost btn-sm" onclick={selectAll} disabled={busy}>Select all</button>
        <button class="btn-ghost btn-sm" onclick={clearSelection}
          disabled={busy || selectedCount === 0}>Clear</button>
      </div>
    </div>

    {#if !setChosen}
      <p class="hint" data-testid="probes-blocker">Pick or name a test set to accept into.</p>
    {/if}
    <p class="hint">Rejecting writes nothing — a rejected probe comes back on refresh.</p>
    <ul class="cards" data-testid="probes-cards">
      {#each visible as p (keyOf(p))}
        {@const k = keyOf(p)}
        <li class="card">
          <div class="card-head">
            <label class="pick">
              <input type="checkbox" checked={selectedKeys.has(k)}
                onchange={() => toggleSelected(p)} disabled={busy}
                aria-label={`Select: ${shortLabel(p)}`} />
            </label>
            <span class="cat-chip">{kindLabel(p.kind)}</span>
            {#if p.source}<span class="src">from {p.source}</span>{/if}
          </div>
          <p class="prompt">{preview(p.prompt)}</p>
          {#if whyLine(p)}<p class="why">{whyLine(p)}</p>{/if}
          {#if p.notes}
            <details class="notes">
              <summary>What good looks like</summary>
              <p>{p.notes}</p>
            </details>
          {/if}
          {#if p.preceding?.length}
            <p class="hint">Pins {p.preceding.length} preceding turn{p.preceding.length === 1 ? '' : 's'} as history.</p>
          {/if}
          <div class="card-actions">
            <button class="btn-primary" onclick={() => accept(p)}
              disabled={busy || !setChosen} aria-label={`Accept: ${shortLabel(p)}`}
              data-testid={`probe-accept-${k}`}>
              {#if busyKeys.has(k)}<span class="spinner" aria-hidden="true"></span>Adding…{:else}Accept{/if}
            </button>
            <button class="btn-ghost btn-sm" onclick={() => reject(p)} disabled={busy}
              aria-label={`Reject: ${shortLabel(p)}`}
              data-testid={`probe-reject-${k}`}>Reject</button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .probes {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 18px;
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

  .inline-error { color: var(--danger); font-size: 12px; margin: 0 0 10px; }

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
    flex-wrap: wrap;
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

  .src {
    font-size: 11px;
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }

  .prompt {
    font-size: 13px;
    line-height: 1.5;
    color: var(--text);
    margin: 0;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }

  .why {
    font-size: 12px;
    color: var(--text-muted);
    margin: 0;
    line-height: 1.5;
  }

  /* Notes are long prose and only matter when the operator is deciding whether
     the probe grades the right thing, so they start collapsed. */
  .notes {
    font-size: 12px;
    color: var(--text-muted);
  }

  .notes summary {
    cursor: pointer;
  }

  .notes p {
    margin: 6px 0 0;
    line-height: 1.5;
    overflow-wrap: anywhere;
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

  @media (max-width: 520px) {
    .probes { padding: 14px; }
  }
</style>
