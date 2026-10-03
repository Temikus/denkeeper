<script>
  // Vertical step list. Each item: {id, label, summary, state, optional},
  // state one of 'done' | 'current' | 'upcoming'. A done step's summary is
  // what the user chose ("anthropic · key works").
  let { steps = [] } = $props()
</script>

<ol class="rail-steps" data-testid="wizard-rail">
  {#each steps as step, i (step.id)}
    <li
      class="rail-step {step.state}"
      class:optional={step.optional}
      aria-current={step.state === 'current' ? 'step' : undefined}
      data-testid="wizard-rail-{step.id}"
    >
      <span class="marker" aria-hidden="true">
        {#if step.state === 'done'}
          <svg width="12" height="12" viewBox="0 0 12 12"><path d="M2.5 6.2l2.3 2.3 4.7-5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
        {:else}
          {i + 1}
        {/if}
      </span>
      <span class="text">
        <span class="label">
          {step.label}
          {#if step.optional}<span class="optional-tag">optional</span>{/if}
          <span class="sr-only">— {step.state === 'done' ? 'done' : step.state === 'current' ? 'current step' : 'not started'}</span>
        </span>
        <span class="summary">{step.summary}</span>
      </span>
    </li>
  {/each}
</ol>

<style>
  .rail-steps {
    list-style: none;
    display: flex;
    flex-direction: column;
  }
  .rail-step {
    position: relative;
    display: flex;
    gap: 14px;
    padding-bottom: 24px;
  }
  .rail-step:last-child { padding-bottom: 0; }
  /* Connector line between markers; accent once the step above is done. */
  .rail-step:not(:last-child)::after {
    content: '';
    position: absolute;
    left: 11px;
    top: 26px;
    bottom: 2px;
    width: 2px;
    background: var(--border);
  }
  .rail-step.done:not(:last-child)::after { background: var(--accent); }

  .marker {
    flex-shrink: 0;
    width: 24px;
    height: 24px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-muted);
    border: 1px solid var(--border);
    background: var(--bg);
  }
  .optional .marker { border-style: dashed; }
  .current .marker {
    border: 2px solid var(--accent);
    color: var(--accent);
    font-weight: 700;
  }
  .done .marker {
    background: var(--accent);
    border-color: var(--accent);
    color: #fff;
  }

  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .label {
    font-size: 15px;
    font-weight: 500;
    line-height: 24px;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .done .label { font-weight: 600; }
  .current .label { color: var(--accent); font-weight: 700; }
  .summary {
    font-size: 13px;
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }
  .optional-tag {
    font-size: 11px;
    font-weight: 400;
    line-height: 15px;
    padding: 1px 6px;
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text-muted);
  }
</style>
