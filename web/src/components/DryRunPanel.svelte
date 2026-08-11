<script>
  import { api } from '../api.js'
  import DryRunTranscript from './DryRunTranscript.svelte'

  // Inline preview for a schedule or skill, rendered in place (never a modal).
  //
  // The panel has two model slots. Slot 1 defaults to the agent's live model
  // and is changeable. Slot 2 starts empty; filling it turns the preview into
  // a comparison. That empty slot *is* the mode switch — there is no toggle to
  // be in the wrong state of, and the common single-model case keeps the width.
  //
  // `run(model)` performs one dry run and resolves to a transcript.
  let { run, liveModel = '', onclose = undefined } = $props()

  let models = $state([])
  let primaryModel = $state('')       // '' = the agent's live model
  let compareModel = $state('')       // '' = no comparison
  let picking = $state(null)          // 'primary' | 'compare' | null

  let loading = $state(false)
  let error = $state('')
  let primary = $state(null)
  let candidate = $state(null)
  let hasRun = $state(false)

  const comparing = $derived(!!compareModel)

  $effect(() => { loadModels() })

  let modelsLoaded = false
  async function loadModels() {
    if (modelsLoaded) return
    modelsLoaded = true
    models = await api.modelDetails()
  }

  function modelLabel(id) { return id || liveModel || 'live model' }

  function priceOf(id) {
    const m = models.find(x => x.id === id || x.name === id)
    if (!m?.pricing) return ''
    const { input, output } = m.pricing
    if (input == null || output == null) return ''
    return `$${Number(input).toFixed(2)} / $${Number(output).toFixed(2)}`
  }

  function choose(slot, id) {
    if (slot === 'primary') primaryModel = id
    else compareModel = id
    picking = null
    // A model change invalidates whatever is on screen — showing a stale
    // transcript under a new model name would be a lie.
    primary = null
    candidate = null
    hasRun = false
  }

  function clearCompare() {
    compareModel = ''
    candidate = null
    picking = null
  }

  async function start() {
    loading = true
    error = ''
    primary = null
    candidate = null
    hasRun = true
    try {
      // Sequential, not parallel: two concurrent turns on one agent would
      // interleave in the audit log and double the instantaneous spend.
      primary = await run(primaryModel)
      if (compareModel) candidate = await run(compareModel)
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function pct(a, b) {
    if (!a || !b) return null
    return Math.round(((b - a) / a) * 100)
  }

  function faultCount(t) {
    return (t?.tool_calls || []).filter(c => c.outcome === 'rejected' || c.outcome === 'failed').length
  }

  // Deltas are the answer; the transcripts are the explanation. Only "worse"
  // is coloured — a candidate that improves on something should not compete
  // for attention with the one that regresses.
  let deltas = $derived(!(primary && candidate) ? [] : [
    { label: 'Rounds', from: primary.rounds, to: candidate.rounds,
      delta: pct(primary.rounds, candidate.rounds), worse: candidate.rounds > primary.rounds, suffix: '%' },
    { label: 'Bad tool args', from: faultCount(primary), to: faultCount(candidate),
      delta: faultCount(candidate) - faultCount(primary), worse: faultCount(candidate) > faultCount(primary), suffix: '', absolute: true },
    { label: 'Cost', from: `$${(primary.cost_usd || 0).toFixed(4)}`, to: `$${(candidate.cost_usd || 0).toFixed(4)}`,
      delta: pct(primary.cost_usd, candidate.cost_usd), worse: (candidate.cost_usd || 0) > (primary.cost_usd || 0), suffix: '%' },
    { label: 'Latency', from: `${((primary.duration_ms || 0) / 1000).toFixed(1)}s`, to: `${((candidate.duration_ms || 0) / 1000).toFixed(1)}s`,
      delta: pct(primary.duration_ms, candidate.duration_ms), worse: false, suffix: '%' },
  ])

  function deltaText(d) {
    if (d.delta == null) return ''
    const sign = d.delta > 0 ? '+' : ''
    return `${sign}${d.delta}${d.suffix}`
  }
</script>

<div class="panel">
  <div class="header">
    <span class="run-as">Run as</span>

    <div class="slot-wrap">
      <button class="slot" onclick={() => picking = picking === 'primary' ? null : 'primary'} aria-expanded={picking === 'primary'}>
        <span class="slot-model">{modelLabel(primaryModel)}</span>
        {#if !primaryModel}<span class="slot-tag">LIVE</span>{/if}
        <span class="slot-caret">&#x25BE;</span>
      </button>
      {#if picking === 'primary'}
        <div class="menu" role="listbox" aria-label="Model for this run">
          <button class="menu-item" role="option" aria-selected={!primaryModel} onclick={() => choose('primary', '')}>
            <span class="menu-model">{liveModel || 'live model'}</span><span class="menu-tag">LIVE</span>
          </button>
          {#each models as m}
            <button class="menu-item" role="option" aria-selected={primaryModel === m.id} onclick={() => choose('primary', m.id)}>
              <span class="menu-model">{m.id}</span><span class="menu-price">{priceOf(m.id)}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Rendered while picking too, not only once a model is chosen: the menu
         that selects the compare model cannot live behind having selected one. -->
    {#if comparing || picking === 'compare'}
      <span class="vs">vs</span>
      <div class="slot-wrap">
        <button class="slot accent" onclick={() => picking = picking === 'compare' ? null : 'compare'} aria-expanded={picking === 'compare'}>
          <span class="slot-model">{compareModel || 'choose a model'}</span>
          <span class="slot-caret">&#x25BE;</span>
        </button>
        {#if picking === 'compare'}
          <div class="menu" role="listbox" aria-label="Model to compare against">
            {#each models as m}
              <button class="menu-item" role="option" aria-selected={compareModel === m.id} onclick={() => choose('compare', m.id)}>
                <span class="menu-model">{m.id}</span><span class="menu-price">{priceOf(m.id)}</span>
              </button>
            {/each}
          </div>
        {/if}
      </div>
      <button class="clear" onclick={clearCompare} aria-label="Remove comparison">&times;</button>
    {/if}

    <span class="spacer"></span>
    <button class="btn-primary" onclick={start} disabled={loading}>
      {loading ? 'Running…' : (hasRun ? 'Run again' : (comparing ? 'Run both' : 'Run'))}
    </button>
    {#if onclose}
      <button class="clear" onclick={onclose} aria-label="Close preview">&times;</button>
    {/if}
  </div>

  <div class="notice">
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.4" />
      <path d="M8 5v3.5" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
      <circle cx="8" cy="11" r="0.9" fill="currentColor" />
    </svg>
    <span>Preview only: nothing was sent, written, or remembered.</span>
  </div>

  {#if error}
    <div class="state error-state">
      <span>{error}</span>
      <button class="btn-sm" onclick={start}>Try again</button>
    </div>
  {/if}

  {#if loading && !primary}
    <div class="state">
      <span class="spinner" aria-hidden="true"></span>
      Running through the agent&hellip;
    </div>
  {/if}

  {#if primary && candidate}
    <div class="deltas">
      {#each deltas as d}
        <div class="delta">
          <div class="delta-label">{d.label}</div>
          <div class="delta-values">
            <span class="delta-from">{d.from} &rarr; {d.to}</span>
            {#if d.delta != null && d.delta !== 0}
              <span class="delta-pct" class:worse={d.worse}>{deltaText(d)}</span>
            {/if}
          </div>
        </div>
      {/each}
    </div>
    <div class="caveat">
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.4" />
        <path d="M8 5v3.5" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
        <circle cx="8" cy="11" r="0.9" fill="currentColor" />
      </svg>
      <span>One sample each &mdash; a smoke test, not a verdict. Two models can differ this much on the same prompt by chance. To decide anything, run this as a full eval over a test set.</span>
    </div>
  {/if}

  {#if primary}
    <div class="body" class:split={comparing && candidate}>
      <div class="col">
        <DryRunTranscript transcript={primary} label={primaryModel ? '' : 'LIVE'} compact={!!(comparing && candidate)} />
      </div>
      {#if comparing && candidate}
        <div class="col right">
          <DryRunTranscript transcript={candidate} label="CANDIDATE" accent={true} compact={true} />
        </div>
      {/if}
    </div>
  {/if}

  {#if !comparing && !loading && picking !== 'compare'}
    <div class="body">
      {#if primary}<div class="col grow"></div>{/if}
      <button class="rail" onclick={() => picking = 'compare'}>
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
        <span class="rail-title">Compare with<br />another model</span>
        <span class="rail-sub">Runs the turn twice and shows the deltas</span>
      </button>
    </div>
  {/if}
</div>

<style>
  .panel { display: flex; flex-direction: column; border-top: 1px solid var(--border); background: var(--bg); }

  .header { display: flex; align-items: center; gap: 10px; padding: 12px 16px; flex-wrap: wrap; }
  .run-as { font-size: 12px; color: var(--text-muted); flex-shrink: 0; }
  .vs { font-size: 12px; color: var(--text-muted); }
  .spacer { flex: 1; min-width: 0; }

  .slot-wrap { position: relative; }
  .slot {
    display: flex; align-items: center; gap: 8px;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 6px 10px; cursor: pointer; font-family: inherit;
  }
  .slot:hover { border-color: var(--text-muted); }
  .slot.accent { border-color: var(--accent); }
  .slot-model {
    font-size: 12px; color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .slot-tag { font-size: 10px; font-weight: 600; letter-spacing: 0.05em; color: var(--text-muted); }
  .slot-caret { font-size: 9px; color: var(--text-muted); }

  .menu {
    position: absolute; top: calc(100% + 4px); left: 0; z-index: 20;
    display: flex; flex-direction: column; min-width: 320px; max-height: 280px; overflow-y: auto;
    background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
    box-shadow: 0 4px 16px rgba(0,0,0,0.10);
  }
  .menu-item {
    display: flex; align-items: center; gap: 10px; padding: 8px 12px;
    background: none; border: none; border-top: 1px solid var(--border);
    cursor: pointer; text-align: left; width: 100%; font-family: inherit;
  }
  .menu-item:first-child { border-top: none; }
  .menu-item:hover { background: var(--hover-overlay); }
  .menu-item[aria-selected="true"] { background: rgba(var(--accent-rgb), 0.08); }
  .menu-model {
    flex: 1; min-width: 0; font-size: 12px; color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .menu-tag, .menu-price { font-size: 11px; color: var(--text-muted); flex-shrink: 0; }

  .clear {
    background: none; border: none; color: var(--text-muted);
    font-size: 18px; line-height: 1; cursor: pointer; padding: 0 2px;
  }
  .clear:hover { color: var(--text); }

  .notice {
    display: flex; align-items: flex-start; gap: 10px; padding: 12px 16px;
    background: rgba(200, 126, 48, 0.08);
    border-top: 1px solid var(--border); border-bottom: 1px solid var(--border);
    font-size: 13px; line-height: 20px; color: var(--text);
  }
  .notice svg { flex-shrink: 0; margin-top: 2px; color: var(--warn); }

  .state { display: flex; align-items: center; gap: 10px; padding: 20px 16px; font-size: 13px; color: var(--text-muted); }
  .error-state { color: var(--danger); }
  .error-state span { flex: 1; }

  .spinner {
    width: 14px; height: 14px; border: 2px solid var(--border); border-top-color: var(--accent);
    border-radius: 50%; animation: spin 0.7s linear infinite; flex-shrink: 0;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .deltas { display: flex; align-items: stretch; flex-wrap: wrap; border-bottom: 1px solid var(--border); }
  .delta {
    display: flex; flex-direction: column; gap: 3px;
    padding: 14px 20px; border-right: 1px solid var(--border); flex: 1; min-width: 150px;
  }
  .delta:last-child { border-right: none; }
  .delta-label { font-size: 11px; color: var(--text-muted); }
  .delta-values { display: flex; align-items: baseline; gap: 8px; }
  .delta-from { font-size: 15px; font-weight: 600; color: var(--text); }
  .delta-pct { font-size: 12px; font-weight: 600; color: var(--text-muted); }
  .delta-pct.worse { color: var(--danger); }

  .caveat {
    display: flex; align-items: flex-start; gap: 10px; padding: 12px 16px;
    background: rgba(200, 126, 48, 0.08); border-bottom: 1px solid var(--border);
    font-size: 13px; line-height: 20px; color: var(--text);
  }
  .caveat svg { flex-shrink: 0; margin-top: 2px; color: var(--warn); }

  .body { display: flex; align-items: stretch; }
  .col { display: flex; flex-direction: column; padding: 16px; flex: 1; min-width: 0; }
  .col.right { border-left: 1px solid var(--border); }
  .col.grow { flex: 1; padding: 0; }

  /* Empty compare slot: a rail, not half the panel. The single-model preview
     is the common case and keeps its width until a comparison is asked for. */
  .rail {
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px;
    width: 272px; flex-shrink: 0; padding: 20px 16px;
    border-left: 1px dashed var(--border); background: rgba(0, 0, 0, 0.015);
    cursor: pointer; font-family: inherit; color: var(--text-muted);
  }
  .rail:hover { background: var(--hover-overlay); color: var(--text); }
  .rail-title { font-size: 13px; font-weight: 500; color: var(--text); text-align: center; line-height: 19px; }
  .rail-sub { font-size: 11px; color: var(--text-muted); text-align: center; line-height: 16px; }

  @media (prefers-reduced-motion: reduce) { .spinner { animation-duration: 2s; } }

  @media (max-width: 860px) {
    .body, .body.split { flex-direction: column; }
    .col.right { border-left: none; border-top: 1px solid var(--border); }
    .rail { width: 100%; border-left: none; border-top: 1px dashed var(--border); }
  }
</style>
