<script>
  // One transcript column: which model ran, what it did, what it said.
  // Used alone for a single-model preview and twice side by side for a
  // comparison, so it carries no assumptions about its own width.
  let { transcript, label = '', accent = false, compact = false } = $props()

  let expanded = $state(new Set())
  const uid = Math.random().toString(36).slice(2, 8)

  function toggle(i) {
    const next = new Set(expanded)
    if (next.has(i)) next.delete(i); else next.add(i)
    expanded = next
  }

  export function duration(ms) {
    if (ms == null) return ''
    return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms} ms`
  }

  function dotClass(outcome) {
    if (outcome === 'suppressed') return 'dot-suppressed'
    if (outcome === 'failed' || outcome === 'rejected' || outcome === 'denied') return 'dot-error'
    return 'dot-ok'
  }

  // A one-line summary replaces the metric tiles: five tiles for one run is
  // over-furnished, and in compare mode the delta strip does the comparing.
  let summary = $derived([
    `${transcript.rounds} round${transcript.rounds === 1 ? '' : 's'}`,
    transcript.suppressed_count > 0 ? `${transcript.suppressed_count} suppressed` : null,
    `$${(transcript.cost_usd || 0).toFixed(4)}`,
    duration(transcript.duration_ms),
  ].filter(Boolean).join(' · '))

  // A stop reason means the tool loop was cut short, so the response below is
  // a wrap-up rather than an answer the model chose to finish on. Without it a
  // turn that exhausted its round budget reads exactly like one that did.
  const STOP_LABEL = {
    max_rounds: 'hit the round limit',
    repeated_calls: 'repeated the same call',
    stop_requested: 'stopped on request',
  }

  // An unknown slug is still a cut-short turn: naming it beats hiding it.
  let stopLabel = $derived(transcript.stop_reason
    ? (STOP_LABEL[transcript.stop_reason] || transcript.stop_reason)
    : '')

  // The provider-reported serving upstream, set only on eval transcripts and
  // only for providers that route (OpenRouter). A candidate served by
  // different upstreams between turns explains latency or quality variance
  // within one variant, which is otherwise invisible. Suppressed when it just
  // repeats the model line — "openrouter/kimi-k3 · OpenRouter" says nothing.
  let upstream = $derived.by(() => {
    const via = (transcript.upstream || '').trim()
    if (!via) return ''
    const model = (transcript.model || transcript.requested_model || '').toLowerCase()
    const low = via.toLowerCase()
    return model === low || model.startsWith(`${low}/`) ? '' : via
  })
</script>

<div class="column">
  <div class="head">
    <span class="dot-model" class:accent></span>
    <span class="model">{transcript.model || transcript.requested_model || 'unknown'}</span>
    {#if label}
      <span class="label" class:accent>{label}</span>
    {/if}
    {#if upstream}
      <span class="upstream" title="The provider that actually served this turn."
        data-testid="upstream">via {upstream}</span>
    {/if}
    <span class="summary">{summary}</span>
    {#if stopLabel}
      <span class="cut-short" title="The response below is a wrap-up, not a completed answer."
        data-testid="stop-reason">Cut short: {stopLabel}</span>
    {/if}
  </div>

  {#if transcript.tool_calls.length > 0}
    <div class="rows">
      {#each transcript.tool_calls as call, i (i)}
        <button
          class="row"
          class:suppressed={call.suppressed}
          class:failed={call.outcome === 'failed' || call.outcome === 'rejected'}
          onclick={() => toggle(i)}
          aria-expanded={expanded.has(i)}
          aria-controls="{uid}-call-{i}"
        >
          <span class="lane-round">R{call.round}</span>
          <span class="dot {dotClass(call.outcome)}"></span>
          <span class="lane-tool">{call.tool}</span>
          {#if !compact}
            <span class="lane-args">{call.arguments || ''}</span>
          {/if}
          <span class="lane-outcome">
            {#if call.suppressed}
              <span class="badge">SUPPRESSED</span>
            {:else if call.outcome !== 'ok' && call.outcome !== 'cached'}
              <span class="badge badge-error">{call.outcome.toUpperCase()}</span>
            {:else}
              {duration(call.duration_ms)}
            {/if}
          </span>
          <span class="lane-chevron" class:open={expanded.has(i)}>&#x25B6;</span>
        </button>
        {#if expanded.has(i)}
          <div class="row-detail" class:suppressed={call.suppressed} id="{uid}-call-{i}">
            {#if call.arguments && compact}
              <div class="detail-label">Arguments</div>
              <pre class="detail-body">{call.arguments}</pre>
            {/if}
            <div class="detail-label">Result returned to the model</div>
            <pre class="detail-body">{call.result || call.error || '(no result)'}</pre>
            {#if call.truncated}
              <div class="detail-note">Trimmed for display.</div>
            {/if}
          </div>
        {/if}
      {/each}
    </div>
  {/if}

  {#if transcript.response}
    <div class="response" class:accent>{transcript.response}</div>
  {:else}
    <div class="response empty">The agent produced no text response.</div>
  {/if}
</div>

<style>
  .column { display: flex; flex-direction: column; gap: 12px; min-width: 0; }

  .head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .dot-model { width: 8px; height: 8px; border-radius: 4px; background: var(--text-muted); flex-shrink: 0; }
  .dot-model.accent { background: var(--accent); }
  .model {
    font-size: 13px; font-weight: 600; color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .label {
    font-size: 10px; font-weight: 600; letter-spacing: 0.05em;
    color: var(--text-muted); border: 1px solid var(--border);
    border-radius: 4px; padding: 1px 5px;
  }
  .label.accent { color: var(--accent); border-color: var(--accent); }
  .summary { font-size: 11px; color: var(--text-muted); }
  .upstream {
    font-size: 10px; color: var(--text-muted);
    border: 1px solid var(--border); border-radius: 4px; padding: 1px 5px;
  }
  .cut-short {
    font-size: 10px; font-weight: 600; letter-spacing: 0.05em;
    color: var(--warn); border: 1px solid var(--warn);
    border-radius: 4px; padding: 1px 5px;
  }

  .rows {
    display: flex; flex-direction: column;
    border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden;
  }

  /* Fixed-width lanes so round, tool, outcome and chevron line up down the
     column regardless of how long any one argument string is. */
  .row {
    display: flex; align-items: center; gap: 12px;
    padding: 9px 12px; background: var(--bg);
    border: none; border-top: 1px solid var(--border);
    width: 100%; text-align: left; cursor: pointer; font-family: inherit;
  }
  .row:first-child { border-top: none; }
  .row:hover { background: var(--hover-overlay); }
  .row.suppressed { background: rgba(200, 126, 48, 0.06); }
  .row.failed { background: rgba(196, 58, 58, 0.07); }
  .row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

  .lane-round {
    width: 26px; flex-shrink: 0; font-size: 11px; color: var(--text-muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .dot { width: 8px; height: 8px; border-radius: 4px; flex-shrink: 0; }
  .dot-ok { background: var(--success); }
  .dot-suppressed { background: var(--warn); }
  .dot-error { background: var(--danger); }

  .lane-tool {
    width: 140px; flex-shrink: 0; font-size: 12px; font-weight: 500; color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .lane-args {
    flex: 1; min-width: 0; font-size: 11px; color: var(--text-muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  /* In compact (two-column) mode the args lane is dropped, so the outcome
     still needs to be pushed to the right edge. */
  .lane-outcome {
    width: 92px; flex-shrink: 0; margin-left: auto; text-align: right;
    font-size: 11px; color: var(--text-muted);
  }
  .lane-chevron {
    width: 14px; flex-shrink: 0; font-size: 9px; color: var(--text-muted);
    transition: transform 0.2s;
  }
  .lane-chevron.open { transform: rotate(90deg); }

  .badge {
    display: inline-block; font-size: 10px; font-weight: 600; letter-spacing: 0.05em;
    color: var(--warn); border: 1px solid var(--warn); border-radius: 4px; padding: 1px 5px;
  }
  .badge-error { color: var(--danger); border-color: var(--danger); }

  .row-detail {
    display: flex; flex-direction: column; gap: 8px;
    padding: 12px 12px 14px 50px; background: var(--bg);
    border-top: 1px solid var(--border);
  }
  .row-detail.suppressed { background: rgba(200, 126, 48, 0.06); }
  .detail-label {
    font-size: 11px; font-weight: 600; letter-spacing: 0.06em;
    text-transform: uppercase; color: var(--text-muted);
  }
  .detail-body {
    font-size: 12px; line-height: 18px; color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 10px 12px; margin: 0;
    max-height: 240px; overflow: auto; white-space: pre-wrap; word-break: break-word;
  }
  .detail-note { font-size: 11px; color: var(--text-muted); }

  .response {
    font-size: 13px; line-height: 20px; color: var(--text);
    background: var(--surface); border: 1px solid var(--border);
    border-left: 2px solid var(--text-muted); border-radius: var(--radius);
    padding: 12px 14px; white-space: pre-wrap; word-break: break-word;
  }
  .response.accent { border-left-color: var(--accent); }
  .response.empty { color: var(--text-muted); font-style: italic; }

  @media (prefers-reduced-motion: reduce) {
    .lane-chevron { transition: none; }
  }

  @media (max-width: 640px) {
    .lane-tool { width: 110px; }
    .lane-args { display: none; }
    .row-detail { padding-left: 12px; }
  }
</style>
