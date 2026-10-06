<script>
  // Tool-call arguments as labelled rows; `call` comes from parseToolCall().
  // Pass head={false} when the caller already shows the tool name.
  let { call, head = true } = $props()
</script>

<div class="tool-call">
  {#if head}
    <div class="tool-head">
      <code class="tool">{call.tool}</code>
      {#if call.retry}<span class="pill">{call.retry}</span>{/if}
    </div>
  {/if}
  {#if call.args === null}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scroll region needs a tab stop to be reachable at all) -->
    <pre class="arg-block" tabindex="0" role="region" aria-label="{call.tool} arguments">{call.raw}</pre>
  {:else if call.args.length === 0}
    <span class="no-args">No arguments</span>
  {:else}
    <dl class="args">
      {#each call.args as arg (arg.key)}
        <dt>{arg.key}</dt>
        <dd>
          {#if arg.block}
            <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scroll region needs a tab stop to be reachable at all) -->
            <pre class="arg-block" tabindex="0" role="region" aria-label="{call.tool} {arg.key}">{arg.value}</pre>
          {:else}
            <code class="arg-inline">{arg.value}</code>
          {/if}
        </dd>
      {/each}
    </dl>
  {/if}
</div>

<style>
  .tool-call { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
  .tool-head { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
  .tool { font-weight: 600; font-size: 13px; }
  .no-args { font-size: 12px; color: var(--text-muted); }
  .args { margin: 0; display: flex; flex-direction: column; gap: 4px; }
  .args dt { font-size: 11px; color: var(--text-muted); font-family: monospace; }
  .args dd { margin: 0 0 4px; min-width: 0; }
  .arg-inline { font-size: 12px; word-break: break-word; }
  .arg-block {
    margin: 0;
    padding: 6px 8px;
    font-size: 12px;
    line-height: 1.45;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 240px;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--surface);
  }
  .arg-block:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
</style>
