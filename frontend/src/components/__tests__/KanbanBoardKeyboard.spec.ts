import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import KanbanBoard from '../KanbanBoard.vue'
import VirtualTaskList from '../VirtualTaskList.vue'
import TaskModal from '../TaskModal.vue'
import { store } from '../../lib/store'
import { viewCache } from '../../lib/viewCache'
import { loadLocalSettings, setKanbanKeyBinding, setKanbanKeyboardControl } from '../../lib/localSettings'
import type { FullTask, Project } from '../../lib/models'

const { routerPushMock } = vi.hoisted(() => ({ routerPushMock: vi.fn() }))
vi.mock('@/router', () => ({ default: { push: routerPushMock } }))

const projects = [
  { id: 'project_1', workspaceId: 'test-workspace-id', title: 'Alpha', priority: 'high' },
  { id: 'project_2', workspaceId: 'test-workspace-id', title: 'Beta', priority: 'none' },
] as Project[]

// Timestamps arrive from the API as ISO strings
const task = (overrides: Omit<Partial<FullTask>, 'updated'> & { id: string, updated?: string }): FullTask => ({
  agentType: 'human',
  status: 'drafting',
  updated: '2024-01-01T00:00:00Z',
  workspaceId: 'test-workspace-id',
  ...overrides,
} as unknown as FullTask)

// Alpha: human [a1 (newer), a2], llm [a3]; Beta: human [b1]; Everything else: llm [e1]
const baseTasks = (): FullTask[] => [
  task({ id: 'a2', projectId: 'project_1' }),
  task({ id: 'a1', projectId: 'project_1', updated: '2024-01-02T00:00:00Z' }),
  task({ id: 'a3', projectId: 'project_1', agentType: 'llm', status: 'in_progress' }),
  task({ id: 'b1', projectId: 'project_2' }),
  task({ id: 'e1', agentType: 'llm', status: 'blocked' }),
]

describe('KanbanBoard keyboard control', () => {
  let wrapper: VueWrapper | null = null

  const mountBoard = async (tasks: FullTask[] = baseTasks()) => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ projects }),
    } as Response)))
    wrapper = mount(KanbanBoard, {
      props: { tasks, showGuidedOverlay: false },
      attachTo: document.body,
      global: { stubs: { VirtualTaskList: true, TaskModal: true, ShortcutHint: true } },
    })
    await flushPromises()
    return wrapper
  }

  const highlightedIds = (w: VueWrapper) =>
    w.findAllComponents(VirtualTaskList)
      .map(list => list.props('highlightedTaskId') as string | null)
      .filter((id): id is string => id !== null)

  const highlightedId = (w: VueWrapper) => {
    const ids = new Set(highlightedIds(w))
    expect(ids.size).toBeLessThanOrEqual(1)
    return [...ids][0] ?? null
  }

  const press = async (key: string, init: KeyboardEventInit = {}, target: EventTarget = document.body) => {
    const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init })
    target.dispatchEvent(event)
    await flushPromises()
    return event
  }

  beforeEach(() => {
    vi.stubEnv('MODE', 'development')
    localStorage.clear()
    loadLocalSettings()
    setKanbanKeyboardControl(true)
    store.workspaceId = 'test-workspace-id'
    viewCache.setProjects('test-workspace-id', [])
    routerPushMock.mockClear()
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
    localStorage.clear()
    loadLocalSettings()
  })

  it('highlights the top-left card of the topmost expanded group by default', async () => {
    const w = await mountBoard()
    expect(highlightedId(w)).toBe('a1')
  })

  it('renders no highlight when disabled', async () => {
    setKanbanKeyboardControl(false)
    const w = await mountBoard()
    expect(highlightedIds(w)).toEqual([])
    const event = await press('j')
    expect(event.defaultPrevented).toBe(false)
    expect(highlightedIds(w)).toEqual([])
  })

  it('renders no highlight outside dev mode even when stored as enabled', async () => {
    vi.stubEnv('MODE', 'production')
    const w = await mountBoard()
    expect(highlightedIds(w)).toEqual([])
  })

  it('re-snaps to the top-left card as tasks stream in before the first move', async () => {
    const tasks = baseTasks()
    const w = await mountBoard(tasks)
    tasks.push(task({ id: 'a0', projectId: 'project_1', updated: '2024-02-01T00:00:00Z' }))
    await w.setProps({ tasks: [...tasks] })
    expect(highlightedId(w)).toBe('a0')
  })

  it('follows the chosen task after a keyboard move, falling back when it disappears', async () => {
    const tasks = baseTasks()
    const w = await mountBoard(tasks)
    await press('j')
    expect(highlightedId(w)).toBe('a2')

    const resorted = tasks.map(t => t.id === 'a2' ? { ...t, updated: '2024-03-01T00:00:00Z' } as unknown as FullTask : t)
    await w.setProps({ tasks: resorted })
    expect(highlightedId(w)).toBe('a2')
    expect(w.findAllComponents(VirtualTaskList)[0].props('tasks')[0].id).toBe('a2')

    await w.setProps({ tasks: resorted.filter(t => t.id !== 'a2') })
    expect(highlightedId(w)).toBe('a1')
  })

  it('traverses columns and groups with arrow and secondary keys', async () => {
    const w = await mountBoard()
    const steps: [string, string][] = [
      ['ArrowRight', 'a3'],
      ['l', 'a3'],
      ['j', 'e1'],
      ['j', 'e1'],
      ['h', 'e1'],
      ['k', 'a3'],
      ['ArrowLeft', 'a1'],
      ['ArrowDown', 'a2'],
      ['ArrowDown', 'b1'],
      ['K', 'a2'],
    ]
    for (const [key, expected] of steps) {
      const event = await press(key)
      expect(event.defaultPrevented).toBe(true)
      expect(highlightedId(w), `after ${key}`).toBe(expected)
    }
  })

  it('moves with custom secondary keys while keeping the new-task shortcut', async () => {
    expect(setKanbanKeyBinding('down', 's')).toBeNull()
    expect(setKanbanKeyBinding('right', 'd')).toBeNull()
    expect(setKanbanKeyBinding('up', 'T')).not.toBeNull()
    const w = await mountBoard()

    await press('j')
    expect(highlightedId(w)).toBe('a1')
    await press('s')
    expect(highlightedId(w)).toBe('a2')
    await press('d')
    expect(highlightedId(w)).toBe('a3')
    await press('k')
    expect(highlightedId(w)).toBe('a3')

    await press('T')
    expect(w.findComponent(TaskModal).exists()).toBe(true)
    expect(highlightedId(w)).toBe('a3')
  })

  it('skips collapsed groups when moving vertically', async () => {
    const w = await mountBoard()
    const betaToggle = w.findAll('.project-group-toggle')[1]
    await betaToggle.trigger('click')
    await press('j')
    await press('j')
    expect(highlightedId(w)).toBe('a2')
    await press('l')
    await press('j')
    expect(highlightedId(w)).toBe('e1')
  })

  it('ignores keys with modifiers, from interactive elements, or while a modal is open', async () => {
    const w = await mountBoard()
    await press('j', { metaKey: true })
    await press('j', { ctrlKey: true })
    await press('j', { altKey: true })
    expect(highlightedId(w)).toBe('a1')

    const input = document.createElement('input')
    document.body.appendChild(input)
    await press('j', {}, input)
    await press('Enter', {}, input)
    input.remove()
    expect(highlightedId(w)).toBe('a1')
    expect(routerPushMock).not.toHaveBeenCalled()

    await press('t')
    expect(w.findComponent(TaskModal).exists()).toBe(true)
    await press('j')
    await press('Enter')
    expect(highlightedId(w)).toBe('a1')
    expect(routerPushMock).not.toHaveBeenCalled()
  })

  it('opens the highlighted task flow on Enter', async () => {
    const tasks = baseTasks().map(t => t.id === 'a1'
      ? { ...t, flowType: 'basic_dev', flows: [{ id: 'flow_a1', type: 'basic_dev' }] } as FullTask
      : t)
    await mountBoard(tasks)
    const event = await press('Enter')
    expect(event.defaultPrevented).toBe(true)
    expect(routerPushMock).toHaveBeenCalledWith({ name: 'flow', params: { id: 'flow_a1' } })
  })

  it('reveals the highlighted card when moving into another group', async () => {
    const revealed: (string | null)[] = []
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600)
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(600)
    HTMLElement.prototype.scrollIntoView = vi.fn(function (this: HTMLElement) {
      revealed.push(this.querySelector('.task-card')?.getAttribute('data-task-id') ?? null)
    })
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(2000)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true, json: () => Promise.resolve({ projects }) } as Response)))
    const scrollParent = document.createElement('div')
    scrollParent.style.overflow = 'auto'
    document.body.appendChild(scrollParent)
    try {
      wrapper = mount(KanbanBoard, {
        props: { tasks: baseTasks(), showGuidedOverlay: false },
        attachTo: scrollParent,
        global: {
          stubs: {
            TaskModal: true,
            ShortcutHint: true,
            TaskCard: { props: ['task', 'highlighted'], template: '<div class="task-card" :data-task-id="task.id"></div>' },
          },
        },
      })
      const frames = async () => {
        for (let i = 0; i < 4; i++) {
          await new Promise(resolve => requestAnimationFrame(() => resolve(null)))
          await flushPromises()
        }
      }
      await frames()
      expect(revealed).toContain('a1')

      await press('j')
      await press('j')
      await frames()
      expect(revealed.at(-1)).toBe('b1')
    } finally {
      wrapper?.unmount()
      wrapper = null
      scrollParent.remove()
      vi.restoreAllMocks()
      delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView
    }
  })

  it('opens the edit modal on Enter for a task without flows', async () => {
    const w = await mountBoard()
    await press('Enter')
    expect(routerPushMock).not.toHaveBeenCalled()
    const modal = w.findComponent(TaskModal)
    expect(modal.exists()).toBe(true)
    expect(modal.props('task').id).toBe('a1')
  })
})