<script>
  let { title, open = $bindable(true), id = undefined, children } = $props()
  let slug = $derived(id || title.toLowerCase().replace(/[^a-z0-9]+/g, '-'))
  let headingId = $derived(`heading-${slug}`)
  let sectionId = $derived(`section-${slug}`)
</script>

<button class="section-toggle" aria-expanded={open} aria-controls={sectionId} onclick={() => open = !open}>
  <span class="section-arrow" class:open>&#x25B6;</span>
  <h2 class="section-title" id={headingId}>{title}</h2>
</button>

{#if open}
  <div class="section-body" id={sectionId} role="region" aria-labelledby={headingId}>
    {@render children()}
  </div>
{/if}

<style>
  .section-toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    background: none;
    border: none;
    cursor: pointer;
    padding: 10px 0;
    width: 100%;
    text-align: left;
  }

  .section-toggle:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
    border-radius: var(--radius);
  }

  .section-arrow {
    font-size: 10px;
    color: var(--text-muted);
    transition: transform 0.2s;
    display: inline-block;
  }
  .section-arrow.open { transform: rotate(90deg); }

  .section-title {
    font-size: 15px;
    font-weight: 600;
    color: var(--text);
    margin: 0;
  }

  .section-body {
    padding: 0 0 20px 18px;
  }
</style>
