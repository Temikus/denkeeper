<script>
  export let message = ''
</script>

{#if message}
  <div class="banner">{message}</div>
{/if}

<style>
  .banner {
    background: rgba(224,92,110,0.12);
    border: 1px solid var(--danger);
    color: var(--danger);
    border-radius: var(--radius);
    padding: 10px 14px;
    margin-bottom: 16px;
    font-size: 13px;
  }
</style>
