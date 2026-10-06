import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import LocalSettingsView from '../LocalSettingsView.vue'
import {
  getKanbanKeyBindings,
  isKanbanKeyboardControlEnabled,
  loadLocalSettings,
} from '../../lib/localSettings'

describe('LocalSettingsView', () => {
  beforeEach(() => {
    vi.stubEnv('MODE', 'development')
    localStorage.clear()
    loadLocalSettings()
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    localStorage.clear()
    loadLocalSettings()
  })

  it('toggles kanban keyboard control', async () => {
    const wrapper = mount(LocalSettingsView)
    const toggle = wrapper.get<HTMLInputElement>('.keyboard-control-toggle')
    expect(toggle.element.checked).toBe(false)
    await toggle.setValue(true)
    expect(isKanbanKeyboardControlEnabled()).toBe(true)
  })

  it('saves a valid key and rejects a duplicate with inline feedback', async () => {
    const wrapper = mount(LocalSettingsView)
    const upInput = wrapper.get<HTMLInputElement>('.key-input[data-direction="up"]')

    await upInput.setValue('w')
    expect(getKanbanKeyBindings().up).toBe('w')
    expect(wrapper.find('.key-error').exists()).toBe(false)

    await upInput.setValue('j')
    expect(getKanbanKeyBindings().up).toBe('w')
    expect(wrapper.get('.key-error').text()).toContain('down')

    await upInput.trigger('blur')
    expect(upInput.element.value).toBe('w')
    expect(wrapper.find('.key-error').exists()).toBe(false)
  })

  it('rejects the new-task shortcut key with inline feedback', async () => {
    const wrapper = mount(LocalSettingsView)
    await wrapper.get<HTMLInputElement>('.key-input[data-direction="left"]').setValue('t')
    expect(getKanbanKeyBindings().left).toBe('h')
    expect(wrapper.get('.key-error').text()).toContain('new task')
  })
})