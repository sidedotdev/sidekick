import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import KanbanView from '../KanbanView.vue'
import { store } from '../../lib/store'
import { closeTaskChangesStream } from '../../lib/taskChangesStream'

class MockWebSocket {
  static OPEN = 1
  static instances: MockWebSocket[] = []
  readyState = MockWebSocket.OPEN
  closed = false
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null

  constructor(public url: string) {
    MockWebSocket.instances.push(this)
  }

  send() {}
  close() {
    this.closed = true
    this.onclose?.({ reason: '' } as CloseEvent)
  }

  open() {
    this.onopen?.()
  }

  receive(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent)
  }
}

let workspaceSequence = 0

const task = (id: string, title: string) => ({
  id,
  title,
  description: '',
  status: 'to_do',
  agentType: 'llm',
  flowType: 'basic_dev',
  created: '2024-01-01T00:00:00Z',
  updated: '2024-01-01T00:00:00Z',
})

const mountKanban = () =>
  mount(KanbanView, { global: { stubs: { KanbanBoard: true } } })

const boardTaskTitles = (wrapper: ReturnType<typeof mountKanban>) =>
  (wrapper.getComponent({ name: 'KanbanBoard' }).props('tasks') as Array<{ title: string }>).map(t => t.title)

const taskSockets = () => MockWebSocket.instances.filter(ws => ws.url.includes('/task_changes'))

describe('KanbanView task changes stream', () => {
  let fetchedTasks: unknown[]

  beforeEach(() => {
    store.workspaceId = `kanban-ws-${++workspaceSequence}`
    MockWebSocket.instances = []
    fetchedTasks = []
    vi.stubGlobal('WebSocket', MockWebSocket)
    vi.stubGlobal('fetch', vi.fn(async () => ({ json: async () => ({ tasks: fetchedTasks }) })))
  })

  afterEach(() => {
    closeTaskChangesStream()
    vi.unstubAllGlobals()
  })

  it('keeps receiving task changes while unmounted and shows them on return without reconnecting', async () => {
    fetchedTasks = [task('t1', 'First')]
    const first = mountKanban()
    taskSockets()[0].open()
    await flushPromises()
    expect(boardTaskTitles(first)).toEqual(['First'])

    first.unmount()
    const socket = taskSockets()[0]
    expect(socket.closed).toBe(false)

    socket.receive({ tasks: [task('t1', 'First renamed'), task('t2', 'Second')], lastTaskStreamId: '5-0' })

    const second = mountKanban()
    await flushPromises()
    expect(taskSockets()).toHaveLength(1)
    expect(boardTaskTitles(second)).toEqual(['First renamed', 'Second'])
    second.unmount()
  })

  it('replaces the stream when the workspace changes', async () => {
    const wrapper = mountKanban()
    const original = taskSockets()[0]
    original.open()
    await flushPromises()

    store.workspaceId = `kanban-ws-${++workspaceSequence}`
    await flushPromises()

    expect(original.closed).toBe(true)
    expect(taskSockets()).toHaveLength(2)
    expect(taskSockets()[1].url).toContain(`/workspaces/${store.workspaceId}/task_changes`)
    wrapper.unmount()
  })

  it('resumes from the last stream id after an unexpected disconnect', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = mountKanban()
      const socket = taskSockets()[0]
      socket.open()
      socket.receive({ tasks: [], lastTaskStreamId: '7-1' })

      socket.onclose?.({ reason: 'gone' } as CloseEvent)
      await vi.advanceTimersByTimeAsync(1000)

      expect(taskSockets()).toHaveLength(2)
      expect(taskSockets()[1].url).toContain('lastTaskStreamId=7-1')
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })
})
