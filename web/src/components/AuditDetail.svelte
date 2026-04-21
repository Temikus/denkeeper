<script>
  let { event } = $props()

  function parseDetail(detail) {
    if (!detail) return null
    try { return JSON.parse(detail) } catch { return null }
  }

  let detail = $derived(parseDetail(event.detail))
  let isError = $derived(event.status === 'error')

  // Build CONTEXT section from detail fields (minus tool/error/model which are shown elsewhere)
  let contextFields = $derived(() => {
    if (!detail) return []
    const skip = new Set(['tool', 'error', 'model', 'provider', 'tokens', 'cost', 'tokens_prompt', 'tokens_completion', 'finish_reason', 'round'])
    return Object.entries(detail).filter(([k]) => !skip.has(k)).map(([k, v]) => [k, String(v)])
  })

  // Build ARGUMENTS section for tool calls
  let hasArguments = $derived(event.category === 'tool_call' && detail)
  let argumentsJSON = $derived(() => {
    if (!detail) return ''
    // Show the full detail as arguments for now; backend can provide actual args later
    const args = {}
    for (const [k, v] of Object.entries(detail)) {
      if (k !== 'error' && k !== 'server' && k !== 'round') args[k] = v
    }
    return JSON.stringify(args, null, 2)
  })

  let errorMsg = $derived(detail?.error || '')
  let serverName = $derived(detail?.server || '')
</script>

<div class="detail-pane">
  {#if hasArguments && event.category === 'tool_call'}
    <div class="detail-section">
      <div class="detail-label">ARGUMENTS</div>
      <div class="detail-content args-block">
        <pre>{argumentsJSON()}</pre>
      </div>
    </div>
  {/if}

  {#if isError && errorMsg}
    <div class="detail-section">
      <div class="detail-label">ERROR</div>
      <div class="detail-content error-block">
        <pre>{errorMsg}</pre>
      </div>
    </div>
  {/if}

  {#if contextFields().length > 0 || serverName || event.agent || event.conversation_id}
    <div class="detail-section">
      <div class="detail-label">CONTEXT</div>
      <div class="detail-content context-block">
        {#if event.agent}
          <div class="context-row"><span class="ctx-key">agent</span><span class="ctx-val">{event.agent}</span></div>
        {/if}
        {#if serverName}
          <div class="context-row"><span class="ctx-key">server</span><span class="ctx-val">{serverName}</span></div>
        {/if}
        {#if event.conversation_id}
          <div class="context-row"><span class="ctx-key">session</span><span class="ctx-val mono">{event.conversation_id}</span></div>
        {/if}
        {#if event.source}
          <div class="context-row"><span class="ctx-key">source</span><span class="ctx-val">{event.source}</span></div>
        {/if}
        {#each contextFields() as [key, value]}
          <div class="context-row"><span class="ctx-key">{key}</span><span class="ctx-val">{value}</span></div>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .detail-pane {
    padding: 8px 0 4px;
  }
  .detail-section {
    display: flex;
    gap: 16px;
    margin-bottom: 10px;
  }
  .detail-label {
    min-width: 80px;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-muted);
    padding-top: 6px;
    flex-shrink: 0;
  }
  .detail-content {
    flex: 1;
    min-width: 0;
  }
  .args-block {
    background: var(--hover-overlay);
    border-radius: var(--radius);
    padding: 8px 10px;
  }
  .args-block pre {
    margin: 0;
    font-size: 12px;
    font-family: monospace;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--text);
  }
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
</style>
