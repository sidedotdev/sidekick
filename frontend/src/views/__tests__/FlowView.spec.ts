import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { routeLocationKey } from 'vue-router'
import FlowView from '../FlowView.vue'
import type { FlowAction, FlowStatus, SubflowTree } from '../../lib/models'
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
}: {
  status: FlowStatus
  embedded?: boolean
  mode?: string
}) => {
  flowSequence++
  const flowId = `target-flow-${flowSequence}`
  const workspaceId = `target-workspace-${flowSequence}`
  vi.stubEnv('MODE', mode)

  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const url = String(input)
    if (url.endsWith(`/flows/${flowId}/actions`)) {
      return { ok: true, json: async () => ({ flowActions: [] }) }
    }
    if (url.endsWith(`/flows/${flowId}/subflows`)) {
      return { ok: true, json: async () => ({ subflows: [] }) }
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
        SubflowContainer: true,
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