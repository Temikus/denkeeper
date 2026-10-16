<script>
  import { currentQuery } from '../router.js'
  import TaskBoard from '../components/TaskBoard.svelte'
  import TaskDetail from '../components/TaskDetail.svelte'

  // #/tasks is the board; #/tasks?id=131 (or DK-131) opens one task.
  const id = $derived($currentQuery.get('id'))
</script>

{#if id}
  {#key id}
    <TaskDetail {id} />
  {/key}
{:else}
  <TaskBoard />
{/if}
