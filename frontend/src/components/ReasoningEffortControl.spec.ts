import { describe, it, expect, afterEach, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import ReasoningEffortControl from './ReasoningEffortControl.vue'
import type { ModelsData } from '../lib/store'

const modelsData: ModelsData = {
  openai: {
    models: {
      'gpt-5.2': { reasoning: true, reasoning_options: [{ type: 'effort', values: ['none', 'low', 'medium', 'high', 'xhigh'] }] },
      'gpt-5.5': { reasoning: true, reasoning_options: [{ type: 'effort', values: ['low', 'medium', 'high', 'xhigh', 'max'] }] },
      'o1': { reasoning: true, reasoning_options: [{ type: 'toggle' }] },
      'mystery-reasoner': { reasoning: true },
      'gpt-4o': { reasoning: false },
    },
  },
  anthropic: {
    models: {
      'claude-sonnet-4-5': { reasoning: true, reasoning_options: [{ type: 'budget_tokens', min: 1024 }] },
    },
  },
}

type Props = { modelValue?: string; provider: string; model: string; modelsData: ModelsData }

const mountControl = (props: Props) =>
  mount(ReasoningEffortControl, { props, global: { plugins: [PrimeVue] } })

// Mirrors parent v-model wiring so emitted values flow back in as props.
const mountWithVModel = (props: Props) => {
  const wrapper: VueWrapper = mount(ReasoningEffortControl, {
    props: {
      ...props,
      'onUpdate:modelValue': (value: string) => wrapper.setProps({ modelValue: value }),
    },
    global: { plugins: [PrimeVue] },
  })
  return wrapper
}

const emittedValues = (wrapper: VueWrapper): string[] =>
  (wrapper.emitted('update:modelValue') ?? []).map((args) => args[0] as string)

describe('ReasoningEffortControl', () => {
  it.each([
    ['gpt-5.2', ['', 'none', 'low', 'medium', 'high', 'xhigh'], ['Default', 'None', 'Low', 'Medium', 'High', 'Extra']],
    ['gpt-5.5', ['', 'low', 'medium', 'high', 'xhigh', 'max'], ['Default', 'Low', 'Medium', 'High', 'Extra', 'Max']],
  ])('lists Default plus the %s ladder in declared order', (model, values, labels) => {
    const wrapper = mountControl({ modelValue: '', provider: 'openai', model, modelsData })

    const options = wrapper.find('select.reasoning-select-inline').findAll('option')
    expect(options.map((o) => o.attributes('value'))).toEqual(values)
    expect(options.map((o) => o.text())).toEqual(labels)
    expect(wrapper.find('.reasoning-effort-input').exists()).toBe(false)
  })

  it.each([
    ['openai', 'mystery-reasoner'],
    ['openai', 'o1'],
    ['anthropic', 'claude-sonnet-4-5'],
  ])('renders a free-text input for %s/%s which has no effort ladder', (provider, model) => {
    const wrapper = mountControl({ modelValue: '', provider, model, modelsData })

    expect(wrapper.find('.reasoning-effort-input').exists()).toBe(true)
    expect(wrapper.find('select').exists()).toBe(false)
  })

  it('emits custom typed values from the free-text input', async () => {
    const wrapper = mountControl({ modelValue: '', provider: 'openai', model: 'mystery-reasoner', modelsData })

    await wrapper.find('input').setValue('ultra')

    expect(emittedValues(wrapper)).toContain('ultra')
  })

  describe('autocomplete suggestions', () => {
    let wrapper: VueWrapper | undefined

    afterEach(() => {
      wrapper?.unmount()
      wrapper = undefined
    })

    const mountAttached = () => {
      wrapper = mount(ReasoningEffortControl, {
        props: { modelValue: '', provider: 'openai', model: 'mystery-reasoner', modelsData },
        global: { plugins: [PrimeVue] },
        attachTo: document.body,
      })
      return wrapper
    }

    // The overlay is teleported to document.body, outside the wrapper's subtree.
    const renderedOptions = () => Array.from(document.body.querySelectorAll<HTMLElement>('.p-autocomplete-option'))
    const renderedLabels = () => renderedOptions().map((o) => o.textContent?.trim())

    it('shows the default effort set with display labels on focus and emits the chosen value', async () => {
      const control = mountAttached()

      await control.find('input').trigger('focus')

      await vi.waitFor(() => {
        expect(renderedLabels()).toEqual(['Low', 'Medium', 'High', 'Extra', 'Max'])
      })

      renderedOptions().find((o) => o.textContent?.trim() === 'Extra')!.click()

      expect(emittedValues(control).at(-1)).toBe('xhigh')
      await vi.waitFor(() => {
        expect(renderedOptions()).toHaveLength(0)
      })
    })

    it('filters suggestions by display label as the user types', async () => {
      const control = mountAttached()

      await control.find('input').setValue('extra')

      await vi.waitFor(() => {
        expect(renderedLabels()).toEqual(['Extra'])
      })
    })
  })

  it.each([
    ['openai', 'gpt-4o'],
    ['openai', 'unknown-model'],
    ['openai', ''],
  ])('renders nothing for non-reasoning model %s/%s', (provider, model) => {
    const wrapper = mountControl({ modelValue: 'high', provider, model, modelsData })

    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.find('.reasoning-effort-input').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('switches to the nearest level on model change and restores the manual choice later', async () => {
    const wrapper = mountWithVModel({ modelValue: '', provider: 'openai', model: 'gpt-5.5', modelsData })

    await wrapper.find('select').setValue('max')
    expect(wrapper.props('modelValue')).toBe('max')

    await wrapper.setProps({ model: 'gpt-5.2' })
    expect(wrapper.props('modelValue')).toBe('xhigh')

    await wrapper.setProps({ model: 'gpt-5.5' })
    expect(wrapper.props('modelValue')).toBe('max')
  })

  it('does not remember auto-switched values as manual choices', async () => {
    const wrapper = mountWithVModel({ modelValue: 'max', provider: 'openai', model: 'gpt-5.5', modelsData })

    await wrapper.setProps({ model: 'gpt-5.2' })
    expect(wrapper.props('modelValue')).toBe('xhigh')

    await wrapper.setProps({ model: 'gpt-5.5' })
    expect(wrapper.props('modelValue')).toBe('xhigh')
    expect(emittedValues(wrapper)).toEqual(['xhigh'])
  })

  it.each([
    ['lowest', 'gpt-5.5', 'low'],
    ['highest', 'gpt-5.2', 'xhigh'],
    ['highest', 'gpt-5.5', 'max'],
    ['turbo', 'gpt-5.2', ''],
  ])('rewrites initial %s on %s to %s', (initial, model, expected) => {
    const wrapper = mountControl({ modelValue: initial, provider: 'openai', model, modelsData })

    expect(emittedValues(wrapper)).toEqual([expected])
  })

  it('keeps supported initial values without emitting', () => {
    const wrapper = mountControl({ modelValue: 'none', provider: 'openai', model: 'gpt-5.2', modelsData })

    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('auto-switches once models data arrives', async () => {
    const wrapper = mountControl({ modelValue: 'highest', provider: 'openai', model: 'gpt-5.2', modelsData: {} })
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()

    await wrapper.setProps({ modelsData })

    expect(emittedValues(wrapper)).toEqual(['xhigh'])
  })
})
