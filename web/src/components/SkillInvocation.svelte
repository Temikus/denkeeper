<script>
  // Chooses how a skill preview is invoked.
  //
  // A skill fires three ways and only one of them involves a message: a
  // command: trigger the user types, a schedule that injects a fire-time
  // header, or ambient matching on an ordinary chat turn. Asking for "a
  // message to run this skill against" is wrong for two of the three, so the
  // control defaults to the entry point the skill actually has — which takes
  // the schedules that name it, not just its triggers: ambient and scheduled
  // skills look identical in frontmatter.
  let { skill, timezone = 'UTC', onrun } = $props()

  const command = $derived(
    (skill.triggers || [])
      .filter(t => t.startsWith('command:'))
      .map(t => t.slice('command:'.length))[0] || '',
  )

  // Names of the schedules that fire this skill; empty means nothing does.
  const scheduledBy = $derived(skill.scheduled_by || [])

  let mode = $state('')
  let message = $state('')
  let args = $state('')

  // Mirrors the server's inference so the UI never disagrees with what it is
  // about to ask for.
  const effectiveMode = $derived(
    mode || (command ? 'command' : scheduledBy.length ? 'schedule' : 'message'),
  )

  const modes = $derived([
    { id: 'schedule', label: 'On a schedule' },
    { id: 'message', label: 'As a chat message' },
    { id: 'command', label: 'As a command', disabled: !command },
  ])

  // ---- Fire time ---------------------------------------------------------
  //
  // Every clock calculation below runs in the *agent's* zone, not the
  // browser's: that is the zone the field is labelled with, the zone the
  // scheduled header is stamped in, and the zone whose midnight decides which
  // dated key the skill writes.

  // An unresolvable zone would throw on every render, so a bad [api] timezone
  // degrades to UTC instead of blanking the panel.
  function resolveZone(tz) {
    try {
      new Intl.DateTimeFormat('en-US', { timeZone: tz })
      return tz
    } catch {
      return 'UTC'
    }
  }

  const zone = $derived(resolveZone(timezone))

  // Wall-clock parts of an instant, as that zone reads them.
  function zoneParts(d, tz) {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: tz, hourCycle: 'h23',
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    }).formatToParts(d)
    const p = {}
    for (const { type, value } of parts) p[type] = value
    return p
  }

  function nowIn(tz) {
    const p = zoneParts(new Date(), tz)
    return { date: `${p.year}-${p.month}-${p.day}`, time: `${p.hour}:${p.minute}` }
  }

  // Minutes east of UTC at a given instant — read back off the formatted wall
  // time, so DST is handled without a table.
  function zoneOffset(d, tz) {
    const p = zoneParts(d, tz)
    const wall = Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute, +p.second)
    return (wall - Math.floor(d.getTime() / 1000) * 1000) / 60000
  }

  // The instant a wall time in `tz` names. Solved twice because the first pass
  // uses the offset in effect at the wrong instant — which only differs within
  // an hour of a DST change, and the second pass lands the right side of it.
  function instantIn(date, time, tz) {
    const [y, mo, da] = date.split('-').map(Number)
    const [h, mi] = time.split(':').map(Number)
    const wall = Date.UTC(y, mo - 1, da, h, mi)
    const first = wall - zoneOffset(new Date(wall), tz) * 60000
    return new Date(wall - zoneOffset(new Date(first), tz) * 60000)
  }

  // Split across two native controls rather than one datetime-local: several
  // browsers open a date-only popover for datetime-local, leaving the time half
  // typable but not pickable. Defaults to now so the common case — preview this
  // as if it fired right now — needs no input at all.
  // A deliberate one-shot read: the default is "now, when you opened this",
  // and the panel only mounts once the config that carries the zone has loaded.
  // svelte-ignore state_referenced_locally
  const initial = nowIn(resolveZone(timezone))
  let asOfDate = $state(initial.date)
  let asOfTime = $state(initial.time)

  function resetToNow() {
    const n = nowIn(zone)
    asOfDate = n.date
    asOfTime = n.time
  }

  // Both halves are required: browsers let either be cleared, and a half-filled
  // pair means "unpinned", which is the server's own default.
  const pinned = $derived(!!asOfDate && !!asOfTime)
  const fireTime = $derived(pinned ? instantIn(asOfDate, asOfTime, zone) : new Date())

  function isoWeek(y, m, d) {
    const t = new Date(Date.UTC(y, m - 1, d))
    t.setUTCDate(t.getUTCDate() + 4 - (t.getUTCDay() || 7))
    const start = new Date(Date.UTC(t.getUTCFullYear(), 0, 1))
    return [t.getUTCFullYear(), Math.ceil(((t - start) / 86400000 + 1) / 7)]
  }

  function offset(d, tz) {
    const m = zoneOffset(d, tz)
    const sign = m >= 0 ? '+' : '-'
    const p = n => String(Math.floor(Math.abs(n))).padStart(2, '0')
    return `${sign}${p(m / 60)}:${p(m % 60)}`
  }

  // Rendered client-side purely so the user can see what will be sent before
  // spending tokens; the server builds the real one via FormatScheduledText.
  function scheduledHeader() {
    const p = zoneParts(fireTime, zone)
    const stamp = `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}:${p.second}` +
      offset(fireTime, zone)
    const [y, w] = isoWeek(+p.year, +p.month, +p.day)
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
      ...(pinned ? { as_of: fireTime.toISOString() } : {}),
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
      <label class="row-label" for="dry-run-asof-date">Fire time</label>
      <div class="datetime">
        <input id="dry-run-asof-date" type="date" bind:value={asOfDate}
          aria-label="Fire date" />
        <input id="dry-run-asof-time" type="time" bind:value={asOfTime}
          aria-label="Fire time of day" />
        <button class="btn-sm" onclick={resetToNow}
          title="Reset to the current time in {timezone}">Now</button>
      </div>
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
    {#if effectiveMode === 'schedule' && scheduledBy.length}
      <!-- The server previews the first schedule by name when several fire the
           skill, so naming that one is what will actually run. -->
      <div class="hint">
        No message to write — this is what <code>{scheduledBy[0]}</code> sends when it fires.{scheduledBy.length > 1 ? ` (+${scheduledBy.length - 1} more schedules fire it)` : ''}
      </div>
    {:else if effectiveMode === 'schedule'}
      <div class="hint">No schedule fires {skill.name} — this previews what one would send if you added it.</div>
    {:else if effectiveMode === 'command'}
      <div class="hint">Sent as a real command, so trigger matching is exercised too — not just the skill body.</div>
    {:else if !command && !scheduledBy.length}
      <div class="hint">No <code>command:</code> trigger and no schedule, so this skill matches ordinary turns — it reads whatever the user said.</div>
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

  .datetime { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
  .datetime input {
    background: var(--bg); border: 1px solid var(--border);
    border-radius: var(--radius); color: var(--text);
    padding: 6px 10px; font-size: 13px; font-family: inherit;
  }
  .datetime input:focus { outline: none; border-color: var(--accent); }
  /* Native pickers draw their own chrome — tell them which theme they are in,
     or the popover and its indicator icon come back light on a dark page. */
  .datetime input { color-scheme: light; }
  :global(:root.dark) .datetime input { color-scheme: dark; }
  .datetime .btn-sm { margin-right: 0; }

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
