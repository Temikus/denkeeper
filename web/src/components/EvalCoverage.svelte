<script>
  import { CATEGORIES, countByCategory } from '../evalCategories.js'

  // A test set's mix of kinds. The results view breaks the verdict down by
  // kind, so a kind with no cases is one the verdict cannot speak for.
  let {
    tasks = [],
    // One line for the launcher instead of the bar, legend and gap prompts.
    compact = false,
    // Called with a category slug when a gap prompt is followed.
    onfill = undefined,
    // Compact only: opens the full view.
    onsee = undefined,
    // False when no agent exists to generate probes from.
    canProbe = true,
    // Kinds the agent cannot produce, slug to reason. Not counted as gaps.
    notApplicable = {},
    // The agent notApplicable describes, named in the line that explains it.
    agent = '',
  } = $props()

  // Must match DrawStratified's per-run draw for a Quick check.
  const QUICK_TASKS = 10

  const GAP_ACTION = {
    chat: 'Suggest chat turns',
    skill_command: 'Suggest skill-command turns',
    scheduled: 'Suggest scheduled runs',
    tool_heavy: 'Suggest tool-heavy turns',
    probe: 'Generate probes',
  }

  let counts = $derived(countByCategory(tasks))
  let total = $derived(tasks.length)
  let present = $derived(CATEGORIES.filter(c => counts[c.value] > 0))
  let missing = $derived(CATEGORIES.filter(c => counts[c.value] === 0 && !notApplicable[c.value]))
  // A kind with cases is present whatever the agent can produce now: the set
  // may predate a config change, or have been built for another agent.
  let skipped = $derived(CATEGORIES.filter(c => counts[c.value] === 0 && notApplicable[c.value]))

  function plural(n, word) {
    return `${n} ${word}${n === 1 ? '' : 's'}`
  }
</script>

{#if compact}
  <p class="compact hint" data-testid="coverage-compact">
    {#if total === 0}
      This set has no cases yet.
    {:else}
      {present.map(c => `${c.label} ${counts[c.value]}`).join(' · ')}{#if missing.length}
        <span class="gap-count"> · {plural(missing.length, 'kind')} missing</span>{/if}
    {/if}
    {#if onsee}
      <button type="button" class="btn-link" onclick={() => onsee()}
        data-testid="coverage-see">See coverage</button>
    {/if}
  </p>
{:else if total > 0}
  <section class="coverage" aria-labelledby="coverage-title" data-testid="coverage">
    <h3 id="coverage-title" class="section-title">Coverage · {plural(total, 'case')}</h3>
    <div class="bar" aria-hidden="true">
      {#each present as c (c.value)}
        <span class="seg seg-{c.value}" style:flex-grow={counts[c.value]}></span>
      {/each}
    </div>
    <ul class="legend">
      {#each CATEGORIES as c (c.value)}
        {@const na = counts[c.value] === 0 && notApplicable[c.value]}
        <li class:empty={counts[c.value] === 0} data-testid="coverage-{c.value}">
          <span class="swatch seg-{c.value}" aria-hidden="true"></span>
          {c.label} <strong>{na ? 'n/a' : counts[c.value]}</strong>
        </li>
      {/each}
    </ul>

    {#if missing.length && onfill}
      <div class="gaps" data-testid="coverage-gaps">
        <p class="hint">
          The verdict is broken down by kind, so a kind with no cases goes unmeasured.
        </p>
        <ul>
          {#each missing as c (c.value)}
            {@const blocked = c.value === 'probe' && !canProbe}
            <li>
              <span>No {c.label.toLowerCase()} cases.</span>
              <button type="button" class="btn-ghost btn-sm" onclick={() => onfill(c.value)}
                disabled={blocked}
                title={blocked ? 'Configure an agent first — probes come from its configuration' : undefined}
                data-testid="gap-{c.value}">{GAP_ACTION[c.value]}</button>
            </li>
          {/each}
        </ul>
      </div>
    {/if}

    {#if skipped.length}
      <p class="hint" data-testid="coverage-skipped">
        Not gaps{agent ? ` for ${agent}` : ''}: {skipped.map(c => `${c.label.toLowerCase()} (${notApplicable[c.value]})`).join(', ')}.
      </p>
    {/if}

    <p class="hint" data-testid="coverage-quick">
      {#if total <= QUICK_TASKS}
        A Quick check runs all {plural(total, 'case')}.
      {:else}
        A Quick check draws {QUICK_TASKS} of the {total}, spread evenly across the
        {plural(present.length, 'kind')} this set has.
      {/if}
    </p>
  </section>
{/if}

<style>
  .section-title {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin: 0 0 8px;
  }

  .coverage {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .bar {
    display: flex;
    gap: 2px;
    height: 10px;
    border-radius: 999px;
    overflow: hidden;
    background: var(--border);
  }

  .seg {
    flex-basis: 0;
    min-width: 4px;
  }

  /* No categorical palette exists, so the kinds step through the accent. The
     legend carries label and count, so colour is never the only cue. */
  .seg-chat { background: var(--accent); }
  .seg-tool_heavy { background: rgba(var(--accent-rgb), 0.7); }
  .seg-skill_command { background: rgba(var(--accent-rgb), 0.45); }
  .seg-scheduled { background: rgba(var(--accent-rgb), 0.25); }
  .seg-probe { background: var(--text-muted); }

  .legend {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 4px 14px;
    font-size: 12px;
    color: var(--text);
  }

  .legend li {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .legend li.empty { color: var(--text-muted); }
  .legend strong { font-weight: 600; }

  .swatch {
    width: 8px;
    height: 8px;
    border-radius: 2px;
    flex-shrink: 0;
  }

  .legend li.empty .swatch {
    background: transparent;
    border: 1px solid var(--border);
  }

  .gaps {
    border-left: 2px solid var(--warn);
    padding-left: 10px;
  }

  .gaps ul {
    list-style: none;
    margin: 6px 0 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .gaps li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    font-size: 12px;
  }

  p.hint { margin: 0; }

  .compact {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 6px;
  }

  .gap-count { color: var(--warn-text); }

  .btn-link {
    border: none;
    background: none;
    padding: 0 2px;
    color: var(--accent);
    font-size: 11px;
    cursor: pointer;
  }

  .btn-link:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
</style>
