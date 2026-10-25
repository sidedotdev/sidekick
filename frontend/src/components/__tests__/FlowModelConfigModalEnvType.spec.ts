import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FlowModelConfigModal from '../FlowModelConfigModal.vue'

const flowUrl = '/api/v1/workspaces/workspace-1/flows/flow-1'
const modal = {
  vm: true,
  cpu: 2,
  memory: 2048,
  volumes: [{ name: 'cache', mountPath: '/cache' }],
}
const portForwards = [{ hostPort: 3000, containerPort: 8080 }]

const setup = (envType = 'local', error = '') => {
  const settings = { envType, modal, portForwards }
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    if (url === `${flowUrl}/query` && body?.query === 'model_config') {
      return { ok: true, json: async () => ({ result: { defaults: [{ provider: 'openai', model: 'test' }] } }) }
    }
    if (url === `${flowUrl}/query` && body?.query === 'env') {
      return { ok: true, json: async () => ({ result: settings }) }
    }
    if (url === `${flowUrl}/env` && init?.method === 'PUT') {
      return error
        ? { ok: false, json: async () => ({ error }) }
        : { ok: true, json: async () => ({ flowActionId: 'switch-1', settings }) }
    }
    if (url === '/api/v1/workspaces/workspace-1') {
      return { ok: true, json: async () => ({ workspace: { id: 'workspace-1' } }) }
    }
    if (url.startsWith('/api/v1/providers')) {
      return { ok: true, json: async () => ({ providers: ['openai'] }) }
    }
    throw new Error(`Unexpected request: ${url} ${init?.body ?? ''}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  const wrapper = mount(FlowModelConfigModal, {
    props: { workspaceId: 'workspace-1', flowId: 'flow-1' },
    global: { stubs: { Teleport: true, ModelConfigPresetEditor: true } },
  })
  return { wrapper, fetchMock }
}

describe('FlowModelConfigModal environment type', () => {
  beforeEach(() => {
    localStorage.clear()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it.each([
    ['local', 'devpod'],
    ['local', 'openshell'],
    ['local', 'modal'],
    ['modal', 'local'],
  ])('switches %s to %s with the complete desired settings', async (from, to) => {
    const { wrapper, fetchMock } = setup(from)
    await flushPromises()

    const select = wrapper.get<HTMLSelectElement>('#environment-type')
    expect(select.element.value).toBe(from)
    expect(select.findAll('option').map((option) => option.element.value))
      .toEqual(['local', 'devpod', 'openshell', 'modal'])

    await select.setValue(to)
    expect(wrapper.find('.modal-environment').exists()).toBe(to === 'modal')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1)
    expect(fetchMock).toHaveBeenCalledWith(`${flowUrl}/env`, expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ envType: to, portForwards, modal }),
    }))
    expect(wrapper.emitted('applied')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not submit unchanged settings', async () => {
    const { wrapper, fetchMock } = setup()
    await flushPromises()
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(0)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('retains the selected environment and displays a retryable failure', async () => {
    const { wrapper, fetchMock } = setup('local', 'an environment switch is already in progress')
    await flushPromises()
    await wrapper.get('#environment-type').setValue('devpod')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.error-state[role="alert"]').text()).toContain('switch is already in progress')
    expect(wrapper.get<HTMLSelectElement>('#environment-type').element.value).toBe('devpod')
    expect(wrapper.get('.apply-button').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeFalsy()

    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(2)
    wrapper.unmount()
  })
})