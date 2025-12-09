import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import LlmConfigEditor from './LlmConfigEditor.vue'
import type { LLMConfig } from '../lib/models'

describe('LlmConfigEditor', () => {
  it('renders default model row with empty values when no modelValue provided', () => {
    const wrapper = mount(LlmConfigEditor)
    
    const providerSelect = wrapper.find('.provider-select')
    expect(providerSelect.exists()).toBe(true)
    expect((providerSelect.element as HTMLSelectElement).value).toBe('')
    
    const modelInput = wrapper.find('.model-input')
    expect(modelInput.exists()).toBe(true)
    expect((modelInput.element as HTMLInputElement).value).toBe('')
  })

  it('renders default model row with provided values', () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: 'medium' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    const providerSelect = wrapper.find('.provider-select')
    expect((providerSelect.element as HTMLSelectElement).value).toBe('anthropic')
    
    const modelInput = wrapper.find('.model-input')
    expect((modelInput.element as HTMLInputElement).value).toBe('claude-3')
  })

  it('renders all use case rows', () => {
    const wrapper = mount(LlmConfigEditor)
    
    const useCaseCheckboxes = wrapper.findAll('.use-case-checkbox')
    expect(useCaseCheckboxes.length).toBe(3)
    
    const labels = useCaseCheckboxes.map(cb => cb.find('.model-label').text())
    expect(labels).toContain('planning')
    expect(labels).toContain('judging')
    expect(labels).toContain('code_localization')
  })

  it('enables use case inputs when checkbox is checked', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const useCaseCheckbox = wrapper.find('.use-case-checkbox input[type="checkbox"]')
    const useCaseProviderSelect = wrapper.findAll('.provider-select')[1]
    const useCaseModelInput = wrapper.findAll('.model-input')[1]
    
    expect((useCaseProviderSelect.element as HTMLSelectElement).disabled).toBe(true)
    expect((useCaseModelInput.element as HTMLInputElement).disabled).toBe(true)
    
    await useCaseCheckbox.setValue(true)
    
    expect((useCaseProviderSelect.element as HTMLSelectElement).disabled).toBe(false)
    expect((useCaseModelInput.element as HTMLInputElement).disabled).toBe(false)
  })

  it('emits update:modelValue when default provider changes', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const providerSelect = wrapper.find('.provider-select')
    await providerSelect.setValue('openai')
    
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    const emittedValue = wrapper.emitted('update:modelValue')![0][0] as LLMConfig
    expect(emittedValue.defaults[0].provider).toBe('openai')
  })

  it('emits update:modelValue when default model changes', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const modelInput = wrapper.find('.model-input')
    await modelInput.setValue('gpt-4')
    
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    const emittedValue = wrapper.emitted('update:modelValue')![0][0] as LLMConfig
    expect(emittedValue.defaults[0].model).toBe('gpt-4')
  })

  it('toggles options row visibility when options button is clicked', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    expect(wrapper.find('.options-row').exists()).toBe(false)
    
    const optionsToggle = wrapper.find('.options-toggle')
    await optionsToggle.trigger('click')
    
    expect(wrapper.find('.options-row').exists()).toBe(true)
    expect(wrapper.find('.reasoning-select').exists()).toBe(true)
    
    await optionsToggle.trigger('click')
    expect(wrapper.find('.options-row').exists()).toBe(false)
  })

  it('includes enabled use case in emitted config', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const useCaseCheckbox = wrapper.find('.use-case-checkbox input[type="checkbox"]')
    await useCaseCheckbox.setValue(true)
    
    const useCaseProviderSelect = wrapper.findAll('.provider-select')[1]
    await useCaseProviderSelect.setValue('google')
    
    const useCaseModelInput = wrapper.findAll('.model-input')[1]
    await useCaseModelInput.setValue('gemini-pro')
    
    const emittedEvents = wrapper.emitted('update:modelValue')!
    const lastEmittedValue = emittedEvents[emittedEvents.length - 1][0] as LLMConfig
    expect(lastEmittedValue.useCaseConfigs['planning']).toBeDefined()
    expect(lastEmittedValue.useCaseConfigs['planning'][0].provider).toBe('google')
    expect(lastEmittedValue.useCaseConfigs['planning'][0].model).toBe('gemini-pro')
  })

  it('excludes use case from config when provider or model is empty', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const useCaseCheckbox = wrapper.find('.use-case-checkbox input[type="checkbox"]')
    await useCaseCheckbox.setValue(true)
    
    const useCaseProviderSelect = wrapper.findAll('.provider-select')[1]
    await useCaseProviderSelect.setValue('google')
    // model is still empty
    
    const emittedEvents = wrapper.emitted('update:modelValue')!
    const lastEmittedValue = emittedEvents[emittedEvents.length - 1][0] as LLMConfig
    expect(lastEmittedValue.useCaseConfigs['planning']).toBeUndefined()
  })

  it('loads existing use case configs from modelValue', () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: '' }],
      useCaseConfigs: {
        'judging': [{ provider: 'openai', model: 'gpt-4', reasoningEffort: 'high' }]
      },
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    const useCaseCheckboxes = wrapper.findAll('.use-case-checkbox input[type="checkbox"]')
    // planning is first, judging is second
    expect((useCaseCheckboxes[1].element as HTMLInputElement).checked).toBe(true)
    
    const providerSelects = wrapper.findAll('.provider-select')
    // index 2 is judging (0=default, 1=planning, 2=judging)
    expect((providerSelects[2].element as HTMLSelectElement).value).toBe('openai')
  })

  it('updates reasoning effort in emitted config', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const optionsToggle = wrapper.find('.options-toggle')
    await optionsToggle.trigger('click')
    
    const reasoningSelect = wrapper.find('.reasoning-select')
    await reasoningSelect.setValue('high')
    
    const emittedEvents = wrapper.emitted('update:modelValue')!
    const lastEmittedValue = emittedEvents[emittedEvents.length - 1][0] as LLMConfig
    expect(lastEmittedValue.defaults[0].reasoningEffort).toBe('high')
  })
})