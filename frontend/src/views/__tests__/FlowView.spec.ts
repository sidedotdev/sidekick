import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FlowView from '../FlowView.vue'
import { store } from '../../lib/store'

class MockWebSocket {
  static OPEN = 1
  readyState = MockWebSocket.OPEN
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null

  constructor(public url: string) {}

  send() {}
  close() {}
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
  status: string
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
      return { ok: true, json: async () => ({ actions: [] }) }
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
      mocks: {
        $route: { params: {} },
        $router: { replace: vi.fn() },
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
    const { wrapper, flowId, workspaceId } = await mountFlow({ status: 'started' })
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

  it.each(['completed', 'failed', 'canceled'])(
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
      status: 'started',
      mode: 'production',
    })

    expect(wrapper.getComponent(FlowEditorLinksStub).props('showModelConfiguration')).toBe(false)
    expect(wrapper.find('.open-model-configuration').exists()).toBe(false)
    wrapper.unmount()
  })
})