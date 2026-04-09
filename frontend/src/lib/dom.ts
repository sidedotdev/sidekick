const interactiveRoles = new Set([
  'textbox', 'searchbox', 'combobox', 'spinbutton',
  'listbox', 'menu', 'menubar', 'tree', 'treegrid', 'grid',
  'tablist', 'dialog',
])

const interactiveAncestorSelector = Array.from(interactiveRoles)
  .map(r => `[role="${r}"]`)
  .join(', ')

/** Returns true if single-key shortcuts should be suppressed for this element. */
export const isInteractiveElement = (el: HTMLElement | null): boolean => {
  if (!el) return false
  if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT') return true
  if (el.isContentEditable || el.getAttribute('contenteditable') === 'true') return true
  const role = el.getAttribute('role')
  if (role && interactiveRoles.has(role)) return true
  if (el.closest(interactiveAncestorSelector)) return true
  return false
}