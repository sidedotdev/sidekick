import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import WorkflowResetView from '../WorkflowResetView.vue'
import { store } from '../../lib/store'

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'flow-1' } }),
  useRouter: () => ({ push: vi.fn() }),
}))

describe('WorkflowResetView failure details', () => {
  beforeEach(() => {
    vi.stubEnv('MODE', 'development')
    store.selectWorkspaceId('ws-1')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
  })

  const mountView = async (failure?: { cause: string; message: string; stackTrace: string }) => {
    const event = {
      eventId: 7,
      eventType: 'WorkflowTaskFailed',
      name: '',
      timestamp: 0,
      resetBeforeEventId: null,
      resetAfterEventId: null,
    }
    vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
      const url = String(input)
      if (url.endsWith('/history')) {
        return { ok: true, json: async () => ({ events: [event] }) }
      }
      if (url.endsWith('/history/7')) {
        return { ok: true, json: async () => ({ event: { ...event, input: null, output: null, failure } }) }
      }
      throw new Error(`Unexpected request: ${url}`)
    }))
    const wrapper = mount(WorkflowResetView, {
      global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } },
    })
    await flushPromises()
    return wrapper
  }

  it('shows cause, message and multiline stack trace only when expanded', async () => {
    const message = 'Replay failed: <script>alert("error")</script>'
    const stackTrace = 'workflow.Run()\n\tworkflow.go:42\nworker.Execute()'
    const wrapper = await mountView({ cause: 'NonDeterministicError', message, stackTrace })

    expect(wrapper.text()).not.toContain(message)
    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('NonDeterministicError')
    expect(wrapper.text()).toContain(message)
    expect(wrapper.get('pre').element.textContent).toBe(stackTrace)
    expect(wrapper.find('script').exists()).toBe(false)

    await wrapper.get('button').trigger('click')
    expect(wrapper.text()).not.toContain(message)
  })

  it('shows explicit fallbacks when the failure has no message or stack trace', async () => {
    const wrapper = await mountView({ cause: 'NonDeterministicError', message: '', stackTrace: '' })
    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('NonDeterministicError')
    expect(wrapper.text()).toContain('No error message available.')
    expect(wrapper.text()).toContain('No stack trace available.')
  })

  it('keeps payload details usable when failure information is absent', async () => {
    const wrapper = await mountView()
    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).not.toContain('Stack trace')
    expect(wrapper.text()).toContain('No input payloads.')
    expect(wrapper.text()).toContain('No output payloads.')
  })
})

describe('WorkflowResetView paginated history', () => {
  let intersect: (entries: { isIntersecting: boolean }[]) => void

  const historyEvent = (eventId: number, name = `activity-${eventId}`, extra = {}) => ({
    eventId,
    eventType: 'ActivityTaskScheduled',
    name,
    timestamp: 0,
    resetBeforeEventId: null,
    resetAfterEventId: null,
    ...extra,
  })

  beforeEach(() => {
    vi.stubEnv('MODE', 'development')
    store.selectWorkspaceId('ws-1')
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: typeof intersect) { intersect = callback }
      observe() {}
      disconnect() {}
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
  })

  const mountPages = async (pages: ReturnType<typeof historyEvent>[][]) => {
    let page = 0
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const url = new URL(String(input), 'http://localhost')
      const id = Number(url.pathname.split('/').at(-1))
      if (Number.isFinite(id)) {
        return {
          ok: true,
          json: async () => ({ event: { eventId: id, input: [`input-${id}`], output: [] } }),
        }
      }
      const index = page++
      return {
        ok: true,
        json: async () => ({
          events: pages[index],
          runId: 'run-1',
          nextCursor: index + 1 < pages.length ? `page-${index + 1}` : '',
        }),
      }
    })
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mount(WorkflowResetView, {
      global: { stubs: { RouterLink: true, JsonTree: { props: ['data'], template: '<div>{{ data }}</div>' } } },
    })
    await flushPromises()
    return { wrapper, fetchMock }
  }

  it('loads the next visible page on intersection without hiding existing rows', async () => {
    const first = Array.from({ length: 100 }, (_, i) => historyEvent(200 - i))
    const { wrapper, fetchMock } = await mountPages([first, [historyEvent(99, 'older activity')]])
    expect(fetchMock).toHaveBeenCalledTimes(1)
    intersect([{ isIntersecting: true }])
    await flushPromises()
    expect(wrapper.text()).toContain('older activity')
    expect(wrapper.text()).toContain('activity-200')
    expect(String(fetchMock.mock.calls[1][0])).toContain('cursor=page-1')
    wrapper.unmount()
  })

  it('searches older batches until a visible page is full or history is exhausted', async () => {
    const first = Array.from({ length: 100 }, (_, i) => historyEvent(300 - i))
    const { wrapper, fetchMock } = await mountPages([
      first,
      [historyEvent(199, 'unrelated')],
      [historyEvent(99, 'needle')],
    ])
    await wrapper.get('input').setValue('needle')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(wrapper.text()).toContain('needle')
    expect(wrapper.text()).not.toContain('unrelated')
    wrapper.unmount()
  })

  it('keeps more than one detail expanded and pins detail requests to the history run', async () => {
    const { wrapper, fetchMock } = await mountPages([[historyEvent(9), historyEvent(8)]])
    await wrapper.findAll('button').find(button => button.text() === 'Details')!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Details')!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.details-row')).toHaveLength(2)
    expect(wrapper.text()).toContain('input-9')
    expect(wrapper.text()).toContain('input-8')
    expect(fetchMock.mock.calls.filter(call => String(call[0]).includes('runId=run-1'))).toHaveLength(2)
    wrapper.unmount()
  })

  it('combines a task lifecycle while retaining all event details', async () => {
    const { wrapper } = await mountPages([[
      historyEvent(9, '', { eventType: 'ActivityTaskCompleted', groupId: 5 }),
      historyEvent(7, '', { eventType: 'ActivityTaskStarted', groupId: 5 }),
      historyEvent(5, 'my activity', { groupId: 5 }),
    ]])
    expect(wrapper.findAll('tbody > tr')).toHaveLength(1)
    expect(wrapper.text()).toContain('my activity')
    await wrapper.findAll('button').find(button => button.text() === 'Details')!.trigger('click')
    await flushPromises()
    for (const id of [5, 7, 9]) expect(wrapper.text()).toContain(`input-${id}`)
    wrapper.unmount()
  })

  it('shows a collapsed unresolved nondeterminism error above the history', async () => {
    const { wrapper } = await mountPages([[
      historyEvent(9, '', {
        eventType: 'WorkflowTaskFailed',
        failure: { cause: 'NonDeterministicError', message: 'replay mismatch', stackTrace: 'workflow.go:42' },
      }),
    ]])
    const diagnostic = wrapper.get('.workflow-diagnostic')
    expect(diagnostic.element.hasAttribute('open')).toBe(false)
    expect(diagnostic.text()).toContain('replay mismatch')
    expect(diagnostic.text()).toContain('workflow.go:42')
    wrapper.unmount()
  })

  it('does not present a recovered workflow task failure as a current problem', async () => {
    const { wrapper } = await mountPages([[
      historyEvent(12, '', { eventType: 'WorkflowTaskCompleted' }),
      historyEvent(9, '', {
        eventType: 'WorkflowTaskFailed',
        failure: { cause: 'NonDeterministicError', message: 'old failure', stackTrace: '' },
      }),
    ]])
    expect(wrapper.find('.workflow-diagnostic').exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('WorkflowResetView workspace lifecycle', () => {
  beforeEach(() => {
    vi.stubEnv('MODE', 'development')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
  })

  it('waits for a workspace and discards history from a previous workspace', async () => {
    store.workspaceId = null
    let finishOldRequest!: (response: unknown) => void
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => new Promise(resolve => { finishOldRequest = resolve }))
      .mockResolvedValue({
        ok: true,
        json: async () => ({
          events: [{
            eventId: 2, eventType: 'WorkflowExecutionSignaled', name: 'current workspace',
            timestamp: 0, resetBeforeEventId: null, resetAfterEventId: null,
          }],
          nextCursor: '', runId: 'current-run',
        }),
      })
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mount(WorkflowResetView, { global: { stubs: { RouterLink: true } } })
    await flushPromises()
    expect(fetchMock).not.toHaveBeenCalled()

    store.workspaceId = 'old-workspace'
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    store.workspaceId = 'current-workspace'
    await flushPromises()
    finishOldRequest({
      ok: true,
      json: async () => ({
        events: [{
          eventId: 1, eventType: 'WorkflowExecutionSignaled', name: 'stale workspace',
          timestamp: 0, resetBeforeEventId: null, resetAfterEventId: null,
        }],
        nextCursor: '', runId: 'old-run',
      }),
    })
    await flushPromises()
    expect(wrapper.text()).toContain('current workspace')
    expect(wrapper.text()).not.toContain('stale workspace')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })
})