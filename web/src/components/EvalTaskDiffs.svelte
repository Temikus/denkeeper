<script>
  import { api, evalSampleTranscript } from '../api.js'
  import DryRunTranscript from './DryRunTranscript.svelte'

  // Per-test-case results for one run: a row per test case with each model's
  // cost, rounds and latency plus the candidate's deltas, expanding inline to
  // the runs behind those numbers side by side and the judge's call on them.
  //
  // Props:
  //   runId  — the run to show (required).
  //   summary — an already-fetched GET /eval/runs/{id}/summary, when the
  //             parent has one. Omit and the component fetches its own.
  //
  // Mounting is therefore: <EvalTaskDiffs runId={id} {summary} />
  //
  // Samples and judged pairs are fetched once, on the first expansion: they
  // carry whole transcripts, and a run's worth of them is not worth loading
  // for a table nobody may open.
  let { runId, summary = null } = $props()

  let ownSummary = $state(null)
  let loading = $state(false)
  let error = $state('')

  // Fetched lazily and shared by every row.
  let samples = $state(null)
  let pairs = $state(null)
  let detailLoading = $state(false)
  let detailError = $state('')
  let detailPromise = null

  let expanded = $state(new Set())

  const view = $derived(summary || ownSummary)
  const variants = $derived(view?.variants || [])
  const perTask = $derived(view?.per_task || [])
  // The baseline is the first variant by creation order — the incumbent.
  const baselineName = $derived(view?.baseline_variant || variants[0]?.name || '')

  $effect(() => {
    if (summary || !runId) return
    loadSummary(runId)
  })

  async function loadSummary(id) {
    loading = true
    error = ''
    try {
      ownSummary = await api.evalRunSummary(id)
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function loadDetail() {
    if (detailPromise) return detailPromise
    detailLoading = true
    detailError = ''
    detailPromise = Promise.all([
      api.evalRunSamples(runId),
      api.evalRunPairs(runId),
    ]).then(([s, p]) => {
      samples = Array.isArray(s) ? s : []
      // The pairs endpoint returns the whole judging grid, not a bare list.
      pairs = Array.isArray(p?.pairs) ? p.pairs : (Array.isArray(p) ? p : [])
    }).catch(e => {
      detailError = e.message
      // Cleared so the next expansion retries rather than showing a stale
      // failure forever.
      detailPromise = null
    }).finally(() => {
      detailLoading = false
    })
    return detailPromise
  }

  function toggle(taskID) {
    const next = new Set(expanded)
    if (next.has(taskID)) {
      next.delete(taskID)
    } else {
      next.add(taskID)
      loadDetail()
    }
    expanded = next
  }

  /** Model name to head a transcript with: the overlay's, else the variant's. */
  function modelFor(name) {
    const v = variants.find(x => x.name === name)
    return v?.overlay?.llm_model || name
  }

  /** Samples for one test case, grouped by run index then variant name. */
  function rowsFor(taskID) {
    if (!samples) return []
    const byK = new Map()
    for (const smp of samples) {
      if (smp.task_id !== taskID) continue
      if (!byK.has(smp.k_index)) byK.set(smp.k_index, new Map())
      const v = variants.find(x => x.variant_id === smp.variant_id)
      byK.get(smp.k_index).set(v?.name || String(smp.variant_id), smp)
    }
    return [...byK.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([k, byVariant]) => ({ k, byVariant }))
  }

  function pairFor(taskID, k) {
    return (pairs || []).find(p => p.task_id === taskID && p.k === k) || null
  }

  /** Every verdict recorded against a pair, across both presentation orders. */
  function verdictsOf(pair) {
    return (pair?.items || []).flatMap(item => item.verdicts || [])
  }

  const OUTCOME_LABEL = {
    win: 'Candidate preferred',
    loss: 'Current preferred',
    tie: 'Tie',
    pending: 'Not judged yet',
  }

  function money(n) {
    return `$${(n || 0).toFixed(4)}`
  }

  function latency(ms) {
    if (!ms) return '0 ms'
    return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`
  }

  function rounds(n) {
    return (n || 0).toFixed(1)
  }

  // Deltas are absolute, against the baseline, and zero on the baseline row.
  // Lower is better for all three, so a negative delta is the good direction.
  function deltaClass(d, epsilon) {
    if (!d || Math.abs(d) < epsilon) return 'flat'
    return d < 0 ? 'good' : 'bad'
  }

  function deltaCost(d) {
    if (!d || Math.abs(d) < 0.00005) return '±0'
    return `${d > 0 ? '+' : '−'}${money(Math.abs(d)).slice(1)}`
  }

  function deltaRounds(d) {
    if (!d || Math.abs(d) < 0.05) return '±0'
    return `${d > 0 ? '+' : '−'}${Math.abs(d).toFixed(1)}`
  }

  function deltaLatency(d) {
    if (!d || Math.abs(d) < 1) return '±0'
    return `${d > 0 ? '+' : '−'}${latency(Math.abs(d))}`
  }

  function shortPrompt(p) {
    const s = (p || '').replace(/\s+/g, ' ').trim()
    return s.length > 90 ? `${s.slice(0, 90)}…` : s
  }
</script>

<div class="diffs">
  <h3 class="section-title">Per-test-case results</h3>

  {#if loading}
    <p class="muted">Loading results...</p>
  {:else if error}
    <div class="banner error" role="alert">{error}</div>
  {:else if perTask.length === 0}
    <p class="muted">
      No per-test-case results yet. Numbers appear once the run has produced
      results for at least one test case.
    </p>
  {:else}
    <div class="table-wrap">
      <table class="table">
        <thead>
          <tr>
            <th rowspan="2" class="col-prompt">Test case</th>
            <th rowspan="2">Category</th>
            {#each variants as v (v.variant_id)}
              <th colspan="3" class="group" class:candidate={v.name !== baselineName}>
                {v.name}
                <span class="role">{v.name === baselineName ? 'Current' : 'Candidate'}</span>
              </th>
            {/each}
            <th rowspan="2" class="col-toggle"></th>
          </tr>
          <tr>
            {#each variants as v (v.variant_id)}
              <th class="sub group-start">Cost</th>
              <th class="sub">Rounds</th>
              <th class="sub">Latency</th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each perTask as t (t.task_id)}
            <tr data-testid="task-row-{t.task_id}">
              <td class="col-prompt" title={t.prompt}>{shortPrompt(t.prompt)}</td>
              <td><span class="pill">{t.category || '—'}</span></td>
              {#each variants as v (v.variant_id)}
                {@const cell = t.variants?.find(c => c.variant_id === v.variant_id)}
                {#if !cell || cell.samples_ok === 0}
                  <td class="num group-start muted">—</td>
                  <td class="num muted">—</td>
                  <td class="num muted">—</td>
                {:else}
                  <td class="num group-start">
                    {money(cell.mean_cost)}
                    {#if v.name !== baselineName}
                      <span class="delta {deltaClass(cell.delta_cost, 0.00005)}">{deltaCost(cell.delta_cost)}</span>
                    {/if}
                  </td>
                  <td class="num">
                    {rounds(cell.mean_rounds)}
                    {#if v.name !== baselineName}
                      <span class="delta {deltaClass(cell.delta_rounds, 0.05)}">{deltaRounds(cell.delta_rounds)}</span>
                    {/if}
                  </td>
                  <td class="num">
                    {latency(cell.mean_latency_ms)}
                    {#if v.name !== baselineName}
                      <span class="delta {deltaClass(cell.delta_latency_ms, 1)}">{deltaLatency(cell.delta_latency_ms)}</span>
                    {/if}
                  </td>
                {/if}
              {/each}
              <td class="col-toggle">
                <button
                  class="expand-btn"
                  onclick={() => toggle(t.task_id)}
                  aria-expanded={expanded.has(t.task_id)}
                  aria-controls="task-detail-{t.task_id}"
                  aria-label="{expanded.has(t.task_id) ? 'Hide' : 'Show'} the runs for this test case"
                >
                  <span class="chevron" class:open={expanded.has(t.task_id)}>&#x25B6;</span>
                </button>
              </td>
            </tr>
            {#if expanded.has(t.task_id)}
              <tr class="detail-row">
                <td colspan={2 + variants.length * 3 + 1}>
                  <div class="detail" id="task-detail-{t.task_id}" data-testid="task-detail-{t.task_id}">
                    <div class="prompt-full">{t.prompt}</div>

                    {#if detailLoading}
                      <p class="muted">Loading the runs for this test case...</p>
                    {:else if detailError}
                      <div class="banner error" role="alert">{detailError}</div>
                    {:else}
                      {@const groups = rowsFor(t.task_id)}
                      {#if groups.length === 0}
                        <p class="muted">This test case produced no runs.</p>
                      {:else}
                        {#each groups as g (g.k)}
                          {@const pair = pairFor(t.task_id, g.k)}
                          <div class="k-block" data-testid="k-block-{t.task_id}-{g.k}">
                            {#if groups.length > 1}
                              <div class="k-label">Run {g.k + 1} of {groups.length}</div>
                            {/if}
                            <div class="columns">
                              {#each variants as v (v.variant_id)}
                                {@const smp = g.byVariant.get(v.name)}
                                <div class="column" class:candidate={v.name !== baselineName}>
                                  {#if !smp}
                                    <p class="muted">No result for {v.name}.</p>
                                  {:else if smp.status !== 'ok'}
                                    <div class="failed">
                                      <span class="failed-label">{v.name} failed</span>
                                      <span class="failed-msg">{smp.error || 'No error recorded.'}</span>
                                    </div>
                                  {:else}
                                    <DryRunTranscript
                                      transcript={evalSampleTranscript(smp, { model: modelFor(v.name) })}
                                      label={v.name === baselineName ? 'CURRENT' : 'CANDIDATE'}
                                      accent={v.name !== baselineName}
                                      compact={true}
                                    />
                                  {/if}
                                </div>
                              {/each}
                            </div>

                            {#if pair}
                              {@const verdicts = verdictsOf(pair)}
                              <div class="judgment" data-testid="judgment-{t.task_id}-{g.k}">
                                <div class="judgment-head">
                                  <span class="outcome outcome-{pair.outcome}">
                                    {OUTCOME_LABEL[pair.outcome] || pair.outcome}
                                  </span>
                                  {#if verdicts.length > 0}
                                    <span class="hint">
                                      {verdicts.length} judgement{verdicts.length === 1 ? '' : 's'}
                                    </span>
                                  {/if}
                                </div>
                                {#if verdicts.length === 0}
                                  <p class="hint">
                                    Nobody has judged this comparison yet.
                                  </p>
                                {:else}
                                  {#each verdicts as vd, i (i)}
                                    <div class="verdict">
                                      <div class="verdict-head">
                                        <span class="judge">{vd.judge_ident || 'unknown judge'}</span>
                                        <span class="picked">
                                          {vd.winner_variant ? `picked ${vd.winner_variant}` : 'called it a tie'}
                                        </span>
                                        {#if vd.rubric_version}
                                          <span class="hint">rubric {vd.rubric_version}</span>
                                        {/if}
                                      </div>
                                      {#if vd.dimensions && Object.keys(vd.dimensions).length > 0}
                                        <ul class="dimensions">
                                          {#each Object.entries(vd.dimensions) as [dim, winner] (dim)}
                                            <li><span class="dim">{dim}</span><span class="dim-winner">{winner}</span></li>
                                          {/each}
                                        </ul>
                                      {/if}
                                      {#if vd.notes}
                                        <p class="notes">{vd.notes}</p>
                                      {/if}
                                    </div>
                                  {/each}
                                {/if}
                              </div>
                            {/if}
                          </div>
                        {/each}
                      {/if}
                    {/if}
                  </div>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<style>
  .diffs { margin-top: 24px; }

  .section-title {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin: 0 0 12px;
  }

  .table-wrap { overflow-x: auto; }
  .table-wrap .table { margin-bottom: 0; }

  th.group {
    text-align: center;
    border-left: 1px solid var(--border);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  th.group.candidate { color: var(--accent); }
  th.group .role {
    display: block;
    font-family: inherit;
    font-size: 10px;
    font-weight: 500;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  th.sub { font-size: 10px; font-weight: 500; }
  th.group-start, td.group-start { border-left: 1px solid var(--border); }

  .col-prompt { max-width: 320px; }
  .col-toggle { width: 32px; }

  td.num {
    text-align: right;
    white-space: nowrap;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
  }
  .delta { display: block; font-size: 11px; }
  .delta.good { color: var(--success); }
  .delta.bad { color: var(--danger); }
  .delta.flat { color: var(--text-muted); }

  .expand-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
  }
  .expand-btn:hover { color: var(--text); }
  .expand-btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .chevron { font-size: 9px; transition: transform 0.2s; }
  .chevron.open { transform: rotate(90deg); }

  /* The detail spans the whole row and carries its own padding, like the
     dry-run preview row on the Schedules page. */
  .detail-row > td { padding: 0; background: var(--surface); }
  .detail { display: flex; flex-direction: column; gap: 16px; padding: 16px; }

  .prompt-full {
    font-size: 13px;
    line-height: 19px;
    color: var(--text);
    white-space: pre-wrap;
    word-break: break-word;
    border-left: 2px solid var(--border);
    padding-left: 12px;
  }

  .k-block { display: flex; flex-direction: column; gap: 12px; }
  .k-label {
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--text-muted);
  }

  .columns { display: flex; align-items: stretch; gap: 0; }
  .column {
    flex: 1;
    min-width: 0;
    padding: 0 16px;
  }
  .column:first-child { padding-left: 0; }
  .column.candidate { border-left: 1px solid var(--border); }

  .failed {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 12px 14px;
    border: 1px solid var(--danger);
    border-radius: var(--radius);
  }
  .failed-label {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--danger);
  }
  .failed-msg { font-size: 12px; color: var(--text); word-break: break-word; }

  .judgment {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 14px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .judgment-head { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; }
  .outcome { font-size: 12px; font-weight: 600; color: var(--text); }
  .outcome-win { color: var(--success); }
  .outcome-loss { color: var(--danger); }
  .outcome-pending { color: var(--text-muted); }

  .verdict { display: flex; flex-direction: column; gap: 6px; }
  .verdict-head { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; font-size: 12px; }
  .judge { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text); }
  .picked { color: var(--text-muted); }

  .dimensions { list-style: none; display: flex; flex-wrap: wrap; gap: 6px; margin: 0; padding: 0; }
  .dimensions li {
    display: flex;
    gap: 6px;
    font-size: 11px;
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 2px 6px;
  }
  .dim { color: var(--text-muted); }
  .dim-winner { color: var(--text); font-weight: 500; }

  .notes {
    margin: 0;
    font-size: 12px;
    line-height: 18px;
    color: var(--text);
    white-space: pre-wrap;
    word-break: break-word;
  }

  @media (prefers-reduced-motion: reduce) {
    .chevron { transition: none; }
  }

  /* Below the split point the transcripts stack: two of them side by side
     stop being readable long before 320px. */
  @media (max-width: 720px) {
    .columns { flex-direction: column; gap: 16px; }
    .column { padding: 0; }
    .column.candidate { border-left: none; border-top: 1px solid var(--border); padding-top: 16px; }
    .detail { padding: 12px; }
  }
</style>
