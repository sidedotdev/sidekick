import { describe, it, expect, beforeEach, vi } from 'vitest'
import { config, mount, flushPromises, RouterLinkStub } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import IntentCanvasView from '../IntentCanvasView.vue'

config.global.plugins.push(PrimeVue)
config.global.stubs.RouterLink = RouterLinkStub

const { routerPush } = vi.hoisted(() => ({ routerPush: vi.fn() }))

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'flow-1' } }),
  useRouter: () => ({ push: routerPush }),
}))

vi.mock('../../lib/store', () => ({
  store: { workspaceId: 'ws-1' },
}))

vi.mock('../FlowView.vue', () => ({
  default: {
    name: 'FlowView',
    props: {
      flowId: { type: String, default: '' },
      embedded: { type: Boolean, default: false },
    },
    template: '<div class="side-panel-flow" :data-flow-id="flowId" :data-embedded="String(embedded)"></div>',
  },
}))

vi.mock('../../lib/intent_diff_editor', () => ({
  uncommittedHighlightExtension: () => [],
  applyUncommittedHighlight: () => {},
}))

vi.mock('../../components/BranchSelector.vue', () => ({
  default: {
    name: 'BranchSelector',
    props: { modelValue: { type: String, default: '' } },
    template: '<div class="branch-selector-stub"></div>',
  },
}))

const intentBase = '/api/v1/workspaces/ws-1/flows/flow-1/intent'
const flowBase = '/api/v1/workspaces/ws-1/flows/flow-1'
const taskFlowsUrl = '/api/v1/workspaces/ws-1/tasks/task-1/flows'

type FetchImpl = (url: string, opts?: RequestInit) => Promise<Response>

const installFetch = (impl: FetchImpl) => {
  const spy = vi.fn(impl as unknown as typeof fetch)
  vi.stubGlobal('fetch', spy)
  return spy
}

const jsonResponse = (body: unknown): Response =>
  ({ ok: true, json: () => Promise.resolve(body), text: () => Promise.resolve('') } as Response)


const canvasFixture = (url: string): Response => {
  if (url === flowBase) return jsonResponse({ flow: { id: 'flow-1', parentId: 'task-1' } })
  if (url === taskFlowsUrl) return jsonResponse({ flows: [] })
  if (url === `${flowBase}/actions`) return jsonResponse({ flowActions: [{
    actionType: 'user_request.approve.merge', actionStatus: 'pending',
    actionParams: { mergeApprovalInfo: { defaultTargetBranch: 'main' } },
  }] })
  return jsonResponse({})
}

describe('IntentCanvasView', () => {
  beforeEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    try {
      window.localStorage.clear()
    } catch {
      // Ignore environments without localStorage.
    }
  })

  it('renders the intent filetree and prompts the user to pick a file without auto-opening one', async () => {
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
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    const rows = wrapper.findAll('.file-row')
    expect(rows.map((r) => r.text())).toEqual(['overview.md', 'specs', 'auth.md'])

    const fileFetches = fetchSpy.mock.calls.filter(([url]) =>
      String(url).includes('/intent/file?path='),
    )
    expect(fileFetches).toHaveLength(0)
    expect(wrapper.find('.crumb').exists()).toBe(false)
    expect(wrapper.find('.welcome').text()).toContain('Pick a file')
  })

  it('orders .generated entries last and still does not auto-open a file', async () => {
    const fetchSpy = installFetch((url) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(
          jsonResponse({
            files: [
              { path: 'intent/.generated', isDir: true },
              { path: 'intent/.generated/inferred.md', isDir: false },
              { path: 'intent/overview.md', isDir: false },
            ],
          })
        )
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    const rows = wrapper.findAll('.file-row')
    expect(rows.map((r) => r.text())).toEqual(['overview.md', '.generated', 'inferred.md'])

    const fileFetches = fetchSpy.mock.calls.filter(([url]) =>
      String(url).includes('/intent/file?path='),
    )
    expect(fileFetches).toHaveLength(0)
    expect(wrapper.find('.crumb').exists()).toBe(false)
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
      return Promise.resolve(canvasFixture(u))
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

  it('renders ongoing sub-tasks and clarifications from persisted flows', async () => {
    installFetch((url) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(jsonResponse({ files: [{ path: 'intent/overview.md', isDir: false }] }))
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      if (u === taskFlowsUrl) {
        return Promise.resolve(
          jsonResponse({
            flows: [
                { id: 'sub-1', title: 'abcdef1234567', status: 'blocked' },
                { id: 'sub-2', title: 'fedcba7654321', status: 'completed' },
              ],
          })
        )
      }
      if (u.endsWith('/sub-1/actions')) return Promise.resolve(jsonResponse({ flowActions: [{
        actionType: 'user_request', actionStatus: 'pending', isHumanAction: true,
        actionParams: { requestContent: 'Which auth provider?' },
      }] }))
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    const rows = wrapper.findAll('.subtask-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].find('.subtask-title').text()).toBe('abcdef1234567')
    expect(rows[0].find('.subtask-status').text()).toBe('blocked')
    expect(rows[1].find('.subtask-status').classes()).toContain('done')

    expect(wrapper.find('.clarify-question').text()).toBe('Which auth provider?')

    expect(wrapper.find('.side-panel').exists()).toBe(false)
    await rows[0].trigger('click')
    const embeddedFlow = wrapper.find('.side-panel-flow')
    expect(embeddedFlow.exists()).toBe(true)
    expect(embeddedFlow.attributes('data-flow-id')).toBe('sub-1')
    expect(embeddedFlow.attributes('data-embedded')).toBe('true')

    await wrapper.find('.side-panel-close').trigger('click')
    expect(wrapper.find('.side-panel').exists()).toBe(false)
  })

  it('groups sub-tasks by status and orders them newest-first within a group', async () => {
    const now = Date.now()
    const iso = (msAgo: number) => new Date(now - msAgo).toISOString()
    installFetch((url) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(jsonResponse({ files: [{ path: 'intent/overview.md', isDir: false }] }))
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      if (u === taskFlowsUrl) {
        return Promise.resolve(
          jsonResponse({
            flows: [
                { id: 'done-new', title: 'aaa0000', status: 'completed', updated: iso(1000) },
                { id: 'active-old', title: 'bbb0000', status: 'in_progress', updated: iso(5000) },
                { id: 'active-new', title: 'ccc0000', status: 'in_progress', updated: iso(1000) },
                { id: 'blocked-1', title: 'ddd0000', status: 'blocked', updated: iso(9000) },
              ],
          })
        )
      }
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    const statuses = wrapper.findAll('.subtask-row .subtask-status').map((s) => s.text())
    expect(statuses).toEqual(['blocked', 'in_progress', 'in_progress', 'completed'])

    const titles = wrapper.findAll('.subtask-row .subtask-title').map((c) => c.text())
    expect(titles).toEqual(['ddd0000', 'ccc0000', 'bbb0000', 'aaa0000'])
  })

  it('collapses stale completed sub-tasks only once the list is long enough to scroll', async () => {
    const now = Date.now()
    const twoHoursAgo = new Date(now - 2 * 60 * 60 * 1000).toISOString()
    const recent = new Date(now - 1000).toISOString()
    const makeSubtasks = (count: number) =>
      Array.from({ length: count }, (_, i) => ({
        id: `done-${i}`,
        title: `c${String(i).padStart(6, '0')}`,
        status: 'completed',
        updated: twoHoursAgo,
      }))

    let subtasks = [
      { id: 'active', title: 'active0', status: 'in_progress', updated: recent },
      ...makeSubtasks(2),
    ]
    installFetch((url) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(jsonResponse({ files: [{ path: 'intent/overview.md', isDir: false }] }))
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      if (u === taskFlowsUrl) {
        return Promise.resolve(jsonResponse({ flows: subtasks }))
      }
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    // Short list: stale completed sub-tasks stay visible, no collapse toggle.
    expect(wrapper.find('.subtask-collapse-toggle').exists()).toBe(false)
    expect(wrapper.findAll('.subtask-row')).toHaveLength(3)

    // Long list: stale completed sub-tasks fold behind a collapse toggle.
    subtasks = [
      { id: 'active', title: 'active0', status: 'in_progress', updated: recent },
      ...makeSubtasks(10),
    ]
    await (wrapper.vm as unknown as { fetchCanvasState: () => Promise<void> }).fetchCanvasState()
    await flushPromises()

    const toggle = wrapper.find('.subtask-collapse-toggle')
    expect(toggle.exists()).toBe(true)
    expect(toggle.text()).toContain('10 Completed')
    expect(wrapper.findAll('.subtask-row')).toHaveLength(1)

    await toggle.trigger('click')
    expect(wrapper.findAll('.subtask-row')).toHaveLength(11)
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
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    await wrapper.find('.implement-btn').trigger('click')
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledWith(`${intentBase}/start_subtask`, expect.objectContaining({ method: 'POST' }))
    expect(startBodies).toHaveLength(1)
    expect(JSON.parse(startBodies[0])).toEqual({ update: false })
  })

  it('starts an intent sub-task when Cmd/Ctrl+I is pressed', async () => {
    const startBodies: string[] = []
    const fetchSpy = installFetch((url, opts) => {
      const u = url.toString()
      if (u.includes('/intent/files')) {
        return Promise.resolve(jsonResponse({ files: [{ path: 'intent/overview.md', isDir: false }] }))
      }
      if (u.includes('/intent/file?path=')) {
        return Promise.resolve(jsonResponse({ path: 'intent/overview.md', content: '# Overview' }))
      }
      if (u.endsWith('/intent/start_subtask') && opts?.method === 'POST') {
        startBodies.push(String(opts.body))
        return Promise.resolve(jsonResponse({ message: 'Intent sub-task started' }))
      }
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'i', ctrlKey: true }))
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledWith(`${intentBase}/start_subtask`, expect.objectContaining({ method: 'POST' }))
    expect(startBodies).toHaveLength(1)

    wrapper.unmount()
  })

  it('reopens the last viewed file on mount when one is remembered', async () => {
    const fileReads: string[] = []
    installFetch((url) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(
          jsonResponse({
            files: [
              { path: 'intent/overview.md', isDir: false },
              { path: 'intent/specs/auth.md', isDir: false },
            ],
          })
        )
      }
      const fileMatch = u.match(/\/intent\/file\?path=(.+)$/)
      if (fileMatch) {
        const path = decodeURIComponent(fileMatch[1])
        fileReads.push(path)
        return Promise.resolve(jsonResponse({ path, content: `# ${path}` }))
      }
      return Promise.resolve(canvasFixture(u))
    })

    window.localStorage.setItem('intent-canvas:last-file:flow-1', 'intent/specs/auth.md')

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    expect(fileReads[0]).toBe('intent/specs/auth.md')
    expect(wrapper.find('.crumb').text()).toBe('intent/specs/auth.md')
  })

  it('remembers the most recently opened file in localStorage', async () => {
    installFetch((url) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) {
        return Promise.resolve(
          jsonResponse({
            files: [
              { path: 'intent/overview.md', isDir: false },
              { path: 'intent/specs/auth.md', isDir: false },
            ],
          })
        )
      }
      const fileMatch = u.match(/\/intent\/file\?path=(.+)$/)
      if (fileMatch) {
        const path = decodeURIComponent(fileMatch[1])
        return Promise.resolve(jsonResponse({ path, content: `# ${path}` }))
      }
      return Promise.resolve(canvasFixture(u))
    })

    const wrapper = mount(IntentCanvasView)
    await flushPromises()

    const rows = wrapper.findAll('.file-row')
    const authRow = rows.find((r) => r.text() === 'auth.md')!
    await authRow.trigger('click')
    await flushPromises()

    expect(window.localStorage.getItem('intent-canvas:last-file:flow-1')).toBe('intent/specs/auth.md')
  })

  it.each([false, true])('submits merge approval through UserRequest (merge fails: %s)', async (fails) => {
    vi.useFakeTimers()
    routerPush.mockClear()
    let submitted = false
    let finished = false
    const completeUrl = '/api/v1/workspaces/ws-1/flow_actions/approval-1/complete'
    const fetchSpy = installFetch(async (url, opts) => {
      const u = url.toString()
      if (u.endsWith('/intent/files')) return jsonResponse({ files: [] })
      if (u === completeUrl && opts?.method === 'POST') {
        submitted = true
        return jsonResponse({})
      }
      if (u === flowBase) {
        return jsonResponse({
          flow: { id: 'flow-1', parentId: 'task-1', status: finished && !fails ? 'completed' : 'in_progress' },
        })
      }
      if (u === `${flowBase}/actions`) return jsonResponse({ flowActions: [{
        id: 'approval-1', workspaceId: 'ws-1', flowId: 'flow-1',
        created: new Date().toISOString(), actionResult: '',
        actionType: 'user_request.approve.merge', actionStatus: 'pending', isHumanAction: true,
        actionParams: {
          requestKind: 'merge_approval',
          mergeApprovalInfo: { defaultTargetBranch: 'main', diff: '' },
          mergeError: finished && fails ? 'Merge conflict on main' : '',
        },
      }] })
      return canvasFixture(u)
    })

    const wrapper = mount(IntentCanvasView)
    try {
      await flushPromises()
      await wrapper.get('.finish-btn').trigger('click')
      await flushPromises()
      await wrapper.get('.finish-body form').trigger('keydown', { key: 'Enter', ctrlKey: true })
      await flushPromises()

      expect(submitted).toBe(true)
      const submission = fetchSpy.mock.calls.find(([url]) => String(url) === completeUrl)
      expect(JSON.parse(String(submission?.[1]?.body))).toMatchObject({
        userResponse: { approved: true, params: { targetBranch: 'main', ignoreWhitespace: false } },
      })
      expect(routerPush).not.toHaveBeenCalled()

      if (!fails) {
        await wrapper.get('[aria-label="Cancel finish"]').trigger('click')
        expect(wrapper.find('.finish-panel').exists()).toBe(false)
      }

      finished = true
      await vi.advanceTimersByTimeAsync(5000)
      await flushPromises()
      if (fails) {
        expect(wrapper.get('[role="alert"]').text()).toBe('Merge conflict on main')
        expect(routerPush).not.toHaveBeenCalled()
        expect(wrapper.find('.finish-body form').exists()).toBe(true)
      } else {
        expect(routerPush).toHaveBeenCalledWith({ name: 'kanban' })
      }
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

})

it('loads persisted canvas state and refreshes pending questions without querying workflow state', async () => {
  let answered = false
  const request = (question: string, overrides = {}) => ({
    actionType: 'user_request',
    actionStatus: 'pending',
    isHumanAction: true,
    actionParams: { requestContent: question },
    ...overrides,
  })
  const fetchSpy = installFetch(async (url) => {
    if (url === flowBase) return jsonResponse({ flow: { id: 'flow-1', parentId: 'task-1' } })
    if (url.endsWith('/tasks/task-1/flows')) return jsonResponse({
      flows: [
        { id: 'flow-1', metadata: { autoMode: !answered, nudges: [{ text: 'Consider retries' }] } },
        { id: 'sub-1', title: 'Authentication', status: 'blocked' },
        { id: 'sub-2', title: 'Storage', status: 'completed' },
      ],
    })
    if (url === `${flowBase}/actions`) return jsonResponse({
      flowActions: [
        request('Which region?'),
        request('Review changes', { actionType: 'user_request.approve.merge' }),
        request('Old question', { actionStatus: 'complete' }),
        request('Not human', { isHumanAction: false }),
      ],
    })
    if (url.endsWith('/sub-1/actions')) return jsonResponse({
      flowActions: answered ? [] : [
        request('Which provider?', { isHumanAction: false, isCallbackAction: true }),
      ],
    })
    return jsonResponse({})
  })
  const wrapper = mount(IntentCanvasView)
  try {
    await flushPromises()
    expect(wrapper.findAll('.subtask-title').map((row) => row.text())).toEqual(['Authentication', 'Storage'])
    expect(wrapper.text()).toContain('Consider retries')
    expect(wrapper.get<HTMLInputElement>('.auto-mode-toggle input').element.checked).toBe(true)
    expect(wrapper.findAll('.clarify-question').map((row) => row.text())).toEqual(['Which region?', 'Which provider?', 'Consider retries'])
    expect(fetchSpy.mock.calls.some(([url]) => String(url).endsWith('/sub-2/actions'))).toBe(false)
    expect(fetchSpy.mock.calls.some(([, opts]) => String(opts?.body).includes('idd_state'))).toBe(false)

    answered = true
    await (wrapper.vm as unknown as { fetchCanvasState: () => Promise<void> }).fetchCanvasState()
    await flushPromises()
    expect(wrapper.findAll('.clarify-question').map((row) => row.text())).toEqual(['Which region?', 'Consider retries'])
    expect(wrapper.get<HTMLInputElement>('.auto-mode-toggle input').element.checked).toBe(false)
  } finally {
    wrapper.unmount()
  }
})

describe('IDD merge approval panel', () => {
  it('renders the persisted approval, refreshes errors, and waits for flow completion before redirecting', async () => {
    vi.useFakeTimers()
    routerPush.mockClear()
    let status = 'in_progress'
    let mergeError = ''
    const approval = () => ({
      id: 'approval-1',
      workspaceId: 'ws-1',
      flowId: 'flow-1',
      actionType: 'user_request.approve.merge',
      actionStatus: 'pending',
      isHumanAction: true,
      actionParams: {
        requestKind: 'merge_approval',
        mergeApprovalInfo: { defaultTargetBranch: 'main', diff: 'persisted diff' },
        mergeError,
      },
    })
    const fetchSpy = installFetch(async (url) => {
      const u = url.toString()
      if (u === flowBase) {
        return jsonResponse({ flow: { id: 'flow-1', parentId: 'task-1', status } })
      }
      if (u === `${flowBase}/actions`) return jsonResponse({ flowActions: [approval()] })
      if (u.endsWith('/intent/files')) return jsonResponse({ files: [] })
      return canvasFixture(u)
    })
    const wrapper = mount(IntentCanvasView, {
      global: {
        stubs: {
          UserRequest: {
            props: ['flowAction', 'expand'],
            template: '<div class="approval-request">{{ flowAction.actionParams.mergeApprovalInfo.diff }}</div>',
          },
          DevRunControls: true,
          IntentMarkdownEditor: true,
          FlowView: true,
        },
      },
    })
    try {
      await flushPromises()
      await wrapper.get('.finish-btn').trigger('click')
      await flushPromises()
      expect(wrapper.get('.approval-request').text()).toBe('persisted diff')
      expect(routerPush).not.toHaveBeenCalled()

      mergeError = 'Merge conflict on main'
      await vi.advanceTimersByTimeAsync(5000)
      await flushPromises()
      expect(wrapper.get('[role="alert"]').text()).toBe(mergeError)
      expect(routerPush).not.toHaveBeenCalled()

      mergeError = ''
      await vi.advanceTimersByTimeAsync(5000)
      await flushPromises()
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)

      status = 'completed'
      await vi.advanceTimersByTimeAsync(5000)
      await flushPromises()
      expect(routerPush).toHaveBeenCalledWith({ name: 'kanban' })
      expect(fetchSpy.mock.calls.some(([url]) => /\/intent\/finish/.test(String(url)))).toBe(false)
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })
})