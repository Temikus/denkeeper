<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { api } from '../api.js'
  import { wsStatus } from '../wsStore.js'
  import { watchTasks } from '../taskLive.js'
  import { relativeTime } from '../relativeTime.js'
  import { COLUMNS, columnOf, taskKey, memberName, isWaitingOnYou, cardMeta } from '../tasks.js'
  import ErrorBanner from './ErrorBanner.svelte'
  import TaskComposer from './TaskComposer.svelte'
  import TasksOff from './TasksOff.svelte'
  import FilterChips from './FilterChips.svelte'
  import { loadCheckIns } from '../checkIn.js'

  const DONE_PREVIEW = 3

  let tasks = $state([])
  let members = $state([])
  let checkIns = $state(null)
  let loading = $state(true)
  let off = $state(false)
  let error = $state('')
  let updatedAt = $state(0)
  let now = $state(Date.now())
  let filter = $state('all')
  let composing = $state(false)
  let showAllDone = $state(false)
  let stop = () => {}
  let clock
  let newBtn = $state()

  async function load() {
    try {
      const [ts, ms, ci] = await Promise.all([api.tasks(), api.taskMembers(), loadCheckIns()])
      tasks = ts
      members = ms
      checkIns = ci
      error = ''
      off = false
      updatedAt = Date.now()
    } catch (e) {
      if (e.status === 503) off = true
      else error = e.message
    } finally {
      loading = false
    }
  }

  onMount(() => {
    load()
    stop = watchTasks(load)
    clock = setInterval(() => { now = Date.now() }, 5000)
  })
  onDestroy(() => {
    stop()
    clearInterval(clock)
  })

  const waiting = $derived(tasks.filter(isWaitingOnYou))

  // A chip for each member who has a task on the board, so the row stays short.
  const memberChips = $derived(
    members.filter(m => m.id !== 'human' && tasks.some(t => t.assignee === m.id))
  )

  const chipItems = $derived([
    { value: 'all', label: 'All' },
    { value: 'waiting', label: waiting.length ? `Waiting on you · ${waiting.length}` : 'Waiting on you' },
    ...memberChips.map(m => ({ value: m.id, label: m.name })),
  ])

  const visible = $derived(
    filter === 'all' ? tasks
      : filter === 'waiting' ? waiting
      : tasks.filter(t => t.assignee === filter)
  )

  const byColumn = $derived(
    Object.fromEntries(COLUMNS.map(c => [c.id, visible.filter(t => columnOf(t.status) === c.id)]))
  )

  const done = $derived(byColumn.done || [])
  const doneShown = $derived(showAllDone ? done : done.slice(0, DONE_PREVIEW))

  const liveLabel = $derived($wsStatus === 'connected' ? 'Live' : 'Checking every 15s')

  // Closing the composer unmounts the focused field, so hand focus back to the
  // button that opened it. A filed task shows under All, whatever filter was on.
  async function closeComposer() {
    composing = false
    await tick()
    newBtn?.focus()
  }

  function filed() {
    filter = 'all'
    closeComposer()
    load()
  }
</script>

<div class="page-header">
  <h1 class="page-title">Tasks</h1>
  {#if !off}
    <button class="btn-primary" bind:this={newBtn} aria-expanded={composing} aria-controls="task-composer"
      onclick={() => { composing = true }}>+ New task</button>
  {/if}
</div>

{#if off}
  <TasksOff />
{:else}
  {#if composing}
    <TaskComposer {members} {checkIns} oncheckin={async () => { checkIns = await loadCheckIns() }} onfiled={filed} oncancel={closeComposer} />
  {/if}

  <ErrorBanner message={error} />

  <div class="toolbar">
    <FilterChips items={chipItems} value={filter} label="Filter tasks" onselect={(v) => { filter = v }} />
    {#if updatedAt}
      <span class="hint live" data-testid="tasks-live">{liveLabel} · updated {relativeTime(updatedAt, now)}</span>
    {/if}
  </div>

  {#if loading}
    <p class="muted">Loading tasks…</p>
  {:else if tasks.length === 0 && !error}
    <div class="empty">
      <p>No tasks yet. File one for yourself, hand one to an agent, or leave it unassigned for anyone to claim.</p>
      {#if !composing}
        <button class="btn-primary" onclick={() => { composing = true }}>File your first task</button>
      {/if}
    </div>
  {:else}
    <div class="board">
      {#each COLUMNS as col (col.id)}
        {@const items = col.id === 'done' ? doneShown : byColumn[col.id]}
        <section class="column" aria-labelledby="col-{col.id}">
          <h2 class="col-head" id="col-{col.id}">
            <span>{col.label}</span>
            <span class="col-count">{col.id === 'done' ? `this week · ${done.length}` : byColumn[col.id].length}</span>
          </h2>
          {#if col.id === 'done'}
            {#if done.length > 0}
              <ul class="done-list">
                {#each items as t (t.id)}
                  <li>
                    <a href="#/tasks?id={t.id}" class:cancelled={t.status === 'cancelled'}>
                      <span class="mono">{taskKey(t.id)}</span> {t.title}
                    </a>
                  </li>
                {/each}
              </ul>
              {#if done.length > DONE_PREVIEW}
                <button class="btn-link show-more" onclick={() => { showAllDone = !showAllDone }}>
                  {showAllDone ? 'Show fewer' : `Show ${done.length - DONE_PREVIEW} more`}
                </button>
              {/if}
            {/if}
          {:else}
            <ul class="cards">
              {#each items as t (t.id)}
                {@const meta = cardMeta(t, now)}
                <li>
                  <a class="card" class:mine={isWaitingOnYou(t)} href="#/tasks?id={t.id}">
                    <span class="card-top">
                      <span class="mono key">{taskKey(t.id)}{t.parent_id ? ` · ↳ ${taskKey(t.parent_id)}` : ''}</span>
                      {#if t.children_total > 0}<span class="hint">subtasks {t.children_done}/{t.children_total}</span>{/if}
                    </span>
                    <span class="card-title">{t.title}</span>
                    {#if isWaitingOnYou(t)}<span class="sr-only">Waiting on you</span>{/if}
                    <span class="card-foot">
                      <span class="who" class:unassigned={!t.assignee} class:you={t.assignee === 'human'}>
                        {#if t.status === 'in_progress' && t.holder}<span class="status-dot green" aria-hidden="true"></span>{/if}
                        {memberName(t.assignee, members)}
                      </span>
                      {#if t.status === 'blocked'}
                        <span class="pill blocked" title={t.blocked_reason}>blocked</span>
                      {:else if meta}
                        <span class="hint">{meta}</span>
                      {/if}
                    </span>
                  </a>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      {/each}
    </div>
  {/if}
{/if}

<style>
  .toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; margin-bottom: 16px; }
  .empty { color: var(--text-muted); font-size: 13px; display: flex; flex-direction: column; align-items: flex-start; gap: 12px; }

  .board {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 16px;
    align-items: start;
  }
  @media (max-width: 1100px) { .board { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 640px)  { .board { grid-template-columns: 1fr; } }

  .col-head {
    display: flex;
    justify-content: space-between;
    margin-bottom: 8px;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .col-count { font-weight: 400; letter-spacing: 0; text-transform: none; }
  .cards, .done-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }

  .card {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    text-decoration: none;
  }
  .card:hover { border-color: var(--text-muted); }
  .card:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  /* Waiting on you: an accent edge, matching the stage 03 mock. */
  .card.mine { border-left: 3px solid var(--accent); }
  .card-top { display: flex; justify-content: space-between; gap: 8px; }
  .key { font-size: 11px; color: var(--text-muted); }
  .card-title { font-size: 14px; font-weight: 600; line-height: 1.35; }
  .card-foot { display: flex; justify-content: space-between; align-items: center; gap: 8px; font-size: 13px; }
  .who.unassigned { font-style: italic; color: var(--text-muted); }
  .who.you { color: var(--accent); font-weight: 600; }
  .pill.blocked { background: rgba(224,92,110,0.15); color: var(--danger); margin: 0; }

  .done-list {
    gap: 6px;
    padding: 10px 12px;
    border: 1px dashed var(--border);
    border-radius: var(--radius);
    font-size: 13px;
  }
  .done-list a { color: var(--text-muted); text-decoration: none; }
  .done-list a:hover { color: var(--text); }
  .done-list a.cancelled { text-decoration: line-through; }
  .show-more { margin-top: 6px; }
</style>
