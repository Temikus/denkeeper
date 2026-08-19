<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from '../api.js'
  import { navigate } from '../router.js'
  import { inert } from '../inert.js'
  import { evalProgress } from '../wsStore.js'
  import { relativeTime } from '../relativeTime.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import ModelSelector from '../components/ModelSelector.svelte'

  // Quick check draws this many test cases; Full eval runs the whole set.
  const QUICK_TASKS = 10
  // Fallbacks for when GET /eval/config is unavailable. They mirror the
  // shipped [eval] defaults.
  const FALLBACK_K = 3
  const FALLBACK_CAP = 2
  const POLL_MS = 4000
  const ESTIMATE_DEBOUNCE_MS = 400

  let loading = $state(true)
  let error = $state('')

  let taskSets = $state([])
  let runs = $state([])
  let agents = $state([])
  let cfg = $state(null)

  // --- Launcher ---
  let baseAgent = $state('')
  let candidate = $state('')
  let candidateProvider = $state('')
  let taskSetName = $state('')
  let preset = $state('quick')
  let costCap = $state('')
  let estimate = $state(null)
  let estimating = $state(false)
  let starting = $state(false)
  let launchError = $state('')

  // --- Import ---
  let showImport = $state(false)
  let importName = $state('')
  let importFile = $state(null)
  let importing = $state(false)
  let importError = $state('')
  let importOk = $state('')

  // --- Runs ---
  let expandedRun = $state(null)
  let confirmStop = $state(null)
  let stopping = $state(false)

  let currentAgent = $derived(agents.find(a => a.name === baseAgent) || null)
  let selectedSet = $derived(taskSets.find(t => t.name === taskSetName) || null)
  let k = $derived(preset === 'quick' ? 1 : (cfg?.default_k || FALLBACK_K))
  let sampleTasks = $derived(preset === 'quick' ? QUICK_TASKS : 0)
  let isEmpty = $derived(!loading && taskSets.length === 0 && runs.length === 0)
  let canStart = $derived(!!baseAgent && !!candidate.trim() && !!taskSetName && !starting)

  function setName(id) {
    return taskSets.find(t => t.id === id)?.name || `#${id}`
  }

  /** Total cases in the run's set, when that set is still around to ask. */
  function setTotal(id) {
    return taskSets.find(t => t.id === id)?.task_count ?? null
  }

  /** A run is live while the server says so, or while its status is non-terminal. */
  function isActive(run) {
    if (typeof run.active === 'boolean') return run.active
    return run.status === 'pending' || run.status === 'running'
  }

  function fmtUSD(v) {
    if (v == null) return '—'
    if (v > 0 && v < 0.01) return '$' + v.toFixed(4)
    return '$' + v.toFixed(2)
  }

  function fmtETA(secs) {
    if (!secs || secs <= 0) return ''
    if (secs < 60) return `${secs}s left`
    const m = Math.round(secs / 60)
    if (m < 60) return `${m}m left`
    return `${Math.round(m / 6) / 10}h left`
  }

  function variantLabel(run) {
    const names = (run.variants || []).map(v => v.name)
    if (names.length === 0) return ''
    return names.join(' vs ')
  }

  const BASIS_LABEL = {
    history: 'from history',
    list_price: 'list price',
  }

  /**
   * The estimate line, or '' when there is nothing honest to say. An unknown
   * basis shows the cap alone rather than a number nobody can stand behind.
   */
  let estimateLabel = $derived.by(() => {
    if (!estimate) return ''
    const basis = BASIS_LABEL[estimate.basis]
    if (!basis) return ''
    if (estimate.low == null || estimate.high == null) return ''
    return `~${fmtUSD(estimate.low)}–${fmtUSD(estimate.high)}, ${basis}`
  })

  async function loadTaskSets() {
    taskSets = (await api.evalTaskSets()) || []
    if (!taskSetName && taskSets.length) taskSetName = taskSets[0].name
  }

  onMount(async () => {
    try {
      const [sets, runList, agentList] = await Promise.all([
        api.evalTaskSets(),
        api.evalRuns(),
        api.agents().catch(() => []),
      ])
      taskSets = sets || []
      runs = runList || []
      agents = agentList || []
      if (taskSets.length) taskSetName = taskSets[0].name
      if (agents.length) baseAgent = agents[0].name
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
    // The config endpoint only supplies defaults, so a failure downgrades to
    // the shipped ones rather than failing the page.
    try {
      cfg = await api.evalConfig()
    } catch {
      cfg = null
    }
    if (!costCap) costCap = String(cfg?.max_cost_per_run ?? FALLBACK_CAP)
    for (const r of runs.filter(isActive)) refreshRun(r.id)
  })

  // --- Estimating -----------------------------------------------------------

  let estimateTimer = null

  async function fetchEstimate() {
    if (!baseAgent || !taskSetName || !candidate.trim()) {
      estimate = null
      return
    }
    estimating = true
    try {
      estimate = await api.evalEstimate({
        task_set: taskSetName,
        base_agent: baseAgent,
        variants: buildVariants(),
        k,
        ...(sampleTasks ? { sample_tasks: sampleTasks } : {}),
      })
    } catch {
      // No estimate is a supported state — the cap stands on its own.
      estimate = null
    } finally {
      estimating = false
    }
  }

  // Every launcher input that changes the price re-asks, debounced so typing a
  // model id does not fire a request per keystroke.
  $effect(() => {
    void baseAgent; void taskSetName; void candidate; void preset; void k; void sampleTasks
    clearTimeout(estimateTimer)
    estimateTimer = setTimeout(fetchEstimate, ESTIMATE_DEBOUNCE_MS)
    return () => clearTimeout(estimateTimer)
  })

  // --- Launching ------------------------------------------------------------

  /**
   * The incumbent is the empty overlay and MUST come first: per-task deltas and
   * blinded pairing both baseline against the first variant by creation order.
   */
  function buildVariants() {
    const cand = { name: candidate.trim(), llm_model: candidate.trim() }
    if (candidateProvider) cand.llm_provider = candidateProvider
    return [{ name: 'current' }, cand]
  }

  async function start() {
    launchError = ''
    starting = true
    try {
      const body = {
        task_set: taskSetName,
        base_agent: baseAgent,
        variants: buildVariants(),
        k,
      }
      if (sampleTasks) body.sample_tasks = sampleTasks
      const cap = parseFloat(costCap)
      if (!Number.isNaN(cap) && cap > 0) body.cost_cap = cap
      const run = await api.createEvalRun(body)
      runs = [run, ...runs]
      refreshRun(run.id)
    } catch (e) {
      launchError = e.message
    } finally {
      starting = false
    }
  }

  // --- Import ---------------------------------------------------------------

  function toggleImport() {
    showImport = !showImport
    importError = ''
    importOk = ''
  }

  function pickFile(e) {
    importFile = e.target.files?.[0] || null
    // A file named work-set.jsonl is almost always the set's name.
    if (importFile && !importName.trim()) {
      importName = importFile.name.replace(/\.jsonl?$/i, '')
    }
  }

  async function doImport() {
    const name = importName.trim()
    if (!name || !importFile) {
      importError = 'Pick a file and name the test set'
      return
    }
    importing = true
    importError = ''
    importOk = ''
    try {
      const text = await importFile.text()
      try {
        await api.createEvalTaskSet({ name })
      } catch (e) {
        // Importing into an existing set is the normal second import.
        if (!/exists|taken|already/i.test(e.message)) throw e
      }
      const res = await api.importEvalTaskSet(name, text)
      const n = res?.imported ?? 0
      importOk = `Imported ${n} test case${n === 1 ? '' : 's'} into "${name}"`
      importFile = null
      await loadTaskSets()
      taskSetName = name
    } catch (e) {
      importError = e.message
    } finally {
      importing = false
    }
  }

  // --- Run status -----------------------------------------------------------

  async function refreshRun(id) {
    try {
      const detail = await api.evalRun(id)
      runs = runs.map(r => (r.id === detail.id ? { ...r, ...detail } : r))
    } catch {
      // A transient read failure leaves the last known progress on screen.
    }
  }

  function refreshActive() {
    for (const r of runs.filter(isActive)) refreshRun(r.id)
  }

  let pollTimer = null

  // Poll only while something is live, so an idle page makes no requests.
  $effect(() => {
    const active = runs.some(isActive)
    if (active && !pollTimer) {
      pollTimer = setInterval(refreshActive, POLL_MS)
    } else if (!active && pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  })

  // A progress frame is a wake-up, not truth: it triggers an immediate re-read
  // of the authoritative GET /eval/runs/{id}.
  let seenProgress = new Map()
  $effect(() => {
    for (const [id, frame] of $evalProgress) {
      const sig = `${frame.status}:${frame.samples_done}:${frame.cost_spent}`
      if (seenProgress.get(id) === sig) continue
      seenProgress.set(id, sig)
      refreshRun(id)
    }
  })

  onDestroy(() => {
    clearInterval(pollTimer)
    clearTimeout(estimateTimer)
  })

  async function doStop() {
    stopping = true
    try {
      const id = confirmStop
      await api.stopEvalRun(id)
      confirmStop = null
      await refreshRun(id)
    } catch (e) {
      error = e.message
      confirmStop = null
    } finally {
      stopping = false
    }
  }

  function toggleResults(id) {
    expandedRun = expandedRun === id ? null : id
  }
</script>

<div class="page-header">
  <h1 class="page-title">Evals</h1>
  {#if !isEmpty}
    <button class="btn-ghost btn-sm" onclick={toggleImport}
      aria-expanded={showImport} aria-controls="eval-import-panel"
      data-testid="import-toggle">Import JSONL</button>
  {/if}
</div>

<ErrorBanner message={error} />

{#if loading}
  <p class="muted">Loading…</p>
{:else if isEmpty}
  <div class="empty-state" data-testid="evals-empty">
    <p class="empty-lead">
      Save real conversations as test cases, then compare your current model against a
      candidate on them.
    </p>
    <div class="empty-actions">
      <button class="btn-primary" onclick={() => navigate('chat')} data-testid="empty-chat-cta">Go to Chat</button>
      <button class="btn-ghost" onclick={toggleImport} data-testid="empty-import-cta"
        aria-expanded={showImport} aria-controls="eval-import-panel">Import JSONL</button>
    </div>
  </div>
{/if}

<div class="inline-panel" id="eval-import-panel" class:open={showImport} use:inert={!showImport}>
  <div class="inline-panel-inner">
    <div class="inline-form" data-testid="import-form">
      <h2 class="form-title">Import test cases</h2>
      {#if importError}<div class="inline-error" role="alert">{importError}</div>{/if}
      {#if importOk}<div class="save-ok" role="status">{importOk}</div>{/if}
      <div class="row">
        <label>
          Test set name
          <input type="text" bind:value={importName} disabled={importing} placeholder="e.g. golden-set" />
          <span class="hint">Created if it does not exist yet.</span>
        </label>
        <label>
          JSONL file
          <input type="file" accept=".jsonl,.json,text/plain" onchange={pickFile} disabled={importing} />
          <span class="hint">One test case per line.</span>
        </label>
      </div>
      <div class="form-actions">
        <button class="btn-primary" onclick={doImport} disabled={importing || !importName.trim() || !importFile}>
          {importing ? 'Importing…' : 'Import'}
        </button>
        <button class="btn-ghost" onclick={toggleImport} disabled={importing}>Close</button>
      </div>
    </div>
  </div>
</div>

{#if !loading && !isEmpty}
  <section class="launcher" data-testid="launcher">
    <h2 class="section-title">Compare current vs candidate</h2>
    {#if launchError}<div class="inline-error" role="alert">{launchError}</div>{/if}

    <div class="launch-grid">
      <label class="field">
        <span class="field-label">Agent</span>
        <select bind:value={baseAgent} disabled={starting} data-testid="agent-select">
          {#each agents as a}
            <option value={a.name}>{a.name}</option>
          {/each}
        </select>
        {#if currentAgent}
          <span class="hint">
            Current: {currentAgent.model || 'default model'}{currentAgent.provider ? ` · ${currentAgent.provider}` : ''}
          </span>
        {/if}
      </label>

      <label class="field">
        <span class="field-label">Candidate</span>
        <ModelSelector bind:value={candidate}
          onchange={(id, provider) => { candidate = id; candidateProvider = provider || '' }} />
        <span class="hint">The model to test against the one running now.</span>
      </label>

      <label class="field">
        <span class="field-label">Test set</span>
        <select bind:value={taskSetName} disabled={starting} data-testid="task-set-select">
          {#each taskSets as t}
            <option value={t.name}>{t.name} ({t.task_count} case{t.task_count === 1 ? '' : 's'})</option>
          {/each}
        </select>
      </label>
    </div>

    <div class="launch-row">
      <div class="field">
        <span class="field-label">Depth</span>
        <div class="filter-chips filter-chips-sm" role="radiogroup" aria-label="Run depth">
          <button class="chip" class:active={preset === 'quick'} role="radio"
            aria-checked={preset === 'quick'} onclick={() => preset = 'quick'}
            data-testid="preset-quick">Quick check</button>
          <button class="chip" class:active={preset === 'full'} role="radio"
            aria-checked={preset === 'full'} onclick={() => preset = 'full'}
            data-testid="preset-full">Full eval</button>
        </div>
        <span class="hint" data-testid="preset-hint">
          {#if preset === 'quick'}
            {QUICK_TASKS} cases, 1 run each — a cheap first signal.
          {:else}
            All {selectedSet?.task_count ?? 0} cases, {k} runs each.
          {/if}
        </span>
      </div>

      <label class="field cap-field">
        <span class="field-label">Cost cap</span>
        <input type="number" min="0" step="0.5" bind:value={costCap} disabled={starting}
          data-testid="cost-cap" aria-label="Cost cap in USD" />
        <span class="hint" data-testid="estimate">
          {#if estimating}
            Estimating…
          {:else if estimateLabel}
            Estimated {estimateLabel}
          {:else}
            The run stops cleanly at the cap and keeps what it produced.
          {/if}
        </span>
        {#if !estimating && estimate?.note}
          <span class="hint" data-testid="estimate-note">{estimate.note}</span>
        {/if}
      </label>

      <div class="field start-field">
        <button class="btn-primary" onclick={start} disabled={!canStart} data-testid="start-run">
          {starting ? 'Starting…' : 'Start'}
        </button>
      </div>
    </div>
  </section>

  <section class="runs">
    <h2 class="section-title">Runs</h2>
    {#if runs.length === 0}
      <p class="muted" data-testid="no-runs">No runs yet. Pick a candidate above to compare it against your current model.</p>
    {/if}
    {#each runs as run (run.id)}
      {@const total = run.samples_total ?? 0}
      {@const done = run.samples_done ?? 0}
      {@const setCases = setTotal(run.task_set_id)}
      <article class="run-card" data-testid="run-{run.id}">
        <header class="run-head">
          <span class="status-chip {run.status}" data-testid="run-status-{run.id}">{run.status}</span>
          <span class="run-title">{variantLabel(run) || 'comparison'}</span>
          <span class="run-sub">on {setName(run.task_set_id)}</span>
          <span class="spacer"></span>
          {#if run.created_at}
            <span class="run-when">{relativeTime(run.created_at)}</span>
          {/if}
          {#if isActive(run)}
            <button class="btn-sm danger" onclick={() => confirmStop = run.id}
              data-testid="stop-{run.id}">Stop</button>
          {:else}
            <button class="btn-sm" onclick={() => toggleResults(run.id)}
              aria-expanded={expandedRun === run.id}
              data-testid="results-{run.id}">
              {expandedRun === run.id ? 'Hide results' : 'Results'}
            </button>
          {/if}
        </header>

        {#if total > 0}
          <div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax={total}
            aria-valuenow={done} aria-label="Run progress">
            <div class="progress-fill" style:width={`${Math.min(100, (done / total) * 100)}%`}></div>
          </div>
        {/if}

        <div class="run-meta">
          <span data-testid="progress-{run.id}">{done} / {total || '?'} turns</span>
          <span>{fmtUSD(run.cost_spent)} of {fmtUSD(run.cost_cap)}</span>
          {#if run.task_count != null && setCases != null && run.task_count < setCases}
            <span data-testid="subset-{run.id}">{run.task_count} of {setCases} test cases</span>
          {/if}
          {#if isActive(run) && fmtETA(run.eta_seconds)}
            <span>{fmtETA(run.eta_seconds)}</span>
          {/if}
          {#if run.error}
            <span class="run-error">{run.error}</span>
          {/if}
        </div>

        {#if expandedRun === run.id}
          <div class="results-panel" data-testid="results-panel-{run.id}">
            <!-- D2: EvalResults mounts here -->
            <p class="muted">
              The scorecard and judged pairs land in the next slice. Until then read
              <code>GET /api/v1/eval/runs/{run.id}/summary</code>.
            </p>
          </div>
        {/if}
      </article>
    {/each}
  </section>
{/if}

{#if confirmStop != null}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmStop = null }}
    onkeydown={(e) => { if (e.key === 'Escape') confirmStop = null }}
    role="dialog" aria-modal="true" tabindex="-1">
    <div class="confirm-modal" data-testid="stop-confirm">
      <h2>Stop run</h2>
      <p>
        Stop this run? Work already finished is kept and stays readable, but the remaining
        comparisons never start.
      </p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={doStop} disabled={stopping}>
          {stopping ? 'Stopping…' : 'Stop run'}
        </button>
        <button class="btn-ghost" onclick={() => confirmStop = null} disabled={stopping}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .section-title {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin: 0 0 12px;
  }

  /* Empty state */
  .empty-state {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 24px;
    max-width: 620px;
  }
  .empty-lead {
    font-size: 14px;
    color: var(--text);
    margin-bottom: 16px;
    line-height: 1.6;
  }
  .empty-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  /* Launcher */
  .launcher {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 18px;
    margin-bottom: 24px;
  }
  .launch-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 16px;
    margin-bottom: 16px;
  }
  .launch-row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 16px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }
  .cap-field { width: 160px; }
  .start-field { justify-content: flex-end; padding-top: 20px; }
  .field-label {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
  }
  .field select,
  .field input[type="number"] {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 6px 10px;
    font-size: 13px;
    width: 100%;
  }
  .field select:focus,
  .field input:focus { outline: none; border-color: var(--accent); }

  .inline-error { color: var(--danger); font-size: 12px; margin-bottom: 10px; }

  /* Runs */
  .run-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 12px 14px;
    margin-bottom: 10px;
  }
  .run-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .spacer { flex: 1; }
  .run-title { font-size: 13px; font-weight: 600; font-family: monospace; }
  .run-sub { font-size: 12px; color: var(--text-muted); }
  .run-when { font-size: 11px; color: var(--text-muted); }

  .status-chip {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-muted);
    text-transform: lowercase;
  }
  .status-chip.running,
  .status-chip.pending { border-color: var(--accent); color: var(--accent); }
  .status-chip.done { border-color: var(--success); color: var(--success); }
  .status-chip.failed { border-color: var(--danger); color: var(--danger); }
  .status-chip.capped,
  .status-chip.stopped { border-color: var(--warn); color: var(--warn); }

  .progress {
    height: 6px;
    background: var(--border);
    border-radius: 999px;
    overflow: hidden;
    margin: 10px 0 6px;
  }
  .progress-fill {
    height: 100%;
    background: var(--accent);
    transition: width 0.3s ease;
  }

  .run-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 14px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .run-error { color: var(--danger); }

  .results-panel {
    margin-top: 12px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
    font-size: 12px;
  }
  code {
    font-family: monospace;
    font-size: 11px;
    background: var(--hover-overlay);
    padding: 1px 5px;
    border-radius: 3px;
  }

  @media (max-width: 520px) {
    .launch-row { flex-direction: column; align-items: stretch; }
    .cap-field { width: 100%; }
    .start-field { padding-top: 0; }
  }
</style>
