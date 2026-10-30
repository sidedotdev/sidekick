import { defineComponent } from 'vue'
import { useEventBus } from '@vueuse/core'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { routeLocationKey } from 'vue-router'
import FlowView from '../FlowView.vue'
import type { FlowAction, FlowStatus, Subflow, SubflowTree } from '../../lib/models'
import { SubflowStatus } from '../../lib/models'
import { store } from '../../lib/store'

class MockWebSocket {
  static OPEN = 1
  static instances: MockWebSocket[] = []
  readyState = MockWebSocket.OPEN
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null

  constructor(public url: string) {
    MockWebSocket.instances.push(this)
  }

  send() {}
  close() {}

  receive(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent)
  }
}

const FlowEditorLinksStub = defineComponent({
  name: 'FlowEditorLinks',
  props: {
    flowId: { type: String, required: true },
    worktrees: { type: Array, default: () => [] },
    subtask: Boolean,
    showModelConfiguration: Boolean,
  },
  emits: ['model-configuration'],
  template: `
    <div class="flow-editor-links-stub" :data-subtask="subtask">
      <button
        v-if="showModelConfiguration"
        class="open-model-configuration"
        @click="$emit('model-configuration')"
      >
        Model configuration
      </button>
    </div>
  `,
})

const FlowModelConfigModalStub = defineComponent({
  name: 'FlowModelConfigModal',
  props: {
    workspaceId: { type: String, required: true },
    flowId: { type: String, required: true },
  },
  template: '<div class="flow-model-config-modal-stub"></div>',
})

let flowSequence = 0

const mountFlow = async ({
  status,
  embedded = false,
  mode = 'development',
  actions = [],
  nextCursor = '',
  subflows = [],
  stubSubflows = true,
}: {
  status: FlowStatus
  embedded?: boolean
  mode?: string
  actions?: FlowAction[]
  nextCursor?: string
  subflows?: Subflow[]
  stubSubflows?: boolean
}) => {
  flowSequence++
  const flowId = `target-flow-${flowSequence}`
  const workspaceId = `target-workspace-${flowSequence}`
  vi.stubEnv('MODE', mode)

  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const url = String(input)
    if (url.endsWith(`/flows/${flowId}/actions?limit=50`)) {
      return { ok: true, json: async () => ({ flowActions: actions, nextCursor }) }
    }
    if (url.endsWith(`/flows/${flowId}/subflows?limit=50`)) {
      return { ok: true, json: async () => ({ subflows, nextCursor: '' }) }
    }
    const subflow = subflows.find(item => url.endsWith(`/subflows/${item.id}`))
    if (subflow) {
      return { ok: true, json: async () => ({ subflow }) }
    }
    if (url.endsWith(`/flows/${flowId}`)) {
      return {
        ok: true,
        json: async () => ({
          flow: {
            id: flowId,
            workspaceId,
            type: 'basic_dev',
            parentId: 'task-1',
            status,
            worktrees: [],
          },
        }),
      }
    }
    if (url.endsWith(`/workspaces/${workspaceId}`)) {
      return {
        ok: true,
        json: async () => ({
          workspace: {
            id: workspaceId,
            localRepoDir: '/repo',
          },
        }),
      }
    }
    throw new Error(`Unexpected request: ${url}`)
  }))

  const wrapper = mount(FlowView, {
    props: {
      flowId,
      embedded,
    },
    global: {
      stubs: {
        FlowEditorLinks: FlowEditorLinksStub,
        FlowModelConfigModal: FlowModelConfigModalStub,
        SubflowContainer: stubSubflows,
        IdeSelectorDialog: true,
      },
      provide: {
        [routeLocationKey as symbol]: { params: {}, query: {} },
      },
    },
  })
  await flushPromises()

  return { wrapper, flowId, workspaceId }
}

describe('FlowView model configuration integration', () => {
  beforeEach(() => {
    store.workspaceId = 'route-workspace'
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  it('targets the displayed running flow from the normal development toolbar', async () => {
    const { wrapper, flowId, workspaceId } = await mountFlow({ status: 'in_progress' })
    const links = wrapper.getComponent(FlowEditorLinksStub)

    expect(links.props()).toMatchObject({
      flowId,
      subtask: false,
      showModelConfiguration: true,
    })

    await wrapper.get('.open-model-configuration').trigger('click')

    expect(wrapper.getComponent(FlowModelConfigModalStub).props()).toEqual({
      flowId,
      workspaceId,
    })
    wrapper.unmount()
  })

  it('places the action in the embedded toolbar for a paused flow', async () => {
    const { wrapper, flowId } = await mountFlow({
      status: 'paused',
      embedded: true,
    })
    const links = wrapper.getComponent(FlowEditorLinksStub)

    expect(links.props()).toMatchObject({
      flowId,
      subtask: true,
      showModelConfiguration: true,
    })
    expect(wrapper.get('.flow-editor-links-stub').attributes('data-subtask')).toBe('true')
    wrapper.unmount()
  })

  it.each<FlowStatus>(['completed', 'failed', 'canceled'])(
    'hides the action for terminal %s flows',
    async (status) => {
      const { wrapper } = await mountFlow({ status })
      expect(wrapper.getComponent(FlowEditorLinksStub).props('showModelConfiguration')).toBe(false)
      expect(wrapper.find('.open-model-configuration').exists()).toBe(false)
      wrapper.unmount()
    },
  )

  it('omits the action outside development mode', async () => {
    const { wrapper } = await mountFlow({
      status: 'in_progress',
      mode: 'production',
    })

    expect(wrapper.getComponent(FlowEditorLinksStub).props('showModelConfiguration')).toBe(false)
    expect(wrapper.find('.open-model-configuration').exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('FlowView llm2 streaming events', () => {
  beforeEach(() => {
    store.workspaceId = 'route-workspace'
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  // Action-change handling and subflow tree rebuilds are debounced by 100ms.
  const settle = () => new Promise((resolve) => setTimeout(resolve, 150))

  const socket = (fragment: string) => {
    const found = MockWebSocket.instances.find((ws) => ws.url.includes(fragment))
    if (!found) throw new Error(`No websocket matching ${fragment}`)
    return found
  }

  const action = (id: string, actionStatus: FlowAction['actionStatus'], updated: string): FlowAction => ({
    id,
    flowId: 'flow',
    workspaceId: 'route-workspace',
    created: '2024-01-01T00:00:00Z' as unknown as Date,
    updated: updated as unknown as Date,
    actionType: 'chat_completion',
    actionStatus,
    actionParams: {},
    actionResult: '',
    subflow: 'dev',
    isHumanAction: false,
  })

  const renderedActions = (wrapper: VueWrapper): FlowAction[] => {
    const trees = wrapper.findAllComponents({ name: 'SubflowContainer' }).map((c) => c.props('subflowTree') as SubflowTree)
    return trees.flatMap((tree) => tree.children.filter((child): child is FlowAction => 'actionStatus' in child))
  }

  const findAction = (wrapper: VueWrapper, id: string): FlowAction => {
    const found = renderedActions(wrapper).find((a) => a.id === id)
    if (!found) throw new Error(`Action ${id} not rendered`)
    return found
  }

  it('accumulates blocks per action and preserves them across started updates', async () => {
    const { wrapper } = await mountFlow({ status: 'in_progress' })
    const actions = socket('action_changes_ws')
    const events = socket('/events')

    actions.receive(action('a', 'started', '2024-01-01T00:00:01Z'))
    actions.receive(action('b', 'started', '2024-01-01T00:00:01Z'))
    await settle()

    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'a', type: 'block_started', index: 0, contentBlock: { type: 'text' } })
    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'a', type: 'text_delta', index: 0, delta: 'Hello' })
    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'b', type: 'block_started', index: 0, contentBlock: { type: 'tool_use', toolUse: { id: 'tc', name: 'search', arguments: '' } } })
    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'b', type: 'text_delta', index: 0, delta: '{"query":"te' })
    await settle()

    expect(findAction(wrapper, 'a').streamingData?.blocks).toEqual([{ type: 'text', text: 'Hello' }])
    expect(findAction(wrapper, 'a').streamingData?.toolArguments).toEqual({})
    expect(findAction(wrapper, 'b').streamingData?.blocks).toEqual([
      { type: 'tool_use', toolUse: { id: 'tc', name: 'search', arguments: '{"query":"te' } }
    ])
    expect(findAction(wrapper, 'b').streamingData?.toolArguments).toEqual({ 0: { query: 'te' } })

    // A fresh started update replaces the action object but must keep the streamed state.
    actions.receive(action('a', 'started', '2024-01-01T00:00:02Z'))
    await settle()
    expect(findAction(wrapper, 'a').streamingData?.blocks).toEqual([{ type: 'text', text: 'Hello' }])

    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'a', type: 'text_delta', index: 0, delta: ' world' })
    await settle()
    expect(findAction(wrapper, 'a').streamingData?.blocks).toEqual([{ type: 'text', text: 'Hello world' }])

    wrapper.unmount()
  })

  it('ignores stale completions but stops accumulating once an action really completes', async () => {
    const { wrapper } = await mountFlow({ status: 'in_progress' })
    const actions = socket('action_changes_ws')
    const events = socket('/events')

    actions.receive(action('a', 'started', '2024-01-01T00:00:05Z'))
    await settle()
    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'a', type: 'block_started', index: 0, contentBlock: { type: 'text' } })
    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'a', type: 'text_delta', index: 0, delta: 'Hello' })
    await settle()

    // An out-of-order older completion is dropped and must not tear down the stream.
    actions.receive(action('a', 'complete', '2024-01-01T00:00:01Z'))
    await settle()
    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'a', type: 'text_delta', index: 0, delta: '!' })
    await settle()
    expect(findAction(wrapper, 'a').actionStatus).toBe('started')
    expect(findAction(wrapper, 'a').streamingData?.blocks).toEqual([{ type: 'text', text: 'Hello!' }])

    actions.receive(action('a', 'complete', '2024-01-01T00:00:06Z'))
    await settle()
    events.receive({ eventType: 'llm2_stream_event', flowActionId: 'a', type: 'text_delta', index: 0, delta: ' late' })
    await settle()
    expect(findAction(wrapper, 'a').actionStatus).toBe('complete')
    expect(findAction(wrapper, 'a').streamingData).toBeUndefined()

    wrapper.unmount()
  })
})
describe('FlowView collapse layout', () => {
  beforeEach(() => {
    store.workspaceId = 'route-workspace'
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  // Content-height measurements are retried for up to 250ms to allow rendering to finish.
  const settleLayout = () => new Promise((resolve) => setTimeout(resolve, 300))

  const setLayout = (wrapper: VueWrapper, { contentHeight, containerHeight }: { contentHeight: number, containerHeight: number }) => {
    Object.defineProperty(wrapper.get('.scroll-container').element, 'scrollHeight', { configurable: true, value: contentHeight })
    Object.defineProperty(wrapper.get('.flow-actions-container').element, 'clientHeight', { configurable: true, value: containerHeight })
  }

  it('keeps the scrollable layout when content still overflows after a collapse', async () => {
    const { wrapper } = await mountFlow({ status: 'in_progress' })
    setLayout(wrapper, { contentHeight: 5000, containerHeight: 800 })
    await settleLayout()
    expect(wrapper.get('.flow-actions-container').classes()).not.toContain('short-content')

    useEventBus('flow-view-collapse').emit()
    await settleLayout()

    expect(wrapper.get('.flow-actions-container').classes()).not.toContain('short-content')
    wrapper.unmount()
  })

  it('switches to the short layout when content fits after a collapse', async () => {
    const { wrapper } = await mountFlow({ status: 'in_progress' })
    setLayout(wrapper, { contentHeight: 5000, containerHeight: 800 })
    await settleLayout()

    setLayout(wrapper, { contentHeight: 600, containerHeight: 800 })
    useEventBus('flow-view-collapse').emit()
    await settleLayout()

    expect(wrapper.get('.flow-actions-container').classes()).toContain('short-content')
    wrapper.unmount()
  })
})

describe('FlowView paginated history integration', () => {
  beforeEach(() => {
    store.workspaceId = 'route-workspace'
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  const action = (id: string, created: number): FlowAction => ({
    id, flowId: 'flow', workspaceId: 'route-workspace', subflow: 'Ignored legacy name',
    created: new Date(created), updated: new Date(created),
    actionType: 'tool_call.run_command', actionParams: { command: id },
    actionStatus: 'started', actionResult: '', isHumanAction: false,
  })

  const renderedActions = (wrapper: VueWrapper): FlowAction[] =>
    wrapper.findAllComponents({ name: 'SubflowContainer' })
      .flatMap(component => (component.props('subflowTree') as SubflowTree).children)
      .filter((child): child is FlowAction => 'actionStatus' in child)

  const setHistoryDistance = (wrapper: VueWrapper, distance: number) => {
    const container = wrapper.get('.flow-actions-container').element
    const content = wrapper.get('.scroll-container').element
    Object.defineProperty(container, 'clientHeight', { configurable: true, value: 800 })
    Object.defineProperty(content, 'scrollHeight', { configurable: true, value: 5000 })
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({ top: 100, bottom: 900, height: 800 } as DOMRect)
    vi.spyOn(content, 'getBoundingClientRect').mockReturnValue({ top: 100 - distance } as DOMRect)
  }

  const scrollHistory = async (wrapper: VueWrapper) => {
    await wrapper.get('.flow-actions-container').trigger('scroll')
    await new Promise(resolve => setTimeout(resolve, 50))
    await flushPromises()
  }

  it('loads only the newest page initially, then merges older history without overwriting live updates', async () => {
    const latest = action('latest', 3000)
    const { wrapper, flowId } = await mountFlow({
      status: 'in_progress', actions: [latest], nextCursor: 'older/cursor',
    })
    try {
      expect(renderedActions(wrapper).map(item => item.id)).toEqual(['latest'])
      const historyCalls = vi.mocked(fetch).mock.calls.filter(([url]) => /\/(actions|subflows)\?/.test(String(url)))
      expect(historyCalls).toHaveLength(2)
      expect(historyCalls.every(([url]) => String(url).endsWith('?limit=50'))).toBe(true)

      const socket = MockWebSocket.instances.find(ws => ws.url.includes('action_changes_ws'))!
      socket.receive({ ...latest, updated: new Date(5000), actionStatus: 'complete', actionResult: 'live result' })
      await new Promise(resolve => setTimeout(resolve, 150))
      vi.mocked(fetch).mockResolvedValueOnce({
        ok: true,
        json: async () => ({ flowActions: [latest, action('older', 1000)], nextCursor: '' }),
      } as Response)
      setHistoryDistance(wrapper, 400)
      await scrollHistory(wrapper)

      expect(vi.mocked(fetch).mock.lastCall?.[0]).toBe(
        `/api/v1/workspaces/route-workspace/flows/${flowId}/actions?limit=50&before=older%2Fcursor`,
      )
      expect(renderedActions(wrapper).map(item => item.id)).toEqual(['older', 'latest'])
      expect(renderedActions(wrapper)[1].actionResult).toBe('live result')
      expect(wrapper.text()).not.toContain('Load older history')
    } finally {
      wrapper.unmount()
    }
  })

  it('prefetches within one viewport, deduplicates requests, and stops once the buffer is filled', async () => {
    const { wrapper } = await mountFlow({
      status: 'in_progress', actions: [action('latest', 3000)], nextCursor: 'older',
    })
    try {
      expect(wrapper.text()).not.toContain('Load older history')
      const initialCalls = vi.mocked(fetch).mock.calls.length
      setHistoryDistance(wrapper, 1600)
      await scrollHistory(wrapper)
      expect(vi.mocked(fetch).mock.calls.length).toBe(initialCalls)

      let resolve!: (response: Response) => void
      vi.mocked(fetch).mockImplementationOnce(() => new Promise<Response>(r => { resolve = r }))
      setHistoryDistance(wrapper, 400)
      await scrollHistory(wrapper)
      await scrollHistory(wrapper)
      expect(vi.mocked(fetch).mock.calls.length).toBe(initialCalls + 1)

      setHistoryDistance(wrapper, 2000)
      resolve(Response.json({ flowActions: [action('older', 1000)], nextCursor: 'more' }))
      await flushPromises()
      await scrollHistory(wrapper)
      expect(vi.mocked(fetch).mock.calls.length).toBe(initialCalls + 1)
      expect(renderedActions(wrapper).map(item => item.id)).toEqual(['older', 'latest'])
    } finally {
      wrapper.unmount()
    }
  })

  it('checks the initial rendered history without requiring a scroll event', async () => {
    const { wrapper } = await mountFlow({
      status: 'in_progress', actions: [action('latest', 3000)], nextCursor: 'older',
    })
    try {
      vi.mocked(fetch).mockResolvedValueOnce(Response.json({
        flowActions: [action('older', 1000)], nextCursor: '',
      }))
      setHistoryDistance(wrapper, 400)
      await new Promise(resolve => setTimeout(resolve, 100))
      await flushPromises()
      expect(renderedActions(wrapper).map(item => item.id)).toEqual(['older', 'latest'])
    } finally {
      wrapper.unmount()
    }
  })

  it.each(['switch', 'unmount'])('cancels scheduled loading on %s', async (operation) => {
    const { wrapper } = await mountFlow({
      status: 'in_progress', actions: [action('latest', 3000)], nextCursor: 'older',
    })
    let unmounted = false
    try {
      setHistoryDistance(wrapper, 400)
      const calls = vi.mocked(fetch).mock.calls.length
      await wrapper.get('.flow-actions-container').trigger('scroll')
      if (operation === 'switch') {
        await wrapper.setProps({ flowId: undefined })
      } else {
        wrapper.unmount()
        unmounted = true
      }
      await new Promise(resolve => setTimeout(resolve, 100))
      await flushPromises()
      expect(vi.mocked(fetch).mock.calls.length).toBe(calls)
    } finally {
      if (!unmounted) wrapper.unmount()
    }
  })

  it('fills a short history buffer without requiring another scroll event', async () => {
    const { wrapper } = await mountFlow({
      status: 'in_progress', actions: [action('latest', 3000)], nextCursor: 'older',
    })
    try {
      vi.mocked(fetch)
        .mockResolvedValueOnce(Response.json({ flowActions: [action('middle', 2000)], nextCursor: 'oldest' }))
        .mockResolvedValueOnce(Response.json({ flowActions: [action('oldest', 1000)], nextCursor: '' }))
      setHistoryDistance(wrapper, 100)
      await scrollHistory(wrapper)
      await new Promise(resolve => setTimeout(resolve, 100))
      await flushPromises()
      expect(renderedActions(wrapper).map(item => item.id)).toEqual(['oldest', 'middle', 'latest'])
      const calls = vi.mocked(fetch).mock.calls.length
      await scrollHistory(wrapper)
      expect(vi.mocked(fetch).mock.calls.length).toBe(calls)
    } finally {
      wrapper.unmount()
    }
  })

  it('does not repeatedly retry failed pages and allows an explicit retry', async () => {
    const { wrapper } = await mountFlow({
      status: 'in_progress', actions: [action('latest', 3000)], nextCursor: 'older',
    })
    try {
      vi.mocked(fetch).mockResolvedValueOnce(new Response('', { status: 500 }))
      setHistoryDistance(wrapper, 400)
      await scrollHistory(wrapper)
      expect(wrapper.get('[role="alert"]').text()).toContain('Failed to load actions')
      const calls = vi.mocked(fetch).mock.calls.length
      await scrollHistory(wrapper)
      expect(vi.mocked(fetch).mock.calls.length).toBe(calls)

      vi.mocked(fetch).mockResolvedValueOnce(Response.json({
        flowActions: [action('older', 1000)], nextCursor: '',
      }))
      await wrapper.get('[role="alert"] button').trigger('click')
      await flushPromises()
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
      expect(renderedActions(wrapper).map(item => item.id)).toEqual(['older', 'latest'])
    } finally {
      wrapper.unmount()
    }
  })

  it('preserves action instances and expansion when streamed actions move to a newer subflow', async () => {
    const subflows: Subflow[] = ['first', 'second'].map(id => ({
      id, flowId: 'flow', workspaceId: 'route-workspace', name: id,
      type: 'step.dev', status: SubflowStatus.Started,
    }))
    const actions = [1, 2, 3].map(index => ({
      ...action(`action-${index}`, index * 1000), subflowId: 'first',
    }))
    const { wrapper } = await mountFlow({
      status: 'in_progress', actions, subflows, stubSubflows: false,
    })
    try {
      const findLatest = () => wrapper.findAllComponents({ name: 'FlowActionItem' })
        .find(component => component.props('flowAction').id === 'action-3')!
      const latest = findLatest()
      const instance = latest.vm.$
      const element = latest.element
      const renderer = latest.getComponent({ name: 'RunCommandFlowAction' }).vm.$
      expect(latest.classes()).toContain('expanded-action')

      const socket = MockWebSocket.instances.find(ws => ws.url.includes('action_changes_ws'))!
      socket.receive({ ...actions[2], actionResult: 'streamed result', updated: new Date(4000) })
      await new Promise(resolve => setTimeout(resolve, 150))
      await flushPromises()
      expect(findLatest().vm.$).toBe(instance)
      expect(findLatest().element).toBe(element)
      expect(findLatest().getComponent({ name: 'RunCommandFlowAction' }).vm.$).toBe(renderer)

      socket.receive({ ...action('new-step-action', 5000), subflowId: 'second' })
      await new Promise(resolve => setTimeout(resolve, 150))
      await flushPromises()
      expect(findLatest()).toBeDefined()
      expect(findLatest().vm.$).toBe(instance)
      expect(findLatest().element).toBe(element)
      expect(findLatest().classes()).toContain('expanded-action')
      expect(findLatest().getComponent({ name: 'RunCommandFlowAction' }).vm.$).toBe(renderer)
    } finally {
      wrapper.unmount()
    }
  })

  it('fetches metadata on live status events but hides the subflow until an action arrives', async () => {
    const { wrapper, flowId } = await mountFlow({ status: 'in_progress' })
    try {
      vi.mocked(fetch).mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          subflow: {
            id: 'sf_new', name: 'New step', flowId, workspaceId: 'route-workspace',
            type: 'step.dev', status: 'started',
          },
        }),
      } as Response)
      const events = MockWebSocket.instances.find(ws => ws.url.endsWith('/events'))!
      events.receive({ eventType: 'status_change', parentId: flowId, targetId: 'sf_new', status: 'started' })
      await vi.waitFor(() => {
        expect(fetch).toHaveBeenCalledWith('/api/v1/workspaces/route-workspace/subflows/sf_new')
      })
      await flushPromises()
      const findTree = () => wrapper.findAllComponents({ name: 'SubflowContainer' })
        .find(component => component.props('subflowTree').id === 'sf_new')
      expect(findTree()).toBeUndefined()

      const socket = MockWebSocket.instances.find(ws => ws.url.includes('action_changes_ws'))!
      socket.receive({ ...action('first-step-action', 1000), subflowId: 'sf_new' })
      await vi.waitFor(() => {
        expect(findTree()?.props('subflowTree')).toMatchObject({
          name: 'New step', children: [{ id: 'first-step-action' }],
        })
      })
    } finally {
      wrapper.unmount()
    }
  })

  it('ignores messages from sockets belonging to a previous flow', async () => {
    const { wrapper } = await mountFlow({ status: 'in_progress' })
    try {
      const socket = MockWebSocket.instances.find(ws => ws.url.includes('action_changes_ws'))!
      await wrapper.setProps({ flowId: undefined })
      socket.receive(action('obsolete', 1000))
      await new Promise(resolve => setTimeout(resolve, 150))
      expect(renderedActions(wrapper)).toEqual([])
      expect(wrapper.text()).not.toContain('Load older history')
    } finally {
      wrapper.unmount()
    }
  })
})