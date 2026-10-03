<script module>
  export const HOLD_MS = 1500
</script>

<script>
  import { api } from '../api.js'
  import { refreshPanicStatus } from '../wsStore.js'

  const hintId = $props.id()

  // Stop all is reversible (Resume undoes it) but drops every in-flight turn,
  // so it asks for a deliberate hold rather than a click or a confirm() popup.
  let holding = $state(false)
  let busy = $state(false)
  let message = $state('')
  let isError = $state(false)
  let timer = null
  let messageTimer = null
  // Screen readers activate with a synthetic click and cannot hold, so for
  // them a second press within ARM_MS stands in for the hold.
  let armedUntil = 0
  const ARM_MS = 3000

  function say(text, error = false) {
    clearTimeout(messageTimer)
    message = text
    isError = error
    if (!error) messageTimer = setTimeout(() => { message = '' }, 2500)
  }

  function start() {
    if (busy || holding) return
    holding = true
    message = ''
    timer = setTimeout(fire, HOLD_MS)
  }

  function cancel() {
    if (!holding) return
    clearTimeout(timer)
    holding = false
    say('Hold to stop all agents')
  }

  async function fire() {
    holding = false
    busy = true
    try {
      await api.panic()
      message = ''
      // The WS frame normally flips the UI; re-read in case the socket is down.
      refreshPanicStatus()
    } catch (e) {
      say('Stop failed: ' + e.message, true)
    } finally {
      busy = false
    }
  }

  function onKeydown(e) {
    if ((e.key === 'Enter' || e.key === ' ') && !e.repeat) {
      e.preventDefault()
      start()
    }
  }

  function onKeyup(e) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      cancel()
    }
  }

  // Pointer and keyboard presses never reach here: a real click has detail >= 1,
  // and Enter/Space have their default (the click) prevented above.
  function onClick(e) {
    if (e.detail !== 0 || busy) return
    if (Date.now() < armedUntil) {
      armedUntil = 0
      fire()
      return
    }
    armedUntil = Date.now() + ARM_MS
    say('Press again to stop all agents')
  }

  function onPointerdown(e) {
    if (e.button !== 0) return
    start()
  }

  $effect(() => () => {
    clearTimeout(timer)
    clearTimeout(messageTimer)
  })
</script>

<div class="stop-all">
  <button
    type="button"
    class="btn-stop"
    class:holding
    style:--hold-ms={`${HOLD_MS}ms`}
    disabled={busy}
    aria-describedby={hintId}
    onpointerdown={onPointerdown}
    onpointerup={cancel}
    onpointerleave={cancel}
    onpointercancel={cancel}
    onkeydown={onKeydown}
    onkeyup={onKeyup}
    onclick={onClick}
    onblur={cancel}
    oncontextmenu={(e) => e.preventDefault()}
    data-testid="stop-all"
    data-focus-target
  >
    <svg width="12" height="12" viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="5" width="14" height="14" rx="2" fill="currentColor"/></svg>
    <span>{busy ? 'Stopping…' : holding ? 'Keep holding…' : 'Stop all'}</span>
    <span class="progress" aria-hidden="true"><span class="fill"></span></span>
  </button>
  <span id={hintId} class="sr-only">Hold for {HOLD_MS / 1000} seconds to stop all agents</span>
  <!-- Always mounted: a live region inserted with its text already in it is often not announced. -->
  <span class="msg" class:shown={!!message} class:inline-error={isError} class:hint={!isError} role="status">{message}</span>
</div>

<style>
  .stop-all {
    position: relative;
    display: flex;
    align-items: center;
  }

  .btn-stop {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    /* Wide enough for "Keep holding…" so the bar doesn't shift mid-hold. */
    min-width: 118px;
    padding: 5px 12px;
    background: none;
    border: 1px solid var(--danger);
    border-radius: var(--radius);
    color: var(--danger);
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
    cursor: pointer;
    overflow: hidden;
    touch-action: none;
    user-select: none;
    -webkit-user-select: none;
    -webkit-touch-callout: none;
  }
  .btn-stop:hover:not(:disabled) { background: rgba(196, 58, 58, 0.06); }
  .btn-stop:focus-visible { outline: 2px solid var(--danger); outline-offset: 2px; }
  .btn-stop.holding, .btn-stop.holding:hover:not(:disabled) { background: var(--danger-solid); border-color: var(--danger-solid); color: #fff; }
  .btn-stop:disabled { opacity: 0.6; cursor: not-allowed; }

  .progress {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 3px;
    background: rgba(255, 255, 255, 0.35);
    opacity: 0;
  }
  .fill {
    display: block;
    height: 100%;
    width: 0;
    background: #fff;
  }
  .holding .progress { opacity: 1; }
  .holding .fill {
    width: 100%;
    transition: width var(--hold-ms) linear;
  }

  @media (prefers-reduced-motion: reduce) {
    .holding .fill { transition: none; }
  }

  /* Hangs below the button so the top bar keeps its height. */
  .msg {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    white-space: nowrap;
    background: var(--bg);
    padding: 2px 6px;
    border-radius: var(--radius);
    z-index: 5;
  }
  .msg:not(.shown) { padding: 0; background: none; }
</style>
