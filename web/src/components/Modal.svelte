<script>
  export let title = ''
  export let onClose = () => {}
</script>

<!-- svelte-ignore a11y-click-events-have-key-events -->
<div class="overlay" on:click|self={onClose} role="dialog" aria-modal="true" aria-label={title}>
  <div class="modal">
    <div class="modal-header">
      <h2>{title}</h2>
      <button class="close" on:click={onClose} aria-label="Close">✕</button>
    </div>
    <div class="modal-body">
      <slot />
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed; inset: 0;
    background: rgba(0,0,0,0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    width: min(600px, 90vw);
    max-height: 80vh;
    display: flex;
    flex-direction: column;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
  }
  h2 { font-size: 15px; font-weight: 600; }
  .close {
    background: none; border: none;
    color: var(--text-muted); cursor: pointer;
    font-size: 18px; line-height: 1;
    padding: 4px;
  }
  .close:hover { color: var(--text); }
  .modal-body { padding: 20px; overflow-y: auto; }
</style>
