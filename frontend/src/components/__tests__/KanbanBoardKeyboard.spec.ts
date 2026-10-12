import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import KanbanBoard from '../KanbanBoard.vue'
import VirtualTaskList from '../VirtualTaskList.vue'
import TaskModal from '../TaskModal.vue'
import { store } from '../../lib/store'
import { viewCache } from '../../lib/viewCache'
import { loadLocalSettings, setKanbanKeyBinding, setKanbanKeyboardControl } from '../../lib/localSettings'
import { boardControlIds as ids } from '../../lib/kanbanNavigation'
import { installPageNavigation, KEYBOARD_CONTROL_CLASS, KEYBOARD_MODALITY_CLASS } from '../../lib/pageNavigation'
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

const withFlow = (tasks: FullTask[], taskId: string): FullTask[] => tasks.map(t => t.id === taskId
  ? { ...t, flowType: 'basic_dev', flows: [{ id: `flow_${taskId}`, type: 'basic_dev' }] } as FullTask
  : t)

const ALPHA_TOGGLE = ids.groupToggle('project_1')
const BETA_TOGGLE = ids.groupToggle('project_2')
const ELSE_TOGGLE = ids.groupToggle('')
const draft = (groupKey: string) => ids.groupAddTask(groupKey, 'human')
const queue = (groupKey: string) => ids.groupAddTask(groupKey, 'llm')

describe('KanbanBoard keyboard control', () => {
  let wrapper: VueWrapper | null = null
  let scrollParent: HTMLElement | null = null
  let chrome: HTMLElement | null = null
  let uninstallPageNavigation: (() => void) | null = null

  const frames = async () => {
    for (let i = 0; i < 4; i++) {
      await new Promise(resolve => requestAnimationFrame(() => resolve(null)))
      await flushPromises()
    }
  }

  // Renders the real virtualized lists (with minimal task cards) inside a
  // scroll container, so revealing and focusing cards can be observed
  const mountBoard = async (
    tasks: FullTask[] = baseTasks(),
    onScrollIntoView: (el: HTMLElement) => void = () => {},
    scrollHeight = 2000,
    projectList: Project[] = projects,
  ) => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600)
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(600)
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(scrollHeight)
    HTMLElement.prototype.scrollIntoView = vi.fn(function (this: HTMLElement) {
      onScrollIntoView(this)
    })
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true, json: () => Promise.resolve({ projects: projectList }) } as Response)))
    scrollParent = document.createElement('div')
    scrollParent.style.overflow = 'auto'
    document.body.appendChild(scrollParent)
    wrapper = mount(KanbanBoard, {
      props: { tasks, showGuidedOverlay: false },
      attachTo: scrollParent,
      global: {
        stubs: {
          TaskModal: true,
          ShortcutHint: true,
          TaskCard: { props: ['task'], template: '<div class="task-card" :data-task-id="task.id"></div>' },
        },
      },
    })
    await frames()
    return wrapper
  }

  // Gives cards a real height, so only those near the scroll position mount
  const mountTallColumn = async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
      { top: 0, left: 0, right: 600, bottom: 130, width: 600, height: 130, x: 0, y: 0, toJSON: () => ({}) },
    )
    const count = 30
    const tasks = Array.from({ length: count }, (_, i) => task({ id: `d${i}`, projectId: 'project_1' }))
    // The virtualizer measures rows by their (mocked 600px) offsetHeight and
    // clamps scrolling to the scroll height
    await mountBoard(tasks, () => {}, count * 700)
    const el = scrollParent!
    el.scrollTo = ((options: ScrollToOptions) => {
      el.scrollTop = options.top ?? 0
      el.dispatchEvent(new Event('scroll'))
    }) as typeof el.scrollTo
  }

  // Stands in for the App sidebar and header
  const mountChrome = () => {
    chrome = document.createElement('div')
    chrome.innerHTML = `
      <div class="sidebar"><nav>
        <a href="#" id="nav-board" class="router-link-active">Board</a>
        <a href="#" id="nav-projects">Projects</a>
      </nav></div>
      <header><div class="workspace-selector">
        <span id="workspace-select" role="combobox" tabindex="0" aria-expanded="false">Space</span>
        <a href="#" id="workspace-settings" data-nav-item>Settings</a>
      </div></header>`
    document.body.prepend(chrome)
    return chrome
  }

  const element = (selector: string) => document.querySelector<HTMLElement>(selector)!
  const boardControl = (controlId: string) =>
    [...document.querySelectorAll<HTMLElement>('[data-board-nav]')].find(el => el.dataset.boardNav === controlId)!

  const focusedTaskId = () => (document.activeElement as HTMLElement | null)?.dataset?.taskId ?? null

  // The focused task, board control, or (chrome) element id
  const focused = () => {
    const active = document.activeElement as HTMLElement | null
    if (!active || active === document.body) return null
    return active.dataset.taskId ?? active.dataset.boardNav ?? (active.id || active.tagName)
  }

  const press = async (key: string, init: KeyboardEventInit = {}, target: EventTarget = document.activeElement ?? document.body) => {
    const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init })
    target.dispatchEvent(event)
    await frames()
    return event
  }

  const pressAll = async (steps: [string, string | null][]) => {
    for (const [key, expected] of steps) {
      const event = await press(key)
      expect(event.defaultPrevented, `${key} prevented`).toBe(true)
      expect(focused(), `after ${key}`).toBe(expected)
    }
  }

  // A mouse click focuses (most) buttons without keyboard modality
  const mouseClick = async (el: HTMLElement) => {
    el.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    el.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    el.focus()
    el.click()
    await frames()
  }

  const focusLink = () => {
    const link = document.createElement('a')
    link.href = '#'
    link.textContent = 'Elsewhere'
    document.body.appendChild(link)
    link.focus()
    return link
  }

  const rootClasses = () => document.documentElement.classList

  beforeEach(() => {
    vi.stubEnv('MODE', 'development')
    localStorage.clear()
    loadLocalSettings()
    setKanbanKeyboardControl(true)
    store.workspaceId = 'test-workspace-id'
    viewCache.setProjects('test-workspace-id', [])
    routerPushMock.mockClear()
    uninstallPageNavigation = installPageNavigation()
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    scrollParent?.remove()
    scrollParent = null
    chrome?.remove()
    chrome = null
    uninstallPageNavigation?.()
    vi.restoreAllMocks()
    delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
    localStorage.clear()
    loadLocalSettings()
  })

  it('focuses the top-left card of the topmost expanded group on load, with keyboard modality', async () => {
    await mountBoard()
    expect(focused()).toBe('a1')
    expect(rootClasses().contains(KEYBOARD_MODALITY_CLASS)).toBe(true)
    expect(rootClasses().contains(KEYBOARD_CONTROL_CLASS)).toBe(true)
  })

  it.each([
    { name: 'project card', selector: '.virtual-task-card-container[data-task-id="a1"]', projectId: 'project_1' },
    { name: 'draft button', selector: `[data-board-nav="${draft('project_2')}"]`, projectId: 'project_2' },
    { name: 'queue button', selector: `[data-board-nav="${queue('project_1')}"]`, projectId: 'project_1' },
    { name: 'project header', selector: `[data-board-nav="${BETA_TOGGLE}"]`, projectId: 'project_2' },
    { name: 'everything else card', selector: '.virtual-task-card-container[data-task-id="e1"]' },
    { name: 'everything else header', selector: `[data-board-nav="${ELSE_TOGGLE}"]` },
    { name: 'column heading', selector: `[data-board-nav="${ids.headingAddTask('human')}"]` },
    { name: 'no focus', selector: null },
    { name: 'no projects', selector: '.virtual-task-card-container[data-task-id="a1"]', noProjects: true },
    { name: 'keyboard control disabled', selector: '.virtual-task-card-container[data-task-id="a1"]', disabled: true },
  ])('opens a task with the appropriate project from $name', async ({ selector, projectId, noProjects, disabled }) => {
    if (disabled) setKanbanKeyboardControl(false)
    const w = await mountBoard(baseTasks(), () => {}, 2000, noProjects ? [] : projects)

    for (const key of ['t', 'T']) {
      if (selector) element(selector).focus()
      else (document.activeElement as HTMLElement)?.blur()

      const event = await press(key, { shiftKey: key === 'T' })
      expect(event.defaultPrevented).toBe(true)
      const modal = w.findComponent(TaskModal)
      expect(modal.exists()).toBe(true)
      expect(modal.props('task').projectId).toBe(key === 't' ? projectId : undefined)
      modal.vm.$emit('close')
      await frames()
    }
  })

  it('tracks keyboard modality and the setting on the root element', async () => {
    await mountBoard()
    document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    expect(rootClasses().contains(KEYBOARD_MODALITY_CLASS)).toBe(false)
    await press('Shift')
    expect(rootClasses().contains(KEYBOARD_MODALITY_CLASS)).toBe(true)
    document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    expect(rootClasses().contains(KEYBOARD_MODALITY_CLASS)).toBe(false)

    setKanbanKeyboardControl(false)
    await flushPromises()
    expect(rootClasses().contains(KEYBOARD_CONTROL_CLASS)).toBe(false)
  })

  it('focuses nothing and ignores keys when disabled', async () => {
    setKanbanKeyboardControl(false)
    await mountBoard()
    expect(focused()).toBeNull()
    const event = await press('j')
    expect(event.defaultPrevented).toBe(false)
    expect(focused()).toBeNull()
  })

  it('focuses nothing outside dev mode even when stored as enabled', async () => {
    vi.stubEnv('MODE', 'production')
    await mountBoard()
    expect(focused()).toBeNull()
  })

  it('re-snaps to the top-left card as tasks stream in before the first move', async () => {
    const tasks = baseTasks()
    const w = await mountBoard(tasks)
    tasks.push(task({ id: 'a0', projectId: 'project_1', updated: '2024-02-01T00:00:00Z' }))
    await w.setProps({ tasks: [...tasks] })
    await frames()
    expect(focused()).toBe('a0')
  })

  it('follows the chosen task after a keyboard move, falling back when it disappears', async () => {
    const tasks = baseTasks()
    const w = await mountBoard(tasks)
    await press('j')
    expect(focused()).toBe('a2')

    const resorted = tasks.map(t => t.id === 'a2' ? { ...t, updated: '2024-03-01T00:00:00Z' } as unknown as FullTask : t)
    await w.setProps({ tasks: resorted })
    await frames()
    expect(focused()).toBe('a2')
    expect(w.findAllComponents(VirtualTaskList)[0].props('tasks')[0].id).toBe('a2')

    await w.setProps({ tasks: resorted.filter(t => t.id !== 'a2') })
    await frames()
    expect(focused()).toBe('a1')
  })

  it('does not take focus from other elements as the remembered card changes', async () => {
    const tasks = baseTasks()
    const w = await mountBoard(tasks)
    await press('j')
    expect(focused()).toBe('a2')

    const link = focusLink()
    try {
      await w.setProps({ tasks: tasks.filter(t => t.id !== 'a2') })
      await frames()
      expect(document.activeElement).toBe(link)
    } finally {
      link.remove()
    }
  })

  it('focuses the remembered card without moving when a key is pressed with nothing focused', async () => {
    await mountBoard()
    await press('j')
    ;(document.activeElement as HTMLElement).blur()
    const event = await press('j')
    expect(event.defaultPrevented).toBe(true)
    expect(focused()).toBe('a2')
  })

  it('traverses cards, group headers and add-task buttons with arrow and secondary keys', async () => {
    await mountBoard()
    await pressAll([
      ['ArrowRight', 'a3'],
      ['l', 'a3'],
      ['j', queue('project_1')],
      ['k', 'a3'],
      ['j', queue('project_1')],
      ['j', BETA_TOGGLE],
      // Header rows span all columns, so moving on keeps the column
      ['j', queue('project_2')],
      ['j', ELSE_TOGGLE],
      ['j', 'e1'],
      ['j', queue('')],
      ['j', queue('')],
      ['h', draft('')],
      ['k', ELSE_TOGGLE],
      ['k', draft('project_2')],
      ['k', 'b1'],
      ['k', BETA_TOGGLE],
      ['k', draft('project_1')],
      ['k', 'a2'],
      ['K', 'a1'],
      ['k', ALPHA_TOGGLE],
      ['k', ids.headingAddTask('human')],
      ['l', ids.headingAddTask('llm')],
      ['ArrowRight', ids.headingAddTask('llm')],
      ['ArrowDown', ALPHA_TOGGLE],
      ['ArrowDown', 'a3'],
    ])
  })

  it('moves from the Draft and Queue buttons up to the card above', async () => {
    await mountBoard()
    boardControl(draft('project_1')).focus()
    await press('ArrowUp')
    expect(focused()).toBe('a2')

    boardControl(queue('project_1')).focus()
    await press('k')
    expect(focused()).toBe('a3')
  })

  it('moves with custom secondary keys while keeping the new-task shortcut', async () => {
    expect(setKanbanKeyBinding('down', 's')).toBeNull()
    expect(setKanbanKeyBinding('right', 'd')).toBeNull()
    expect(setKanbanKeyBinding('up', 'T')).not.toBeNull()
    const w = await mountBoard()

    await press('j')
    expect(focused()).toBe('a1')
    await press('s')
    expect(focused()).toBe('a2')
    await press('d')
    expect(focused()).toBe('a3')
    await press('k')
    expect(focused()).toBe(ALPHA_TOGGLE)

    await press('T')
    expect(w.findComponent(TaskModal).exists()).toBe(true)
  })

  it('reaches collapsed project toggles, which contribute only their header row', async () => {
    const w = await mountBoard()
    await w.findAll('.project-group-toggle')[1].trigger('click')
    boardControl(draft('project_1')).focus()
    await pressAll([
      ['j', BETA_TOGGLE],
      ['j', ELSE_TOGGLE],
      ['j', draft('')],
      ['k', ELSE_TOGGLE],
      ['k', BETA_TOGGLE],
      ['k', draft('project_1')],
    ])
  })

  it('keeps focus on a toggle activated from the keyboard', async () => {
    await mountBoard()
    await pressAll([['k', ALPHA_TOGGLE]])
    const toggle = boardControl(ALPHA_TOGGLE)

    // Enter is left to the button's native activation
    const enter = await press('Enter')
    expect(enter.defaultPrevented).toBe(false)
    toggle.click()
    await frames()
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(toggle)
    expect(rootClasses().contains(KEYBOARD_MODALITY_CLASS)).toBe(true)

    await press('Enter')
    toggle.click()
    await frames()
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(document.activeElement).toBe(toggle)
  })

  it('moves on from a mouse-toggled group header without outlining it', async () => {
    await mountBoard()
    const toggle = boardControl(ALPHA_TOGGLE)
    await mouseClick(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(toggle)
    expect(focusedTaskId()).toBeNull()
    expect(rootClasses().contains(KEYBOARD_MODALITY_CLASS)).toBe(false)

    await press('ArrowDown')
    expect(focused()).toBe(BETA_TOGGLE)
    expect(rootClasses().contains(KEYBOARD_MODALITY_CLASS)).toBe(true)
    await press('j')
    expect(focused()).toBe('b1')
  })

  it('ignores keys with modifiers, from interactive elements, or while a modal is open', async () => {
    const w = await mountBoard(withFlow(baseTasks(), 'a1'))
    for (const init of [{ metaKey: true }, { ctrlKey: true }, { altKey: true }]) {
      expect((await press('j', init)).defaultPrevented).toBe(false)
    }
    expect(focused()).toBe('a1')

    const input = document.createElement('input')
    document.body.appendChild(input)
    await press('j', {}, input)
    await press('Enter', {}, input)
    input.remove()
    expect(focused()).toBe('a1')
    expect(routerPushMock).not.toHaveBeenCalled()

    await press('t')
    expect(w.findComponent(TaskModal).exists()).toBe(true)
    await press('j')
    await press('Enter')
    expect(focused()).toBe('a1')
    expect(routerPushMock).not.toHaveBeenCalled()
  })

  it('opens the focused task flow on Enter', async () => {
    await mountBoard(withFlow(baseTasks(), 'a1'))
    const event = await press('Enter')
    expect(event.defaultPrevented).toBe(true)
    expect(routerPushMock).toHaveBeenCalledWith({ name: 'flow', params: { id: 'flow_a1' } })
  })

  it('opens the edit modal on Enter for a task without flows', async () => {
    const w = await mountBoard()
    await press('Enter')
    expect(routerPushMock).not.toHaveBeenCalled()
    const modal = w.findComponent(TaskModal)
    expect(modal.exists()).toBe(true)
    expect(modal.props('task').id).toBe('a1')
  })

  it('reveals a card when moving into another group', async () => {
    const revealed: (string | null)[] = []
    await mountBoard(baseTasks(), el => {
      revealed.push(el.querySelector('.task-card')?.getAttribute('data-task-id') ?? null)
    })
    expect(revealed).toContain('a1')

    await pressAll([['j', 'a2'], ['j', draft('project_1')], ['j', BETA_TOGGLE], ['j', 'b1']])
    expect(revealed.at(-1)).toBe('b1')
  })

  it('drops a pending card reveal once focus or the pointer moves elsewhere', async () => {
    mountChrome()
    await mountTallColumn()

    // Up from the Draft button targets the column's last, offscreen card
    const revealLastCard = async (interrupt: () => void = () => {}) => {
      scrollParent!.scrollTo({ top: 0 })
      await frames()
      boardControl(draft('project_1')).focus()
      const event = new KeyboardEvent('keydown', { key: 'k', bubbles: true, cancelable: true })
      document.activeElement!.dispatchEvent(event)
      expect(event.defaultPrevented).toBe(true)
      interrupt()
      await frames()
    }

    await revealLastCard()
    const lastCard = focused()
    expect(lastCard).toMatch(/^d\d+$/)
    scrollParent!.scrollTo({ top: 0 })
    await frames()
    expect(document.querySelector(`[data-task-id="${lastCard}"]`), 'offscreen card unmounted').toBeNull()

    await revealLastCard(() => element('#nav-projects').focus())
    expect(focused()).toBe('nav-projects')

    await revealLastCard(() => boardControl(ALPHA_TOGGLE).focus())
    expect(focused()).toBe(ALPHA_TOGGLE)

    await revealLastCard(() => {
      document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
      ;(document.activeElement as HTMLElement).blur()
    })
    expect(focused()).toBeNull()
  })

  it('keeps cards out of the tab order', async () => {
    await mountBoard()
    const cards = [...scrollParent!.querySelectorAll<HTMLElement>('.virtual-task-card-container')]
    expect(cards.length).toBeGreaterThan(1)
    expect(cards.every(card => card.tabIndex < 0)).toBe(true)
  })

  it('remembers a card that receives focus', async () => {
    await mountBoard()
    scrollParent!.querySelector<HTMLElement>('.virtual-task-card-container[data-task-id="a3"]')!.focus()
    boardControl(draft('project_1')).focus()
    ;(document.activeElement as HTMLElement).blur()
    await press('j')
    expect(focused()).toBe('a3')
  })

  it('returns focus to the remembered card after a task modal closes', async () => {
    const w = await mountBoard()
    await press('j')
    await press('Enter')
    const modal = w.findComponent(TaskModal)
    expect(modal.props('task').id).toBe('a2')
    ;(document.activeElement as HTMLElement).blur()

    modal.vm.$emit('close')
    await frames()
    expect(focused()).toBe('a2')
  })

  it('leaves keys to other focused elements', async () => {
    await mountBoard(withFlow(baseTasks(), 'a1'))
    const link = focusLink()
    try {
      const events = [await press('j'), await press('ArrowDown'), await press('Enter')]
      expect(events.map(event => event.defaultPrevented)).toEqual([false, false, false])
      expect(document.activeElement).toBe(link)
      expect(routerPushMock).not.toHaveBeenCalled()
    } finally {
      link.remove()
    }
  })

  it('resets focus to the top-left card once a modal creates, starts, or deletes a task', async () => {
    const w = await mountBoard()
    const settle = async (events: ('created' | 'updated' | 'deleted' | 'close')[]) => {
      const modal = w.findComponent(TaskModal)
      for (const event of events) modal.vm.$emit(event)
      await frames()
    }

    await press('j')
    await press('t')
    await settle(['created', 'close'])
    expect(focused()).toBe('a1')

    await press('j')
    await press('Enter')
    expect(w.findComponent(TaskModal).props('task').id).toBe('a2')
    await settle(['updated', 'close'])
    expect(focused()).toBe('a1')

    await press('j')
    await press('Enter')
    await settle(['deleted', 'close'])
    expect(focused()).toBe('a1')
  })

  it('returns focus to the task whose modal was canceled', async () => {
    const w = await mountBoard()
    const betaList = w.findAllComponents(VirtualTaskList)
      .find(list => (list.props('tasks') as FullTask[]).some(t => t.id === 'b1'))!
    betaList.vm.$emit('edit', (betaList.props('tasks') as FullTask[])[0])
    await flushPromises()

    const modal = w.findComponent(TaskModal)
    expect(modal.props('task').id).toBe('b1')
    modal.vm.$emit('close')
    await frames()
    expect(w.findComponent(TaskModal).exists()).toBe(false)
    expect(focused()).toBe('b1')
  })

  it('moves between the sidebar and the board', async () => {
    mountChrome()
    await mountBoard()
    await press('j')
    expect(focused()).toBe('a2')

    element('#nav-projects').focus()
    await pressAll([
      ['ArrowUp', 'nav-board'],
      ['k', 'nav-board'],
      ['j', 'nav-projects'],
      ['ArrowDown', 'nav-projects'],
      ['h', 'nav-projects'],
      ['ArrowRight', 'a2'],
      ['ArrowLeft', 'nav-board'],
      ['l', 'a2'],
      ['k', 'a1'],
      ['k', ALPHA_TOGGLE],
      ['h', 'nav-board'],
    ])

    boardControl(queue('project_1')).focus()
    await pressAll([['h', draft('project_1')], ['h', 'nav-board']])
  })

  it('moves between the header and the board', async () => {
    mountChrome()
    await mountBoard()
    await pressAll([
      ['k', ALPHA_TOGGLE],
      ['k', ids.headingAddTask('human')],
      ['ArrowUp', 'workspace-select'],
      ['k', 'workspace-select'],
      ['l', 'workspace-settings'],
      ['ArrowRight', 'workspace-settings'],
      ['ArrowDown', 'a1'],
      ['k', ALPHA_TOGGLE],
      ['k', ids.headingAddTask('human')],
      ['k', 'workspace-select'],
      ['h', 'nav-board'],
    ])
  })

  it('keeps navigation keys from the workspace selector unless its overlay is open', async () => {
    mountChrome()
    await mountBoard()
    const select = element('#workspace-select')
    const selectKeys: string[] = []
    select.addEventListener('keydown', event => selectKeys.push(event.key))

    select.focus()
    await press('ArrowRight')
    expect(focused()).toBe('workspace-settings')
    select.focus()
    await press('j')
    expect(focused()).toBe('a1')
    expect(selectKeys).toEqual([])

    select.setAttribute('aria-expanded', 'true')
    select.focus()
    const event = await press('ArrowDown')
    expect(event.defaultPrevented).toBe(false)
    expect(selectKeys).toEqual(['ArrowDown'])
    expect(document.activeElement).toBe(select)
  })
})