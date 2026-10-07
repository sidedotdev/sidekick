import { reactive, readonly } from 'vue'

// Client-local settings: stored per browser (not per workspace) and only
// honored in dev mode.

export type KanbanDirection = 'up' | 'down' | 'left' | 'right'
export type KanbanKeyBindings = Record<KanbanDirection, string>

export const KANBAN_DIRECTIONS: readonly KanbanDirection[] = ['up', 'down', 'left', 'right']

export const KANBAN_KEYBOARD_CONTROL_STORAGE_KEY = 'sidekick_kanban_keyboard_control'
export const KANBAN_KEYBOARD_KEYS_STORAGE_KEY = 'sidekick_kanban_keyboard_keys'

export const DEFAULT_KANBAN_KEY_BINDINGS: Readonly<KanbanKeyBindings> = Object.freeze({
  up: 'k',
  down: 'j',
  left: 'h',
  right: 'l',
})

// Board shortcuts that movement bindings must not shadow, keyed by lowercase key
export const KANBAN_NEW_TASK_SHORTCUT_KEY = 't'
const RESERVED_KANBAN_KEYS: Readonly<Record<string, string>> = {
  [KANBAN_NEW_TASK_SHORTCUT_KEY]: 'new task',
}

const SINGLE_VISIBLE_CHARACTER = /^\S$/u

const state = reactive({
  kanbanKeyboardControl: false,
  kanbanKeyBindings: { ...DEFAULT_KANBAN_KEY_BINDINGS } as KanbanKeyBindings,
})

export const localSettings = readonly(state)

export const areLocalSettingsAvailable = (): boolean => import.meta.env.MODE === 'development'

export const isKanbanKeyboardControlEnabled = (): boolean =>
  areLocalSettingsAvailable() && state.kanbanKeyboardControl

export const getKanbanKeyBindings = (): Readonly<KanbanKeyBindings> => state.kanbanKeyBindings

const readStorage = (key: string): string | null => {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

const writeStorage = (key: string, value: string) => {
  try {
    localStorage.setItem(key, value)
  } catch {
    // The setting still applies for the current page when storage is unavailable.
  }
}

/** Returns why `key` can't be bound to `direction` given the other bindings, or null if it can. */
export const kanbanKeyBindingError = (
  bindings: Readonly<KanbanKeyBindings>,
  direction: KanbanDirection,
  key: string,
): string | null => {
  if (!SINGLE_VISIBLE_CHARACTER.test(key)) return 'Enter a single character'
  const reservedFor = RESERVED_KANBAN_KEYS[key.toLowerCase()]
  if (reservedFor) return `Reserved for the ${reservedFor} shortcut`
  const conflict = KANBAN_DIRECTIONS.find(other =>
    other !== direction && bindings[other].toLowerCase() === key.toLowerCase(),
  )
  return conflict ? `Already used for ${conflict}` : null
}

const parseKeyBindings = (raw: string | null): KanbanKeyBindings => {
  if (!raw) return { ...DEFAULT_KANBAN_KEY_BINDINGS }
  try {
    const parsed = JSON.parse(raw)
    const bindings = { ...DEFAULT_KANBAN_KEY_BINDINGS }
    for (const direction of KANBAN_DIRECTIONS) {
      if (typeof parsed?.[direction] !== 'string') return { ...DEFAULT_KANBAN_KEY_BINDINGS }
      bindings[direction] = parsed[direction]
    }
    const valid = KANBAN_DIRECTIONS.every(direction =>
      kanbanKeyBindingError(bindings, direction, bindings[direction]) === null,
    )
    return valid ? bindings : { ...DEFAULT_KANBAN_KEY_BINDINGS }
  } catch {
    return { ...DEFAULT_KANBAN_KEY_BINDINGS }
  }
}

export const loadLocalSettings = () => {
  state.kanbanKeyboardControl = readStorage(KANBAN_KEYBOARD_CONTROL_STORAGE_KEY) === 'true'
  state.kanbanKeyBindings = parseKeyBindings(readStorage(KANBAN_KEYBOARD_KEYS_STORAGE_KEY))
}

export const setKanbanKeyboardControl = (enabled: boolean) => {
  state.kanbanKeyboardControl = enabled
  writeStorage(KANBAN_KEYBOARD_CONTROL_STORAGE_KEY, String(enabled))
}

/** Binds `key` to `direction`, returning a validation error (and keeping the current binding) when invalid. */
export const setKanbanKeyBinding = (direction: KanbanDirection, key: string): string | null => {
  const error = kanbanKeyBindingError(state.kanbanKeyBindings, direction, key)
  if (error) return error
  state.kanbanKeyBindings = { ...state.kanbanKeyBindings, [direction]: key }
  writeStorage(KANBAN_KEYBOARD_KEYS_STORAGE_KEY, JSON.stringify(state.kanbanKeyBindings))
  return null
}

loadLocalSettings()

if (typeof window !== 'undefined') {
  window.addEventListener('storage', event => {
    if (
      event.key === null ||
      event.key === KANBAN_KEYBOARD_CONTROL_STORAGE_KEY ||
      event.key === KANBAN_KEYBOARD_KEYS_STORAGE_KEY
    ) {
      loadLocalSettings()
    }
  })
}