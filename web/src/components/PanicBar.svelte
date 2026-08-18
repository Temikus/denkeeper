<script>
  import { api } from '../api.js'
  import { panicStatus } from '../wsStore.js'
  import { relativeTime } from '../relativeTime.js'

  let error = $state('')
  let busy = $state(false)

  // Ticks so the elapsed label doesn't freeze, and re-baselines on each new
  // panic: this component mounts at app boot, so a panic arriving between
  // ticks would otherwise be compared against a `now` captured before it and
  // render as "in 6s".
  let now = $state(Date.now())
  $effect(() => {
    $panicStatus.since
    now = Date.now()
    const t = setInterval(() => { now = Date.now() }, 30000)
    return () => clearInterval(t)
  })

  // A server clock marginally ahead of the browser must still read as elapsed,
  // never as a countdown to something that already happened.
  const elapsed = $derived(
    $panicStatus.since
      ? relativeTime($panicStatus.since, Math.max(now, new Date($panicStatus.since).getTime()))
      : ''
  )

  async function triggerResume() {
    busy = true
    try {
      await api.resume()
      error = ''
    } catch (e) {
      error = 'Resume failed: ' + e.message
    } finally {
      busy = false
    }
  }
</script>

{#if $panicStatus.active}
  <div class="panic-bar" role="alert" data-testid="global-panic-bar">
    <span class="label">
      <svg class="icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/>
        <line x1="12" y1="9" x2="12" y2="13"/>
        <line x1="12" y1="17" x2="12.01" y2="17"/>
      </svg>
      <span class="text-long">All processing paused</span>
      <span class="text-short">Paused</span>
      {#if elapsed}
        <span class="since">{elapsed}</span>
      {/if}
    </span>

    {#if error}
      <span class="error">{error}</span>
    {/if}

    <button class="btn-resume" onclick={triggerResume} disabled={busy} data-testid="global-panic-resume">
      {busy ? 'Resuming…' : 'Resume'}
    </button>
  </div>
{/if}

<style>
  .panic-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-shrink: 0;
    background: var(--danger);
    color: #fff;
    padding: 9px 16px;
    font-size: 13px;
    font-weight: 600;
  }

  .label {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  .icon { flex-shrink: 0; }

  .since {
    font-weight: 400;
    opacity: 0.85;
    white-space: nowrap;
  }

  .error {
    font-weight: 400;
    font-size: 12px;
    opacity: 0.9;
    text-align: right;
    min-width: 0;
  }

  .btn-resume {
    background: #fff;
    color: var(--danger);
    border: none;
    padding: 4px 14px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
    font-weight: 600;
    white-space: nowrap;
    flex-shrink: 0;
  }
  .btn-resume:hover:not(:disabled) { opacity: 0.85; }
  .btn-resume:disabled { opacity: 0.6; cursor: not-allowed; }

  /* The long label is the first thing to go — the button must survive to 320px. */
  .text-short { display: none; }

  @media (max-width: 480px) {
    .panic-bar { padding: 8px 12px; gap: 8px; }
    .text-long { display: none; }
    .text-short { display: inline; }
    .error { display: none; }
  }
</style>
