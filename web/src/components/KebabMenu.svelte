<script>
  let { items = [] } = $props()

  let open = $state(false)
  let containerEl = $state(null)

  function handleBlur(e) {
    if (e.relatedTarget && containerEl?.contains(e.relatedTarget)) return
    setTimeout(() => { open = false }, 180)
  }

  function handleKeydown(e) {
    if (e.key === 'Escape') open = false
  }

  function handleItemClick(item) {
    if (item.disabled) return
    open = false
    item.onclick?.()
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div class="kebab-wrap" role="group" bind:this={containerEl} onfocusout={handleBlur} onkeydown={handleKeydown}>
  <button class="kebab-trigger" onclick={() => { open = !open }} aria-haspopup="true" aria-expanded={open} title="More actions">
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg>
  </button>
  {#if open}
    <div class="kebab-dropdown" role="menu">
      {#each items as item}
        {#if item.separator}
          <div class="kebab-sep" role="separator"></div>
        {:else}
          <button
            class="kebab-item"
            class:danger={item.danger}
            class:disabled={item.disabled}
            disabled={item.disabled}
            role="menuitem"
            onclick={() => handleItemClick(item)}
          >{item.label}</button>
        {/if}
      {/each}
    </div>
  {/if}
</div>

<style>
  .kebab-wrap {
    position: relative;
  }

  .kebab-trigger {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    padding: 0;
    border: 1px solid transparent;
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
    transition: all 0.12s ease;
  }
  .kebab-trigger:hover {
    background: var(--hover-overlay);
    border-color: var(--border);
    color: var(--text);
  }

  .kebab-dropdown {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    min-width: 150px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 4px;
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.1);
    z-index: 50;
  }

  .kebab-item {
    display: block;
    width: 100%;
    text-align: left;
    padding: 7px 10px;
    background: none;
    border: none;
    font: inherit;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
    border-radius: calc(var(--radius) - 2px);
  }
  .kebab-item:hover { background: var(--hover-overlay); }
  .kebab-item.danger { color: var(--danger); }
  .kebab-item.danger:hover { background: rgba(196, 58, 58, 0.06); }
  .kebab-item.disabled {
    opacity: 0.4;
    cursor: default;
  }
  .kebab-item.disabled:hover { background: none; }

  .kebab-sep {
    height: 1px;
    background: var(--border);
    margin: 4px 2px;
  }
</style>
