import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FlowModelConfigModal from '../FlowModelConfigModal.vue'
import type { LLMConfig } from '../../lib/models'

const initialConfig: LLMConfig = {
  defaults: [{ provider: 'anthropic', model: 'old-model' }],
  useCaseConfigs: {},
}

const updatedConfig: LLMConfig = {
  defaults: [{ provider: 'openai', model: 'new-model' }],
  useCaseConfigs: {
    planning: [{ provider: 'anthropic', model: 'planner' }],
  },
}

const EditorStub = {
  name: 'ModelConfigPresetEditor',
  props: ['editor'],
  template: '<button class="change-config" @click="editor.llmConfig.value = config">Change</button>',
  data: () => ({ config: updatedConfig }),
}

// The preset editor resolves the workspace profile on its own and port
// mappings are covered by their own suite, so both are answered outside the
// flow config sequence these tests set up.
const withProfileRequests = (
  fetchMock: (url: string, options?: RequestInit) => unknown,
) => (url: string, options?: RequestInit) => {
  if (url === '/api/v1/workspaces/workspace-1') {
    return Promise.resolve({ ok: true, json: async () => ({ workspace: { id: 'workspace-1' } }) })
  }
  if (url.startsWith('/api/v1/providers')) {
    return Promise.resolve({ ok: true, json: async () => ({ providers: ['anthropic', 'openai'] }) })
  }
  if (typeof options?.body === 'string' && options.body.includes('port_forwards')) {
    return Promise.resolve({ ok: false })
  }
  return fetchMock(url, options)
}

const mountModal = () => mount(FlowModelConfigModal, {
  props: {
    workspaceId: 'workspace-1',
    flowId: 'flow-1',
  },
  global: {
    stubs: {
      Teleport: true,
      ModelConfigPresetEditor: EditorStub,
    },
  },
})

describe('FlowModelConfigModal', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('shows an actionable query error and retries loading', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: false,
        statusText: 'Unavailable',
        json: async () => ({ error: 'workflow unavailable' }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
      .mockResolvedValueOnce({
        ok: false,
      })
    vi.stubGlobal('fetch', withProfileRequests(fetchMock))

    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.find('[role="alert"]').text()).toContain('workflow unavailable')
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()

    expect(wrapper.findComponent({ name: 'ModelConfigPresetEditor' }).exists()).toBe(true)
  })

  it('keeps applying until the queried workflow state matches', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
      .mockResolvedValueOnce({
        ok: false,
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ message: 'accepted' }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: updatedConfig }),
      })
    vi.stubGlobal('fetch', withProfileRequests(fetchMock))
    vi.useFakeTimers()

    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('.change-config').trigger('click')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.apply-button').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('close')).toBeFalsy()

    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()

    expect(wrapper.emitted('applied')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/workspaces/workspace-1/flows/flow-1/model_config',
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ config: updatedConfig }),
      }),
    )

    vi.useRealTimers()
  })

  it('retains edits and exposes a retryable polling error', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
      .mockResolvedValueOnce({
        ok: false,
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ message: 'accepted' }),
      })
      .mockResolvedValueOnce({
        ok: false,
        statusText: 'Unavailable',
        json: async () => ({ error: 'query failed' }),
      })
    vi.stubGlobal('fetch', withProfileRequests(fetchMock))

    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('.change-config').trigger('click')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.find('[role="alert"]').text()).toContain('Retry')
    expect(wrapper.findComponent({ name: 'ModelConfigPresetEditor' }).exists()).toBe(true)
    expect(wrapper.get('.apply-button').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeFalsy()
  })

  it('retains edits and allows retry after the update request fails', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
      .mockResolvedValueOnce({
        ok: false,
      })
      .mockResolvedValueOnce({
        ok: false,
        statusText: 'Conflict',
        json: async () => ({ error: 'update rejected' }),
      })
    vi.stubGlobal('fetch', withProfileRequests(fetchMock))

    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('.change-config').trigger('click')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.find('[role="alert"]').text()).toContain('update rejected')
    expect(wrapper.find('[role="alert"]').text()).toContain('Retry')
    expect(wrapper.findComponent({ name: 'ModelConfigPresetEditor' }).exists()).toBe(true)
    expect(wrapper.get('.apply-button').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeFalsy()
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('ignores close controls until the accepted update is observed', async () => {
    let resolveQuery: ((response: unknown) => void) | undefined
    const pendingQuery = new Promise((resolve) => {
      resolveQuery = resolve
    })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
      .mockResolvedValueOnce({
        ok: false,
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ message: 'accepted' }),
      })
      .mockReturnValueOnce(pendingQuery)
    vi.stubGlobal('fetch', withProfileRequests(fetchMock))

    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('.change-config').trigger('click')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.close-button').attributes('disabled')).toBeDefined()
    await wrapper.get('.close-button').trigger('click')
    await wrapper.get('.model-config-overlay').trigger('click')
    expect(wrapper.emitted('close')).toBeFalsy()

    resolveQuery?.({
      ok: true,
      json: async () => ({ result: updatedConfig }),
    })
    await flushPromises()

    expect(wrapper.emitted('applied')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('stops polling after the bounded timeout without discarding edits', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
      .mockResolvedValueOnce({
        ok: false,
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ message: 'accepted' }),
      })
      .mockResolvedValue({
        ok: true,
        json: async () => ({ result: initialConfig }),
      })
    vi.stubGlobal('fetch', withProfileRequests(fetchMock))
    vi.useFakeTimers()

    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('.change-config').trigger('click')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()

    expect(wrapper.find('[role="alert"]').text()).toContain('did not apply it in time')
    expect(wrapper.findComponent({ name: 'ModelConfigPresetEditor' }).exists()).toBe(true)
    expect(wrapper.get('.apply-button').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeFalsy()

    vi.useRealTimers()
  })
})