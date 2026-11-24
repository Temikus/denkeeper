<script>
  // The server's high-risk classification of a pending call (#586): a fixed
  // warning plus the arguments that make it risky, shown in full and open.
  // `risk` is { level, warning, flagged: [{ name, value }] }; values are text.
  // `id` lets a button point at this block with aria-describedby.
  let { risk, id = undefined } = $props()
</script>

<div class="approval-risk" {id}>
  <p class="risk-head"><span class="pill risk-pill">High risk</span> {risk.warning}</p>
  {#if risk.flagged?.length}
    <dl class="risk-args">
      {#each risk.flagged as arg (arg.name)}
        <dt>{arg.name}</dt>
        <dd><code>{arg.value}</code></dd>
      {/each}
    </dl>
  {/if}
</div>

<style>
  .approval-risk { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  .risk-head { margin: 0; font-size: 12px; line-height: 1.45; }
  /* The shared .pill is muted; this one marks danger with the existing token. */
  .risk-pill { color: var(--danger); background: transparent; border: 1px solid var(--danger); margin: 0; }
  .risk-args { margin: 0; display: flex; flex-direction: column; gap: 2px; }
  .risk-args dt { font-size: 11px; color: var(--text-muted); font-family: monospace; }
  /* Flagged values are never cut: they wrap at any width. */
  .risk-args dd { margin: 0 0 4px; min-width: 0; }
  .risk-args code { font-size: 12px; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
