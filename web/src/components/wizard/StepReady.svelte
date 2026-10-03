<script>
  import { onDestroy } from 'svelte'
  import { api } from '../../api.js'
  import { refreshSetup } from '../../setupStore.js'
  import { EXAMPLE_PROMPTS } from './wizardContent.js'

  // A saved chat app only connects after a restart (adapters start at boot).
  // restart.managed says whether a process manager brings the server back.
  let {
    name = '',
    supervised = false,
    chatApp = '',
    restartRequired = false,
    restart = { available: false, managed: false },
    onTry,
  } = $props()

  let phase = $state('idle') // idle | restarting | back | timeout | error
  let restartError = $state('')
  let cancelled = false

  const appLabel = $derived(chatApp ? chatApp.charAt(0).toUpperCase() + chatApp.slice(1) : '')

  onDestroy(() => { cancelled = true })

  const sleep = ms => new Promise(r => setTimeout(r, ms))

  async function doRestart() {
    phase = 'restarting'
    restartError = ''
    try {
      await api.restartProcess()
    } catch (e) {
      phase = 'error'
      restartError = e.message
      return
    }
    await sleep(1500) // the server waits 500ms before stopping
    for (let i = 0; i < 60 && !cancelled; i++) {
      try {
        const h = await api.health()
        if (h?.status) {
          phase = 'back'
          await refreshSetup()
          return
        }
      } catch { /* still down */ }
      await sleep(1000)
    }
    if (!cancelled) phase = 'timeout'
  }
</script>

<div class="wz-stack">
  {#if restartRequired && restart.available}
    <div class="wz-panel restart" data-testid="wizard-restart">
      {#if phase === 'back'}
        <p class="wz-status ok">Restarted. {appLabel} is connected; say hi to {name} there.</p>
      {:else if restart.managed}
        <p class="wz-panel-title">Restart to connect {appLabel}</p>
        <p class="wz-hint">Chat apps start when the server starts. It comes back on its own in a few seconds.</p>
        {#if phase === 'restarting'}
          <p class="wz-status muted">Restarting… this page reconnects by itself.</p>
        {:else}
          <button type="button" class="btn-primary" onclick={doRestart} data-testid="wizard-restart-now">Restart now</button>
        {/if}
      {:else}
        <p class="wz-panel-title">Restart Denkeeper to connect {appLabel}</p>
        <p class="wz-hint">Chat apps start when the server starts. Stop Denkeeper and run <code>denkeeper serve</code> again. It doesn't look like a service manager will restart it for you.</p>
        {#if phase === 'restarting'}
          <p class="wz-status muted">Stopping the server…</p>
        {:else}
          <button type="button" class="wz-link muted" onclick={doRestart}>Stop the server now</button>
        {/if}
      {/if}
      {#if phase === 'timeout'}<p class="wz-status warn">The server hasn't come back yet. Start it again if it isn't managed by a service.</p>{/if}
      {#if restartError}<p class="inline-error" role="alert">{restartError}</p>{/if}
    </div>
  {/if}

  <div class="try">
    <h3 class="try-title">Try asking</h3>
    {#each EXAMPLE_PROMPTS as p (p)}
      <button type="button" class="prompt" onclick={() => onTry?.(p)}>
        <span>"{p}"</span>
        <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><path d="M3 8h10M9.5 4.5L13 8l-3.5 3.5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </button>
    {/each}
  </div>

  {#if supervised}
    <p class="note">When {name} uses a tool, the supervisor checks it first. Anything it's unsure about lands in Approvals{chatApp ? ` and in ${appLabel}` : ''} for you to decide.</p>
  {/if}
</div>

<style>
  .try { display: flex; flex-direction: column; gap: 10px; }
  .try-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .prompt {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
    background: #fff;
    border: 1px solid var(--border);
    border-radius: 10px;
    font: inherit;
    font-size: 15px;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }
  :global(:root.dark) .prompt { background: var(--bg); }
  .prompt:hover { border-color: var(--accent); }
  .prompt svg { color: var(--accent); flex-shrink: 0; }
  .note {
    padding: 14px 16px;
    background: rgba(var(--accent-rgb), 0.07);
    border-radius: 10px;
    font-size: 13px;
    color: var(--text-muted);
  }
  .restart { align-items: flex-start; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
</style>
