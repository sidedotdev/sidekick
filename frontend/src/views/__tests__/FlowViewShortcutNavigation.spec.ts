import { defineComponent, inject } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { routeLocationKey, routerKey } from 'vue-router'
import FlowView from '../FlowView.vue'
import type { FlowAction } from '../../lib/models'
import { store } from '../../lib/store'
import { loadLocalSettings, setKanbanKeyboardControl } from '../../lib/localSettings'
import {
  USER_REQUEST_SHORTCUT_SUBMITTED_KEY,
  type UserRequestShortcutSubmittedHandler,
} from '../../lib/userRequestShortcut'

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

// Stands in for the UserRequest components nested below FlowView
let capturedHandlers: UserRequestShortcutSubmittedHandler[] = []
const ShortcutSubmitterStub = defineComponent({
  name: 'FlowEditorLinks',
  setup() {
    const handler = inject(USER_REQUEST_SHORTCUT_SUBMITTED_KEY, null)
    if (handler) capturedHandlers.push(handler)
    return {}
  },
  template: '<div></div>',
})

// FlowView caches actions per flow id, so each mount uses a fresh flow
let flowSequence = 0
let flowId = ''

const action = (id: string, overrides: Partial<FlowAction> = {}): FlowAction => ({
  id,
  flowId,
  workspaceId: 'ws-1',
  created: new Date(),
  updated: new Date(),
  subflow: '',
  actionType: 'user_request',
  actionStatus: 'pending',
  actionParams: { requestKind: 'free_form' },
  actionResult: '',
  isHumanAction: true,
  ...overrides,
} as FlowAction)

let mounted: VueWrapper[] = []

const mountFlow = async ({ actions, embedded = false }: { actions: FlowAction[], embedded?: boolean }) => {
  capturedHandlers = []
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const url = String(input)
    if (url.endsWith(`/flows/${flowId}/actions?limit=50`)) return { ok: true, json: async () => ({ flowActions: actions, nextCursor: '' }) }
    if (url.endsWith(`/flows/${flowId}/subflows?limit=50`)) return { ok: true, json: async () => ({ subflows: [], nextCursor: '' }) }
    if (url.endsWith(`/flows/${flowId}`)) {
      return { ok: true, json: async () => ({ flow: { id: flowId, workspaceId: 'ws-1', type: 'basic_dev', status: 'running', worktrees: [] } }) }
    }
    if (url.endsWith('/workspaces/ws-1')) return { ok: true, json: async () => ({ workspace: { id: 'ws-1' } }) }
    throw new Error(`Unexpected request: ${url}`)
  }))
  const push = vi.fn()
  const wrapper = mount(FlowView, {
    props: { flowId, embedded },
    global: {
      stubs: {
        FlowEditorLinks: ShortcutSubmitterStub,
        FlowModelConfigModal: true,
        SubflowContainer: true,
        IdeSelectorDialog: true,
      },
      provide: {
        [routeLocationKey as symbol]: { params: {}, query: {} },
        [routerKey as symbol]: { push },
      },
    },
  })
  mounted.push(wrapper)
  await flushPromises()
  expect(capturedHandlers).toHaveLength(1)
  return { wrapper, push, notifyShortcutSubmitted: capturedHandlers[0] }
}

describe('FlowView keyboard shortcut submission navigation', () => {
  beforeEach(() => {
    flowId = `flow-${++flowSequence}`
    store.workspaceId = 'ws-1'
    vi.stubGlobal('WebSocket', MockWebSocket)
    vi.stubEnv('MODE', 'development')
    localStorage.clear()
    loadLocalSettings()
    setKanbanKeyboardControl(true)
  })

  afterEach(() => {
    mounted.forEach(wrapper => wrapper.unmount())
    mounted = []
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
    localStorage.clear()
    loadLocalSettings()
  })

  it('returns to the board when no other user request is pending', async () => {
    const submitted = action('a1')
    const { push, notifyShortcutSubmitted } = await mountFlow({
      actions: [submitted, action('a2', { actionStatus: 'complete' }), action('a3', { actionType: 'tool_call' })],
    })
    notifyShortcutSubmitted(submitted)
    expect(push).toHaveBeenCalledWith({ name: 'kanban' })
  })

  it('stays on the flow while another user request is pending', async () => {
    const submitted = action('a1')
    const { push, notifyShortcutSubmitted } = await mountFlow({
      actions: [submitted, action('a2', { actionType: 'user_request.approve.merge' })],
    })
    notifyShortcutSubmitted(submitted)
    expect(push).not.toHaveBeenCalled()
  })

  it('does nothing when keyboard control is disabled', async () => {
    setKanbanKeyboardControl(false)
    const submitted = action('a1')
    const { push, notifyShortcutSubmitted } = await mountFlow({ actions: [submitted] })
    notifyShortcutSubmitted(submitted)
    expect(push).not.toHaveBeenCalled()
  })

  it('does nothing outside dev mode', async () => {
    vi.stubEnv('MODE', 'production')
    const submitted = action('a1')
    const { push, notifyShortcutSubmitted } = await mountFlow({ actions: [submitted] })
    notifyShortcutSubmitted(submitted)
    expect(push).not.toHaveBeenCalled()
  })

  it('notifies the embedding view instead of navigating when embedded', async () => {
    const submitted = action('a1')
    const { wrapper, push, notifyShortcutSubmitted } = await mountFlow({ actions: [submitted], embedded: true })
    notifyShortcutSubmitted(submitted)
    expect(push).not.toHaveBeenCalled()
    expect(wrapper.emitted('userRequestsCleared')).toHaveLength(1)
  })

  it('does not notify the embedding view while another user request is pending', async () => {
    const submitted = action('a1')
    const { wrapper, notifyShortcutSubmitted } = await mountFlow({ actions: [submitted, action('a2')], embedded: true })
    notifyShortcutSubmitted(submitted)
    expect(wrapper.emitted('userRequestsCleared')).toBeUndefined()
  })
})