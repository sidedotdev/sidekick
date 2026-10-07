import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import LlmConfigEditor from './LlmConfigEditor.vue'
import EmbeddingConfigEditor from './EmbeddingConfigEditor.vue'

afterEach(() => {
  vi.unstubAllGlobals()
  sessionStorage.clear()
})

describe.each([
  ['LLM', LlmConfigEditor],
  ['embedding', EmbeddingConfigEditor],
] as const)('%s provider loading', (_name, component) => {
  it('displays configuration errors and clears them when the next request succeeds', async () => {
    let failed = true
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.startsWith('/api/v1/providers')) {
        return failed
          ? {
              ok: false,
              json: async () => ({ error: 'Failed to load sidekick config: invalid provider type: unsupported' }),
            }
          : { ok: true, json: async () => ({ providers: ['custom-anthropic'] }) }
      }
      return { ok: true, json: async () => ({}) }
    }))

    const wrapper = mount(component, { global: { plugins: [PrimeVue] } })
    try {
      await flushPromises()
      expect(wrapper.get('[role="alert"]').text()).toContain('invalid provider type: unsupported')

      failed = false
      await wrapper.setProps({ profileId: 'work' })
      await flushPromises()
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
      expect(wrapper.get('select').text()).toContain('custom-anthropic')
    } finally {
      wrapper.unmount()
    }
  })

  it.each(['network', 'non-JSON response'])('displays a fallback error for a %s failure', async (failure) => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.startsWith('/api/v1/providers')) {
        if (failure === 'network') throw new Error('network unavailable')
        return { ok: false, json: async () => { throw new SyntaxError('not JSON') } }
      }
      return { ok: true, json: async () => ({}) }
    }))

    const wrapper = mount(component, { global: { plugins: [PrimeVue] } })
    try {
      await flushPromises()
      expect(wrapper.get('[role="alert"]').text()).toContain('Failed to load providers')
    } finally {
      wrapper.unmount()
    }
  })
})