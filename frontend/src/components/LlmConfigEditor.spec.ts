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
      'o1': { reasoning: true, reasoning_options: [{ type: 'effort', values: ['low', 'medium', 'high'] }] },
      'gpt-4': { reasoning: false },
      'gpt-5.2': {
        reasoning: false,
        experimental: {
          modes: {
            fast: { provider: { body: { service_tier: 'priority' } } },
            flex: { provider: { body: { service_tier: 'flex' } } },
            ultrafast: { provider: { body: { service_tier: 'ultrafast' } } },
          },
        },
      },
    }
  },
  anthropic: {
    models: {
      'claude-3': { reasoning: false, release_date: '2024-03-04' },
      'claude-opus-5': {
        reasoning: true,
        release_date: '2026-05-01',
        experimental: { modes: { fast: { provider: { body: { speed: 'fast' } } } } },
      },
      'claude-opus-4-1': { reasoning: true, release_date: '2025-08-05' },
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
    if (url.startsWith('/api/v1/providers') || url.startsWith('/api/v1/workspaces/')) {
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

  describe('model input', () => {
    const typeModel = async (wrapper: ReturnType<typeof mount>, text: string, inputType: string) => {
      const input = wrapper.find('.model-input-inner')
      ;(input.element as HTMLInputElement).value = text
      await input.trigger('input', { inputType })
      return input.element as HTMLInputElement
    }

    const mountWithAnthropic = async () => {
      const wrapper = mount(LlmConfigEditor, {
        props: { modelValue: { defaults: [{ provider: 'anthropic', model: '' }], useCaseConfigs: {} } }
      })
      await vi.waitFor(() => {
        expect(store.getModelsCache()).not.toBeNull()
      })
      return wrapper
    }

    it('inline-completes typed text with the newest matching model, selecting the rest', async () => {
      const wrapper = await mountWithAnthropic()

      const input = await typeModel(wrapper, 'claude-o', 'insertText')

      expect(input.value).toBe('claude-opus-5')
      expect([input.selectionStart, input.selectionEnd]).toEqual([8, 13])
      const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig
      expect(emitted.defaults[0].model).toBe('claude-opus-5')
    })

    it.each(['deleteContentBackward', 'insertFromPaste'])('leaves %s input uncompleted', async (inputType) => {
      const wrapper = await mountWithAnthropic()

      const input = await typeModel(wrapper, 'claude-o', inputType)

      expect(input.value).toBe('claude-o')
      const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig
      expect(emitted.defaults[0].model).toBe('claude-o')
    })

    it('suggests the newest released models first', async () => {
      const wrapper = await mountWithAnthropic()

      wrapper.findComponent(AutoComplete).vm.$emit('complete', { query: 'claude' })
      await wrapper.vm.$nextTick()

      expect(wrapper.findComponent(AutoComplete).props('suggestions')).toEqual(['claude-opus-5', 'claude-opus-4-1', 'claude-3'])
    })
  })

  it('marks autocomplete overlays so they stack above containing modals', () => {
    const wrapper = mount(LlmConfigEditor)

    const autocomplete = wrapper.findComponent(AutoComplete)
    expect(autocomplete.props('overlayClass')).toBe('llm-config-editor-overlay')
  })

  it('renders all use case rows', () => {
    const wrapper = mount(LlmConfigEditor)
    
    const useCaseCheckboxes = wrapper.findAll('.use-case-checkbox')
    expect(useCaseCheckboxes.length).toBe(4)
    
    const labels = useCaseCheckboxes.map(cb => cb.find('.model-label').text())
    expect(labels).toContain('Plan')
    expect(labels).toContain('Review')
    expect(labels).toContain('Context')
    expect(labels).toContain('Advisor')
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

  it('renders the model ladder as reasoning options and emits the selected level', async () => {
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
    const options = reasoningSelect.findAll('option')
    expect(options.map((o) => o.attributes('value'))).toEqual(['', 'low', 'medium', 'high'])
    expect(options.map((o) => o.text())).toEqual(['Default', 'Low', 'Medium', 'High'])

    await reasoningSelect.setValue('medium')

    const lastEmittedValue = wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig
    expect(lastEmittedValue.defaults[0].reasoningEffort).toBe('medium')
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

  const selectedTierLabel = (select: ReturnType<ReturnType<typeof mount>['find']>) => {
    const el = select.element as HTMLSelectElement
    return el.options[el.selectedIndex]?.text
  }

  it('offers the openai tiers available for the model and emits the chosen service tier', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })

    await vi.waitFor(() => {
      expect(wrapper.find('.service-tier-select').exists()).toBe(true)
    })

    const tierSelect = wrapper.find('.service-tier-select')
    expect(tierSelect.findAll('option').map((o) => o.text())).toEqual(['Standard', 'Flex', 'Fast', 'Ultrafast'])
    expect(selectedTierLabel(tierSelect)).toBe('Standard')

    for (const [label, serviceTier] of [['Flex', 'flex'], ['Fast', 'priority'], ['Ultrafast', 'ultrafast'], ['Standard', '']]) {
      const option = tierSelect.findAll('option').find((o) => o.text() === label)!
      await tierSelect.setValue(option.attributes('value'))
      const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig
      expect(emitted.defaults[0].serviceTier).toBe(serviceTier)
      expect(emitted.defaults[0].speed).toBe('')
    }
  })

  it('round-trips anthropic fast mode through the tier selector', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-opus-5', reasoningEffort: '', speed: 'fast' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })

    await vi.waitFor(() => {
      expect(wrapper.find('.service-tier-select').exists()).toBe(true)
    })

    const tierSelect = wrapper.find('.service-tier-select')
    expect(tierSelect.findAll('option').map((o) => o.text())).toEqual(['Standard', 'Fast'])
    expect(selectedTierLabel(tierSelect)).toBe('Fast')

    const standard = tierSelect.findAll('option').find((o) => o.text() === 'Standard')!
    await tierSelect.setValue(standard.attributes('value'))
    expect((wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig).defaults[0].speed).toBe('')

    const fast = tierSelect.findAll('option').find((o) => o.text() === 'Fast')!
    await tierSelect.setValue(fast.attributes('value'))
    expect((wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig).defaults[0].speed).toBe('fast')
  })

  it('notes that anthropic fast mode requires usage credits only while it is selected', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-opus-5', reasoningEffort: '' }],
      useCaseConfigs: {
        planning: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '', serviceTier: 'ultrafast' }],
      },
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })

    await vi.waitFor(() => {
      expect(wrapper.findAll('.service-tier-select')).toHaveLength(2)
    })
    expect(wrapper.find('[role="note"]').exists()).toBe(false)

    const tierSelect = wrapper.find('.service-tier-select')
    const fast = tierSelect.findAll('option').find((o) => o.text() === 'Fast')!
    await tierSelect.setValue(fast.attributes('value'))
    expect(wrapper.findAll('[role="note"]')).toHaveLength(1)
    expect(wrapper.find('[role="note"]').text()).toContain('usage credits')

    const standard = tierSelect.findAll('option').find((o) => o.text() === 'Standard')!
    await tierSelect.setValue(standard.attributes('value'))
    expect(wrapper.find('[role="note"]').exists()).toBe(false)
  })

  it('resets a tier the newly selected model does not offer', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '', serviceTier: 'ultrafast' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })
    await vi.waitFor(() => {
      expect(wrapper.find('.provider-select').findAll('option').length).toBeGreaterThan(1)
      expect(selectedTierLabel(wrapper.find('.service-tier-select'))).toBe('Ultrafast')
    })

    await wrapper.find('.model-input-inner').setValue('claude-opus-5')
    await wrapper.find('.provider-select').setValue('anthropic')

    const tierSelect = wrapper.find('.service-tier-select')
    expect(tierSelect.findAll('option').map((o) => o.text())).toEqual(['Standard', 'Fast'])
    expect(selectedTierLabel(tierSelect)).toBe('Standard')
    const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig
    expect(emitted.defaults[0]).toMatchObject({ provider: 'anthropic', model: 'claude-opus-5', serviceTier: '', speed: '' })
  })

  it('keeps a tier when the provider changes to one that offers it for the model', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: '', model: 'claude-opus-5', reasoningEffort: '', speed: 'fast' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })
    await vi.waitFor(() => {
      expect(wrapper.find('.provider-select').findAll('option').length).toBeGreaterThan(1)
      expect(wrapper.find('.service-tier-select').exists()).toBe(true)
    })

    await wrapper.find('.provider-select').setValue('anthropic')

    expect(selectedTierLabel(wrapper.find('.service-tier-select'))).toBe('Fast')
    const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig
    expect(emitted.defaults[0]).toMatchObject({ provider: 'anthropic', speed: 'fast' })
  })

  describe('remembered tier', () => {
    // Mirrors the forms using the editor, which feed each emitted config back in
    const mountWithParentEcho = async (initialConfig: LLMConfig) => {
      const wrapper: ReturnType<typeof mount> = mount(LlmConfigEditor, {
        props: {
          modelValue: initialConfig,
          'onUpdate:modelValue': (value: LLMConfig) => wrapper.setProps({ modelValue: value }),
        },
      })
      await vi.waitFor(() => {
        expect(store.getModelsCache()).not.toBeNull()
        expect(wrapper.find('.provider-select').findAll('option').length).toBeGreaterThan(1)
      })
      return wrapper
    }

    const tierShown = (wrapper: ReturnType<typeof mount>) => {
      const select = wrapper.find('.service-tier-select')
      return select.exists() ? selectedTierLabel(select) : 'none'
    }

    const lastEmittedDefault = (wrapper: ReturnType<typeof mount>) =>
      (wrapper.emitted('update:modelValue')!.at(-1)![0] as LLMConfig).defaults[0]

    const editModel = async (wrapper: ReturnType<typeof mount>, text: string) => {
      const input = wrapper.find('.model-input-inner')
      ;(input.element as HTMLInputElement).value = text
      await input.trigger('input', { inputType: 'deleteContentBackward' })
    }

    it('restores the loaded tier once the model offers it again', async () => {
      const wrapper = await mountWithParentEcho({
        defaults: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '', serviceTier: 'ultrafast' }],
        useCaseConfigs: {},
      })
      expect(tierShown(wrapper)).toBe('Ultrafast')

      await editModel(wrapper, 'claude-opus-5')
      await wrapper.find('.provider-select').setValue('anthropic')
      expect(tierShown(wrapper)).toBe('Standard')
      expect(lastEmittedDefault(wrapper).serviceTier).toBe('')

      await wrapper.find('.provider-select').setValue('openai')
      await editModel(wrapper, 'gpt-5.')
      expect(tierShown(wrapper)).toBe('none')
      await editModel(wrapper, 'gpt-5.2')

      expect(tierShown(wrapper)).toBe('Ultrafast')
      expect(lastEmittedDefault(wrapper)).toMatchObject({ provider: 'openai', model: 'gpt-5.2', serviceTier: 'ultrafast', speed: '' })
    })

    it('remembers the tier chosen most recently', async () => {
      const wrapper = await mountWithParentEcho({
        defaults: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '', serviceTier: 'ultrafast' }],
        useCaseConfigs: {},
      })
      expect(tierShown(wrapper)).toBe('Ultrafast')

      const tierSelect = wrapper.find('.service-tier-select')
      const flex = tierSelect.findAll('option').find((o) => o.text() === 'Flex')!
      await tierSelect.setValue(flex.attributes('value'))
      await editModel(wrapper, 'gpt-')
      await editModel(wrapper, 'gpt-5.2')

      expect(tierShown(wrapper)).toBe('Flex')
      expect(lastEmittedDefault(wrapper).serviceTier).toBe('flex')
    })

    it('replaces the remembered tier when a different config is loaded', async () => {
      const wrapper = await mountWithParentEcho({
        defaults: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '', serviceTier: 'ultrafast' }],
        useCaseConfigs: {},
      })
      expect(tierShown(wrapper)).toBe('Ultrafast')

      await wrapper.setProps({
        modelValue: { defaults: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '', serviceTier: 'flex' }], useCaseConfigs: {} },
      })
      await editModel(wrapper, 'gpt-')
      await editModel(wrapper, 'gpt-5.2')

      expect(tierShown(wrapper)).toBe('Flex')
    })
  })

  it('initializes the tier for use case config from props.modelValue', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: '' }],
      useCaseConfigs: {
        planning: [{ provider: 'openai', model: 'gpt-5.2', reasoningEffort: '', serviceTier: 'flex' }],
      },
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })

    await vi.waitFor(() => {
      expect(wrapper.find('.service-tier-select').exists()).toBe(true)
    })

    const tierSelects = wrapper.findAll('.service-tier-select')
    expect(tierSelects).toHaveLength(1)
    expect(selectedTierLabel(tierSelects[0])).toBe('Flex')
  })

  it('keeps a configured tier visible for models that do not list it', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'anthropic', model: 'claude-3', reasoningEffort: '', speed: 'fast' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })

    await vi.waitFor(() => {
      expect(wrapper.find('.service-tier-select').exists()).toBe(true)
    })

    expect(selectedTierLabel(wrapper.find('.service-tier-select'))).toBe('Fast')
  })

  it('hides the tier selector for models without tiers', async () => {
    const initialConfig: LLMConfig = {
      defaults: [{ provider: 'openai', model: 'gpt-4', reasoningEffort: '' }],
      useCaseConfigs: {},
    }
    const wrapper = mount(LlmConfigEditor, {
      props: { modelValue: initialConfig }
    })

    await vi.waitFor(() => {
      expect(fetch).toHaveBeenCalled()
    })
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.service-tier-select').exists()).toBe(false)
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

  it('fetches providers from the workspace-scoped endpoint when workspaceId is provided', async () => {
    const fetchMock = createMockFetch()
    vi.stubGlobal('fetch', fetchMock)

    mount(LlmConfigEditor, {
      props: { workspaceId: 'ws_123', profileId: 'work' }
    })

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith('/api/v1/workspaces/ws_123/providers')
    })

    expect(fetchMock).not.toHaveBeenCalledWith(expect.stringContaining('profileId'))
  })

  it('falls back to the generic profile-based endpoint when no workspaceId is provided', async () => {
    const fetchMock = createMockFetch()
    vi.stubGlobal('fetch', fetchMock)

    mount(LlmConfigEditor, {
      props: { profileId: 'work' }
    })

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith('/api/v1/providers?profileId=work')
    })
  })
})