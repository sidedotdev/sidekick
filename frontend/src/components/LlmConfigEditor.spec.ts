import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { config, mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import AutoComplete from 'primevue/autocomplete'
import LlmConfigEditor from './LlmConfigEditor.vue'

config.global.plugins.push(PrimeVue)
import type { LLMConfig } from '../lib/models'
import { store } from '../lib/store'

const mockModelsData = {
  openai: {
    models: {
      'o1': { reasoning: true },
      'gpt-4': { reasoning: false },
    }
  },
  anthropic: {
    models: {
      'claude-3': { reasoning: false },
    }
  },
  google: {
    models: {
      'gemini-pro': { reasoning: false },
    }
  }
}

const mockProvidersData = {
  providers: ['google', 'anthropic', 'openai']
}

const createMockFetch = (modelsData: object = mockModelsData, providersData: object = mockProvidersData) => {
  return vi.fn((url: string) => {
    if (url === '/api/v1/providers') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(providersData),
      })
    }
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve(modelsData),
    })
  })
}

describe('LlmConfigEditor', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', createMockFetch())
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
  })

  it('renders default model row with empty values when no modelValue provided', () => {
    const wrapper = mount(LlmConfigEditor)
    
    const providerSelect = wrapper.find('.provider-select')
    expect(providerSelect.exists()).toBe(true)
    expect((providerSelect.element as HTMLSelectElement).value).toBe('')
    
    const modelInput = wrapper.find('.model-input-inner')
    expect(modelInput.exists()).toBe(true)
    expect((modelInput.element as HTMLInputElement).value).toBe('')
  })

  it('renders default model row with provided values', async () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: 'medium' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await vi.waitFor(() => {
      const providerSelect = wrapper.find('.provider-select')
      expect((providerSelect.element as HTMLSelectElement).value).toBe('anthropic')
    })
    
    const modelInput = wrapper.find('.model-input-inner')
    expect((modelInput.element as HTMLInputElement).value).toBe('claude-3')
  })

  it('renders all use case rows', () => {
    const wrapper = mount(LlmConfigEditor)
    
    const useCaseCheckboxes = wrapper.findAll('.use-case-checkbox')
    expect(useCaseCheckboxes.length).toBe(3)
    
    const labels = useCaseCheckboxes.map(cb => cb.find('.model-label').text())
    expect(labels).toContain('Plan')
    expect(labels).toContain('Review')
    expect(labels).toContain('Context')
  })

  it('enables use case inputs when checkbox is checked', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const useCaseCheckbox = wrapper.find('.use-case-checkbox input[type="checkbox"]')
    const useCaseProviderSelect = wrapper.findAll('.provider-select')[1]
    const useCaseModelInput = wrapper.findAll('.model-input-inner')[1]
    
    expect((useCaseProviderSelect.element as HTMLSelectElement).disabled).toBe(true)
    expect((useCaseModelInput.element as HTMLInputElement).disabled).toBe(true)
    
    await useCaseCheckbox.setValue(true)
    
    expect((useCaseProviderSelect.element as HTMLSelectElement).disabled).toBe(false)
    expect((useCaseModelInput.element as HTMLInputElement).disabled).toBe(false)
  })

  it('emits update:modelValue when default provider changes', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    await vi.waitFor(() => {
      const options = wrapper.find('.provider-select').findAll('option')
      expect(options.length).toBeGreaterThan(1)
    })
    
    const providerSelect = wrapper.find('.provider-select')
    await providerSelect.setValue('openai')
    
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    const emittedValue = wrapper.emitted('update:modelValue')![0][0] as LLMConfig
    expect(emittedValue.defaults[0].provider).toBe('openai')
  })

  it('emits update:modelValue when default model changes', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    const modelInput = wrapper.find('.model-input-inner')
    await modelInput.setValue('gpt-4')
    
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    const emittedValue = wrapper.emitted('update:modelValue')![0][0] as LLMConfig
    expect(emittedValue.defaults[0].model).toBe('gpt-4')
  })

  it('includes enabled use case in emitted config', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    await vi.waitFor(() => {
      const options = wrapper.find('.provider-select').findAll('option')
      expect(options.length).toBeGreaterThan(1)
    })
    
    const useCaseCheckbox = wrapper.find('.use-case-checkbox input[type="checkbox"]')
    await useCaseCheckbox.setValue(true)
    
    const useCaseProviderSelect = wrapper.findAll('.provider-select')[1]
    await useCaseProviderSelect.setValue('google')
    
    const useCaseModelInput = wrapper.findAll('.model-input-inner')[1]
    await useCaseModelInput.setValue('gemini-pro')
    
    const emittedEvents = wrapper.emitted('update:modelValue')!
    const lastEmittedValue = emittedEvents[emittedEvents.length - 1][0] as LLMConfig
    expect(lastEmittedValue.useCaseConfigs['planning']).toBeDefined()
    expect(lastEmittedValue.useCaseConfigs['planning'][0].provider).toBe('google')
    expect(lastEmittedValue.useCaseConfigs['planning'][0].model).toBe('gemini-pro')
  })

  it('includes use case in config when enabled even if provider or model is empty', async () => {
    const wrapper = mount(LlmConfigEditor)
    
    await vi.waitFor(() => {
      const options = wrapper.find('.provider-select').findAll('option')
      expect(options.length).toBeGreaterThan(1)
    })
    
    const useCaseCheckbox = wrapper.find('.use-case-checkbox input[type="checkbox"]')
    await useCaseCheckbox.setValue(true)
    
    const useCaseProviderSelect = wrapper.findAll('.provider-select')[1]
    await useCaseProviderSelect.setValue('google')
    // model is still empty
    
    const emittedEvents = wrapper.emitted('update:modelValue')!
    const lastEmittedValue = emittedEvents[emittedEvents.length - 1][0] as LLMConfig
    expect(lastEmittedValue.useCaseConfigs['planning']).toBeDefined()
    expect(lastEmittedValue.useCaseConfigs['planning'][0].provider).toBe('google')
    expect(lastEmittedValue.useCaseConfigs['planning'][0].model).toBe('')
  })

  it('loads existing use case configs from modelValue', async () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: '' }],
      useCaseConfigs: {
        'judging': [{ provider: 'openai', model: 'gpt-4', reasoningEffort: 'high' }]
      },
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await vi.waitFor(() => {
      const options = wrapper.find('.provider-select').findAll('option')
      expect(options.length).toBeGreaterThan(1)
    })
    
    const useCaseCheckboxes = wrapper.findAll('.use-case-checkbox input[type="checkbox"]')
    // planning is first, judging is second
    expect((useCaseCheckboxes[1].element as HTMLInputElement).checked).toBe(true)
    
    const providerSelects = wrapper.findAll('.provider-select')
    // index 2 is judging (0=default, 1=planning, 2=judging)
    expect((providerSelects[2].element as HTMLSelectElement).value).toBe('openai')
  })

  it('updates reasoning effort in emitted config', async () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'o1', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await vi.waitFor(() => {
      expect(wrapper.find('.reasoning-select-inline').exists()).toBe(true)
    })
    
    const reasoningSelect = wrapper.find('.reasoning-select-inline')
    await reasoningSelect.setValue('high')
    
    const emittedEvents = wrapper.emitted('update:modelValue')!
    const lastEmittedValue = emittedEvents[emittedEvents.length - 1][0] as LLMConfig
    expect(lastEmittedValue.defaults[0].reasoningEffort).toBe('high')
  })

  it('hides reasoning selector when model does not support reasoning', async () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await vi.waitFor(() => {
      expect(fetch).toHaveBeenCalled()
    })
    
    await wrapper.vm.$nextTick()
    
    expect(wrapper.find('.reasoning-select-inline').exists()).toBe(false)
  })

  it('shows reasoning selector when model supports reasoning', async () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'o1', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await vi.waitFor(() => {
      expect(wrapper.find('.reasoning-select-inline').exists()).toBe(true)
    })
  })

  it('hides reasoning selector when model is not found in API data', async () => {
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'unknown-model', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await vi.waitFor(() => {
      expect(fetch).toHaveBeenCalled()
    })
    
    await wrapper.vm.$nextTick()
    
    expect(wrapper.find('.reasoning-select-inline').exists()).toBe(false)
  })

  it('uses cached models data and does not fetch models when cache is fresh', async () => {
    store.setModelsCache(mockModelsData)
    const fetchMock = createMockFetch()
    vi.stubGlobal('fetch', fetchMock)
    
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'o1', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await vi.waitFor(() => {
      expect(wrapper.find('.reasoning-select-inline').exists()).toBe(true)
    })
    
    // Providers are always fetched, but models should not be fetched when cache is fresh
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/providers')
    expect(fetchMock).not.toHaveBeenCalledWith('/api/v1/models')
  })

  it('uses stale cache immediately and refreshes in background', async () => {
    const staleTimestamp = Date.now() - 6 * 60 * 1000 // 6 minutes ago
    sessionStorage.setItem('models_cache', JSON.stringify({
      data: mockModelsData,
      timestamp: staleTimestamp
    }))
    
    const fetchMock = createMockFetch()
    vi.stubGlobal('fetch', fetchMock)
    
    const llmConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'o1', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: llmConfig }
    })
    
    await wrapper.vm.$nextTick()
    
    // Should show reasoning selector immediately from stale cache
    expect(wrapper.find('.reasoning-select-inline').exists()).toBe(true)
    
    // Should have triggered background refresh
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalled()
    })
  })

  it('keeps use case checkbox checked after parent updates modelValue with emitted config', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })
    
    const useCaseCheckbox = wrapper.find('.use-case-checkbox input[type="checkbox"]')
    expect((useCaseCheckbox.element as HTMLInputElement).checked).toBe(false)
    
    await useCaseCheckbox.setValue(true)
    
    const emittedEvents = wrapper.emitted('update:modelValue')!
    const emittedValue = emittedEvents[emittedEvents.length - 1][0] as LLMConfig
    
    await wrapper.setProps({ modelValue: emittedValue })
    
    expect((useCaseCheckbox.element as HTMLInputElement).checked).toBe(true)
  })
})