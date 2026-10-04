<script>
  import { api } from '../api.js'
  import { relativeTime } from '../relativeTime.js'
  import {
    DECIDER_APPROVE_DEFAULT, DECIDER_DENY_DEFAULT,
    summarize, histogram, disagreements, suggestion,
  } from '../deciderCalibration.js'

  // approveAt/denyAt are the Permission panel's threshold inputs: '' means the
  // default. Dragging a handle or pressing "Use" writes them, nothing saves.
  let {
    agent, decider, supervisor = '', mode = 'shadow', saved = true,
    approveAt = $bindable(''), denyAt = $bindable(''),
  } = $props()

  const CHECK_LABELS = { aligned: 'aligned', safe_args: 'safe', scoped: 'scoped' }
  const RANGES = [{ days: 7, label: 'Last 7 days' }, { days: 30, label: 'Last 30 days' }]
  const SHOWN = 5
  const STEP = 0.01
  const CHART_H = 180
  const AXIS_H = 22

  let days = $state(30)
  let rows = $state([])
  let failed = $state(0)
  let truncated = $state(false)
  let loading = $state(true)
  let error = $state('')
  let hidden = $state(false)
  let chartW = $state(0)
  // Before layout (and in tests) clientWidth is 0; draw at a nominal width.
  let W = $derived(chartW || 600)
  let dragging = $state(null)
  let svgEl = $state(null)

  let seq = 0
  async function load() {
    const mine = ++seq
    loading = true
    error = ''
    try {
      const since = new Date(Date.now() - days * 86400000).toISOString()
      const res = await api.deciderReviews(agent, { decider, since })
      if (mine !== seq) return
      rows = res.reviews || []
      failed = res.failed || 0
      truncated = !!res.truncated
    } catch (e) {
      if (mine !== seq) return
      // Without audit:read the panel has nothing to show; the form still works.
      if (e.status === 403) hidden = true
      else error = e.message
    } finally {
      if (mine === seq) loading = false
    }
  }

  $effect(() => {
    // Re-fetch when the agent, decider or window changes.
    void agent, void decider, void days
    load()
  })

  let A = $derived(Number(approveAt) || DECIDER_APPROVE_DEFAULT)
  let D = $derived(Number(denyAt) || DECIDER_DENY_DEFAULT)
  let stats = $derived(summarize(rows, A, D))
  let bins = $derived(histogram(rows))
  let disagreeing = $derived(disagreements(rows, A, D))
  let suggest = $derived(suggestion(rows, A, D))
  let pct = $derived(stats.total ? Math.round((stats.settled / stats.total) * 100) : 0)
  let models = $derived([...new Set(rows.map(r => r.model).filter(Boolean))])

  // The supervisor whose verdicts the rows carry, for the copy.
  let supName = $derived(supervisor || rows.find(r => r.supervisor_name)?.supervisor_name || 'the supervisor')
  let hasVerdicts = $derived(rows.some(r => r.supervisor))

  function usd(x) {
    if (!x) return '$0'
    return x < 0.01 ? `$${x.toFixed(4)}` : `$${x.toFixed(2)}`
  }
  // Rounded down so a shown score never passes a threshold the real one misses.
  const floor2 = v => (Math.floor(v * 100 + 1e-9) / 100).toFixed(2)
  const pctOf = n => (stats.total ? Math.round((n / stats.total) * 100) : 0)
  const plural = (n, one, many = one + 's') => (n === 1 ? one : many)
  const these = n => (n === 1 ? 'the' : n === 2 ? 'both' : `all ${n}`)

  // Chart geometry. Bars and handles share one x scale over [0, 1].
  let maxBin = $derived(Math.max(1, ...bins.map(b => b.total)))
  const x = v => v * W
  function segH(count) {
    if (!count) return 0
    return Math.max(2, (count / maxBin) * (CHART_H - 24))
  }
  function stack(bin) {
    let y = CHART_H
    return ['APPROVE', 'ESCALATE', 'DENY', 'none'].map(key => {
      const h = segH(bin[key])
      y -= h
      return { key, y, h }
    }).filter(s => s.h > 0)
  }

  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v))
  const round2 = v => Math.round(v * 100) / 100
  // Bounds keep deny below approve. Typed thresholds can already be out of
  // order (the form shows the error); the handles then stay put.
  const bounds = which => (which === 'deny' ? [STEP, round2(A - STEP)] : [round2(D + STEP), 1 - STEP])
  function setThreshold(which, v) {
    const [lo, hi] = bounds(which)
    if (lo > hi) return
    if (which === 'deny') denyAt = round2(clamp(v, lo, hi))
    else approveAt = round2(clamp(v, lo, hi))
  }
  function onPointerDown(which, e) {
    dragging = which
    e.currentTarget.setPointerCapture?.(e.pointerId)
  }
  function onPointerMove(e) {
    if (!dragging || !svgEl) return
    const rect = svgEl.getBoundingClientRect()
    if (rect.width) setThreshold(dragging, (e.clientX - rect.left) / rect.width)
  }
  function onKey(which, e) {
    const step = e.shiftKey ? 0.05 : STEP
    const cur = which === 'deny' ? D : A
    const [lo, hi] = bounds(which)
    const targets = {
      ArrowLeft: cur - step, ArrowDown: cur - step, ArrowRight: cur + step, ArrowUp: cur + step,
      PageDown: cur - 0.1, PageUp: cur + 0.1, Home: lo, End: hi,
    }
    if (!(e.key in targets)) return
    e.preventDefault()
    setThreshold(which, targets[e.key])
  }

  function useSuggestion() {
    if (suggest?.kind === 'approve') approveAt = suggest.value
    else if (suggest?.kind === 'deny') denyAt = suggest.value
  }

  function verdictLine(d) {
    const dec = d.decider_verdict === 'APPROVE' ? 'approved' : 'denied'
    const sup = { APPROVE: 'approved', DENY: 'denied', ESCALATE: 'escalated' }[d.supervisor]
    return [`${decider} ${dec}`, `${supName} ${sup}`]
  }
</script>

{#if !hidden}
<section class="calibration" id="decider-calibration" aria-labelledby="calibration-title">
  <div class="cal-header">
    <div>
      <h3 id="calibration-title" class="cal-title">What {decider} would have done</h3>
      {#if !loading && !error && stats.total}
        <p class="cal-sub">
          {stats.total} shadow {plural(stats.total, 'review')} of {agent}'s tool calls{hasVerdicts ? `, compared with ${supName}'s verdicts` : '. No supervisor verdicts to compare with yet.'}
        </p>
      {/if}
    </div>
    <label class="sr-only" for="cal-range">Time range</label>
    <select id="cal-range" class="cal-range" bind:value={days}>
      {#each RANGES as r}<option value={r.days}>{r.label}</option>{/each}
    </select>
  </div>

  {#if loading && !rows.length}
    <div class="cal-state" role="status"><span class="spinner" aria-hidden="true"></span>Loading shadow reviews…</div>
  {:else if error}
    <div class="inline-error cal-state" role="alert">Could not load shadow reviews: {error}</div>
  {:else if !stats.total}
    <div class="cal-empty" data-testid="calibration-empty">
      <strong>No shadow reviews in the last {days} days</strong>
      <span class="hint">
        {saved
          ? `Each supervised tool call adds one while ${decider} runs in shadow mode.`
          : `Save ${decider} in shadow mode and each supervised tool call adds a review here.`}
      </span>
    </div>
  {:else}
    <div class="cal-stats" data-testid="calibration-stats">
      <div class="cal-stat cal-stat-lead">
        <div class="cal-num">{pct}%</div>
        <div class="cal-cap">settled without {supName}<br />{stats.settled} of {stats.total} calls</div>
      </div>
      <div class="cal-stat">
        <div class="cal-num" class:danger={stats.unsafeApprovals > 0} data-testid="calibration-unsafe">{stats.unsafeApprovals}</div>
        <div class="cal-cap">approved by {decider},<br />not by {supName}</div>
      </div>
      <div class="cal-stat">
        <div class="cal-num">{stats.wrongDenials}</div>
        <div class="cal-cap">denied by {decider},<br />approved by {supName}</div>
      </div>
      <div class="cal-stat">
        {#if stats.costKnown}
          <div class="cal-num">{usd(stats.avoidedUSD)}</div>
          <div class="cal-cap">
            {supName} spend avoided{stats.costKnown < stats.settled ? ` (${stats.costKnown} of ${stats.settled} priced)` : ''}<br />{decider} cost {usd(stats.deciderUSD)}
          </div>
        {:else}
          <div class="cal-num muted">—</div>
          <div class="cal-cap">{supName} cost not recorded yet<br />{decider} cost {usd(stats.deciderUSD)}</div>
        {/if}
      </div>
    </div>

    <div class="cal-chart-head">
      <span class="cal-kicker">Lowest check score per call</span>
      <span class="cal-legend" aria-hidden="true">
        <span><i class="sw approve"></i>{supName} approved</span>
        <span><i class="sw escalate"></i>{supName} escalated</span>
        <span><i class="sw deny"></i>{supName} denied</span>
        {#if bins.some(b => b.none)}<span><i class="sw none"></i>no verdict</span>{/if}
      </span>
    </div>

    <div class="cal-chart" bind:clientWidth={chartW}>
      <svg bind:this={svgEl} width={W} height={CHART_H + AXIS_H}
        onpointermove={onPointerMove} onpointerup={() => (dragging = null)} onpointercancel={() => (dragging = null)}
        role="group" aria-label="Lowest check score per call, split by {supName}'s verdict. Use the two sliders to move the thresholds.">
        <rect class="zone deny" x="0" y="0" width={x(D)} height={CHART_H} />
        <rect class="zone approve" x={x(A)} y="0" width={Math.max(0, W - x(A))} height={CHART_H} />
        {#each bins as bin, i}
          {@const bx = x(i / bins.length) + 1.5}
          {@const bw = Math.max(1, W / bins.length - 3)}
          {#each stack(bin) as s}
            <rect class="bar {s.key.toLowerCase()}" x={bx} y={s.y} width={bw} height={s.h} />
          {/each}
          {#if bin.total && bin.total >= maxBin * 0.1}
            <text class="bar-count" x={bx + bw / 2} y={CHART_H - stack(bin).reduce((h, s) => h + s.h, 0) - 4} text-anchor="middle">{bin.total}</text>
          {/if}
        {/each}
        <line class="baseline" x1="0" x2={W} y1={CHART_H} y2={CHART_H} />
        {#each [0, 0.25, 0.5, 0.75, 1] as t}
          <text class="tick" x={x(t)} y={CHART_H + 16} text-anchor={t === 0 ? 'start' : t === 1 ? 'end' : 'middle'}>{t}</text>
        {/each}
        {#each [['deny', D], ['approve', A]] as [which, v]}
          <g class="handle {which}" class:dragging={dragging === which} transform="translate({x(v)},0)"
            role="slider" tabindex="0"
            aria-label={which === 'deny' ? 'Deny threshold' : 'Approve threshold'}
            aria-valuemin={bounds(which)[0]} aria-valuemax={Math.max(...bounds(which))}
            aria-valuenow={v} aria-valuetext={v.toFixed(2)}
            onpointerdown={e => onPointerDown(which, e)} onlostpointercapture={() => (dragging = null)}
            onkeydown={e => onKey(which, e)}>
            <rect class="hit" x="-10" y="0" width="20" height={CHART_H} />
            <line y1="8" y2={CHART_H} />
            <circle cy="8" r="7" />
          </g>
        {/each}
      </svg>
    </div>
    <div class="cal-zones">
      <span class="z-deny">Deny</span>
      <span class="muted">Escalate to {supName} · {stats.escalated} {plural(stats.escalated, 'call')}</span>
      <span class="z-approve">Approve</span>
    </div>

    <div class="cal-divider"></div>

    <div class="cal-dis-head">
      <span class="cal-kicker">Disagreements at these thresholds</span>
      <a class="cal-link" href="#/audit?agent={encodeURIComponent(agent)}&category=supervisor&range={days}d">Open in audit log →</a>
    </div>
    {#if disagreeing.length}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scroll region needs a tab stop to be reachable at all) -->
      <div class="table-wrap" tabindex="0" role="region" aria-label="Disagreements at these thresholds">
        <table class="cal-table" data-testid="calibration-disagreements">
          <caption class="sr-only">Calls where {decider} and {supName} disagree</caption>
          <thead class="sr-only">
            <tr><th>Verdicts</th><th>Tool</th><th>Arguments</th><th>Lowest check</th><th>When</th></tr>
          </thead>
          <tbody>
            {#each disagreeing.slice(0, SHOWN) as d (d.audit_id)}
              {@const [decLine, supLine] = verdictLine(d)}
              <tr>
                <td class="verdicts" class:unsafe={d.decider_verdict === 'APPROVE'}>{decLine}<br />{supLine}</td>
                <td class="mono">{d.tool}</td>
                <td class="mono args" title={d.arguments}>{d.arguments}</td>
                <td class="mono score">{CHECK_LABELS[d.lowest] || d.lowest} {floor2(d.min_score)}</td>
                <td class="muted when">{relativeTime(d.time)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if disagreeing.length > SHOWN}
        <p class="hint">And {disagreeing.length - SHOWN} more in the audit log.</p>
      {/if}
    {:else}
      <p class="hint" data-testid="calibration-agree">
        {hasVerdicts ? `${decider} and ${supName} agree on every call ${decider} would settle.` : 'Nothing to compare until the supervisor reviews a call.'}
      </p>
    {/if}

    {#if suggest}
      <div class="cal-suggest" data-testid="calibration-suggestion">
        <div class="cal-suggest-text">
          {#if suggest.value !== null}
            <strong>
              {#if suggest.kind === 'approve'}
                Approve at {suggest.value.toFixed(2)} would remove {suggest.removed === suggest.count ? these(suggest.count) : `${suggest.removed} of ${suggest.count}`} unsafe {plural(suggest.count, 'approval')}
              {:else}
                Deny at {suggest.value.toFixed(2)} would stop denying {suggest.removed === suggest.count ? these(suggest.count) : `${suggest.removed} of ${suggest.count}`} {plural(suggest.count, 'call')} {supName} approved
              {/if}
            </strong>
            <span>
              {decider} would settle {pctOf(suggest.settledAfter)}% of calls instead of {pct}%.{suggest.removed < suggest.count ? ` The other ${suggest.count - suggest.removed} scored ${suggest.kind === 'approve' ? '0.99 or higher' : '0.01 or lower'}, so check them above.` : ''}{mode === 'shadow' ? ' This is a prediction from past reviews, so keep shadow mode on for a few days before you switch to enforce.' : ''}
            </span>
          {:else}
            <strong>No {suggest.kind} threshold removes {suggest.count === 1 ? 'this disagreement' : 'these disagreements'}</strong>
            <span>{decider} scored {suggest.count === 1 ? 'it' : 'them'} {suggest.kind === 'approve' ? '0.99 or higher' : '0.01 or lower'}. Keep {decider} in shadow mode and check the calls above.</span>
          {/if}
        </div>
        {#if suggest.value !== null}
          <button type="button" class="btn-ghost cal-use" onclick={useSuggestion}>Use {suggest.value.toFixed(2)}</button>
        {/if}
      </div>
    {/if}

    {#if failed || truncated || models.length > 1}
      <p class="hint cal-notes">
        {#if failed}{failed} {plural(failed, 'review')} failed and {failed === 1 ? 'is' : 'are'} not shown. {/if}
        {#if truncated}Only the newest reviews are shown. {/if}
        {#if models.length > 1}Includes reviews scored by {models.join(' and ')}; thresholds calibrated on one may not carry over.{/if}
      </p>
    {/if}
  {/if}
</section>
{/if}

<style>
  .calibration {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    padding: 20px 24px;
    display: flex; flex-direction: column; gap: 16px;
    min-width: 0;
  }
  .cal-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
  .cal-title { font-size: 16px; font-weight: 600; margin: 0; }
  .cal-sub { font-size: 13px; color: var(--text-muted); margin: 4px 0 0; }
  .cal-range {
    background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
    color: var(--text); padding: 5px 8px; font-size: 13px; flex-shrink: 0; cursor: pointer;
  }
  .cal-state { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text-muted); }
  .spinner {
    width: 14px; height: 14px; border-radius: 50%;
    border: 2px solid var(--border); border-top-color: var(--accent);
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .spinner { animation-duration: 2s; } }
  .cal-empty { display: flex; flex-direction: column; gap: 4px; font-size: 13px; padding: 8px 0; }

  .cal-stats { display: flex; flex-wrap: wrap; row-gap: 16px; }
  .cal-stat { flex: 1 1 140px; display: flex; flex-direction: column; gap: 4px; padding: 0 20px; border-left: 1px solid var(--border); }
  .cal-stat-lead { flex-grow: 1.2; padding-left: 0; border-left: none; }
  .cal-num { font-size: 36px; font-weight: 700; letter-spacing: -0.03em; line-height: 40px; }
  .cal-num.danger { color: var(--danger); }
  .cal-cap { font-size: 12px; line-height: 17px; color: var(--text-muted); }

  .cal-kicker { font-size: 12px; font-weight: 600; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-muted); }
  .cal-chart-head, .cal-dis-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; }
  .cal-legend { display: flex; gap: 14px; flex-wrap: wrap; font-size: 12px; color: var(--text-muted); }
  .cal-legend span { display: inline-flex; align-items: center; gap: 6px; }
  .sw { width: 8px; height: 8px; border-radius: 2px; display: inline-block; }
  .sw.approve, .bar.approve { background: var(--success); fill: var(--success); }
  .sw.escalate, .bar.escalate { background: var(--warn); fill: var(--warn); }
  .sw.deny, .bar.deny { background: var(--danger); fill: var(--danger); }
  .sw.none, .bar.none { background: var(--text-muted); fill: var(--text-muted); }
  .bar { opacity: 0.75; }

  .cal-chart { width: 100%; min-width: 0; touch-action: none; }
  .cal-chart svg { display: block; overflow: visible; user-select: none; }
  .zone.deny { fill: var(--danger); opacity: 0.08; }
  .zone.approve { fill: var(--success); opacity: 0.08; }
  .baseline { stroke: var(--border); }
  .tick, .bar-count { font-size: 11px; fill: var(--text-muted); }
  .handle { cursor: ew-resize; outline: none; }
  .handle .hit { fill: transparent; }
  .handle line { stroke-width: 2; }
  .handle circle { fill: var(--surface); stroke-width: 2; }
  .handle.deny line, .handle.deny circle { stroke: var(--danger); }
  .handle.approve line, .handle.approve circle { stroke: var(--success); }
  .handle:focus-visible circle, .handle.dragging circle { stroke-width: 3; r: 8; }
  .handle:focus-visible .hit { fill: rgba(var(--accent-rgb), 0.12); }

  .cal-zones { display: flex; justify-content: space-between; gap: 8px; font-size: 13px; margin-top: -6px; }
  .z-deny { color: var(--danger); font-weight: 500; }
  .z-approve { color: var(--success); font-weight: 500; }
  .cal-divider { height: 1px; background: var(--border); }
  .cal-link { font-size: 13px; color: var(--accent); text-decoration: none; }
  .cal-link:hover { text-decoration: underline; }

  .cal-table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .cal-table td { padding: 10px 12px 10px 0; border-bottom: 1px solid var(--border); vertical-align: middle; }
  .cal-table tr:last-child td { border-bottom: none; }
  .verdicts { font-weight: 500; color: var(--warn-text, var(--warn)); white-space: nowrap; }
  .verdicts.unsafe { color: var(--danger); }
  .args { color: var(--text-muted); max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .score, .when { white-space: nowrap; text-align: right; }
  .when { padding-right: 0; }

  .cal-suggest {
    display: flex; align-items: center; gap: 16px; flex-wrap: wrap;
    padding: 14px 16px; border-radius: var(--radius);
    background: var(--surface); border: 1px solid var(--border);
  }
  .cal-suggest-text { flex: 1 1 320px; display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
  .cal-suggest-text span { color: var(--text-muted); font-size: 12px; line-height: 17px; }
  /* btn-ghost with the accent outline from the design: it changes the form. */
  .cal-use { border-color: var(--accent); color: var(--accent); font-weight: 600; }
  .cal-notes { margin: 0; }

  @media (max-width: 640px) {
    .calibration { padding: 16px; }
    .cal-stat { padding: 0 12px; flex-basis: 45%; }
    .cal-stat-lead, .cal-stat:nth-child(3) { padding-left: 0; border-left: none; }
    .cal-num { font-size: 28px; line-height: 32px; }
    .args { max-width: 140px; }
  }
</style>
