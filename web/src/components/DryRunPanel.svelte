<script>
  // Inline transcript for a dry run: what the agent said, which tools ran, and
  // which writes were held back. Rendered in place (never a modal) directly
  // under the schedule or skill it previews.
  //
  // `run` is an async function returning the transcript from the API. The panel
  // owns the in-flight and error state so callers only supply the request.
  let { run, onclose = undefined } = $props()

  // Unique per instance so aria-controls links a row to its own detail panel
  // even when two panels are mounted on one page.
  const uid = Math.random().toString(36).slice(2, 8)

  let loading = $state(false)
  let error = $state('')
  let transcript = $state(null)
  let expanded = $state(new Set())

  async function start() {
    loading = true
    error = ''
    transcript = null
    expanded = new Set()
    try {
      transcript = await run()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function toggle(i) {
    const next = new Set(expanded)
    if (next.has(i)) next.delete(i); else next.add(i)
    expanded = next
  }

  function duration(ms) {
    if (ms == null) return ''
    return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms} ms`
  }

  function cost(usd) {
    if (!usd) return '$0.0000'
    return `$${usd.toFixed(4)}`
  }

  function asOfLabel(iso) {
    if (!iso) return ''
    return new Date(iso).toLocaleString(undefined, {
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
    })
  }

  // The dot colour encodes the outcome classes the engine already
  // distinguishes: nothing ran, it ran, or it broke.
  function dotClass(outcome) {
    if (outcome === 'suppressed') return 'dot-suppressed'
    if (outcome === 'failed' || outcome === 'rejected' || outcome === 'denied') return 'dot-error'
    return 'dot-ok'
  }

  start()
</script>

<div class="panel">
  <div class="notice">
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.4" />
      <path d="M8 5v3.5" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
      <circle cx="8" cy="11" r="0.9" fill="currentColor" />
    </svg>
    <span>Preview only: nothing was sent, written, or remembered.</span>
    {#if onclose}
      <button class="close" onclick={onclose} aria-label="Close preview">&times;</button>
    {/if}
  </div>

  {#if loading}
    <div class="state">
      <span class="spinner" aria-hidden="true"></span>
      Running through the agent&hellip;
    </div>
  {:else if error}
    <div class="state error-state">
      <span>{error}</span>
      <button class="btn-sm" onclick={start}>Try again</button>
    </div>
  {:else if transcript}
    <div class="meta">
      <div class="metric"><span class="metric-label">Rounds</span><span class="metric-value">{transcript.rounds}</span></div>
      <div class="metric"><span class="metric-label">Tools</span><span class="metric-value">{transcript.tool_calls.length}</span></div>
      <div class="metric">
        <span class="metric-label">Suppressed</span>
        <span class="metric-value" class:accented={transcript.suppressed_count > 0}>{transcript.suppressed_count}</span>
      </div>
      <div class="metric"><span class="metric-label">Cost</span><span class="metric-value">{cost(transcript.cost_usd)}</span></div>
      <div class="metric"><span class="metric-label">Took</span><span class="metric-value">{duration(transcript.duration_ms)}</span></div>
      <div class="meta-spacer"></div>
      <div class="metric align-end"><span class="metric-label">As of</span><span class="metric-value small">{asOfLabel(transcript.as_of)}</span></div>
    </div>

    {#if transcript.tool_calls.length > 0}
      <div class="section">
        <div class="section-label">Tool trace</div>
        <div class="rows">
          {#each transcript.tool_calls as call, i (i)}
            <button
              class="row"
              class:suppressed={call.suppressed}
              onclick={() => toggle(i)}
              aria-expanded={expanded.has(i)}
              aria-controls="{uid}-call-{i}"
            >
              <span class="lane-round">R{call.round}</span>
              <span class="dot {dotClass(call.outcome)}"></span>
              <span class="lane-tool">{call.tool}</span>
              <span class="lane-args">{call.arguments || ''}</span>
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
                <div class="detail-label">Result returned to the model</div>
                <pre class="detail-body">{call.result || call.error || '(no result)'}</pre>
                {#if call.truncated}
                  <div class="detail-note">Trimmed for display.</div>
                {/if}
              </div>
            {/if}
          {/each}
        </div>
      </div>
    {/if}

    <div class="section">
      <div class="section-label">Response &mdash; not delivered</div>
      {#if transcript.response}
        <div class="response">{transcript.response}</div>
      {:else}
        <div class="response empty">The agent produced no text response.</div>
      {/if}
    </div>

    <div class="footer">
      <button class="btn-sm" onclick={start}>Run again</button>
      <span class="footer-note">{transcript.model || 'model unknown'}</span>
    </div>
  {/if}
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    border-top: 1px solid var(--border);
    background: var(--bg);
  }

  .notice {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 12px 16px;
    background: rgba(200, 126, 48, 0.08);
    border-bottom: 1px solid var(--border);
    font-size: 13px;
    line-height: 20px;
    color: var(--text);
  }
  .notice svg { flex-shrink: 0; margin-top: 2px; color: var(--warn); }
  .notice span { flex: 1; }

  .close {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 18px;
    line-height: 1;
    cursor: pointer;
    padding: 0 2px;
  }
  .close:hover { color: var(--text); }

  .state {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 20px 16px;
    font-size: 13px;
    color: var(--text-muted);
  }
  .error-state { color: var(--danger); }
  .error-state span { flex: 1; }

  .spinner {
    width: 14px;
    height: 14px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    flex-shrink: 0;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  @media (prefers-reduced-motion: reduce) {
    .spinner { animation-duration: 2s; }
    .lane-chevron { transition: none; }
  }

  .meta {
    display: flex;
    align-items: center;
    gap: 20px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
  }
  .metric { display: flex; flex-direction: column; gap: 2px; }
  .align-end { align-items: flex-end; }
  .metric-label { font-size: 11px; color: var(--text-muted); }
  .metric-value { font-size: 14px; font-weight: 600; color: var(--text); }
  .metric-value.small { font-size: 13px; font-weight: 400; }
  .metric-value.accented { color: var(--warn); }
  .meta-spacer { flex: 1; min-width: 0; }

  .section { display: flex; flex-direction: column; gap: 10px; padding: 16px 16px 0 16px; }
  .section:last-of-type { padding-bottom: 4px; }

  .section-label {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-muted);
  }

  .rows {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  /* Fixed-width lanes so round, tool, outcome and chevron line up down the
     column regardless of how long any one argument string is. */
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 12px;
    background: var(--bg);
    border: none;
    border-top: 1px solid var(--border);
    width: 100%;
    text-align: left;
    cursor: pointer;
    font-family: inherit;
  }
  .row:first-child { border-top: none; }
  .row:hover { background: var(--hover-overlay); }
  .row.suppressed { background: rgba(200, 126, 48, 0.06); }
  .row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

  .lane-round {
    width: 30px;
    flex-shrink: 0;
    font-size: 11px;
    color: var(--text-muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .dot { width: 8px; height: 8px; border-radius: 4px; flex-shrink: 0; }
  .dot-ok { background: var(--success); }
  .dot-suppressed { background: var(--warn); }
  .dot-error { background: var(--danger); }

  .lane-tool {
    width: 150px;
    flex-shrink: 0;
    font-size: 13px;
    font-weight: 500;
    color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .lane-args {
    flex: 1;
    min-width: 0;
    font-size: 12px;
    color: var(--text-muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .lane-outcome {
    width: 96px;
    flex-shrink: 0;
    text-align: right;
    font-size: 12px;
    color: var(--text-muted);
  }
  .lane-chevron {
    width: 16px;
    flex-shrink: 0;
    font-size: 9px;
    color: var(--text-muted);
    transition: transform 0.2s;
  }
  .lane-chevron.open { transform: rotate(90deg); }

  .badge {
    display: inline-block;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    color: var(--warn);
    border: 1px solid var(--warn);
    border-radius: 4px;
    padding: 2px 6px;
  }
  .badge-error { color: var(--danger); border-color: var(--danger); }

  .row-detail {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 12px 14px 54px;
    background: var(--bg);
    border-top: 1px solid var(--border);
  }
  .row-detail.suppressed { background: rgba(200, 126, 48, 0.06); }

  .detail-label {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .detail-body {
    font-size: 12px;
    line-height: 18px;
    color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 10px 12px;
    margin: 0;
    max-height: 260px;
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .detail-note { font-size: 11px; color: var(--text-muted); }

  .response {
    font-size: 14px;
    line-height: 22px;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 2px solid var(--accent);
    border-radius: var(--radius);
    padding: 14px 16px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .response.empty { color: var(--text-muted); font-style: italic; }

  .footer {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px 16px 16px;
  }
  .footer-note { font-size: 11px; color: var(--text-muted); }

  @media (max-width: 640px) {
    .lane-tool { width: 110px; }
    .lane-args { display: none; }
    .lane-outcome { width: 88px; }
    .row-detail { padding-left: 12px; }
  }
</style>
