<script>
  import Modal from '../components/Modal.svelte'
  let { title = '', onClose = () => {} } = $props()
</script>

<Modal {title} {onClose}>
  <p data-testid="modal-content">Test content</p>
</Modal>
