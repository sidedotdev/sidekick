const interactiveRoles = new Set([
  'textbox', 'searchbox', 'combobox', 'spinbutton',
  'listbox', 'menu', 'menubar', 'tree', 'treegrid', 'grid',
  'tablist', 'dialog',
])

const interactiveAncestorSelector = Array.from(interactiveRoles)
  .map(r => `[role="${r}"]`)
  .join(', ')

/** Whether focus rests on nothing in particular, i.e. the document itself. */
export const isFocusUnclaimed = (el: EventTarget | null): boolean =>
  !el || el === document.body || el === document.documentElement

/**
 * Whether a keyboard-navigated collection may move focus onto its current
 * item without stealing it: nothing holds focus, or one of the collection's
 * items (matching itemSelector) already does.
 */
export const canMoveNavigationFocus = (itemSelector: string): boolean => {
  const active = document.activeElement
  return isFocusUnclaimed(active) || (active instanceof HTMLElement && active.matches(itemSelector))
}

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