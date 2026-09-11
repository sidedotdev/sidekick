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