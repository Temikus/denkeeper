<script>
  import { marked } from 'marked'

  let { event } = $props()

  let resultExpanded = $state(false)
  let thinkingExpanded = $state(false)
  let outputRawMode = $state(false)
  let copiedJSON = $state(false)
  let copiedOutput = $state(false)

  function parseDetail(detail) {
    if (!detail) return null
    try { return JSON.parse(detail) } catch { return null }
  }

  // Strip dangerous HTML from rendered markdown (scripts, event handlers)
  function sanitizeHTML(html) {
    return html
      .replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '')
      .replace(/<iframe\b[^>]*>/gi, '')
      .replace(/\bon\w+\s*=/gi, 'data-removed=')
  }

  let detail = $derived(parseDetail(event.detail))
  let isError = $derived(event.status === 'error')
  let isToolCall = $derived(event.category === 'tool_call')
  let isLLM = $derived(event.category === 'llm')

  // ─── TOOL CALL: ARGUMENTS ─────────────────────────────────────────────
  let hasArguments = $derived(isToolCall && !!detail?.arguments)
  let argumentsDisplay = $derived.by(() => {
    if (!detail?.arguments) return ''
    try {
      const parsed = JSON.parse(detail.arguments)
      return JSON.stringify(parsed, null, 2)
    } catch {
      return detail.arguments
    }
  })

  let argumentsCompact = $derived.by(() => {
    if (!detail?.arguments) return ''
    try {
      const parsed = JSON.parse(detail.arguments)
      const compact = JSON.stringify(parsed)
      return compact.length <= 120 ? compact : null
    } catch { return null }
  })

  // ─── TOOL CALL: RESULT ────────────────────────────────────────────────
  let hasResult = $derived(isToolCall && detail?.result != null)
  let resultRaw = $derived(detail?.result ?? '')
  let resultTruncated = $derived(!!detail?.result_truncated)

  let resultParsed = $derived.by(() => {
    if (!resultRaw) return { ok: false }
    try {
      const parsed = JSON.parse(resultRaw)
      return { ok: true, value: parsed }
    } catch {
      return { ok: false }
    }
  })

  let resultShape = $derived.by(() => {
    const size = resultRaw.length
    const sizeStr = size >= 1024 ? `${(size / 1024).toFixed(1)} kb` : `${size} B`
    if (!resultParsed.ok) return `string \u00b7 ${sizeStr}`
    const v = resultParsed.value
    if (Array.isArray(v)) return `Array(${v.length}) \u00b7 ${sizeStr}`
    if (v === null) return `null`
    if (typeof v === 'object') return `Object \u00b7 ${sizeStr}`
    if (typeof v === 'boolean') return String(v)
    if (typeof v === 'number') return String(v)
    return `string \u00b7 ${sizeStr}`
  })

  let fieldSignature = $derived.by(() => {
    if (!resultParsed.ok) return ''
    const v = resultParsed.value
    if (Array.isArray(v) && v.length > 0 && v[0] && typeof v[0] === 'object' && !Array.isArray(v[0])) {
      const keys = Object.keys(v[0])
      const shown = keys.slice(0, 4).join(', ')
      const tail = keys.length > 4 ? ', \u2026' : ''
      return `[{${shown}${tail}}, \u2026]`
    }
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      const keys = Object.keys(v)
      const shown = keys.slice(0, 5).join(', ')
      const tail = keys.length > 5 ? ', \u2026' : ''
      return `{${shown}${tail}}`
    }
    return ''
  })

  let samplePills = $derived.by(() => {
    if (!resultParsed.ok || !Array.isArray(resultParsed.value) || resultParsed.value.length === 0) return []
    const labelKeys = ['content', 'name', 'title', 'label', 'summary', 'description', 'text', 'id']
    return resultParsed.value.slice(0, 3).map(item => {
      if (typeof item === 'string') return item.length > 40 ? item.slice(0, 37) + '\u2026' : item
      if (typeof item !== 'object' || item === null) return String(item)
      for (const k of labelKeys) {
        if (item[k] != null) {
          const s = String(item[k])
          return s.length > 40 ? s.slice(0, 37) + '\u2026' : s
        }
      }
      return JSON.stringify(item).slice(0, 40)
    })
  })

  let remainingCount = $derived.by(() => {
    if (!resultParsed.ok || !Array.isArray(resultParsed.value)) return 0
    return Math.max(0, resultParsed.value.length - 3)
  })

  let resultFull = $derived.by(() => {
    if (resultParsed.ok) return JSON.stringify(resultParsed.value, null, 2)
    return resultRaw
  })

  // ─── LLM: OUTPUT ─────────────────────────────────────────────────────
  let hasResponseText = $derived(isLLM && !!detail?.response_text)
  let responseText = $derived(detail?.response_text ?? '')
  let responseTruncated = $derived(!!detail?.response_truncated)

  let renderedOutput = $derived.by(() => {
    if (!responseText) return ''
    return sanitizeHTML(marked(responseText, { breaks: true }))
  })

  function formatSize(len) {
    return len >= 1024 ? `${(len / 1024).toFixed(1)} kb` : `${len} chars`
  }

  // ─── LLM: THINKING ───────────────────────────────────────────────────
  let hasThinking = $derived(isLLM && !!detail?.thinking_content)
  let thinkingContent = $derived(detail?.thinking_content ?? '')
  let thinkingTruncated = $derived(!!detail?.thinking_truncated)
  let thinkingTeaser = $derived.by(() => {
    if (!thinkingContent) return ''
    // First sentence or first 80 chars
    const firstPeriod = thinkingContent.indexOf('. ')
    const firstNewline = thinkingContent.indexOf('\n')
    let end = 80
    if (firstPeriod > 0 && firstPeriod < 120) end = firstPeriod + 1
    else if (firstNewline > 0 && firstNewline < 120) end = firstNewline
    const text = thinkingContent.slice(0, end).trim()
    return text.length < thinkingContent.length ? text + '\u2026' : text
  })

  // ─── LLM: USAGE ──────────────────────────────────────────────────────
  let finishLabel = $derived.by(() => {
    const r = detail?.finish_reason
    if (!r) return ''
    if (r === 'stop' || r === 'end_turn') return 'completed normally'
    if (r === 'tool_calls') return 'called tools'
    if (r === 'max_tokens' || r === 'length') return 'hit token limit'
    return r
  })

  let tokenBar = $derived.by(() => {
    if (!isLLM || !detail) return null
    const input = (detail.tokens_prompt || 0) + (detail.tokens_cached || 0)
    const output = detail.tokens_completion || 0
    const total = input + output
    if (total === 0) return null
    return {
      inputPct: (input / total * 100).toFixed(1),
      outputPct: (output / total * 100).toFixed(1),
    }
  })

  // ─── SHARED: CONTEXT ─────────────────────────────────────────────────
  let contextFields = $derived.by(() => {
    if (!detail) return []
    const skip = new Set([
      'tool', 'error', 'model', 'provider', 'tokens', 'cost',
      'tokens_prompt', 'tokens_completion', 'tokens_cached',
      'finish_reason', 'round', 'arguments', 'result', 'result_truncated',
      'server', 'response_text', 'response_truncated',
      'thinking_content', 'thinking_truncated'
    ])
    return Object.entries(detail).filter(([k]) => !skip.has(k)).map(([k, v]) => [k, String(v)])
  })

  let errorMsg = $derived(detail?.error || '')
  let serverName = $derived(detail?.server || '')
  let roundNum = $derived(detail?.round)
  let modelName = $derived(detail?.model || '')
  let providerName = $derived(detail?.provider || '')

  function copyJSON() {
    const data = {}
    if (detail?.arguments) {
      try { data.arguments = JSON.parse(detail.arguments) } catch { data.arguments = detail.arguments }
    }
    if (detail?.result) {
      try { data.result = JSON.parse(detail.result) } catch { data.result = detail.result }
    }
    if (detail?.response_text) data.response_text = detail.response_text
    if (detail?.thinking_content) data.thinking_content = detail.thinking_content
    navigator.clipboard.writeText(JSON.stringify(data, null, 2))
    copiedJSON = true
    setTimeout(() => copiedJSON = false, 1500)
  }

  function copyOutput() {
    if (responseText) {
      navigator.clipboard.writeText(responseText)
      copiedOutput = true
      setTimeout(() => copiedOutput = false, 1500)
    }
  }
</script>

<div class="detail-pane">
  <!-- ═══ TOOL CALL sections ═══ -->
  {#if hasArguments}
    <div class="detail-section">
      <div class="detail-label">ARGUMENTS</div>
      <div class="detail-content">
        {#if argumentsCompact}
          <div class="args-inline"><code>{argumentsCompact}</code></div>
        {:else}
          <div class="args-block"><pre>{argumentsDisplay}</pre></div>
        {/if}
      </div>
    </div>
  {/if}

  {#if hasResult}
    <div class="detail-section">
      <div class="detail-label">RESULT</div>
      <div class="detail-content">
        <div class="result-summary" role="button" tabindex="0" aria-expanded={resultExpanded}
          onclick={() => resultExpanded = !resultExpanded}
          onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); resultExpanded = !resultExpanded } }}>
          <span class="result-shape">{resultShape}</span>
          {#if fieldSignature}
            <span class="result-sep">&middot;</span>
            <span class="result-sig">{fieldSignature}</span>
          {/if}
          {#if resultTruncated}
            <span class="result-sep">&middot;</span>
            <span class="result-truncated">truncated</span>
          {/if}
          <span class="result-expand">{resultExpanded ? 'collapse \u25b4' : 'expand \u25be'}</span>
        </div>

        {#if samplePills.length > 0 && !resultExpanded}
          <div class="sample-pills">
            {#each samplePills as pill}
              <span class="pill">{pill}</span>
            {/each}
            {#if remainingCount > 0}
              <span class="pill-more">+ {remainingCount} more</span>
            {/if}
          </div>
        {/if}

        {#if resultExpanded}
          <div class="result-full"><pre>{resultFull}</pre></div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- ═══ LLM sections ═══ -->
  {#if hasThinking}
    <div class="detail-section">
      <div class="detail-label thinking-label">THINKING</div>
      <div class="detail-content">
        <div class="thinking-summary" role="button" tabindex="0" aria-expanded={thinkingExpanded}
          onclick={() => thinkingExpanded = !thinkingExpanded}
          onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); thinkingExpanded = !thinkingExpanded } }}>
          <span class="thinking-chevron">{thinkingExpanded ? '\u25be' : '\u25b8'}</span>
          <span class="thinking-meta">{formatSize(thinkingContent.length)}</span>
          <span class="result-sep">&middot;</span>
          <span class="thinking-teaser">{thinkingTeaser}</span>
          <span class="thinking-toggle">{thinkingExpanded ? 'hide' : 'show'}</span>
        </div>
        {#if thinkingExpanded}
          <div class="thinking-full"><pre>{thinkingContent}</pre></div>
        {/if}
      </div>
    </div>
  {/if}

  {#if hasResponseText}
    <div class="detail-section">
      <div class="detail-label">OUTPUT</div>
      <div class="detail-content">
        <div class="output-toggle-row">
          <div class="output-toggle">
            <button class="toggle-btn" class:active={!outputRawMode} onclick={() => outputRawMode = false}>Rendered</button>
            <button class="toggle-btn" class:active={outputRawMode} onclick={() => outputRawMode = true}>Raw</button>
          </div>
          {#if responseTruncated}
            <span class="result-truncated">truncated</span>
          {/if}
        </div>
        {#if outputRawMode}
          <div class="output-raw"><pre>{responseText}</pre></div>
        {:else}
          <div class="output-rendered">{@html renderedOutput}</div>
        {/if}
      </div>
    </div>
  {/if}

  {#if isLLM && detail}
    <div class="detail-section">
      <div class="detail-label">USAGE</div>
      <div class="detail-content">
        <div class="usage-inline">
          {#if detail.tokens_prompt || detail.tokens_cached}
            <span><span class="usage-label">in</span> {((detail.tokens_prompt || 0) + (detail.tokens_cached || 0)).toLocaleString()}</span>
            <span class="result-sep">&middot;</span>
          {/if}
          {#if detail.tokens_completion}
            <span><span class="usage-label">out</span> {detail.tokens_completion.toLocaleString()}</span>
            <span class="result-sep">&middot;</span>
          {/if}
          {#if detail.tokens}
            <span><span class="usage-label">total</span> {detail.tokens.toLocaleString()}</span>
            <span class="result-sep">&middot;</span>
          {/if}
          {#if detail.cost != null}
            <span>${detail.cost.toFixed(4)}</span>
          {/if}
        </div>
        {#if finishLabel}
          <div class="usage-finish" class:finish-ok={finishLabel === 'completed normally'} class:finish-warn={finishLabel === 'hit token limit'}>{finishLabel}</div>
        {/if}
        {#if tokenBar}
          <div class="token-bar-row">
            <div class="token-bar">
              <div class="bar-input" style="width: {tokenBar.inputPct}%" title="input"></div>
              <div class="bar-output" style="width: {tokenBar.outputPct}%" title="output"></div>
            </div>
            <div class="bar-legend">
              <span class="legend-item"><span class="legend-dot dot-input"></span>input</span>
              <span class="legend-item"><span class="legend-dot dot-output"></span>output</span>
            </div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- ═══ ERROR ═══ -->
  {#if isError && errorMsg}
    <div class="detail-section">
      <div class="detail-label">ERROR</div>
      <div class="detail-content error-block">
        <pre>{errorMsg}</pre>
      </div>
    </div>
  {/if}

  <!-- ═══ Generic fallthrough (non-tool, non-LLM) ═══ -->
  {#if !isToolCall && !isLLM && detail}
    <div class="detail-section">
      <div class="detail-label">DETAIL</div>
      <div class="detail-content args-block">
        <pre>{JSON.stringify(detail, null, 2)}</pre>
      </div>
    </div>
  {/if}

  <!-- ═══ CONTEXT ═══ -->
  {#if contextFields.length > 0 || serverName || modelName || event.agent || event.conversation_id}
    <div class="detail-section">
      <div class="detail-label">CONTEXT</div>
      <div class="detail-content context-block">
        {#if event.agent}
          <div class="context-row"><span class="ctx-key">agent</span><span class="ctx-val">{event.agent}</span></div>
        {/if}
        {#if modelName}
          <div class="context-row">
            <span class="ctx-key">model</span>
            <span class="ctx-val">{modelName}{#if providerName} <span class="ctx-muted">via {providerName}</span>{/if}</span>
          </div>
        {/if}
        {#if serverName}
          <div class="context-row"><span class="ctx-key">server</span><span class="ctx-val">{serverName} <span class="ctx-muted">(mcp)</span></span></div>
        {/if}
        {#if roundNum}
          <div class="context-row"><span class="ctx-key">round</span><span class="ctx-val">{roundNum}</span></div>
        {/if}
        {#if event.conversation_id}
          <div class="context-row"><span class="ctx-key">session</span><span class="ctx-val mono">{event.conversation_id}</span></div>
        {/if}
        {#each contextFields as [key, value]}
          <div class="context-row"><span class="ctx-key">{key}</span><span class="ctx-val">{value}</span></div>
        {/each}
      </div>
    </div>
  {/if}

  <!-- ═══ ACTIONS ═══ -->
  {#if isToolCall || isLLM}
    <div class="detail-actions">
      {#if hasResponseText}
        <button class="action-btn" onclick={copyOutput}>{copiedOutput ? 'Copied' : 'Copy output'}</button>
      {/if}
      <button class="action-btn" onclick={copyJSON}>{copiedJSON ? 'Copied' : 'Copy as JSON'}</button>
      {#if isToolCall && hasResult}
        <button class="action-btn" onclick={() => resultExpanded = !resultExpanded}>
          {resultExpanded ? 'Hide raw' : 'View raw'}
        </button>
      {/if}
      {#if hasResponseText}
        <button class="action-btn" onclick={() => outputRawMode = !outputRawMode}>
          {outputRawMode ? 'Rendered' : 'View raw'}
        </button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .detail-pane {
    padding: 10px 0 4px;
  }
  .detail-section {
    display: grid;
    grid-template-columns: 80px 1fr;
    gap: 12px;
    margin-bottom: 10px;
    align-items: start;
  }
  .detail-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
    padding-top: 6px;
    flex-shrink: 0;
  }
  .detail-content {
    min-width: 0;
  }

  /* Arguments */
  .args-inline {
    padding: 6px 8px;
    background: var(--hover-overlay);
    border-radius: var(--radius);
  }
  .args-inline code {
    font-size: 11px;
    font-family: monospace;
    color: var(--text);
    word-break: break-all;
  }
  .args-block {
    background: var(--hover-overlay);
    border-radius: var(--radius);
    padding: 8px 10px;
  }
  .args-block pre {
    margin: 0;
    font-size: 11px;
    font-family: monospace;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--text);
    line-height: 1.5;
  }

  /* Result / tool summary row */
  .result-summary {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    background: var(--hover-overlay);
    border-radius: var(--radius);
    cursor: pointer;
    transition: background 0.1s;
  }
  .result-summary:hover { background: rgba(0,0,0,0.06); }
  .result-shape {
    font-family: monospace;
    font-size: 11px;
    color: var(--text);
  }
  .result-sep {
    color: var(--text-muted);
    font-size: 10px;
    opacity: 0.4;
  }
  .result-sig {
    font-family: monospace;
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .result-truncated {
    font-size: 10px;
    color: var(--text-muted);
    font-style: italic;
  }
  .result-expand {
    margin-left: auto;
    font-size: 10px;
    color: var(--accent);
    white-space: nowrap;
    flex-shrink: 0;
  }

  /* Sample pills */
  .sample-pills {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 6px;
  }
  .pill {
    font-size: 10px;
    color: var(--text);
    background: var(--surface);
    border: 0.5px solid var(--border);
    padding: 2px 7px;
    border-radius: 999px;
    white-space: nowrap;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .pill-more {
    font-size: 10px;
    color: var(--text-muted);
    padding: 2px 4px;
  }

  /* Full expand (shared by tool result and LLM thinking) */
  .result-full {
    margin-top: 6px;
    background: var(--hover-overlay);
    border-radius: var(--radius);
    padding: 8px 10px;
    max-height: 400px;
    overflow: auto;
  }
  .result-full pre {
    margin: 0;
    font-size: 11px;
    font-family: monospace;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--text);
    line-height: 1.5;
  }

  /* ─── THINKING (purple theme) ─── */
  .thinking-label {
    color: #534AB7;
  }
  .thinking-summary {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 10px;
    background: rgba(127,119,221,0.06);
    border: 0.5px solid rgba(127,119,221,0.2);
    border-radius: var(--radius);
    cursor: pointer;
    transition: background 0.1s;
  }
  .thinking-summary:hover { background: rgba(127,119,221,0.1); }
  .thinking-chevron {
    font-size: 10px;
    color: #534AB7;
    flex-shrink: 0;
  }
  .thinking-meta {
    font-size: 11px;
    color: var(--text);
    flex-shrink: 0;
  }
  .thinking-teaser {
    font-size: 11px;
    color: var(--text-muted);
    font-style: italic;
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .thinking-toggle {
    font-size: 10px;
    color: #534AB7;
    flex-shrink: 0;
  }
  .thinking-full {
    margin-top: 6px;
    background: rgba(127,119,221,0.04);
    border: 0.5px solid rgba(127,119,221,0.12);
    border-radius: var(--radius);
    padding: 8px 10px;
    max-height: 400px;
    overflow: auto;
  }
  .thinking-full pre {
    margin: 0;
    font-size: 11px;
    font-family: monospace;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--text);
    line-height: 1.5;
  }

  /* ─── OUTPUT (rendered/raw toggle) ─── */
  .output-toggle-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 6px;
  }
  .output-toggle {
    display: flex;
    border: 0.5px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
    width: fit-content;
  }
  .toggle-btn {
    font-size: 10px;
    padding: 2px 9px;
    border: none;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
    font-family: inherit;
    transition: background 0.1s, color 0.1s;
  }
  .toggle-btn.active {
    background: var(--accent);
    color: #fff;
  }
  .output-rendered {
    background: var(--surface);
    border: 0.5px solid var(--border);
    padding: 10px 12px;
    border-radius: var(--radius);
    font-size: 12px;
    line-height: 1.6;
    color: var(--text);
  }
  .output-rendered :global(p) { margin: 0 0 8px; }
  .output-rendered :global(p:last-child) { margin-bottom: 0; }
  .output-rendered :global(ol), .output-rendered :global(ul) { margin: 0 0 8px; padding-left: 20px; }
  .output-rendered :global(li) { margin-bottom: 4px; }
  .output-rendered :global(li:last-child) { margin-bottom: 0; }
  .output-rendered :global(code) { font-size: 11px; background: var(--hover-overlay); padding: 1px 4px; border-radius: 2px; }
  .output-rendered :global(pre) { background: var(--hover-overlay); padding: 8px 10px; border-radius: var(--radius); overflow-x: auto; margin: 0 0 8px; }
  .output-rendered :global(pre code) { background: none; padding: 0; }
  .output-rendered :global(strong) { font-weight: 500; }
  .output-rendered :global(em) { color: var(--text-muted); }
  .output-rendered :global(a) { color: var(--accent); text-decoration: none; }
  .output-rendered :global(blockquote) { border-left: 2px solid var(--border); margin: 0 0 8px; padding: 4px 12px; color: var(--text-muted); }
  .output-raw {
    background: var(--hover-overlay);
    border-radius: var(--radius);
    padding: 8px 10px;
    max-height: 400px;
    overflow: auto;
  }
  .output-raw pre {
    margin: 0;
    font-size: 11px;
    font-family: monospace;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--text);
    line-height: 1.5;
  }

  /* ─── USAGE (compact inline + ratio bar) ─── */
  .usage-inline {
    display: flex;
    align-items: center;
    gap: 8px;
    font-family: monospace;
    font-size: 11px;
    color: var(--text);
    flex-wrap: wrap;
  }
  .usage-label {
    color: var(--text-muted);
    font-family: inherit;
  }
  .usage-finish {
    margin-top: 4px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .finish-ok { color: #3B6D11; }
  .finish-warn { color: var(--danger); }

  .token-bar-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 8px;
  }
  .token-bar {
    flex: 1;
    height: 5px;
    background: var(--hover-overlay);
    border-radius: 999px;
    overflow: hidden;
    display: flex;
    max-width: 360px;
  }
  .bar-input { background: #E8825E; }
  .bar-output { background: #3B6D11; }
  .bar-legend {
    display: flex;
    gap: 10px;
    font-size: 10px;
    color: var(--text-muted);
  }
  .legend-item {
    display: flex;
    align-items: center;
    gap: 3px;
  }
  .legend-dot {
    display: inline-block;
    width: 7px;
    height: 7px;
    border-radius: 1px;
  }
  .dot-input { background: #E8825E; }
  .dot-output { background: #3B6D11; }

  /* Error */
  .error-block {
    background: rgba(196, 58, 58, 0.06);
    border-radius: var(--radius);
    padding: 8px 10px;
  }
  .error-block pre {
    margin: 0;
    font-size: 12px;
    font-family: monospace;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--danger);
  }

  /* Context */
  .context-block {
    padding: 2px 0;
  }
  .context-row {
    display: flex;
    gap: 12px;
    padding: 2px 0;
    font-size: 12px;
  }
  .ctx-key {
    min-width: 70px;
    color: var(--text-muted);
  }
  .ctx-val {
    color: var(--text);
  }
  .ctx-val.mono {
    font-family: monospace;
    font-size: 11px;
  }
  .ctx-muted {
    color: var(--text-muted);
    font-size: 11px;
  }

  /* Action buttons */
  .detail-actions {
    display: flex;
    gap: 14px;
    margin-top: 6px;
    padding-top: 8px;
    border-top: 0.5px solid var(--border);
  }
  .action-btn {
    background: none;
    border: none;
    padding: 0;
    font-size: 10px;
    color: var(--text-muted);
    cursor: pointer;
    font-family: inherit;
  }
  .action-btn:hover {
    color: var(--text);
  }
</style>
