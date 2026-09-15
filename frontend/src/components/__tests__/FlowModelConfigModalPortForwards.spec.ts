import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FlowModelConfigModal from '../FlowModelConfigModal.vue'
import type { LLMConfig, ModalEnvConfig, PortForwardConfig } from '../../lib/models'

const flowUrl = '/api/v1/workspaces/workspace-1/flows/flow-1'

const loadedLlmConfig: LLMConfig = {
  defaults: [{ provider: 'anthropic', model: 'model-a' }],
  useCaseConfigs: {},
}

const editedLlmConfig: LLMConfig = {
  defaults: [{ provider: 'openai', model: 'model-b' }],
  useCaseConfigs: {},
}

const loadedModalConfig: ModalEnvConfig = { vm: false, cpu: 1, memory: 1024 }

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value))

interface StubResponse {
  ok: boolean
  statusText?: string
  json: () => Promise<unknown>
}

const okResponse = (body: unknown): StubResponse => ({ ok: true, json: async () => body })

const failedResponse = (error: string): StubResponse => ({
  ok: false,
  statusText: 'Bad Request',
  json: async () => ({ error }),
})

interface FlowState {
  llmConfig: LLMConfig
  modalConfig: ModalEnvConfig | null
  portForwards: PortForwardConfig[] | null
  portForwardsQueryFails: boolean
}

interface RecordedCall {
  url: string
  method: string
  body?: Record<string, unknown>
}

interface MockOptions {
  onPortForwardsUpdate?: (attempt: number) => StubResponse | Promise<StubResponse> | undefined
  onModalConfigUpdate?: (attempt: number) => StubResponse | Promise<StubResponse> | undefined
}

const createFetchMock = (state: Partial<FlowState> = {}, options: MockOptions = {}) => {
  const flowState: FlowState = {
    llmConfig: clone(loadedLlmConfig),
    modalConfig: clone(loadedModalConfig),
    portForwards: [],
    portForwardsQueryFails: false,
    ...state,
  }
  const calls: RecordedCall[] = []
  let portForwardAttempts = 0
  let modalConfigAttempts = 0

  const fetchMock = vi.fn(async (url: string, init: RequestInit = {}): Promise<StubResponse> => {
    const method = init.method ?? 'GET'
    const body = typeof init.body === 'string'
      ? (JSON.parse(init.body) as Record<string, unknown>)
      : undefined
    calls.push({ url, method, body })

    if (url === '/api/v1/workspaces/workspace-1') {
      return okResponse({ workspace: { id: 'workspace-1' } })
    }
    if (url.startsWith('/api/v1/providers')) {
      return okResponse({ providers: ['anthropic', 'openai'] })
    }
    if (url === `${flowUrl}/query`) {
      switch (body?.query) {
        case 'model_config':
          return okResponse({ result: flowState.llmConfig })
        case 'modal_config':
          return flowState.modalConfig
            ? okResponse({ result: flowState.modalConfig })
            : failedResponse('modal configuration unavailable')
        case 'port_forwards':
          return flowState.portForwardsQueryFails
            ? failedResponse('port forwards unavailable')
            : okResponse({ result: { portForwards: flowState.portForwards } })
        default:
          throw new Error(`unexpected query: ${String(body?.query)}`)
      }
    }
    if (url === `${flowUrl}/model_config` && method === 'PUT') {
      flowState.llmConfig = body?.config as LLMConfig
      return okResponse({ message: 'accepted' })
    }
    if (url === `${flowUrl}/port_forwards` && method === 'PUT') {
      portForwardAttempts += 1
      const override = await options.onPortForwardsUpdate?.(portForwardAttempts)
      if (override) {
        return override
      }
      flowState.portForwards = body?.portForwards as PortForwardConfig[]
      return okResponse({ portForwards: flowState.portForwards })
    }
    if (url === `${flowUrl}/modal_config` && method === 'PUT') {
      modalConfigAttempts += 1
      const override = await options.onModalConfigUpdate?.(modalConfigAttempts)
      if (override) {
        return override
      }
      flowState.modalConfig = body?.config as ModalEnvConfig
      return okResponse({ message: 'Modal environment recreated' })
    }
    throw new Error(`unexpected request: ${method} ${url}`)
  })

  const putsFor = (path: string) => calls.filter(
    (call) => call.method === 'PUT' && call.url === `${flowUrl}/${path}`,
  )

  return { fetchMock, calls, putsFor, flowState }
}

const EditorStub = {
  name: 'ModelConfigPresetEditor',
  props: ['editor'],
  template: '<button class="change-model" @click="editor.llmConfig.value = config">Change</button>',
  data: () => ({ config: editedLlmConfig }),
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

const inputValue = (element: Element): string => (element as HTMLInputElement).value

describe('FlowModelConfigModal port mappings', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('applies edited mappings without touching the other sections', async () => {
    const { fetchMock, putsFor } = createFetchMock({
      portForwards: [{ hostPort: 3000 }, { hostPort: 5432, containerPort: 6432 }],
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    const rows = wrapper.findAll('.port-forward')
    expect(rows).toHaveLength(2)
    expect(inputValue(rows[0].get('.host-port').element)).toBe('3000')
    expect(inputValue(rows[0].get('.container-port').element)).toBe('')
    expect(inputValue(rows[1].get('.container-port').element)).toBe('6432')

    await rows[0].get('.host-port').setValue('3100')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')).toHaveLength(1)
    expect(putsFor('port_forwards')[0].body).toEqual({
      portForwards: [{ hostPort: 3100 }, { hostPort: 5432, containerPort: 6432 }],
    })
    expect(putsFor('model_config')).toHaveLength(0)
    expect(putsFor('modal_config')).toHaveLength(0)
    expect(wrapper.emitted('applied')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('submits an empty list when every mapping is removed', async () => {
    const { fetchMock, putsFor } = createFetchMock({ portForwards: [{ hostPort: 3000 }] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.remove-port-forward').trigger('click')
    expect(wrapper.findAll('.port-forward')).toHaveLength(0)

    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')[0].body).toEqual({ portForwards: [] })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('defaults a blank sandbox port to the host port', async () => {
    const { fetchMock, putsFor } = createFetchMock({ portForwards: [] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.add-port-forward').trigger('click')
    await wrapper.get('.host-port').setValue('4000')

    expect(wrapper.get('.container-port').attributes('placeholder')).toBe('4000')

    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')[0].body).toEqual({ portForwards: [{ hostPort: 4000 }] })
  })

  it('reports conflicting mappings before contacting the workflow', async () => {
    const { fetchMock, putsFor } = createFetchMock({
      portForwards: [{ hostPort: 3000 }, { hostPort: 4000, containerPort: 8080 }],
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.findAll('.port-forward')[1].get('.container-port').setValue('3000')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.error-state[role="alert"]').text()).toContain('3000')
    expect(putsFor('port_forwards')).toHaveLength(0)
    expect(wrapper.emitted('close')).toBeFalsy()
  })

  it('rejects a mapping without a usable host port', async () => {
    const { fetchMock, putsFor } = createFetchMock({ portForwards: [{ hostPort: 3000 }] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.host-port').setValue('')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.error-state[role="alert"]').text()).toContain('host port')
    expect(putsFor('port_forwards')).toHaveLength(0)
    expect(wrapper.emitted('close')).toBeFalsy()
  })

  it('leaves mappings alone when only sandbox settings change', async () => {
    const { fetchMock, putsFor } = createFetchMock({ portForwards: [{ hostPort: 3000 }] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.modal-environment input[type="checkbox"]').setValue(true)
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('modal_config')).toHaveLength(1)
    expect(putsFor('port_forwards')).toHaveLength(0)
    expect(putsFor('model_config')).toHaveLength(0)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('retries only the section that failed', async () => {
    const { fetchMock, putsFor } = createFetchMock(
      { portForwards: [{ hostPort: 3000 }] },
      {
        onModalConfigUpdate: (attempt) => (
          attempt === 1 ? failedResponse('sandbox recreation failed') : undefined
        ),
      },
    )
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.host-port').setValue('3100')
    await wrapper.get('.modal-environment input[type="number"]').setValue('2')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.error-state[role="alert"]').text()).toContain('sandbox recreation failed')
    expect(putsFor('port_forwards')).toHaveLength(1)
    expect(wrapper.emitted('close')).toBeFalsy()
    expect(inputValue(wrapper.get('.host-port').element)).toBe('3100')

    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')).toHaveLength(1)
    expect(putsFor('modal_config')).toHaveLength(2)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('applies every changed section in a single pass', async () => {
    const { fetchMock, putsFor } = createFetchMock({ portForwards: [] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.change-model').trigger('click')
    await wrapper.get('.add-port-forward').trigger('click')
    await wrapper.get('.host-port').setValue('4000')
    await wrapper.get('.container-port').setValue('4100')
    await wrapper.get('.modal-environment input[type="checkbox"]').setValue(true)
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('model_config')).toHaveLength(1)
    expect(putsFor('modal_config')).toHaveLength(1)
    expect(putsFor('port_forwards')[0].body).toEqual({
      portForwards: [{ hostPort: 4000, containerPort: 4100 }],
    })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('keeps mappings and the reported error after a failed update', async () => {
    const { fetchMock, putsFor } = createFetchMock(
      { portForwards: [{ hostPort: 3000 }] },
      {
        onPortForwardsUpdate: (attempt) => (
          attempt === 1 ? failedResponse('port 3100 is already bound') : undefined
        ),
      },
    )
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.host-port').setValue('3100')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.error-state[role="alert"]').text()).toContain('port 3100 is already bound')
    expect(inputValue(wrapper.get('.host-port').element)).toBe('3100')
    expect(putsFor('model_config')).toHaveLength(0)
    expect(putsFor('modal_config')).toHaveLength(0)
    expect(wrapper.get('.apply-button').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeFalsy()

    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')).toHaveLength(2)
    expect(putsFor('port_forwards')[1].body).toEqual({ portForwards: [{ hostPort: 3100 }] })
    expect(putsFor('model_config')).toHaveLength(0)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('stops every editor from being changed while an update is in flight', async () => {
    let releaseUpdate: ((response: StubResponse) => void) | undefined
    const pendingUpdate = new Promise<StubResponse>((resolve) => {
      releaseUpdate = resolve
    })
    const { fetchMock } = createFetchMock(
      { portForwards: [{ hostPort: 3000 }] },
      { onPortForwardsUpdate: () => pendingUpdate },
    )
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.host-port').setValue('3100')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(wrapper.get('.model-editor').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.modal-environment input[type="checkbox"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.modal-environment input[type="number"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.host-port').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.container-port').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.add-port-forward').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.remove-port-forward').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('close')).toBeFalsy()

    releaseUpdate?.(okResponse({ portForwards: [{ hostPort: 3100 }] }))
    await flushPromises()

    expect(wrapper.get('.model-editor').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('.host-port').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('treats a missing mapping list as no mappings', async () => {
    const { fetchMock } = createFetchMock({ portForwards: null })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.find('.port-forwards').exists()).toBe(true)
    expect(wrapper.findAll('.port-forward')).toHaveLength(0)
    expect(wrapper.get('.add-port-forward').attributes('disabled')).toBeUndefined()
  })

  it('discards edited mappings when the dialog is cancelled', async () => {
    const { fetchMock, calls } = createFetchMock({ portForwards: [{ hostPort: 3000 }] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.host-port').setValue('3100')
    await wrapper.findAll('footer button')[0].trigger('click')
    await flushPromises()

    expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(0)
    expect(wrapper.emitted('applied')).toBeFalsy()
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('applies unchanged mappings without contacting the workflow', async () => {
    const { fetchMock, calls } = createFetchMock({
      portForwards: [
        { hostPort: 3000, containerPort: 3000 },
        { hostPort: 5432, containerPort: 6432 },
      ],
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(0)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('treats a blanked sandbox port matching the host port as unchanged', async () => {
    const { fetchMock, calls } = createFetchMock({
      portForwards: [{ hostPort: 3000, containerPort: 3000 }],
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    expect(inputValue(wrapper.get('.container-port').element)).toBe('3000')

    await wrapper.get('.container-port').setValue('')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(0)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('shows the persisted mappings when the dialog is reopened', async () => {
    const { fetchMock, putsFor } = createFetchMock({ portForwards: [{ hostPort: 3000 }] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.host-port').setValue('3100')
    await wrapper.get('.container-port').setValue('8080')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')).toHaveLength(1)
    wrapper.unmount()

    const reopened = mountModal()
    await flushPromises()

    const rows = reopened.findAll('.port-forward')
    expect(rows).toHaveLength(1)
    expect(inputValue(rows[0].get('.host-port').element)).toBe('3100')
    expect(inputValue(rows[0].get('.container-port').element)).toBe('8080')

    await reopened.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')).toHaveLength(1)
    expect(reopened.emitted('close')).toHaveLength(1)
  })

  it('submits the whole sandbox configuration alongside mapping edits', async () => {
    const { fetchMock, putsFor } = createFetchMock({ portForwards: [{ hostPort: 3000 }] })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('.host-port').setValue('3100')
    await wrapper.get('.modal-environment input[type="number"]').setValue('2')
    await wrapper.get('.apply-button').trigger('click')
    await flushPromises()

    expect(putsFor('port_forwards')[0].body).toEqual({ portForwards: [{ hostPort: 3100 }] })
    expect(putsFor('modal_config')[0].body).toEqual({ config: { ...loadedModalConfig, cpu: 2 } })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('hides mapping controls when the workflow cannot report them', async () => {
    const { fetchMock } = createFetchMock({ portForwardsQueryFails: true })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.find('.port-forwards').exists()).toBe(false)
    expect(wrapper.find('.modal-environment').exists()).toBe(true)
  })
})