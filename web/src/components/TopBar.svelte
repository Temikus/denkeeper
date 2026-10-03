<script>
  import { api } from '../api.js'
  import { tick, untrack } from 'svelte'
  import { panicStatus, wsStatus, refreshPanicStatus } from '../wsStore.js'
  import { attention } from '../attention.js'
  import { relativeTime } from '../relativeTime.js'
  import { findItem } from '../navItems.js'
  import StopAllButton from './StopAllButton.svelte'

  let { active = 'overview' } = $props()

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

  const crumb = $derived(findItem(active))

  const health = $derived.by(() => {
    const broken = $attention.unhealthyTools.length
    if ($wsStatus === 'connecting' || $wsStatus === 'reconnecting') {
      return { tone: 'warn', text: 'Reconnecting…' }
    }
    if ($wsStatus === 'disconnected') return { tone: 'grey', text: 'Offline' }
    const live = $wsStatus === 'sse_fallback' ? 'Live (SSE)' : 'Live'
    if (broken > 0) {
      return { tone: 'bad', text: `${live} · ${broken} tool${broken === 1 ? '' : 's'} unhealthy`, href: '#/tools' }
    }
    return { tone: 'ok', text: `${live} · healthy` }
  })

  const pending = $derived($attention.pendingApprovals)

  // Stopping or resuming swaps the bar's contents, which unmounts the button
  // that had focus. Hand focus to the new bar's action instead of <body>,
  // but only when it was in the bar: a stop from Telegram must not steal it.
  let bar = $state()
  let hadFocus = false
  $effect.pre(() => {
    $panicStatus.active
    hadFocus = untrack(() => !!bar && bar.contains(document.activeElement))
  })
  $effect(() => {
    $panicStatus.active
    if (!hadFocus) return
    tick().then(() => bar?.querySelector('[data-focus-target]')?.focus())
  })

  async function triggerResume() {
    busy = true
    try {
      await api.resume()
      error = ''
      // The WS frame normally flips the UI; re-read in case the socket is down.
      refreshPanicStatus()
    } catch (e) {
      error = 'Resume failed: ' + e.message
    } finally {
      busy = false
    }
  }
</script>

{#if $panicStatus.active}
  <header class="top-bar stopped" bind:this={bar} data-testid="global-panic-bar">
    <div class="left">
      <svg class="stop-icon" width="14" height="14" viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="5" width="14" height="14" rx="2" fill="currentColor"/></svg>
      <div class="stopped-text" role="alert">
        <span class="stopped-title">All agents stopped</span>
        {#if elapsed}<span class="since">{elapsed}</span>{/if}
      </div>
    </div>
    <div class="right">
      {#if error}
        <span class="inline-error" title={error}>{error}</span>
      {:else}
        <span class="stopped-hint">New messages get a “paused” reply · schedules are paused</span>
      {/if}
      <button class="btn-resume" onclick={triggerResume} disabled={busy} data-testid="global-panic-resume" data-focus-target>
        <svg width="12" height="12" viewBox="0 0 24 24" aria-hidden="true"><polygon points="7 4 20 12 7 20" fill="currentColor"/></svg>
        {busy ? 'Resuming…' : 'Resume all'}
      </button>
    </div>
  </header>
{:else}
  <header class="top-bar" bind:this={bar} data-testid="top-bar">
    <div class="left">
      <span class="brand">Denkeeper</span>
      <span class="mobile-health"><span class="dot {health.tone}" title={health.text} aria-hidden="true"></span><span class="sr-only">{health.text}</span></span>
      <nav class="crumb" aria-label="Breadcrumb">
        {#if crumb.section}<span class="crumb-section">{crumb.section.label}</span><span class="crumb-sep" aria-hidden="true">/</span>{/if}
        {#if crumb.item}<span aria-current="page">{crumb.item.label}</span>{/if}
      </nav>
    </div>
    <div class="right">
      {#if health.href}
        <a class="health" href={health.href}><span class="dot {health.tone}" aria-hidden="true"></span>{health.text}</a>
      {:else}
        <span class="health"><span class="dot {health.tone}" aria-hidden="true"></span>{health.text}</span>
      {/if}
      {#if pending > 0}
        <a class="approvals-chip" href="#/approvals" data-testid="approvals-chip">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M18 8A6 6 0 006 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 01-3.46 0"/></svg>
          {pending} approval{pending === 1 ? '' : 's'}
        </a>
      {/if}
      <StopAllButton />
    </div>
  </header>
{/if}

<style>
  .top-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    height: 52px;
    flex-shrink: 0;
    padding: 0 32px;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
    font-size: 13px;
  }

  .left, .right {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  .right { gap: 16px; flex-shrink: 0; }

  .brand { display: none; font-weight: 700; font-size: 16px; color: var(--accent); }
  .mobile-health { display: none; }

  .crumb { display: flex; align-items: center; gap: 8px; color: var(--text-muted); min-width: 0; }
  .crumb [aria-current] { color: var(--text); }
  .crumb-sep { opacity: 0.6; }

  .health {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-muted);
    white-space: nowrap;
  }
  a.health:hover { color: var(--text); }

  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
  .dot.ok   { background: var(--success); }
  .dot.warn { background: var(--warn); }
  .dot.bad  { background: var(--danger); }
  .dot.grey { background: var(--text-muted); }

  .approvals-chip {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    border-radius: 14px;
    background: rgba(200, 126, 48, 0.12);
    color: var(--warn-text);
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
  }
  .approvals-chip:hover { background: rgba(200, 126, 48, 0.2); color: var(--warn-text); }

  /* Stopped */
  .stopped {
    background: rgba(196, 58, 58, 0.1);
    border-bottom-color: var(--danger);
  }
  .stop-icon { color: var(--danger); flex-shrink: 0; }
  .stopped-text { display: flex; align-items: baseline; gap: 10px; min-width: 0; }
  .stopped-title { font-weight: 700; color: var(--text); white-space: nowrap; }
  .since, .stopped-hint { color: var(--text-muted); font-size: 12px; white-space: nowrap; }

  .btn-resume {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 14px;
    background: var(--text);
    color: var(--bg);
    border: none;
    border-radius: var(--radius);
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
    cursor: pointer;
    flex-shrink: 0;
  }
  .btn-resume:hover:not(:disabled) { opacity: 0.85; }
  .btn-resume:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .btn-resume:disabled { opacity: 0.6; cursor: not-allowed; }

  @media (max-width: 1024px) {
    .stopped-hint { display: none; }
  }

  /* Phones: brand plus status dot on the left, the action on the right. The
     tab bar carries the approvals badge, so the chip and health text go. */
  @media (max-width: 768px) {
    .top-bar { height: 48px; padding: 0 16px; }
    .brand { display: inline; }
    .mobile-health { display: flex; align-items: center; }
    .crumb, .health, .approvals-chip { display: none; }
    .stopped-text { flex-direction: column; align-items: flex-start; gap: 0; }
    .stopped-title { font-size: 14px; line-height: 18px; }
    .since { font-size: 11px; line-height: 14px; }
    .right .inline-error { max-width: 40vw; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  }
</style>
