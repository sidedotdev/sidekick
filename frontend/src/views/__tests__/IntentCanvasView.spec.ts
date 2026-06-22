import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import IntentCanvasView from '../IntentCanvasView.vue'

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'flow-1' } }),
}))

vi.mock('../../lib/store', () => ({
  store: { workspaceId: 'ws-1' },
}))

vi.mock('codemirror', () => {
  class EditorView {
    state = { doc: { length: 0, toString: () => '' } }
    dispatch() {}
    destroy() {}
    static theme() {
      return {}
    }
    static lineWrapping = {}
    static updateListener = { of: () => ({}) }
  }
  return { EditorView, basicSetup: {} }
})

vi.mock('@codemirror/state', () => ({ EditorState: { create: () => ({}) } }))
vi.mock('@codemirror/lang-markdown', () => ({ markdown: () => ({}) }))

const intentBase = '/api/v1/workspaces/ws-1/flows/flow-1/intent'
const flowBase = '/api/v1/workspaces/ws-1/flows/flow-1'

type FetchImpl = (url: string, opts?: RequestInit) => Promise<Response>

const installFetch = (impl: FetchImpl) => {
  const spy = vi.fn(impl as unknown as typeof fetch)
  vi.stubGlobal('fetch', spy)
  return spy
}

const jsonResponse = (body: unknown): Response =>
  ({ ok: true, json: () => Promise.resolve(body), text: () => Promise.resolve('') } as Response)

describe('IntentCanvasView', () => {
  beforeEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('renders the intent filetree and opens the first file', async () => {
    const fetchSpy = installFetch((url) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(
          jsonResponse({
            files: [
              { path: 'intent/overview.md', isDir: false },
              { path: 'intent/specs', isDir: true },
              { path: 'intent/specs/auth.md', isDir: false },
            ],
          })
        )
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      return Promise.resolve(jsonResponse({}))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    const rows = wrapper.findAll('.file-row')
    expect(rows.map((r) => r.text())).toEqual(['overview.md', 'specs', 'auth.md'])

    expect(fetchSpy).toHaveBeenCalledWith(`${intentBase}/file?path=${encodeURIComponent('intent/overview.md')}`)
    expect(wrapper.find('.crumb').text()).toBe('intent/overview.md')
    expect(wrapper.find('.welcome').exists()).toBe(false)
  })

  it('shows a create prompt when no intent files exist and creates one in intent/', async () => {
    const putBodies: string[] = []
    let listCalls = 0
    installFetch((url, opts) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        listCalls += 1
        const files = listCalls === 1 ? [] : [{ path: 'intent/overview.md', isDir: false }]
        return Promise.resolve(jsonResponse({ files }))
      }
      if (u.endsWith('/intent/file') && opts?.method === 'PUT') {
        putBodies.push(String(opts.body))
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md' }))
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '' }))
      }
      return Promise.resolve(jsonResponse({}))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    expect(wrapper.find('.welcome-form').exists()).toBe(true)

    await wrapper.find('.welcome-form .new-file-input').setValue('overview')
    await wrapper.find('.welcome-form').trigger('submit')
    await flushPromises()

    expect(putBodies).toHaveLength(1)
    expect(JSON.parse(putBodies[0])).toEqual({ path: 'intent/overview.md', content: '' })
    expect(wrapper.find('.crumb').text()).toBe('intent/overview.md')
  })

  it('renders ongoing sub-tasks and clarifications from the idd state query', async () => {
    installFetch((url, opts) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(jsonResponse({ files: [{ path: 'intent/overview.md', isDir: false }] }))
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      if (u === `${flowBase}/query` && opts?.method === 'POST') {
        return Promise.resolve(
          jsonResponse({
            result: {
              subtasks: [
                { flowId: 'sub-1', commit: 'abcdef1234567', status: 'in_progress' },
                { flowId: 'sub-2', commit: 'fedcba7654321', status: 'completed' },
              ],
              clarifications: [{ subtaskFlowId: 'sub-1', question: 'Which auth provider?' }],
            },
          })
        )
      }
      return Promise.resolve(jsonResponse({}))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    const rows = wrapper.findAll('.subtask-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].find('.subtask-commit').text()).toBe('abcdef1')
    expect(rows[0].find('.subtask-status').text()).toBe('in_progress')
    expect(rows[1].find('.subtask-status').classes()).toContain('done')

    expect(wrapper.find('.clarify-question').text()).toBe('Which auth provider?')

    expect(wrapper.find('.side-panel').exists()).toBe(false)
    await rows[0].trigger('click')
    const frame = wrapper.find('.side-panel-frame')
    expect(frame.exists()).toBe(true)
    expect(frame.attributes('src')).toBe('/flows/sub-1')

    await wrapper.find('.side-panel-close').trigger('click')
    expect(wrapper.find('.side-panel').exists()).toBe(false)
  })

  it('starts an intent sub-task when the implement button is pressed', async () => {
    const startBodies: string[] = []
    const fetchSpy = installFetch((url, opts) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(jsonResponse({ files: [{ path: 'intent/overview.md', isDir: false }] }))
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      if (u.endsWith('/intent/start_subtask') && opts?.method === 'POST') {
        startBodies.push(String(opts.body))
        return Promise.resolve(jsonResponse({ message: 'Intent sub-task started' }))
      }
      return Promise.resolve(jsonResponse({}))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    await wrapper.find('.implement-btn').trigger('click')
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledWith(`${intentBase}/start_subtask`, expect.objectContaining({ method: 'POST' }))
    expect(startBodies).toHaveLength(1)
    expect(JSON.parse(startBodies[0])).toEqual({ update: false })
  })
})