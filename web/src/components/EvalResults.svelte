<script>
  // The results view for one finished comparison run: the verdict with its
  // work, the objective scorecard, and the per-test-case diffs underneath.
  //
  // Terminology: the API's task_set/variant/sample are "test set", "current vs
  // candidate" and "turns" here — six new nouns is a teaching tax this page
  // should not charge.
  import { onMount } from 'svelte'
  import { api, evalSampleTranscript } from '../api.js'
  import ErrorBanner from './ErrorBanner.svelte'
  import DryRunTranscript from './DryRunTranscript.svelte'

  let {
    run,
    agent = null,
    // True when the run covered a subset of the set, i.e. a Quick check. A
    // clean Quick check earns the escalation CTA.
    quick = false,
    onapplied = () => {},
    onrunfull = () => {},
  } = $props()

  let loading = $state(true)
  let error = $state('')
  let summary = $state(null)
  let pairView = $state(null)

  // Turn transcripts are large and only wanted once a row opens, so they load
  // on the first expansion and are kept for the rest of the session.
  let samples = $state(null)
  let samplesLoading = $state(false)
  let samplesError = $state('')
  let expandedTask = $state(null)

  let confirmApply = $state(null)
  let applying = $state(false)
  let applyError = $state('')
  let applyOk = $state('')
  // The candidate this view has already switched the agent to, so the button
  // cannot be clicked twice for the same change.
  let appliedVariant = $state('')
  // '' | 'ok' | 'failed' — a silently dead Copy button reads as broken.
  let copyState = $state('')

  const VERDICT_LABEL = {
    upgrade: 'Upgrade',
    downgrade: 'Downgrade',
    no_regressions: 'No regressions detected',
    inconclusive: 'Inconclusive',
  }

  const GATE_LABEL = {
    rejected_rate: 'Rejected tool calls',
    mean_rounds: 'Rounds per test case',
    mean_cost_per_task: 'Cost per test case',
  }

  // Categories are stored as slugs; SuggestCases.svelte labels them the same
  // way, and the two lists have to agree.
  const CATEGORY_LABEL = {
    chat: 'Chat / persona',
    skill_command: 'Skill command',
    scheduled: 'Scheduled',
    tool_heavy: 'Tool-heavy',
  }

  const OUTCOME_LABEL = {
    win: 'candidate won',
    loss: 'current won',
    tie: 'tie',
    pending: 'not judged yet',
  }

  let variants = $derived(summary?.variants || [])
  let baselineName = $derived(summary?.baseline_variant || variants[0]?.name || 'current')
  let completeness = $derived(summary?.completeness || null)
  let verdicts = $derived(summary?.verdicts || [])

  // Formats on magnitude and hoists the sign, so a saving of $0.0034 reads as
  // -$0.0034 rather than collapsing to -$0.00.
  function fmtUSD(v) {
    if (v == null) return '—'
    const abs = Math.abs(v)
    const body = abs > 0 && abs < 0.01 ? abs.toFixed(4) : abs.toFixed(2)
    return `${v < 0 ? '-' : ''}$${body}`
  }

  /** A signed cost, for delta cells where the direction is the point. */
  function fmtCostDelta(v) {
    return `${v > 0 ? '+' : ''}${fmtUSD(v)}`
  }

  function fmtPct(v) {
    if (v == null) return '—'
    return `${(v * 100).toFixed(1)}%`
  }

  function fmtNum(v, digits = 2) {
    if (v == null) return '—'
    return v.toFixed(digits)
  }

  /** A signed plain number, for delta cells with no unit of their own. */
  function fmtSignedNum(v, digits = 2) {
    return `${v > 0 ? '+' : ''}${v.toFixed(digits)}`
  }

  function fmtMs(ms) {
    if (!ms) return '—'
    return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`
  }

  /** A signed delta in the gate's own unit, so a table row reads on its own. */
  function fmtDelta(v, unit) {
    if (v == null) return '—'
    const sign = v > 0 ? '+' : ''
    return `${sign}${v.toFixed(1)}${unit === 'pp' ? ' pp' : '%'}`
  }

  /** Gate values are rates for the pp gate and magnitudes for the rest. */
  function fmtGateValue(gate, v) {
    if (gate.unit === 'pp') return fmtPct(v)
    if (gate.name === 'mean_cost_per_task') return fmtUSD(v)
    return fmtNum(v)
  }

  function verdictLabel(v) {
    return VERDICT_LABEL[v] || v
  }

  function categoryLabel(c) {
    return CATEGORY_LABEL[c] || c || '—'
  }

  /** The candidate's own metrics row, for the objective table's ordering. */
  function metricsFor(name) {
    return variants.find(v => v.name === name) || null
  }

  function isPending(verdict) {
    const j = verdict.judgment || {}
    return (j.pairs || 0) > 0 && (j.judged_pairs || 0) < j.pairs
  }

  /** A clean Quick check is worth escalating; a failed gate is not. */
  function canEscalate(verdict) {
    return quick && !!modelOf(verdict)
      && (verdict.gates || []).every(g => g.pass) && verdict.verdict !== 'downgrade'
  }

  let judgeCommand = $derived(`claude -p "judge pending pairs for eval run ${run?.id}"`)

  async function copyCommand() {
    try {
      await navigator.clipboard.writeText(judgeCommand)
      copyState = 'ok'
      setTimeout(() => (copyState = ''), 2000)
    } catch {
      // Clipboard access denied or unavailable: the command is on screen and
      // selectable, so the button says to select it rather than dying quietly.
      copyState = 'failed'
    }
  }

  async function load() {
    loading = true
    error = ''
    try {
      const [sum, pairs] = await Promise.all([
        api.evalRunSummary(run.id),
        // The judging grid is optional detail: a run nobody judged still has a
        // scorecard, so a pairs failure must not take the whole view down.
        api.evalRunPairs(run.id).catch(() => null),
      ])
      summary = sum
      pairView = pairs
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  onMount(load)

  async function toggleTask(taskID) {
    if (expandedTask === taskID) {
      expandedTask = null
      return
    }
    expandedTask = taskID
    if (samples || samplesLoading) return
    await loadSamples()
  }

  async function loadSamples() {
    samplesLoading = true
    samplesError = ''
    try {
      samples = (await api.evalRunSamples(run.id)) || []
    } catch (e) {
      samplesError = e.message
    } finally {
      samplesLoading = false
    }
  }

  /** The (k, variantID) grid for one test case, in k then variant order. */
  function turnsFor(taskID) {
    const rows = (samples || []).filter(s => s.task_id === taskID)
    const ks = [...new Set(rows.map(s => s.k_index))].sort((a, b) => a - b)
    return ks.map(k => ({
      k,
      byVariant: new Map(rows.filter(s => s.k_index === k).map(s => [s.variant_id, s])),
    }))
  }

  function pairFor(taskID, k) {
    return (pairView?.pairs || []).find(p => p.task_id === taskID && p.k === k) || null
  }

  /** Judge calls only — the operator's calibration marks are shown apart. */
  function judgeVerdicts(pair) {
    return (pair?.items || []).flatMap(it => {
      const key = letterKey(it, pair)
      return (it.verdicts || []).map(v => ({ ...v, order: it.presentation_order, key }))
    })
  }

  // A judge names a presented letter, and only the pair's assignment — which
  // never leaves the server — says which model that letter was. The pairs
  // endpoint resolves the overall winner but leaves the per-dimension letters
  // raw, so an unresolved "persona_fit: b" names nothing the reader can act
  // on. A non-tie verdict on an item is itself the key for that item's
  // letters: its winner letter is its winner_variant, so the other letter is
  // the other side of the pair. An item everyone judged a tie stays
  // unresolvable and keeps its letter.
  function letterKey(item, pair) {
    for (const v of item.verdicts || []) {
      if (!v.winner_variant || !v.winner) continue
      const won = v.winner.toLowerCase()
      if (won !== 'a' && won !== 'b') continue
      const other = v.winner_variant === pair.candidate?.variant
        ? pair.baseline?.variant
        : pair.candidate?.variant
      return won === 'a'
        ? { a: v.winner_variant, b: other }
        : { a: other, b: v.winner_variant }
    }
    return null
  }

  /** One dimension's winner, as a model name where the letter resolves. */
  function dimensionWinner(value, key) {
    const v = (value || '').toLowerCase()
    if (v === 'tie') return 'tie'
    if (key && (v === 'a' || v === 'b')) return key[v] || value
    return value
  }

  /** The model behind a variant, for the transcript header. */
  function modelFor(variant) {
    if (variant.name === baselineName) return agent?.model || variant.overlay?.llm_model || variant.name
    return variant.overlay?.llm_model || variant.name
  }

  /**
   * Variant names are free text chosen by whoever created the run, so a run
   * started over the API or MCP can be called `variant-a` or `sample-2`. The
   * model the variant actually ran is the honest label, and the only one this
   * page's terminology rule allows; the raw name is the last resort.
   */
  function displayName(variantName) {
    return metricsFor(variantName)?.overlay?.llm_model || variantName
  }

  /** A variant with no model in its overlay is nothing the agent can switch to. */
  function modelOf(verdict) {
    return metricsFor(verdict.variant)?.overlay?.llm_model || ''
  }

  function askApply(verdict) {
    applyError = ''
    applyOk = ''
    const m = metricsFor(verdict.variant)
    confirmApply = {
      variant: verdict.variant,
      model: modelOf(verdict),
      provider: m?.overlay?.llm_provider || '',
    }
  }

  async function doApply() {
    applying = true
    applyError = ''
    try {
      const body = { llm_model: confirmApply.model }
      if (confirmApply.provider) body.llm_provider = confirmApply.provider
      await api.updateAgentConfig(run.base_agent, body)
      applyOk = `${run.base_agent} now runs ${confirmApply.model}`
      appliedVariant = confirmApply.variant
      confirmApply = null
      // The page owns the agent list, so it re-reads and "current" updates.
      onapplied()
    } catch (e) {
      // Kept inside the dialog: closing it on failure would read as success.
      applyError = e.message
    } finally {
      applying = false
    }
  }

  function escalate(verdict) {
    const m = metricsFor(verdict.variant)
    onrunfull({
      model: modelOf(verdict),
      provider: m?.overlay?.llm_provider || '',
      taskSet: summary?.task_set || '',
    })
  }

  function focusOnMount(node) {
    node.focus()
  }

  /** Long prompts get one readable line in the table. */
  function shortPrompt(p) {
    const text = (p || '').replace(/\s+/g, ' ').trim()
    return text.length > 90 ? `${text.slice(0, 90)}…` : text
  }
</script>

{#if loading}
  <p class="muted" data-testid="results-loading">Loading results…</p>
{:else if error}
  <ErrorBanner message={error} />
  <button class="btn-sm" onclick={load} data-testid="results-retry">Try again</button>
{:else if !summary || variants.length === 0}
  <p class="muted" data-testid="results-none">
    This run produced no comparable turns, so there is nothing to score.
  </p>
{:else}

  <!-- Layer 1: the verdict, with its work -->
  {#each verdicts as v (v.variant_id)}
    <section class="verdict {v.verdict}" data-testid="verdict-{v.variant_id}">
      <header class="verdict-head">
        <span class="verdict-label" data-testid="verdict-label-{v.variant_id}">{verdictLabel(v.verdict)}</span>
        <span class="verdict-sub">
          <span class="mono">{displayName(v.variant)}</span> against your current model
          {#if agent?.model}<span class="mono">({agent.model})</span>{/if}
        </span>
      </header>
      <p class="reason" data-testid="verdict-reason-{v.variant_id}">{v.reason}</p>

      {#if v.divergence}
        <p class="divergence" data-testid="divergence-{v.variant_id}">{v.divergence}</p>
      {/if}

      {#if isPending(v)}
        <div class="pending" data-testid="judgment-pending-{v.variant_id}">
          <p>
            {v.judgment.judged_pairs} of {v.judgment.pairs} comparisons are judged. The rest are
            waiting — until they are done, this verdict rests on the objective checks alone.
          </p>
          <p class="pending-step">Judge them from Claude Code with <code>/judge-eval</code>, or run:</p>
          <div class="cmd-row">
            <code class="cmd" data-testid="judge-command">{judgeCommand}</code>
            <button class="btn-sm" onclick={copyCommand} data-testid="copy-command">
              {#if copyState === 'ok'}Copied{:else if copyState === 'failed'}Select it above{:else}Copy{/if}
            </button>
          </div>
          <p class="hint">
            Use an API key scoped to <span class="mono">eval:read</span> and
            <span class="mono">eval:write</span> only — the judge needs nothing else.
          </p>
        </div>
      {/if}

      <h3 class="block-title">Objective checks</h3>
      <div class="table-wrapper">
        <table class="table" data-testid="gates-{v.variant_id}">
          <caption class="sr-only">Objective checks for {displayName(v.variant)}</caption>
          <thead>
            <tr>
              <th>Check</th>
              <th>Current</th>
              <th>Candidate</th>
              <th>Change</th>
              <th>Allowed</th>
              <th>Result</th>
            </tr>
          </thead>
          <tbody>
            {#each v.gates || [] as g (g.name)}
              <tr class:failed={!g.pass}>
                <td>{GATE_LABEL[g.name] || g.name}</td>
                <td>{fmtGateValue(g, g.baseline)}</td>
                <td>{fmtGateValue(g, g.value)}</td>
                <td>{fmtDelta(g.delta, g.unit)}</td>
                <td>{fmtDelta(g.threshold, g.unit)}</td>
                <td>
                  <span class="gate-mark" class:pass={g.pass} data-testid="gate-{g.name}-{v.variant_id}">
                    {g.pass ? 'pass' : 'fail'}
                  </span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <h3 class="block-title">Judgment</h3>
      {#if (v.judgment?.judged_pairs || 0) === 0}
        <p class="muted" data-testid="no-judgment-{v.variant_id}">
          Nothing judged yet, so nothing here can promote the candidate — the objective checks
          stand alone.
        </p>
      {:else}
        <p class="tally" data-testid="tally-{v.variant_id}">
          Candidate won {v.judgment.wins}, lost {v.judgment.losses}, tied {v.judgment.ties}
          of {v.judgment.judged_pairs} judged comparison{v.judgment.judged_pairs === 1 ? '' : 's'}
          ({v.judgment.pairs} in total) · win rate {fmtPct(v.judgment.win_rate)}
          against a {fmtPct(v.judgment.win_threshold)} bar
        </p>
        {#if v.judgment.operator_agreement}
          <p class="hint" data-testid="agreement-{v.variant_id}">
            You agreed with the judge on {v.judgment.operator_agreement.agreed} of
            {v.judgment.operator_agreement.items} spot checks
            ({fmtPct(v.judgment.operator_agreement.rate)}).
          </p>
        {/if}
        {#if v.judgment.rubric_versions?.length}
          <p class="hint" data-testid="rubric-{v.variant_id}">
            Rubric {v.judgment.rubric_versions.join(', ')}
            {#if v.judgment.rubric_versions.length > 1}
              — this tally mixes two rubric revisions.
            {/if}
          </p>
        {/if}
      {/if}

      {#if (v.categories || []).length > 0}
        <h3 class="block-title">By kind of test case</h3>
        <div class="table-wrapper">
          <table class="table" data-testid="categories-{v.variant_id}">
            <caption class="sr-only">Per-category results for {displayName(v.variant)}</caption>
            <thead>
              <tr>
                <th>Kind</th>
                <th>Judged</th>
                <th>Won / lost / tied</th>
                <th>Win rate</th>
                <th>Rejected</th>
                <th>Rounds</th>
                <th>Cost</th>
              </tr>
            </thead>
            <tbody>
              {#each v.categories as c (c.category)}
                <tr class:failed={c.regressed}>
                  <td>
                    {categoryLabel(c.category)}
                    {#if c.regressed}<span class="flag" data-testid="regressed-{c.category}">regressed</span>{/if}
                  </td>
                  <td>{c.judged_pairs}</td>
                  <td>{c.wins} / {c.losses} / {c.ties}</td>
                  <td>{c.judged_pairs > 0 ? fmtPct(c.win_rate) : '—'}</td>
                  <td>{fmtDelta(c.delta_rejected_pp, 'pp')}</td>
                  <td>{fmtDelta(c.delta_rounds_pct, '%')}</td>
                  <td>{fmtDelta(c.delta_cost_pct, '%')}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      <!-- Inline, not an overlay: this is a reversible config write, and the
           gate table above it is the evidence for the decision. The house rule
           is overlay for irreversible actions only (Stop run), inline here. -->
      {#if confirmApply?.variant === v.variant}
        <div class="apply-confirm" data-testid="apply-confirm">
          <span>
            Switch <strong>{run.base_agent}</strong> from
            <span class="mono">{agent?.model || 'its current model'}</span> to
            <span class="mono">{confirmApply.model}</span>{#if confirmApply.provider}
              on <span class="mono">{confirmApply.provider}</span>{/if}?
          </span>
          <p class="hint">Every new conversation on this agent uses it from then on.</p>
          {#if applyError}
            <div class="inline-error" role="alert" data-testid="apply-error">{applyError}</div>
          {/if}
          <div class="confirm-actions">
            <button class="btn-primary" onclick={doApply} disabled={applying}
              data-testid="apply-confirm-btn" use:focusOnMount>
              {applying ? 'Applying…' : 'Switch model'}
            </button>
            <button class="btn-ghost" onclick={() => confirmApply = null} disabled={applying}>Cancel</button>
          </div>
        </div>
      {/if}

      <div class="verdict-actions">
        {#if v.verdict === 'upgrade'}
          <button class="btn-primary" onclick={() => askApply(v)}
            disabled={appliedVariant === v.variant || !modelOf(v)} data-testid="apply-{v.variant_id}">
            {appliedVariant === v.variant ? 'Applied' : `Apply to ${run.base_agent}`}
          </button>
          {#if !modelOf(v)}
            <span class="hint" data-testid="apply-blocker-{v.variant_id}">
              This run did not record a model to switch to.
            </span>
          {/if}
        {/if}
        {#if canEscalate(v)}
          <button class="btn-ghost" onclick={() => escalate(v)} data-testid="escalate-{v.variant_id}">
            Set up full eval
          </button>
        {/if}
        {#if applyOk && appliedVariant === v.variant}
          <!-- Beside the button, not at the top of the view: the banner would
               land a screen above the click that earned it. -->
          <span class="save-ok" role="status" data-testid="apply-ok">{applyOk}</span>
        {/if}
      </div>
    </section>
  {/each}

  {#if verdicts.length === 0}
    <p class="muted" data-testid="single-variant">
      Only one model ran here, so there is nothing to compare it against.
    </p>
  {/if}

  <!-- Layer 2: the objective scorecard -->
  <section class="scorecard">
    <h3 class="block-title">Scorecard</h3>
    <div class="table-wrapper">
      <table class="table" data-testid="objective-table">
        <thead>
          <tr>
            <th>Measure</th>
            {#each variants as v (v.variant_id)}
              <th class="mono">
                {v.name}
                <span class="flag">{v.name === baselineName ? 'current' : 'candidate'}</span>
              </th>
            {/each}
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>Rejected tool calls</td>
            {#each variants as v (v.variant_id)}<td>{fmtPct(v.rejected_rate)}</td>{/each}
          </tr>
          <tr>
            <td>Failed tool calls</td>
            {#each variants as v (v.variant_id)}<td>{fmtPct(v.failed_rate)}</td>{/each}
          </tr>
          <tr>
            <td>Tool calls</td>
            {#each variants as v (v.variant_id)}<td>{v.tool_calls}</td>{/each}
          </tr>
          <tr>
            <td>Rounds per test case</td>
            {#each variants as v (v.variant_id)}<td>{fmtNum(v.mean_rounds)}</td>{/each}
          </tr>
          <tr>
            <td>Cut short</td>
            {#each variants as v (v.variant_id)}<td>{v.wrapup_count}</td>{/each}
          </tr>
          <tr>
            <td>Cost per test case</td>
            {#each variants as v (v.variant_id)}<td>{fmtUSD(v.mean_cost_per_task)}</td>{/each}
          </tr>
          <tr>
            <td>Time per turn</td>
            {#each variants as v (v.variant_id)}<td>{fmtMs(v.mean_latency_ms)}</td>{/each}
          </tr>
          <tr>
            <td>Turns finished / failed</td>
            {#each variants as v (v.variant_id)}<td>{v.samples_ok} / {v.samples_failed}</td>{/each}
          </tr>
        </tbody>
      </table>
    </div>
    {#if completeness}
      <p class="completeness" data-testid="completeness">
        {completeness.samples_ok} of {completeness.samples_expected} turns finished ·
        {completeness.pairs_judged} of {completeness.pairs} comparisons judged ·
        {completeness.conclusive ? 'conclusive' : 'inconclusive'}
        against a {fmtPct(completeness.floor)} floor
      </p>
    {/if}
  </section>

  <!-- Layer 3: per-test-case diffs -->
  {#if (summary.per_task || []).length > 0}
    <section class="per-task">
      <h3 class="block-title">Test case by test case</h3>
      <div class="table-wrapper">
        <table class="table" data-testid="per-task-table">
          <thead>
            <tr>
              <th></th>
              <th>Test case</th>
              <th>Kind</th>
              {#each variants as v (v.variant_id)}
                <th class="mono">
                  {v.name}
                  <span class="flag">{v.name === baselineName ? 'current' : 'candidate'}</span>
                </th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each summary.per_task as t (t.task_id)}
              <tr class="row-clickable" class:row-expanded={expandedTask === t.task_id}
                role="button" tabindex="0"
                onclick={() => toggleTask(t.task_id)}
                onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleTask(t.task_id) } }}
                aria-expanded={expandedTask === t.task_id}
                aria-controls="task-detail-{t.task_id}"
                data-testid="task-row-{t.task_id}">
                <td>
                  <span class="chevron-toggle" class:open={expandedTask === t.task_id}>
                    <svg width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true"><path d="M3.5 2L7 5L3.5 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
                  </span>
                </td>
                <td class="prompt-cell">{shortPrompt(t.prompt)}</td>
                <td>{categoryLabel(t.category)}</td>
                {#each variants as v (v.variant_id)}
                  {@const cell = (t.variants || []).find(c => c.variant_id === v.variant_id)}
                  <td>
                    {#if cell}
                      {fmtUSD(cell.mean_cost)}
                      {#if v.name !== baselineName && cell.delta_cost}
                        <span class="delta" class:worse={cell.delta_cost > 0}>
                          {fmtCostDelta(cell.delta_cost)}
                        </span>
                      {/if}
                      <span class="cell-line">
                        {fmtNum(cell.mean_rounds, 1)} rounds
                        {#if v.name !== baselineName && cell.delta_rounds}
                          <span class="delta" class:worse={cell.delta_rounds > 0}>
                            {fmtSignedNum(cell.delta_rounds, 1)}
                          </span>
                        {/if}
                      </span>
                      <span class="cell-line">
                        {fmtMs(cell.mean_latency_ms)}
                        {#if v.name !== baselineName && cell.delta_latency_ms}
                          <span class="delta" class:worse={cell.delta_latency_ms > 0}>
                            {cell.delta_latency_ms > 0 ? '+' : '-'}{fmtMs(Math.abs(cell.delta_latency_ms))}
                          </span>
                        {/if}
                      </span>
                    {:else}
                      —
                    {/if}
                  </td>
                {/each}
              </tr>
              {#if expandedTask === t.task_id}
                <tr class="detail-row">
                  <td colspan={3 + variants.length} id="task-detail-{t.task_id}" aria-live="polite">
                    {#if samplesLoading}
                      <p class="muted" role="status" data-testid="turns-loading">Loading turns…</p>
                    {:else if samplesError}
                      <ErrorBanner message={samplesError} />
                      <button class="btn-sm" onclick={() => loadSamples()} data-testid="turns-retry">Try again</button>
                    {:else}
                      {#each turnsFor(t.task_id) as turn (turn.k)}
                        {@const pair = pairFor(t.task_id, turn.k)}
                        <div class="turn" data-testid="turn-{t.task_id}-{turn.k}">
                          <div class="turn-head">
                            {#if (summary.k || 1) > 1}
                              <span class="turn-label">Run {turn.k + 1}</span>
                            {/if}
                            {#if pair}
                              <span class="outcome outcome-{pair.outcome}" data-testid="outcome-{t.task_id}-{turn.k}">
                                {OUTCOME_LABEL[pair.outcome] || pair.outcome}
                              </span>
                            {/if}
                          </div>
                          <div class="columns">
                            {#each variants as v (v.variant_id)}
                              {@const smp = turn.byVariant.get(v.variant_id)}
                              <div class="column-wrap">
                                {#if smp && smp.status !== 'failed'}
                                  <DryRunTranscript
                                    transcript={evalSampleTranscript(smp, modelFor(v))}
                                    label={v.name === baselineName ? 'current' : 'candidate'}
                                    accent={v.name !== baselineName}
                                    compact />
                                {:else if smp}
                                  <p class="turn-failed" data-testid="turn-failed-{smp.id}">
                                    This turn failed: {smp.error || 'no detail recorded'}
                                  </p>
                                {:else}
                                  <p class="muted">No turn recorded.</p>
                                {/if}
                              </div>
                            {/each}
                          </div>

                          {#if pair && judgeVerdicts(pair).length > 0}
                            <div class="judgment" data-testid="pair-judgment-{pair.pair_id}">
                              {#each judgeVerdicts(pair) as jv, i (i)}
                                <div class="judge-call">
                                  <div class="judge-head">
                                    <span class="judge-ident">{jv.judge_ident}</span>
                                    <span class="judge-winner">
                                      {jv.winner_variant ? `picked ${jv.winner_variant}` : 'called it a tie'}
                                    </span>
                                    {#if jv.rubric_version}
                                      <span class="hint">rubric {jv.rubric_version}</span>
                                    {/if}
                                  </div>
                                  {#if jv.dimensions}
                                    <ul class="dimensions">
                                      {#each Object.entries(jv.dimensions) as [dim, who] (dim)}
                                        <li><span class="dim">{dim}</span>: {dimensionWinner(who, jv.key)}</li>
                                      {/each}
                                    </ul>
                                  {/if}
                                  {#if jv.notes}
                                    <p class="notes">{jv.notes}</p>
                                  {/if}
                                </div>
                              {/each}
                            </div>
                          {:else if pair}
                            <p class="hint" data-testid="pair-unjudged-{pair.pair_id}">
                              No judgment recorded for this comparison yet.
                            </p>
                          {/if}
                        </div>
                      {:else}
                        <p class="muted" data-testid="no-turns-{t.task_id}">
                          No turns were recorded for this test case.
                        </p>
                      {/each}
                    {/if}
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
{/if}

<style>
  .apply-confirm {
    margin: 10px 0;
    padding: 10px;
    background: color-mix(in srgb, var(--accent) 5%, transparent);
    border: 1px solid color-mix(in srgb, var(--accent) 20%, transparent);
    border-radius: var(--radius);
    font-size: 13px;
  }
  .apply-confirm .hint { margin-top: 2px; }
  .confirm-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 8px;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  .block-title {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin: 18px 0 8px;
  }

  /* Verdict banner */
  .verdict {
    border: 1px solid var(--border);
    border-left: 3px solid var(--text-muted);
    border-radius: var(--radius);
    padding: 14px 16px;
    margin-bottom: 16px;
    background: var(--bg);
  }
  .verdict.upgrade { border-left-color: var(--success); }
  .verdict.downgrade { border-left-color: var(--danger); }
  .verdict.inconclusive { border-left-color: var(--warn); }

  .verdict-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 10px;
  }
  .verdict-label { font-size: 15px; font-weight: 600; color: var(--text); }
  .verdict.upgrade .verdict-label { color: var(--success); }
  .verdict.downgrade .verdict-label { color: var(--danger); }
  .verdict-sub { font-size: 12px; color: var(--text-muted); overflow-wrap: anywhere; min-width: 0; }
  .reason { font-size: 13px; color: var(--text); margin: 8px 0 0; line-height: 1.5; }
  .divergence {
    font-size: 12px;
    color: var(--warn);
    margin: 6px 0 0;
  }

  /* Judgment-pending block */
  .pending {
    margin-top: 12px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .pending p { font-size: 12px; color: var(--text); margin: 0 0 8px; line-height: 1.5; }
  .pending-step { color: var(--text-muted); }
  .cmd-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 8px; }
  .cmd {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 5px 8px;
    overflow-wrap: anywhere;
    min-width: 0;
  }

  .tally { font-size: 12px; color: var(--text); margin: 0 0 6px; line-height: 1.5; }

  .table-wrapper { overflow-x: auto; }
  .gate-mark {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.3px;
    color: var(--danger);
  }
  .gate-mark.pass { color: var(--success); }
  tr.failed td { background: rgba(224, 92, 110, 0.07); }
  .flag {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.3px;
    color: var(--text-muted);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 1px 4px;
    margin-left: 6px;
  }

  .verdict-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }

  .completeness { font-size: 12px; color: var(--text-muted); margin: 8px 0 0; }

  /* Per-task diffs */
  .prompt-cell { max-width: 380px; overflow-wrap: anywhere; }
  /* Rounds and latency sit under the cost, so a cell reads as three lines
     rather than one run-on string. */
  .cell-line { display: block; font-size: 11px; color: var(--text-muted); }
  .delta { color: var(--success); margin-left: 6px; font-size: 11px; }
  .delta.worse { color: var(--warn); }
  .detail-row td { background: var(--surface); }
  tr.row-clickable:focus-visible { outline: 2px solid var(--accent); outline-offset: -1px; }

  .turn { padding: 8px 0 14px; }
  .turn + .turn { border-top: 1px solid var(--border); }
  .turn-head { display: flex; align-items: center; gap: 10px; margin-bottom: 8px; }
  .turn-label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.3px;
    color: var(--text-muted);
  }
  .outcome {
    font-size: 11px;
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 1px 8px;
    color: var(--text-muted);
  }
  .outcome-win { border-color: var(--success); color: var(--success); }
  .outcome-loss { border-color: var(--danger); color: var(--danger); }

  .columns {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 16px;
  }
  .column-wrap { min-width: 0; }
  .turn-failed { font-size: 12px; color: var(--danger); overflow-wrap: anywhere; }

  .judgment { margin-top: 12px; display: flex; flex-direction: column; gap: 8px; }
  .judge-call {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 8px 10px;
    background: var(--bg);
  }
  .judge-head { display: flex; flex-wrap: wrap; gap: 8px; align-items: baseline; font-size: 12px; }
  .judge-ident { font-weight: 600; }
  .judge-winner { color: var(--text-muted); }
  .dimensions { list-style: none; margin: 6px 0 0; padding: 0; font-size: 12px; color: var(--text-muted); }
  .dim { color: var(--text); }
  .notes { font-size: 12px; margin: 6px 0 0; line-height: 1.5; }

  .inline-error { color: var(--danger); font-size: 12px; margin-bottom: 10px; }

  @media (max-width: 520px) {
    .columns { grid-template-columns: 1fr; }
    .prompt-cell { max-width: 180px; }
  }
</style>
