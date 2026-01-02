import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import IdeSelectorDialog from '../IdeSelectorDialog.vue'

// Mock the icon components
vi.mock('@/components/icons/VSCodeIcon.vue', () => ({
  default: { template: '<svg class="vscode-icon"></svg>' }
}))
vi.mock('@/components/icons/IntellijIcon.vue', () => ({
  default: { template: '<svg class="intellij-icon"></svg>' }
}))

describe('IdeSelectorDialog', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    vi.clearAllMocks()
  })

  const mountComponent = (show: boolean = true) => {
    wrapper = mount(IdeSelectorDialog, {
      props: { show }
    })
  }

  it('renders nothing when show is false', () => {
    mountComponent(false)
    
    expect(wrapper.find('.ide-selector-overlay').exists()).toBe(false)
  })

  it('renders dialog when show is true', () => {
    mountComponent(true)
    
    expect(wrapper.find('.ide-selector-overlay').exists()).toBe(true)
    expect(wrapper.find('.ide-selector-dialog').exists()).toBe(true)
    expect(wrapper.find('h3').text()).toBe('Open file in...')
  })

  it('displays VSCode and IntelliJ buttons', () => {
    mountComponent(true)
    
    const buttons = wrapper.findAll('.ide-button')
    expect(buttons).toHaveLength(2)
    expect(buttons[0].text()).toContain('VS Code')
    expect(buttons[1].text()).toContain('IntelliJ')
  })

  it('displays cancel button', () => {
    mountComponent(true)
    
    const cancelButton = wrapper.find('.ide-cancel-button')
    expect(cancelButton.exists()).toBe(true)
    expect(cancelButton.text()).toBe('Cancel')
  })

  it('emits select event with vscode when VSCode button is clicked', async () => {
    mountComponent(true)
    
    const vscodeButton = wrapper.findAll('.ide-button')[0]
    await vscodeButton.trigger('click')
    
    expect(wrapper.emitted('select')).toHaveLength(1)
    expect(wrapper.emitted('select')![0]).toEqual(['vscode'])
  })

  it('emits select event with intellij when IntelliJ button is clicked', async () => {
    mountComponent(true)
    
    const intellijButton = wrapper.findAll('.ide-button')[1]
    await intellijButton.trigger('click')
    
    expect(wrapper.emitted('select')).toHaveLength(1)
    expect(wrapper.emitted('select')![0]).toEqual(['intellij'])
  })

  it('emits cancel event when cancel button is clicked', async () => {
    mountComponent(true)
    
    const cancelButton = wrapper.find('.ide-cancel-button')
    await cancelButton.trigger('click')
    
    expect(wrapper.emitted('cancel')).toHaveLength(1)
  })

  it('emits cancel event when clicking overlay background', async () => {
    mountComponent(true)
    
    const overlay = wrapper.find('.ide-selector-overlay')
    await overlay.trigger('click')
    
    expect(wrapper.emitted('cancel')).toHaveLength(1)
  })

  it('does not emit cancel when clicking dialog content', async () => {
    mountComponent(true)
    
    const dialog = wrapper.find('.ide-selector-dialog')
    await dialog.trigger('click')
    
    expect(wrapper.emitted('cancel')).toBeUndefined()
  })

  it('renders icon components', () => {
    mountComponent(true)
    
    expect(wrapper.find('.vscode-icon').exists()).toBe(true)
    expect(wrapper.find('.intellij-icon').exists()).toBe(true)
  })
})