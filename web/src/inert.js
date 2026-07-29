/**
 * Svelte action that keeps the `inert` content attribute in sync with a
 * boolean, and hands focus back to whatever opened the panel when it closes.
 *
 * Collapsed inline panels stay mounted so the open/close animation can run,
 * but while hidden their inputs must not be reachable by keyboard or exposed
 * to assistive tech. `inert` does both; shared.css also flips `visibility` so
 * the guarantee holds even where `inert` is unsupported.
 *
 * Applied as an action rather than `inert={...}` so the state lands on the
 * content attribute: Svelte assigns the DOM property, which is invisible to
 * environments that do not implement it.
 *
 * Focus handling: making an ancestor inert while focus is inside it drops
 * focus to <body>, so a keyboard user who hits Cancel would restart their
 * next Tab from the top of the page. The action remembers the element that
 * was focused when the panel opened (the trigger) and restores it on close.
 *
 * Usage: <div class="inline-panel" class:open={open} use:inert={!open}>
 */
export function inert(node, isInert) {
  let current = !!isInert
  let opener = null

  node.toggleAttribute('inert', current)

  function restoreFocus() {
    if (!node.contains(document.activeElement)) return
    const target = opener
    opener = null
    if (target && target !== document.body && target.isConnected && typeof target.focus === 'function') {
      target.focus()
    } else {
      document.activeElement?.blur?.()
    }
  }

  return {
    update(next) {
      const value = !!next
      if (value === current) return
      if (value) {
        // Closing: restore focus before the panel becomes unreachable.
        restoreFocus()
      } else {
        // Opening: remember the trigger so Cancel/Save can return focus.
        const active = document.activeElement
        opener = active && active !== document.body && !node.contains(active) ? active : null
      }
      current = value
      node.toggleAttribute('inert', current)
    },
  }
}
