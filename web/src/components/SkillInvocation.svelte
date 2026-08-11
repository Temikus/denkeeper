<script>
  // Chooses how a skill preview is invoked.
  //
  // A skill fires three ways and only one of them involves a message: a
  // command: trigger the user types, a schedule that injects a fire-time
  // header, or ambient matching on an ordinary chat turn. Asking for "a
  // message to run this skill against" is wrong for two of the three, so the
  // control is driven by the skill's own triggers and defaults to the entry
  // point it actually has.
  let { skill, timezone = 'UTC', onrun } = $props()

  const command = $derived(
    (skill.triggers || [])
      .filter(t => t.startsWith('command:'))
      .map(t => t.slice('command:'.length))[0] || '',
  )

  let mode = $state('')
  let message = $state('')
  let args = $state('')
  let asOf = $state('')

  // Mirrors the server's inference so the UI never disagrees with what it is
  // about to ask for.
  const effectiveMode = $derived(mode || (command ? 'command' : 'schedule'))

  const modes = $derived([
    { id: 'schedule', label: 'On a schedule' },
    { id: 'message', label: 'As a chat message' },
    { id: 'command', label: 'As a command', disabled: !command },
  ])

  function fireTime() {
    return asOf ? new Date(asOf) : new Date()
  }

  function isoWeek(d) {
    const t = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()))
    t.setUTCDate(t.getUTCDate() + 4 - (t.getUTCDay() || 7))
    const start = new Date(Date.UTC(t.getUTCFullYear(), 0, 1))
    return [t.getUTCFullYear(), Math.ceil(((t - start) / 86400000 + 1) / 7)]
  }

  function offset(d) {
    const m = -d.getTimezoneOffset()
    const sign = m >= 0 ? '+' : '-'
    const p = n => String(Math.floor(Math.abs(n))).padStart(2, '0')
    return `${sign}${p(m / 60)}:${p(m % 60)}`
  }

  // Rendered client-side purely so the user can see what will be sent before
  // spending tokens; the server builds the real one via FormatScheduledText.
  function scheduledHeader() {
    const d = fireTime()
    const p = n => String(n).padStart(2, '0')
    const stamp = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T` +
      `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}${offset(d)}`
    const [y, w] = isoWeek(d)
    return `[Scheduled: ${skill.name} | ${stamp} ${timezone} | ${y}-W${String(w).padStart(2, '0')}]`
  }

  // The literal text the agent will receive. Showing it is what makes the
  // no-message case explain itself, and stops the preview quietly diverging
  // from what production sends.
  const outgoing = $derived(
    effectiveMode === 'schedule' ? scheduledHeader()
      : effectiveMode === 'command' ? `/${command}${args.trim() ? ' ' + args.trim() : ''}`
        : message,
  )

  const ready = $derived(effectiveMode !== 'message' || !!message.trim())

  function run() {
    if (!ready) return
    onrun({
      mode: effectiveMode,
      ...(effectiveMode === 'message' ? { message } : {}),
      ...(effectiveMode === 'command' && args.trim() ? { args: args.trim() } : {}),
      ...(asOf ? { as_of: new Date(asOf).toISOString() } : {}),
    })
  }
</script>

<div class="invocation">
  <div class="label">How should this run?</div>

  <div class="filter-chips filter-chips-sm">
    {#each modes as m}
      <button
        class="chip"
        class:active={effectiveMode === m.id}
        disabled={m.disabled}
        aria-pressed={effectiveMode === m.id}
        title={m.disabled ? `${skill.name} has no command: trigger` : ''}
        onclick={() => { mode = m.id }}
      >{m.label}</button>
    {/each}
    {#if !command}
      <span class="hint">&mdash; no <code>command:</code> trigger</span>
    {/if}
  </div>

  {#if effectiveMode === 'schedule'}
    <div class="row">
      <label class="row-label" for="dry-run-asof">Fire time</label>
      <input id="dry-run-asof" type="datetime-local" bind:value={asOf} />
      <span class="hint">{timezone} &middot; pins dated keys the skill writes</span>
    </div>
  {:else if effectiveMode === 'command'}
    <div class="command-row">
      <span class="command-prefix">/{command}</span>
      <input type="text" bind:value={args} placeholder="optional arguments — leave blank to run the bare command" />
    </div>
  {:else}
    <input class="message-input" type="text" bind:value={message}
      placeholder="e.g. summarise what happened yesterday"
      onkeydown={(e) => { if (e.key === 'Enter') run() }} />
  {/if}

  <div class="outgoing">
    <div class="label">The agent will receive</div>
    <pre class="outgoing-body" class:empty={!outgoing}>{outgoing || '(nothing yet)'}</pre>
    {#if effectiveMode === 'schedule'}
      <div class="hint">No message to write — this is what a scheduled run actually sends.</div>
    {:else if effectiveMode === 'command'}
      <div class="hint">Sent as a real command, so trigger matching is exercised too — not just the skill body.</div>
    {/if}
  </div>

  <div>
    <button class="btn-primary" onclick={run} disabled={!ready}>Run</button>
  </div>
</div>

<style>
  .invocation {
    display: flex; flex-direction: column; gap: 10px;
    padding: 14px 16px; border-top: 1px solid var(--border);
  }
  .label {
    font-size: 11px; font-weight: 600; letter-spacing: 0.08em;
    text-transform: uppercase; color: var(--text-muted);
  }
  .hint { font-size: 11px; color: var(--text-muted); align-self: center; }
  .hint code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px;
  }

  .chip:disabled { opacity: 0.45; cursor: not-allowed; border-style: dashed; }
  .chip:disabled:hover { border-color: var(--border); }

  .row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .row-label { font-size: 12px; color: var(--text-muted); }

  .command-row {
    display: flex; align-items: stretch;
    border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden;
  }
  .command-prefix {
    display: flex; align-items: center; padding: 8px 12px;
    background: var(--surface); border-right: 1px solid var(--border);
    color: var(--accent); font-size: 13px; flex-shrink: 0;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .command-row input { flex: 1; border: none; background: transparent; }
  .message-input { width: 100%; }

  .outgoing { display: flex; flex-direction: column; gap: 6px; }
  .outgoing-body {
    font-size: 12px; line-height: 18px; color: var(--text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 10px 12px; margin: 0;
    white-space: pre-wrap; word-break: break-word;
  }
  .outgoing-body.empty { color: var(--text-muted); font-style: italic; }
</style>
