import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_KANBAN_KEY_BINDINGS,
  KANBAN_KEYBOARD_CONTROL_STORAGE_KEY,
  KANBAN_KEYBOARD_KEYS_STORAGE_KEY,
  getKanbanKeyBindings,
  isKanbanKeyboardControlEnabled,
  loadLocalSettings,
  setKanbanKeyBinding,
  setKanbanKeyboardControl,
} from '../localSettings'

describe('localSettings', () => {
  beforeEach(() => {
    localStorage.clear()
    loadLocalSettings()
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    localStorage.clear()
    loadLocalSettings()
  })

  it('defaults keyboard control off with vim-style secondary keys', () => {
    vi.stubEnv('MODE', 'development')
    expect(isKanbanKeyboardControlEnabled()).toBe(false)
    expect(getKanbanKeyBindings()).toEqual({ up: 'k', down: 'j', left: 'h', right: 'l' })
  })

  it('enables keyboard control from localStorage in dev mode', () => {
    vi.stubEnv('MODE', 'development')
    localStorage.setItem(KANBAN_KEYBOARD_CONTROL_STORAGE_KEY, 'true')
    loadLocalSettings()
    expect(isKanbanKeyboardControlEnabled()).toBe(true)
  })

  it('ignores the stored setting outside dev mode', () => {
    vi.stubEnv('MODE', 'production')
    localStorage.setItem(KANBAN_KEYBOARD_CONTROL_STORAGE_KEY, 'true')
    loadLocalSettings()
    expect(isKanbanKeyboardControlEnabled()).toBe(false)
  })

  it('persists the toggle and applies it immediately', () => {
    vi.stubEnv('MODE', 'development')
    setKanbanKeyboardControl(true)
    expect(isKanbanKeyboardControlEnabled()).toBe(true)
    expect(localStorage.getItem(KANBAN_KEYBOARD_CONTROL_STORAGE_KEY)).toBe('true')
  })

  it('persists valid key bindings', () => {
    expect(setKanbanKeyBinding('up', 'u')).toBeNull()
    expect(getKanbanKeyBindings().up).toBe('u')
    expect(JSON.parse(localStorage.getItem(KANBAN_KEYBOARD_KEYS_STORAGE_KEY)!)).toEqual({
      ...DEFAULT_KANBAN_KEY_BINDINGS,
      up: 'u',
    })
    loadLocalSettings()
    expect(getKanbanKeyBindings().up).toBe('u')
  })

  it.each([
    ['empty', ''],
    ['multiple characters', 'ab'],
    ['whitespace', ' '],
    ['a key bound to another direction', 'j'],
    ['a key bound to another direction in another case', 'J'],
    ['the new-task shortcut', 't'],
    ['the new-task shortcut in another case', 'T'],
    ['the workspace shortcut', 'w'],
    ['the workspace shortcut in another case', 'W'],
  ])('rejects %s and keeps the last valid binding', (_name, key) => {
    expect(setKanbanKeyBinding('up', key)).not.toBeNull()
    expect(getKanbanKeyBindings().up).toBe('k')
  })

  it.each([
    ['duplicate keys', { up: 'j', down: 'j', left: 'h', right: 'l' }],
    ['the new-task shortcut', { up: 'T', down: 'j', left: 'h', right: 'l' }],
    ['the workspace shortcut', { up: 'W', down: 'j', left: 'h', right: 'l' }],
  ])('falls back to defaults when stored bindings contain %s', (_name, stored) => {
    localStorage.setItem(KANBAN_KEYBOARD_KEYS_STORAGE_KEY, JSON.stringify(stored))
    loadLocalSettings()
    expect(getKanbanKeyBindings()).toEqual(DEFAULT_KANBAN_KEY_BINDINGS)
  })
})