import { watchEffect } from 'vue'
import {
  getKanbanKeyBindings,
  isKanbanKeyboardControlEnabled,
  KANBAN_DIRECTIONS,
  type KanbanDirection,
} from './localSettings'

// Page-wide keyboard navigation for the kanban keyboard control setting: the
// sidebar and header regions are handled here, while the board (when mounted)
// registers how focus enters it.

export const SIDEBAR_LINK_SELECTOR = '.sidebar nav a'
export const HEADER_ITEM_SELECTOR = '.workspace-selector [role="combobox"], .workspace-selector [data-nav-item]'

/** Set on the root element while kanban keyboard control is enabled, scoping its focus styling. */
export const KEYBOARD_CONTROL_CLASS = 'kanban-keyboard-control'

/**
 * Set on the root element while the user is interacting via keyboard. Focus
 * outlines key off this rather than :focus-visible, which browsers apply
 * inconsistently to programmatic focus.
 */
export const KEYBOARD_MODALITY_CLASS = 'keyboard-modality'

const ARROW_KEY_DIRECTIONS: Record<string, KanbanDirection> = {
  ArrowUp: 'up',
  ArrowDown: 'down',
  ArrowLeft: 'left',
  ArrowRight: 'right',
}

/** The direction of an arrow key or configured secondary key, which always behave alike. */
export const navigationDirectionForKey = (key: string): KanbanDirection | null => {
  const arrowDirection = ARROW_KEY_DIRECTIONS[key]
  if (arrowDirection) return arrowDirection
  const bindings = getKanbanKeyBindings()
  const lowered = key.toLowerCase()
  return KANBAN_DIRECTIONS.find(direction => bindings[direction].toLowerCase() === lowered) ?? null
}

export const hasNavigationModifier = (event: KeyboardEvent): boolean =>
  event.metaKey || event.ctrlKey || event.altKey

export interface BoardNavigation {
  /** Focuses the board's entry item, returning whether anything was focused. */
  enter(): boolean
  /** Whether a board modal currently suspends navigation keys. */
  isSuspended(): boolean
}

let boardNavigation: BoardNavigation | null = null

export const registerBoardNavigation = (navigation: BoardNavigation): (() => void) => {
  boardNavigation = navigation
  return () => {
    if (boardNavigation === navigation) boardNavigation = null
  }
}

export const enterBoard = (): boolean => boardNavigation?.enter() ?? false

const queryItems = (selector: string): HTMLElement[] => [...document.querySelectorAll<HTMLElement>(selector)]

const focusItem = (item: HTMLElement | undefined): boolean => {
  if (!item) return false
  item.focus()
  return true
}

/** Focuses the sidebar link of the current page, else the first one. */
export const focusSidebar = (): boolean => {
  const links = queryItems(SIDEBAR_LINK_SELECTOR)
  return focusItem(links.find(link => link.classList.contains('router-link-active')) ?? links[0])
}

export const focusHeader = (): boolean => focusItem(queryItems(HEADER_ITEM_SELECTOR)[0])

const consume = (event: KeyboardEvent) => {
  event.preventDefault()
  // Keeps focused widgets (e.g. the workspace Select) from also acting on the key
  event.stopPropagation()
}

const handleSidebarKey = (event: KeyboardEvent, links: HTMLElement[], index: number, direction: KanbanDirection) => {
  consume(event)
  if (direction === 'right') {
    enterBoard()
  } else if (direction === 'up' || direction === 'down') {
    focusItem(links[index + (direction === 'up' ? -1 : 1)])
  }
}

const handleHeaderKey = (event: KeyboardEvent, items: HTMLElement[], index: number, direction: KanbanDirection) => {
  consume(event)
  if (direction === 'down') {
    enterBoard()
  } else if (direction === 'left') {
    if (index === 0) focusSidebar()
    else focusItem(items[index - 1])
  } else if (direction === 'right') {
    focusItem(items[index + 1])
  }
}

const handleNavigationKey = (event: KeyboardEvent) => {
  if (!isKanbanKeyboardControlEnabled() || hasNavigationModifier(event)) return
  if (boardNavigation?.isSuspended()) return
  const direction = navigationDirectionForKey(event.key)
  const target = event.target
  if (!direction || !(target instanceof HTMLElement)) return

  const links = queryItems(SIDEBAR_LINK_SELECTOR)
  const linkIndex = links.indexOf(target)
  if (linkIndex >= 0) {
    handleSidebarKey(event, links, linkIndex, direction)
    return
  }

  const headerItems = queryItems(HEADER_ITEM_SELECTOR)
  const headerIndex = headerItems.indexOf(target)
  // An open select overlay keeps all keys for itself
  if (headerIndex >= 0 && target.getAttribute('aria-expanded') !== 'true') {
    handleHeaderKey(event, headerItems, headerIndex, direction)
  }
}

/**
 * Installs document-wide keyboard modality tracking and sidebar/header
 * navigation. Keys are handled in the capture phase so focused widgets don't
 * see the ones taken for navigation.
 */
export const installPageNavigation = (): (() => void) => {
  const root = document.documentElement
  // Focus placed on load, before any interaction, is shown as keyboard focus
  root.classList.add(KEYBOARD_MODALITY_CLASS)

  const onKeydown = (event: KeyboardEvent) => {
    root.classList.add(KEYBOARD_MODALITY_CLASS)
    handleNavigationKey(event)
  }
  const onPointerDown = () => root.classList.remove(KEYBOARD_MODALITY_CLASS)

  const stopControlClass = watchEffect(() => {
    root.classList.toggle(KEYBOARD_CONTROL_CLASS, isKanbanKeyboardControlEnabled())
  })
  document.addEventListener('keydown', onKeydown, true)
  document.addEventListener('pointerdown', onPointerDown, true)
  document.addEventListener('mousedown', onPointerDown, true)

  return () => {
    stopControlClass()
    document.removeEventListener('keydown', onKeydown, true)
    document.removeEventListener('pointerdown', onPointerDown, true)
    document.removeEventListener('mousedown', onPointerDown, true)
    root.classList.remove(KEYBOARD_CONTROL_CLASS, KEYBOARD_MODALITY_CLASS)
  }
}