import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import VirtualTaskList from '../VirtualTaskList.vue'
import VirtualTaskGrid from '../VirtualTaskGrid.vue'
import type { FullTask } from '../../lib/models'

const ROW_HEIGHT = 130
const TASK_COUNT = 100
const tasks = Array.from({ length: TASK_COUNT }, (_, index) => ({
  id: `t${index}`,
  agentType: 'human',
  status: 'drafting',
  workspaceId: 'w',
  flows: [],
})) as unknown as FullTask[]

const TaskCardStub = defineComponent({
  props: ['task'],
  template: '<div class="task-card" :data-task-id="task.id"></div>',
})

const frames = async () => {
  for (let i = 0; i < 3; i++) {
    await new Promise(resolve => requestAnimationFrame(resolve))
    await flushPromises()
  }
}

describe.each([
  ['list', VirtualTaskList],
  ['grid', VirtualTaskGrid],
] as const)('virtual task %s layout', (_name, component) => {
  let wrapper: VueWrapper | undefined
  let precedingHeight: number
  let viewportHeight: number
  let observers: Set<{ targets: Set<Element>, callback: ResizeObserverCallback }>

  const rect = (top: number, height: number): DOMRect => ({
    top, bottom: top + height, left: 0, right: 300,
    width: 300, height, x: 0, y: top, toJSON: () => ({}),
  })

  const notifyResize = (element: Element) => {
    for (const observer of [...observers]) {
      if (!observer.targets.has(element)) continue
      const height = element.getBoundingClientRect().height
      observer.callback([{
        target: element,
        contentRect: rect(0, height),
        borderBoxSize: [{ inlineSize: 300, blockSize: height }],
      } as unknown as ResizeObserverEntry], {} as ResizeObserver)
    }
  }

  beforeEach(() => {
    precedingHeight = ROW_HEIGHT * 40
    viewportHeight = 600
    observers = new Set()
    vi.stubGlobal('ResizeObserver', class {
      targets = new Set<Element>()
      constructor(public callback: ResizeObserverCallback) { observers.add(this) }
      observe(element: Element) { this.targets.add(element) }
      unobserve(element: Element) { this.targets.delete(element) }
      disconnect() { this.targets.clear(); observers.delete(this) }
    })
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('scroll-parent') ? viewportHeight : ROW_HEIGHT
    })
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(300)
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(300)
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() =>
      precedingHeight + TASK_COUNT * ROW_HEIGHT,
    )
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const scrollTop = this.closest('.scroll-parent')?.scrollTop ?? 0
      if (this.classList.contains('scroll-parent')) return rect(0, viewportHeight)
      if (this.classList.contains('board')) {
        return rect(-scrollTop, precedingHeight + TASK_COUNT * ROW_HEIGHT)
      }
      if (this.classList.contains('preceding-project')) return rect(-scrollTop, precedingHeight)
      if (this.classList.contains('virtual-task-list') || this.classList.contains('virtual-task-grid')) {
        return rect(precedingHeight - scrollTop, TASK_COUNT * ROW_HEIGHT)
      }
      return rect(0, ROW_HEIGHT)
    })
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  const mountTasks = async () => {
    wrapper = mount(defineComponent({
      setup: () => () => h('div', { class: 'scroll-parent', style: 'overflow: auto' }, [
        h('div', { class: 'board' }, [
          h('div', { class: 'preceding-project' }),
          h('section', [
            h(component, { tasks, gap: 0, rowGap: 0 }),
          ]),
        ]),
      ]),
    }), {
      attachTo: document.body,
      global: { stubs: { TaskCard: TaskCardStub } },
    })
    await frames()
    return wrapper
  }

  const expectRows = (start: number, end: number) => {
    for (let index = start; index <= end; index++) {
      expect(wrapper!.find(`[data-task-id="t${index}"]`).exists(), `row ${index}`).toBe(true)
    }
    expect(wrapper!.findAll('.task-card').length).toBeLessThan(TASK_COUNT)
  }

  const scrollTo = async (offset: number) => {
    const element = wrapper!.get('.scroll-parent').element
    element.scrollTop = offset
    element.dispatchEvent(new Event('scroll'))
    await frames()
  }

  it('renders later rows after an earlier project collapses without resizing this list', async () => {
    const w = await mountTasks()
    precedingHeight = 0
    notifyResize(w.get('.board').element)
    notifyResize(w.get('.preceding-project').element)
    await frames()

    await scrollTo(ROW_HEIGHT * 20)
    expectRows(20, 24)
  })

  it('updates the visible range after a layout shift even without a scroll event', async () => {
    const w = await mountTasks()
    await scrollTo(precedingHeight + ROW_HEIGHT * 20)
    expectRows(20, 24)

    precedingHeight += ROW_HEIGHT * 15
    notifyResize(w.get('.board').element)
    notifyResize(w.get('.preceding-project').element)
    await frames()
    expectRows(5, 9)
  })

  it('prerenders two viewports in either direction for fast scrolling', async () => {
    precedingHeight = 0
    await mountTasks()
    await scrollTo(ROW_HEIGHT * 30)
    expectRows(21, 43)
  })

  it('adapts the rendering buffer when the viewport grows', async () => {
    precedingHeight = 0
    const w = await mountTasks()
    viewportHeight = 1200
    notifyResize(w.get('.scroll-parent').element)
    await frames()
    await scrollTo(ROW_HEIGHT * 30)
    expectRows(12, 57)
  })

  it('releases resize observations when unmounted', async () => {
    const w = await mountTasks()
    w.unmount()
    wrapper = undefined
    expect([...observers].every(observer => observer.targets.size === 0)).toBe(true)
  })
})