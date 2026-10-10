<script>
  import { onMount, onDestroy, untrack, tick } from 'svelte'
  import { api } from '../api.js'
  import { navigate, currentQuery } from '../router.js'
  import { inert } from '../inert.js'
  import { evalProgress } from '../wsStore.js'
  import { relativeTime } from '../relativeTime.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import FilterChips from '../components/FilterChips.svelte'
  import ModelSelector from '../components/ModelSelector.svelte'
  import EvalResults from '../components/EvalResults.svelte'
  import SuggestCases from '../components/SuggestCases.svelte'
  import GenerateProbes from '../components/GenerateProbes.svelte'
  import EvalTestSets from '../components/EvalTestSets.svelte'
  import EvalCoverage from '../components/EvalCoverage.svelte'
  import { PROBE, inapplicableKinds } from '../evalCategories.js'
  import { pickBaseAgent, recallAgent, rememberAgent, turnsLine } from '../evalLaunch.js'

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
  // The model id the provider was captured for. The candidate field is also
  // free text, so a hand-edit after picking from the list must not keep the
  // old provider — that would measure a model/provider pair nobody chose.
  let providerFor = $state('')
  let taskSetName = $state('')
  let preset = $state('quick')
  let costCap = $state('')
  let estimate = $state(null)
  let estimating = $state(false)
  let starting = $state(false)
  let launchError = $state('')
  // The cap and the reason Full eval repeats each case, kept out of the sentence.
  let showOptions = $state(false)
  // Set by a Compare link from Agents (?agent=&candidate=&provider=).
  let linkedAgent = ''
  let fromLink = $state(false)

  // --- Import ---
  let showImport = $state(false)
  let importName = $state('')
  let importFile = $state(null)
  let importing = $state(false)
  let importError = $state('')
  let importOk = $state('')

  // --- Tabs ---
  // Runs holds the launcher and results; Test sets holds the cases and the
  // panels that fill them. Synced to ?tab=sets so a link can open either.
  const TABS = [
    { value: 'runs', label: 'Runs' },
    { value: 'sets', label: 'Test sets' },
  ]
  let tab = $state('runs')
  // Bumped after any write to a set, so views holding its cases re-read.
  let setsVersion = $state(0)

  let appliedQuery = null
  $effect(() => {
    const q = $currentQuery
    const key = q.toString()
    if (key === appliedQuery) return
    appliedQuery = key
    tab = q.get('tab') === 'sets' ? 'sets' : 'runs'
    untrack(() => applyLaunchLink(q))
  })

  /** Prefills the launcher from a Compare link and shows it. */
  function applyLaunchLink(q) {
    const model = (q.get('candidate') || '').trim()
    const name = q.get('agent') || ''
    if (!model && !name) return
    if (model) {
      candidate = model
      candidateProvider = q.get('provider') || ''
      providerFor = model
      fromLink = true
    }
    if (name) {
      linkedAgent = name
      // Before the agent list loads, onMount applies it instead.
      if (agents.some(a => a.name === name)) baseAgent = name
    }
    tab = 'runs'
  }

  function chooseAgent(name) {
    baseAgent = name
    rememberAgent(name)
  }

  function setTab(next) {
    tab = next
    const q = next === 'sets' ? 'tab=sets' : ''
    // Replaced, not pushed: a tab is a view of this page, and pushing would
    // make Back walk through every arrow-key press.
    history.replaceState(history.state, '', `#/evals${q ? `?${q}` : ''}`)
    appliedQuery = q
    currentQuery.set(new URLSearchParams(q))
  }

  /** Arrow keys move between tabs, per the WAI-ARIA tabs pattern. */
  function tabKeydown(e) {
    const i = TABS.findIndex(t => t.value === tab)
    let j = -1
    if (e.key === 'ArrowRight') j = (i + 1) % TABS.length
    else if (e.key === 'ArrowLeft') j = (i - 1 + TABS.length) % TABS.length
    else if (e.key === 'Home') j = 0
    else if (e.key === 'End') j = TABS.length - 1
    if (j < 0) return
    e.preventDefault()
    setTab(TABS[j].value)
    document.getElementById(`eval-tab-${TABS[j].value}`)?.focus()
  }

  // --- Suggestions and probes ---
  let showSuggest = $state(false)
  let showProbes = $state(false)
  // Narrows the suggestion pass to one kind when opened from a coverage gap.
  let suggestCategory = $state('')

  function toggleSuggest() {
    if (showSuggest) {
      showSuggest = false
      return
    }
    openSuggest('')
  }

  function openSuggest(category) {
    suggestCategory = category
    showSuggest = true
    // Stacked panels push what is below them off screen, so they take turns.
    showImport = false
    showProbes = false
  }

  /** A coverage gap prompt: open the fill path for that kind and show it. */
  function fillGap(category) {
    if (category === PROBE) {
      if (!showProbes) toggleProbes()
      reveal('eval-probes-panel')
    } else {
      openSuggest(category)
      reveal('eval-suggest-panel')
    }
  }

  /** The panels open above the gap prompt that asked for them. */
  function reveal(id) {
    const still = window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches
    document.getElementById(id)?.scrollIntoView?.({ behavior: still ? 'auto' : 'smooth', block: 'start' })
  }

  function toggleProbes() {
    showProbes = !showProbes
    if (showProbes) {
      showImport = false
      showSuggest = false
    }
  }

  // A case accepted into a set changes its count, and may have created the set
  // the launcher should now be pointing at. Shared by both fill paths.
  async function onSuggestAccepted(name) {
    try {
      await afterFill(name)
    } catch { /* the panel already reported the write; the count can lag */ }
  }

  // The first fill ends the empty state, and the panel that did it lives in
  // the Test sets tab, so the page follows it there.
  async function afterFill(name) {
    const wasEmpty = isEmpty
    await loadTaskSets()
    if (name) taskSetName = name
    setsVersion++
    if (wasEmpty && !isEmpty) setTab('sets')
  }

  /** After a case or set is deleted from the Test sets tab. */
  async function onSetsChanged(deleted) {
    try {
      await loadTaskSets()
    } catch (e) {
      error = e.message
    }
    if (deleted || !taskSets.some(t => t.name === taskSetName)) {
      taskSetName = taskSets[0]?.name || ''
    }
    setsVersion++
  }

  // The compared agent's detail says which kinds it can produce at all, so
  // coverage does not report a gap nothing could fill.
  let agentDetail = $state(null)
  let detailSeq = 0
  $effect(() => {
    const name = baseAgent
    const seq = ++detailSeq
    agentDetail = null
    if (!name) return
    api.agent(name)
      .then(d => { if (seq === detailSeq) agentDetail = d })
      // Without it every kind counts, which is the old behaviour.
      .catch(() => {})
  })
  let notApplicable = $derived(inapplicableKinds(agentDetail))

  // The launcher's set, read for its mix of kinds. Only while the Runs tab is
  // showing: the Test sets tab reads the same set itself.
  let launchTasks = $state(null)
  let launchSeq = 0
  $effect(() => {
    void setsVersion
    const name = taskSetName
    // Invalidates any read in flight, so another set's counts never land.
    const seq = ++launchSeq
    launchTasks = null
    if (!name || tab !== 'runs') return
    api.evalTaskSet(name)
      .then(d => { if (seq === launchSeq) launchTasks = d?.tasks || [] })
      // The line is a hint; without it the launcher still works.
      .catch(() => { if (seq === launchSeq) launchTasks = null })
  })

  // The launcher section, so an escalation from a result can move focus and
  // the viewport back to it.
  let launcherEl = $state(null)

  // --- Runs ---
  let expandedRun = $state(null)
  let confirmStop = $state(null)
  let stopping = $state(false)
  let stopError = $state('')
  // Runs whose status reads have failed repeatedly, so the card can say the
  // progress it shows is stale instead of silently freezing.
  let staleRuns = $state(new Set())

  let currentAgent = $derived(agents.find(a => a.name === baseAgent) || null)
  let selectedSet = $derived(taskSets.find(t => t.name === taskSetName) || null)
  let k = $derived(preset === 'quick' ? 1 : (cfg?.default_k || FALLBACK_K))
  let sampleTasks = $derived(preset === 'quick' ? QUICK_TASKS : 0)
  let fullK = $derived(cfg?.default_k || FALLBACK_K)
  // A Quick check over a set of ten or fewer runs every case.
  let runTasks = $derived.by(() => {
    const n = selectedSet?.task_count ?? 0
    return preset === 'quick' ? Math.min(QUICK_TASKS, n) : n
  })
  let turns = $derived.by(() => {
    // The estimate is the server's own count, so it wins once it is current.
    const known = !estimating && estimate?.tasks
    return turnsLine({
      tasks: known ? estimate.tasks : runTasks,
      k: known && estimate.k ? estimate.k : k,
      whole: preset === 'full',
    })
  })
  // What the run will stop at: the cap typed in Options, else the server default.
  let capUSD = $derived.by(() => {
    const v = parseFloat(costCap)
    return !Number.isNaN(v) && v > 0 ? v : (cfg?.max_cost_per_run ?? FALLBACK_CAP)
  })
  let presetName = $derived(preset === 'quick' ? 'Quick check' : 'Full eval')
  let isEmpty = $derived(!loading && taskSets.length === 0 && runs.length === 0)
  // The three-step checklist stands in for an empty page until the first run.
  let showChecklist = $derived(!loading && runs.length === 0)
  let totalCases = $derived(taskSets.reduce((n, t) => n + (t.task_count || 0), 0))
  let hasCases = $derived(totalCases > 0)
  // The tablist renders only once loaded and non-empty.
  let hasTabs = $derived(!loading && !isEmpty)
  let canStart = $derived(!!baseAgent && !!candidate.trim() && !!taskSetName && !starting)

  /** Names the input still missing, so a disabled Start says why. */
  let startBlocker = $derived.by(() => {
    if (!baseAgent) return 'No agent to compare on.'
    if (!taskSetName) return 'Import a test set first.'
    if (!candidate.trim()) return 'Pick a candidate model to compare.'
    return ''
  })

  // Operator-facing names for the run statuses. "capped" especially: the API
  // word does not say what happened to the money.
  const STATUS_LABEL = {
    pending: 'queued',
    running: 'running',
    done: 'finished',
    capped: 'stopped at cost cap',
    stopped: 'stopped',
    failed: 'failed',
  }

  function statusLabel(status) {
    return STATUS_LABEL[status] || status
  }

  function setName(id) {
    return taskSets.find(t => t.id === id)?.name || `#${id}`
  }

  /**
   * A Quick check drew a subset of the set. task_ids is the authoritative
   * signal — the server records the drawn ids and leaves it unset for a whole-
   * set run — so a Quick check over a set of ten or fewer cases is still
   * recognised. Comparing the run's task count against the set's current size
   * would instead call an old full run a Quick check as soon as a case is
   * added to the set.
   */
  function isQuickCheck(run) {
    return Array.isArray(run.task_ids) && run.task_ids.length > 0
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
  let estimateRange = $derived.by(() => {
    if (!estimate || !BASIS_LABEL[estimate.basis]) return ''
    if (estimate.low == null || estimate.high == null) return ''
    return `${fmtUSD(estimate.low)}–${fmtUSD(estimate.high)}`
  })
  let estimateBasis = $derived(estimateRange ? BASIS_LABEL[estimate.basis] : '')

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
      baseAgent = pickBaseAgent(agents, linkedAgent, recallAgent())
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
    // Hydrate every run, not just the live ones: the list endpoint carries the
    // bare run row, while the variants a run compared and its turn counts live
    // on the detail. Without this a finished run renders nameless.
    await Promise.all(runs.map(r => refreshRun(r.id)))
  })

  // --- Estimating -----------------------------------------------------------

  let estimateTimer = null
  // Set once the endpoint answers 404, so a server without it is asked once
  // rather than on every keystroke for the life of the page.
  let estimateUnavailable = $state(false)

  async function fetchEstimate() {
    if (estimateUnavailable || !baseAgent || !taskSetName || !candidate.trim()) {
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
    } catch (e) {
      // No estimate is a supported state — the cap stands on its own.
      estimate = null
      if (/404/.test(e.message)) estimateUnavailable = true
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
    const model = candidate.trim()
    const cand = { name: model, llm_model: model }
    // Only send the provider the operator actually picked this model from.
    if (candidateProvider && providerFor === model) cand.llm_provider = candidateProvider
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

  /**
   * A checklist fill button. With sets on the page the panels live in the Test
   * sets tab, so the page moves there and opens the panel rather than toggling.
   */
  async function fillFromChecklist(which) {
    const moving = !isEmpty && tab !== 'sets'
    if (moving) {
      setTab('sets')
      showSuggest = showProbes = showImport = false
    }
    if (which === 'suggest') toggleSuggest()
    else if (which === 'probes') toggleProbes()
    else toggleImport()
    if (!moving) return
    // The clicked button is gone with the Runs tab, so focus follows the panel.
    await tick()
    const panel = document.getElementById(`eval-${which}-panel`)
    const target = panel?.querySelector('button, input, select, textarea') || document.getElementById('eval-tab-sets')
    target?.focus()
  }

  async function toggleOptions() {
    showOptions = !showOptions
    if (!showOptions) return
    await tick()
    document.querySelector('[data-testid="cost-cap"]')?.focus()
  }

  function toggleImport() {
    showImport = !showImport
    if (showImport) {
      showSuggest = false
      showProbes = false
    }
    importError = ''
    importOk = ''
  }

  let fileEl = $state(null)

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
      // Importing into an existing set is the normal second import. Ask the
      // list we already hold rather than matching the server's error prose.
      if (!taskSets.some(t => t.name === name)) {
        await api.createEvalTaskSet({ name })
      }
      const res = await api.importEvalTaskSet(name, text)
      const n = res?.imported ?? 0
      importOk = `Imported ${n} test case${n === 1 ? '' : 's'} into "${name}"`
      // Clear the control too, not just the state — a picker still showing a
      // filename beside a disabled Import button reads as a broken button.
      importFile = null
      if (fileEl) fileEl.value = ''
      await afterFill(name)
    } catch (e) {
      importError = e.message
    } finally {
      importing = false
    }
  }

  // --- Run status -----------------------------------------------------------

  // Consecutive failed status reads per run. One blip is nothing; a card that
  // silently freezes while polling every few seconds is a lie.
  const STALE_AFTER = 3
  let readFailures = new Map()

  async function refreshRun(id) {
    try {
      const detail = await api.evalRun(id)
      runs = runs.map(r => (r.id === detail.id ? { ...r, ...detail } : r))
      readFailures.delete(id)
      if (staleRuns.has(id)) {
        staleRuns = new Set([...staleRuns].filter(x => x !== id))
      }
    } catch {
      const n = (readFailures.get(id) || 0) + 1
      readFailures.set(id, n)
      // The last known progress stays on screen, but stops claiming to be live.
      if (n >= STALE_AFTER && !staleRuns.has(id)) {
        staleRuns = new Set(staleRuns).add(id)
      }
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
    stopError = ''
    try {
      const id = confirmStop
      await api.stopEvalRun(id)
      confirmStop = null
      await refreshRun(id)
    } catch (e) {
      // Reported inside the dialog and the dialog stays open: the run card may
      // be screens away, so closing on failure would look like it worked.
      stopError = e.message
    } finally {
      stopping = false
    }
  }

  function askStop(id) {
    stopError = ''
    confirmStop = id
  }

  /**
   * Re-reads the one agent that changed so "current" reflects a just-applied
   * model. Only that agent: a full list re-read would also discard whatever
   * the rest of the page holds for the others.
   */
  async function reloadAgents(name) {
    try {
      const fresh = await api.agent(name)
      agents = agents.map(a => (a.name === name ? { ...a, ...fresh } : a))
    } catch {
      // The banner in the results view already reported the applied change;
      // a stale "current" line is not worth failing the page over.
    }
  }

  /** Refills the launcher from a result and switches it to the full preset. */
  function runFull(pick) {
    launchError = ''
    candidate = pick.model
    candidateProvider = pick.provider || ''
    providerFor = pick.model
    if (pick.taskSet) {
      if (taskSets.some(t => t.name === pick.taskSet)) {
        taskSetName = pick.taskSet
      } else {
        // Launching against a different corpus than the quick check ran on is
        // not the same comparison, so say so rather than silently keeping
        // whatever set the select happened to hold.
        launchError = `The test set that quick check used ("${pick.taskSet}") is no longer available — pick one below.`
      }
    }
    preset = 'full'
    // The button that had focus lives in the results panel, so focus moves to
    // the launcher explicitly; without it a keyboard user lands on <body>.
    launcherEl?.focus?.({ preventScroll: true })
    // A JS scroll ignores the stylesheet's reduced-motion block on its own.
    const still = window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches
    launcherEl?.scrollIntoView?.({ behavior: still ? 'auto' : 'smooth', block: 'start' })
  }

  function toggleResults(id) {
    expandedRun = expandedRun === id ? null : id
  }

  /** Focuses the confirm dialog so its Escape handler and SR focus work. */
  function focusOnMount(node) {
    node.focus()
  }
</script>

<div class="page-header">
  <h1 class="page-title">Evals</h1>
</div>

<ErrorBanner message={error} />

{#if loading}
  <p class="muted">Loading…</p>
{:else}
  {#if showChecklist}
    <section class="checklist" data-testid="eval-checklist" aria-labelledby="checklist-title">
      <h2 class="checklist-title" id="checklist-title" data-testid="checklist-title">
        {#if fromLink && candidate.trim() && baseAgent}
          Is {candidate.trim()} better for {baseAgent}? Three steps.
        {:else}
          Find out whether another model would serve an agent better. Three steps.
        {/if}
      </h2>
      <!-- role="list": Safari drops list semantics under list-style: none. -->
      <ol class="steps" role="list">
        <li class="step" class:done={hasCases} class:current={!hasCases} data-testid="step-build">
          <span class="step-num" aria-hidden="true">{hasCases ? '✓' : '1'}</span>
          <div class="step-body">
            <span class="step-name">
              Build a test set{hasCases ? ` · ${totalCases} case${totalCases === 1 ? '' : 's'}` : ''}
              <span class="sr-only">{hasCases ? '(done)' : '(next)'}</span>
            </span>
            {#if !hasCases}
              <span class="hint">
                Real conversations make the best cases. Suggest them from history, generate
                probes from the agent's setup, save turns from Chat, or import JSONL.
              </span>
              {#if isEmpty || tab === 'runs'}
                <div class="step-actions">
                  <button class="btn-primary btn-sm" onclick={() => fillFromChecklist('suggest')}
                    aria-expanded={isEmpty ? showSuggest : undefined}
                    aria-controls={isEmpty ? 'eval-suggest-panel' : undefined}
                    data-testid="empty-suggest-cta">Suggest from history</button>
                  <button class="btn-ghost btn-sm" onclick={() => fillFromChecklist('probes')} disabled={!baseAgent}
                    title={baseAgent ? undefined : 'Configure an agent first — probes come from its configuration'}
                    aria-expanded={isEmpty ? showProbes : undefined}
                    aria-controls={isEmpty ? 'eval-probes-panel' : undefined}
                    data-testid="empty-probes-cta">Generate probes</button>
                  <button class="btn-ghost btn-sm" onclick={() => navigate('chat')} data-testid="empty-chat-cta">Go to Chat</button>
                  <button class="btn-ghost btn-sm" onclick={() => fillFromChecklist('import')}
                    aria-expanded={isEmpty ? showImport : undefined}
                    aria-controls={isEmpty ? 'eval-import-panel' : undefined}
                    data-testid="empty-import-cta">Import JSONL</button>
                </div>
              {/if}
            {/if}
          </div>
        </li>
        <li class="step" class:current={hasCases} data-testid="step-quick">
          <span class="step-num" aria-hidden="true">2</span>
          <div class="step-body">
            <span class="step-name">
              Run a Quick check{hasCases && estimateRange ? ` · ${estimateRange}` : ''}
              {#if hasCases}<span class="sr-only">(next)</span>{/if}
            </span>
            <span class="hint">
              {#if hasCases}
                Pick the candidate below and start it. Up to ten cases, one run each, for a cheap first signal.
              {:else}
                Up to ten cases, one run each, for a cheap first signal. The launcher appears once a set exists.
              {/if}
            </span>
          </div>
        </li>
        <li class="step" data-testid="step-verdict">
          <span class="step-num" aria-hidden="true">3</span>
          <div class="step-body">
            <span class="step-name">Read the verdict</span>
            <span class="hint">If the candidate wins, apply it to the agent from the results.</span>
          </div>
        </li>
      </ol>
    </section>
  {/if}
  {#if !isEmpty}
    <div class="tabs" role="tablist" aria-label="Evals views">
      {#each TABS as t (t.value)}
        <button class="tab" class:active={tab === t.value} role="tab" id="eval-tab-{t.value}"
          aria-selected={tab === t.value} aria-controls="eval-tabpanel"
          tabindex={tab === t.value ? 0 : -1}
          onclick={() => setTab(t.value)} onkeydown={tabKeydown}
          data-testid="tab-{t.value}">{t.label}</button>
      {/each}
    </div>
  {/if}
{/if}

<div id="eval-tabpanel" role={hasTabs ? 'tabpanel' : undefined}
  aria-labelledby={hasTabs ? `eval-tab-${tab}` : undefined}>
{#if !loading && !isEmpty && tab === 'sets'}
  <div class="add-cases" data-testid="add-cases">
    <span class="field-label">Add cases</span>
    <button class="btn-ghost btn-sm" onclick={toggleSuggest}
      aria-expanded={showSuggest} aria-controls="eval-suggest-panel"
      data-testid="suggest-toggle">Suggest from history</button>
    <button class="btn-ghost btn-sm" onclick={toggleProbes} disabled={!baseAgent}
      title={baseAgent ? undefined : 'Configure an agent first — probes come from its configuration'}
      aria-expanded={showProbes} aria-controls="eval-probes-panel"
      data-testid="probes-toggle">Generate probes</button>
    <button class="btn-ghost btn-sm" onclick={toggleImport}
      aria-expanded={showImport} aria-controls="eval-import-panel"
      data-testid="import-toggle">Import JSONL</button>
    <button class="btn-ghost btn-sm" onclick={() => navigate('chat')}
      data-testid="chat-toggle">Save from Chat</button>
  </div>
{/if}

{#if isEmpty || tab === 'sets'}
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
          <input type="file" accept=".jsonl,.json,text/plain" onchange={pickFile}
            disabled={importing} bind:this={fileEl} />
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

<!-- Collapses like the import panel above it. The wrapper always exists so the
     triggers' aria-controls resolves; the component itself is gated, so nothing
     is fetched until the panel is opened. -->
<div class="inline-panel" id="eval-suggest-panel" class:open={showSuggest} use:inert={!showSuggest}>
  <div class="inline-panel-inner">
    {#if showSuggest}
      <SuggestCases
        agent={baseAgent}
        category={suggestCategory}
        sets={taskSets}
        defaultSet={taskSetName}
        onaccepted={onSuggestAccepted}
        onclose={() => (showSuggest = false)}
      />
    {/if}
  </div>
</div>

<!-- The top-down fill path, collapsing like the two above it. -->
<div class="inline-panel" id="eval-probes-panel" class:open={showProbes} use:inert={!showProbes}>
  <div class="inline-panel-inner">
    {#if showProbes}
      <GenerateProbes
        agent={baseAgent}
        sets={taskSets}
        defaultSet={taskSetName}
        onaccepted={onSuggestAccepted}
        onclose={() => (showProbes = false)}
      />
    {/if}
  </div>
</div>
{/if}

{#if !loading && !isEmpty && tab === 'sets'}
  <EvalTestSets
    sets={taskSets}
    bind:selected={taskSetName}
    version={setsVersion}
    onchanged={onSetsChanged}
    onfill={fillGap}
    canProbe={!!baseAgent}
    {notApplicable}
    agent={baseAgent} />
{/if}

{#if !loading && !isEmpty && tab === 'runs'}
  <section class="launcher" data-testid="launcher" bind:this={launcherEl} tabindex="-1"
    aria-labelledby="launcher-title">
    <h2 class="section-title" id="launcher-title">Compare models</h2>
    {#if launchError}<div class="inline-error" role="alert">{launchError}</div>{/if}

    <!-- One sentence with the inputs inline; it wraps at narrow widths. -->
    <div class="sentence" data-testid="launch-sentence">
      <span class="word">Compare</span>
      <select class="inline-select" value={baseAgent} disabled={starting}
        onchange={(e) => chooseAgent(e.currentTarget.value)}
        aria-label="Agent" data-testid="agent-select">
        {#each agents as a}
          <option value={a.name}>{a.name}</option>
        {/each}
      </select>
      {#if currentAgent}
        <span class="word muted" data-testid="current-model">
          (now {currentAgent.model || 'its default model'}{currentAgent.provider ? ` · ${currentAgent.provider}` : ''})
        </span>
      {/if}
      <span class="word">against</span>
      <div class="candidate">
        <ModelSelector bind:value={candidate} ariaLabel="Candidate model"
          onchange={(id, provider) => { candidate = id; candidateProvider = provider || ''; providerFor = id }} />
      </div>
      <span class="word">on</span>
      <select class="inline-select" bind:value={taskSetName} disabled={starting}
        aria-label="Test set" data-testid="task-set-select">
        {#each taskSets as t}
          <option value={t.name}>{t.name} ({t.task_count} case{t.task_count === 1 ? '' : 's'})</option>
        {/each}
      </select>
      <span class="word">as a</span>
      <FilterChips
        items={[
          { value: 'quick', label: 'Quick check', testid: 'preset-quick' },
          { value: 'full', label: 'Full eval', testid: 'preset-full' },
        ]}
        value={preset}
        label="Run depth"
        size="sm"
        onselect={(v) => preset = v} />
    </div>

    {#if launchTasks}
      <EvalCoverage tasks={launchTasks} compact {notApplicable}
        onsee={() => setTab('sets')} />
    {/if}

    <div class="estimate-block">
      <div class="estimate" data-testid="estimate" aria-live="polite">
        {#if estimating}
          <span class="estimate-pending">Estimating…</span>
        {:else if estimateRange}
          <span class="estimate-figure">{estimateRange}</span>
          <span class="estimate-cap">· stops at {fmtUSD(capUSD)}</span>
          <span class="hint estimate-basis">{estimateBasis}</span>
        {:else}
          <span class="hint">No estimate yet. The run stops cleanly at the cap ({fmtUSD(capUSD)}) and keeps what it produced.</span>
        {/if}
      </div>
      {#if !estimating && estimate?.note}
        <span class="hint" data-testid="estimate-note">{estimate.note}</span>
      {/if}
      {#if turns}
        <span class="hint" data-testid="preset-hint">{turns}</span>
      {/if}
    </div>

    <div class="launch-actions">
      <button class="btn-primary" onclick={start} disabled={!canStart} data-testid="start-run">
        {starting ? 'Starting…' : `Start ${presetName}`}
      </button>
      <button class="btn-ghost btn-sm" onclick={toggleOptions}
        aria-expanded={showOptions} aria-controls="launch-options"
        data-testid="options-toggle">Options</button>
      {#if startBlocker && !starting}
        <span class="hint" data-testid="start-blocker">{startBlocker}</span>
      {/if}
    </div>

    <div class="launch-options" id="launch-options" hidden={!showOptions} data-testid="launch-options">
      <label class="field cap-field">
        <span class="field-label">Cost cap (USD)</span>
        <input type="number" min="0.01" step="0.5" bind:value={costCap} disabled={starting}
          data-testid="cost-cap" />
        <span class="hint">The run stops cleanly here and keeps what it produced.</span>
      </label>
      <p class="hint">
        Full eval runs each case {fullK} times, so one lucky reply can't decide the verdict.
      </p>
    </div>
  </section>

  {#if runs.length > 0}
  <section class="runs">
    <h2 class="section-title">Runs</h2>
    {#each runs as run (run.id)}
      {@const total = run.samples_total ?? 0}
      {@const done = run.samples_done ?? 0}
      {@const setCases = setTotal(run.task_set_id)}
      <article class="run-card" data-testid="run-{run.id}">
        <header class="run-head">
          <span class="status-chip {run.status}" data-testid="run-status-{run.id}">{statusLabel(run.status)}</span>
          {#if variantLabel(run)}
            <span class="run-title">{variantLabel(run)}</span>
          {/if}
          <span class="run-sub">on {setName(run.task_set_id)}</span>
          <span class="spacer"></span>
          {#if run.created_at}
            <span class="run-when">{relativeTime(run.created_at)}</span>
          {/if}
          {#if isActive(run)}
            <button class="btn-sm danger" onclick={() => askStop(run.id)}
              data-testid="stop-{run.id}">Stop</button>
          {:else}
            <button class="btn-sm" onclick={() => toggleResults(run.id)}
              aria-expanded={expandedRun === run.id}
              aria-controls="results-panel-{run.id}"
              data-testid="results-{run.id}">
              {expandedRun === run.id ? 'Hide results' : 'Results'}
            </button>
          {/if}
        </header>

        {#if total > 0}
          <div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax={total}
            aria-valuenow={done} aria-valuetext="{done} of {total} turns" aria-label="Run progress">
            <div class="progress-fill" style:width={`${Math.min(100, (done / total) * 100)}%`}></div>
          </div>
        {/if}

        <div class="run-meta">
          {#if total > 0}
            <span data-testid="progress-{run.id}">{done} / {total} turns</span>
          {/if}
          <span>{fmtUSD(run.cost_spent)} of {fmtUSD(run.cost_cap)}</span>
          {#if run.task_count != null && setCases != null && run.task_count < setCases}
            <span data-testid="subset-{run.id}">{run.task_count} of {setCases} test cases</span>
          {/if}
          {#if isActive(run) && fmtETA(run.eta_seconds)}
            <span>{fmtETA(run.eta_seconds)}</span>
          {/if}
          {#if staleRuns.has(run.id)}
            <span class="run-stale" data-testid="stale-{run.id}">Progress unavailable — showing the last reading</span>
          {/if}
          {#if run.error}
            <span class="run-error">{run.error}</span>
          {/if}
        </div>

        {#if expandedRun === run.id}
          <div class="results-panel" id="results-panel-{run.id}" data-testid="results-panel-{run.id}">
            <EvalResults {run}
              agent={agents.find(a => a.name === run.base_agent) || null}
              quick={isQuickCheck(run)}
              judgeModel={cfg?.judge_model || ''}
              judgeDecider={cfg?.judge_decider || ''}
              judgeCostCap={cfg?.judge_max_cost_per_run || 0}
              onapplied={reloadAgents}
              onrunfull={runFull} />
          </div>
        {/if}
      </article>
    {/each}
  </section>
  {/if}
{/if}
</div>

{#if confirmStop != null}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmStop = null }}
    onkeydown={(e) => { if (e.key === 'Escape') confirmStop = null }}
    role="dialog" aria-modal="true" aria-labelledby="stop-run-title"
    tabindex="-1" use:focusOnMount>
    <div class="confirm-modal" data-testid="stop-confirm">
      <h2 id="stop-run-title">Stop run</h2>
      <p>
        Stop this run? Work already finished is kept and stays readable, but the remaining
        comparisons never start.
      </p>
      {#if stopError}
        <div class="inline-error" role="alert" data-testid="stop-error">{stopError}</div>
      {/if}
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
  /* Matches the local declaration every other inline form on the dashboard
     carries (Schedules, Skills, Providers). */
  .form-title {
    font-size: 16px;
    font-weight: 600;
    margin-bottom: 16px;
  }

  .section-title {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin: 0 0 12px;
  }

  /* Checklist: stands in for an empty page until the first run. */
  .checklist {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    /* Shares --card-inset with .launcher and SuggestCases' .suggest so the
       card lines up with whichever panel opens under it at every width. */
    padding: var(--card-inset);
    margin-bottom: 24px;
  }
  .checklist-title {
    font-size: 15px;
    font-weight: 600;
    margin: 0 0 14px;
    overflow-wrap: anywhere;
  }
  .steps {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .step {
    display: flex;
    gap: 12px;
    align-items: flex-start;
  }
  .step-num {
    flex-shrink: 0;
    width: 24px;
    height: 24px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 12px;
    font-weight: 600;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .step.current .step-num { border-color: var(--accent); color: var(--accent); }
  .step.done .step-num { border-color: var(--success); color: var(--success); }
  .step-body {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
    /* Prose stays readable on a wide card. */
    max-width: 620px;
  }
  .step-name { font-size: 13px; font-weight: 600; padding-top: 3px; }
  .step:not(.current):not(.done) .step-name { color: var(--text-muted); }
  .step-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 6px;
  }

  /* Tabs: an underline bar, since the page has one level of navigation. */
  .tabs {
    display: flex;
    gap: 4px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 20px;
  }
  .tab {
    border: none;
    background: none;
    padding: 8px 12px;
    margin-bottom: -1px;
    border-bottom: 2px solid transparent;
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
  }
  .tab:hover { color: var(--text); }
  .tab.active { color: var(--accent); border-bottom-color: var(--accent); }
  .tab:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

  .add-cases {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin-bottom: 16px;
  }
  .add-cases .field-label { margin-right: 4px; }

  /* Launcher */
  .launcher {
    background: var(--surface);
    /* Focused programmatically after an escalation; the ring would be noise. */
    outline: none;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: var(--card-inset);
    margin-bottom: 24px;
  }
  .sentence {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    font-size: 14px;
    line-height: 1.5;
    margin-bottom: 12px;
  }
  .word { white-space: nowrap; }
  .word.muted { color: var(--text-muted); font-size: 13px; white-space: normal; overflow-wrap: anywhere; }
  .candidate { flex: 1 1 240px; min-width: 0; max-width: 360px; }
  .inline-select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 5px 8px;
    font-size: 13px;
    max-width: 100%;
  }
  .inline-select:focus { outline: none; border-color: var(--accent); }
  .inline-select:disabled { opacity: 0.5; cursor: not-allowed; }

  .estimate-block {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin: 16px 0;
  }
  .estimate {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 6px;
  }
  /* The second-loudest thing on the card, after the sentence. */
  .estimate-figure { font-size: 22px; font-weight: 700; font-variant-numeric: tabular-nums; }
  .estimate-cap { font-size: 13px; color: var(--text-muted); }
  .estimate-pending { font-size: 13px; color: var(--text-muted); }

  .launch-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
  }
  .launch-options {
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .launch-options[hidden] { display: none; }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }
  .cap-field { width: 160px; }
  .field-label {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
  }
  .field input[type="number"] {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 6px 10px;
    font-size: 13px;
    width: 100%;
  }
  .field input:focus { outline: none; border-color: var(--accent); }

  .inline-error { margin-bottom: 10px; }

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
  /* Model ids are long unbreakable tokens; without this the card scrolls
     sideways at 320px. */
  .run-title {
    font-size: 13px;
    font-weight: 600;
    font-family: monospace;
    overflow-wrap: anywhere;
    min-width: 0;
  }
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
  .run-error { color: var(--danger); overflow-wrap: anywhere; min-width: 0; }
  .run-stale { color: var(--warn-text); }

  .results-panel {
    margin-top: 12px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
    font-size: 12px;
  }
  @media (max-width: 520px) {
    .candidate { flex-basis: 100%; max-width: none; }
    .cap-field { width: 100%; }
  }
</style>
