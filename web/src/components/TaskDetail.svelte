<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { api } from '../api.js'
  import { navigate } from '../router.js'
  import { watchTasks } from '../taskLive.js'
  import { shortAbsolute } from '../relativeTime.js'
  import {
    STATUS_LABELS, taskKey, memberName, memberKind, memberGroups, eventLine, isClosed, heldFor,
  } from '../tasks.js'
  import ErrorBanner from './ErrorBanner.svelte'
  import TasksOff from './TasksOff.svelte'

  let { id } = $props()

  let detail = $state(null)
  let members = $state([])
  let loading = $state(true)
  let off = $state(false)
  let missing = $state(false)
  let error = $state('')
  let now = $state(Date.now())

  let comment = $state('')
  let commenting = $state(false)
  let commentError = $state('')

  let saving = $state('')
  let railError = $state('')
  let blocking = $state(false)
  let reason = $state('')
  let confirmRelease = $state(false)

  let stop = () => {}
  let clock
  let statusEl = $state()
  let releaseEl = $state()

  // Inline steps open in place, so move focus into them as they appear.
  function autofocus(node) {
    node.focus()
  }

  const t = $derived(detail?.task)
  const groups = $derived(memberGroups(members))
  const kindLabel = { agent: 'den agent', external: 'external' }

  async function load() {
    try {
      const [d, ms] = await Promise.all([api.task(id), api.taskMembers()])
      detail = d
      members = ms
      error = ''
      missing = false
      off = false
    } catch (e) {
      if (e.status === 503) off = true
      else if (e.status === 404) missing = true
      else error = e.message
    } finally {
      loading = false
    }
  }

  onMount(() => {
    load()
    stop = watchTasks(load)
    clock = setInterval(() => { now = Date.now() }, 30000)
  })
  onDestroy(() => {
    stop()
    clearInterval(clock)
  })

  async function patch(field, body) {
    saving = field
    railError = ''
    try {
      await api.updateTask(t.id, body)
      await load()
      return true
    } catch (e) {
      railError = e.message
      return false
    } finally {
      saving = ''
    }
  }

  // A refused change leaves the select showing what was picked, not what the
  // task is, so put the select back.
  async function patchFrom(el, field, body, current) {
    if (!(await patch(field, body))) el.value = current
  }

  function onStatus(e) {
    const next = e.currentTarget.value
    if (next === 'blocked') {
      blocking = true
      return
    }
    blocking = false
    patchFrom(e.currentTarget, 'status', { status: next }, t.status)
  }

  async function markBlocked() {
    if (!reason.trim() || saving !== '') return
    if (await patch('status', { status: 'blocked', reason: reason.trim() })) {
      blocking = false
      reason = ''
    }
  }

  async function cancelBlocked() {
    blocking = false
    reason = ''
    await tick()
    statusEl?.focus()
  }

  async function keepClaim() {
    confirmRelease = false
    await tick()
    releaseEl?.focus()
  }

  async function release() {
    saving = 'holder'
    railError = ''
    try {
      await api.releaseTask(t.id)
      confirmRelease = false
      await load()
    } catch (e) {
      railError = e.message
    } finally {
      saving = ''
    }
  }

  async function postComment() {
    if (!comment.trim() || commenting) return
    commenting = true
    commentError = ''
    try {
      await api.commentTask(t.id, comment.trim())
      comment = ''
      await load()
    } catch (e) {
      commentError = e.message
    } finally {
      commenting = false
    }
  }

  function commentKey(e) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      postComment()
    }
  }

  function reviewLabel(task) {
    if (!task.reviewer) return 'No review step'
    if (task.reviewer === 'human') return "You, before it's done"
    return memberName(task.reviewer, members)
  }
</script>

<nav class="crumbs" aria-label="Breadcrumb">
  <a href="#/tasks" onclick={(e) => { e.preventDefault(); navigate('tasks') }}>Tasks</a>
  <span aria-hidden="true">›</span>
  <span>{t ? taskKey(t.id) : id}</span>
</nav>

{#if off}
  <TasksOff />
{:else if missing}
  <p class="muted">No task {id}. It may have been filed on another server. <a href="#/tasks">Back to the board</a></p>
{:else if loading && !detail}
  <p class="muted">Loading task…</p>
{:else if !t && error}
  <ErrorBanner message={error} />
  <button class="btn-ghost" onclick={load}>Try again</button>
{:else if t}
  <div class="title-row">
    <h1 class="page-title">{t.title}</h1>
    <span class="status status-{t.status}">{STATUS_LABELS[t.status] || t.status}</span>
  </div>
  {#if t.status === 'blocked' && t.blocked_reason}
    <p class="blocked-note">Blocked: {t.blocked_reason}</p>
  {/if}

  <ErrorBanner message={error} />

  <div class="layout">
    <div class="thread">
      <ol class="events">
        {#each detail.events as e (e.id)}
          {#if e.kind === 'comment' || (e.kind === 'created' && t.body)}
            {@const kind = memberKind(e.actor)}
            <li class="comment">
              <div class="comment-head">
                <span>
                  <strong>{e.kind === 'created' ? `${memberName(e.actor, members)} filed this` : memberName(e.actor, members)}</strong>
                  {#if e.kind === 'comment' && kindLabel[kind]}<span class="hint">{kindLabel[kind]}</span>{/if}
                </span>
                <time class="hint" datetime={e.at} title={new Date(e.at).toLocaleString()}>{shortAbsolute(e.at, now)}</time>
              </div>
              <p class="comment-body">{e.kind === 'created' ? t.body : e.body}</p>
            </li>
          {:else}
            <li class="event">
              <span class="dot dot-{e.kind}" aria-hidden="true"></span>
              <span class="event-text">{eventLine(e, members)}</span>
              <time class="hint" datetime={e.at} title={new Date(e.at).toLocaleString()}>{shortAbsolute(e.at, now)}</time>
            </li>
          {/if}
        {/each}
      </ol>

      <form class="composer" onsubmit={(e) => { e.preventDefault(); postComment() }}>
        <label class="sr-only" for="task-comment">Comment</label>
        <textarea
          id="task-comment"
          rows="3"
          placeholder="Comment as you…"
          bind:value={comment}
          onkeydown={commentKey}
          disabled={commenting}
        ></textarea>
        {#if commentError}<p class="inline-error" role="alert">{commentError}</p>{/if}
        <div class="composer-foot">
          <span class="hint">
            {#if t.assignee && t.assignee !== 'human'}
              {memberName(t.assignee, members)} reads it when it next checks this task.
            {:else}
              Added to the thread.
            {/if}
          </span>
          <button type="submit" class="btn-primary" disabled={!comment.trim() || commenting}>
            {commenting ? 'Posting…' : 'Comment'}
          </button>
        </div>
      </form>
    </div>

    <aside class="rail" aria-label="Task details">
      <dl class="fields">
        <div class="field">
          <dt><label for="task-status">Status</label></dt>
          <dd>
            <select id="task-status" bind:this={statusEl} value={blocking ? 'blocked' : t.status} onchange={onStatus} disabled={saving !== ''}>
              {#each Object.entries(STATUS_LABELS) as [value, label]}
                <option {value}>{label}</option>
              {/each}
            </select>
          </dd>
          {#if blocking}
            <dd class="extra">
              <label for="task-reason" class="hint">What's in the way?</label>
              <input id="task-reason" type="text" maxlength="500" bind:value={reason} use:autofocus
                onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); markBlocked() } if (e.key === 'Escape') cancelBlocked() }} />
              <div class="row-actions">
                <button class="btn-primary btn-sm" onclick={markBlocked} disabled={!reason.trim() || saving !== ''}>Mark blocked</button>
                <button class="btn-ghost btn-sm" onclick={cancelBlocked}>Cancel</button>
              </div>
            </dd>
          {/if}
        </div>
        <div class="field">
          <dt><label for="task-assignee">Assignee</label></dt>
          <dd>
            <select id="task-assignee" value={t.assignee} onchange={(e) => patchFrom(e.currentTarget, 'assignee', { assignee: e.currentTarget.value }, t.assignee)} disabled={saving !== ''}>
              {#each groups as g}
                <optgroup label={g.label}>
                  {#each g.members as m}<option value={m.id}>{m.name}</option>{/each}
                </optgroup>
              {/each}
              <optgroup label="People">
                <option value="human">You</option>
                <option value="">Unassigned</option>
              </optgroup>
            </select>
          </dd>
        </div>
        <div class="field">
          <dt>Held by</dt>
          <dd>
            {#if t.holder}
              {memberName(t.holder, members)}{t.held_at ? ` · ${heldFor(t.held_at, now)}` : ''}
              <button class="btn-link release" bind:this={releaseEl} aria-expanded={confirmRelease}
                onclick={() => { confirmRelease = true }} disabled={saving !== '' || confirmRelease}>Release</button>
            {:else}
              <span class="muted">nobody</span>
            {/if}
          </dd>
          {#if confirmRelease && t.holder}
            <dd class="extra" role="group" aria-label="Confirm release">
              <p>Release {memberName(t.holder, members)}'s claim? {t.status === 'in_progress' ? 'The task goes back to To do.' : ''}</p>
              <div class="row-actions">
                <button class="btn-danger btn-sm" onclick={release} disabled={saving !== ''} use:autofocus>{saving === 'holder' ? 'Releasing…' : 'Release'}</button>
                <button class="btn-ghost btn-sm" onclick={keepClaim}>Keep</button>
              </div>
            </dd>
          {/if}
        </div>
        <div class="field">
          <dt>Review</dt>
          <dd>{reviewLabel(t)}</dd>
        </div>
        <div class="field">
          <dt>Filed by</dt>
          <dd>{memberName(t.created_by, members)} · {shortAbsolute(t.created_at, now)}</dd>
        </div>
        {#if detail.parent}
          <div class="field">
            <dt>Parent</dt>
            <dd><a href="#/tasks?id={detail.parent.id}">{taskKey(detail.parent.id)} {detail.parent.title}</a></dd>
          </div>
        {/if}
      </dl>
      {#if railError}<p class="inline-error" role="alert">{railError}</p>{/if}

      {#if detail.children.length > 0}
        <h2 class="rail-head">Subtasks · {detail.children.filter(c => isClosed(c.status)).length} of {detail.children.length} done</h2>
        <ul class="subtasks">
          {#each detail.children as c (c.id)}
            <li>
              <a href="#/tasks?id={c.id}" class:closed={isClosed(c.status)}>{taskKey(c.id)} {c.title}</a>
              <span class="hint">{memberName(c.assignee, members)} · {(STATUS_LABELS[c.status] || c.status).toLowerCase()}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </aside>
  </div>
{/if}

<style>
  .crumbs { display: flex; gap: 6px; font-size: 12px; color: var(--text-muted); margin-bottom: 6px; }
  .crumbs a { color: var(--text-muted); }
  .title-row { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; margin-bottom: 16px; }
  .status {
    padding: 2px 8px;
    border-radius: 10px;
    font-size: 11px;
    font-weight: 600;
    background: var(--border);
    color: var(--text-muted);
  }
  .status-in_progress, .status-done { background: rgba(76,175,125,0.15); color: var(--success); }
  .status-review { background: rgba(var(--accent-rgb), 0.12); color: var(--accent); }
  .status-blocked { background: rgba(224,92,110,0.15); color: var(--danger); }
  .blocked-note { color: var(--danger); font-size: 13px; margin: -8px 0 16px; }

  .layout { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 20px; align-items: start; }
  @media (max-width: 900px) { .layout { grid-template-columns: 1fr; } }

  .events { list-style: none; margin: 0 0 16px; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .comment {
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .comment-head { display: flex; justify-content: space-between; gap: 8px; font-size: 13px; margin-bottom: 6px; }
  .comment-head .hint { margin-left: 6px; }
  .comment-body { font-size: 13px; line-height: 1.5; white-space: pre-wrap; overflow-wrap: anywhere; }
  .event { display: flex; align-items: baseline; gap: 8px; padding: 0 4px; font-size: 13px; color: var(--text-muted); }
  .event-text { flex: 1; overflow-wrap: anywhere; }
  .dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; background: var(--border); transform: translateY(-1px); }
  .dot-subtask, .dot-assigned { background: var(--accent); }
  .dot-status { background: var(--success); }

  .composer {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .composer textarea {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 8px 10px;
    font: inherit;
    font-size: 13px;
    resize: vertical;
  }
  .composer textarea:focus { outline: none; border-color: var(--accent); }
  .composer-foot { display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap; }

  .rail {
    padding: 14px 16px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    font-size: 13px;
  }
  .fields { display: flex; flex-direction: column; gap: 10px; margin: 0; }
  .field { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: 8px; }
  .field dt { color: var(--text-muted); }
  .field dd { margin: 0; text-align: right; overflow-wrap: anywhere; }
  .field select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 4px 6px;
    font-size: 13px;
    max-width: 170px;
  }
  .field dd.extra { flex-basis: 100%; display: flex; flex-direction: column; gap: 6px; text-align: left; }
  .extra input {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 6px 8px;
    font-size: 13px;
  }
  .extra p { font-size: 12px; color: var(--text-muted); }
  .row-actions { display: flex; gap: 6px; }
  .release { margin-left: 6px; }
  .rail-head {
    margin: 16px 0 8px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .subtasks { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
  .subtasks li { display: flex; justify-content: space-between; gap: 8px; }
  .subtasks a { color: var(--text); text-decoration: none; }
  .subtasks a.closed { text-decoration: line-through; color: var(--text-muted); }
</style>
