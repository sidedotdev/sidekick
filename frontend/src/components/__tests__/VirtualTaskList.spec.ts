import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, onMounted, provide, ref, type PropType } from 'vue'
import VirtualTaskList from '../VirtualTaskList.vue'
import { BOARD_CARD_LISTS_KEY, type BoardCardList } from '../../lib/kanbanNavigation'
import type { FullTask } from '../../lib/models'

const CARD_HEIGHT = 130
const VIEWPORT_HEIGHT = 300

const tasks = Array.from({ length: 40 }, (_, i) => ({
  id: `t${i}`,
  agentType: 'human',
  status: 'drafting',
  workspaceId: 'w',
  flows: [],
})) as unknown as FullTask[]

const TaskCardStub = defineComponent({
  props: { task: { type: Object as PropType<FullTask>, required: true } },
  template: '<div class="task-card" :data-task-id="task.id"></div>',
})

// jsdom has no layout, so give every element a fixed size and make the scroll
// parent emit scroll events as a browser would after programmatic scrolling.
// Unless boardless, the host plays the board's part, providing the card list
// registry.
const Host = defineComponent({
  props: { boardless: Boolean },
  setup(props, { expose }) {
    const scrollParent = ref<HTMLElement | null>(null)
    const lists = new Set<BoardCardList>()
    if (!props.boardless) {
      provide(BOARD_CARD_LISTS_KEY, {
        register(list) {
          lists.add(list)
          return () => lists.delete(list)
        },
      })
    }
    let latestRequest = 0
    const reveal = (taskId: string, force = true): boolean => {
      const request = ++latestRequest
      return [...lists].some(list => list.revealCard({ taskId, force, isCurrent: () => request === latestRequest }))
    }
    expose({ reveal })
    onMounted(() => {
      const el = scrollParent.value!
      el.scrollTo = ((options: ScrollToOptions) => {
        el.scrollTop = options.top ?? 0
        el.dispatchEvent(new Event('scroll'))
      }) as typeof el.scrollTo
    })
    return () => h('div', { ref: scrollParent, class: 'scroll-parent', style: 'overflow: auto' }, [
      h(VirtualTaskList, { tasks }),
    ])
  },
})

type HostWrapper = VueWrapper<InstanceType<typeof Host> & { reveal: (taskId: string, force?: boolean) => boolean }>

const nextFrames = async (count = 4) => {
  for (let i = 0; i < count; i++) {
    await new Promise(resolve => requestAnimationFrame(() => resolve(null)))
    await flushPromises()
  }
}

const focusedTaskId = () => (document.activeElement as HTMLElement | null)?.dataset?.taskId ?? null

describe('VirtualTaskList card reveal', () => {
  let wrapper: HostWrapper | null = null
  let revealedTaskIds: (string | null)[] = []

  beforeEach(() => {
    revealedTaskIds = []
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(VIEWPORT_HEIGHT)
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(VIEWPORT_HEIGHT)
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(CARD_HEIGHT * tasks.length)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
      { top: 0, left: 0, right: 0, bottom: CARD_HEIGHT, width: VIEWPORT_HEIGHT, height: CARD_HEIGHT, x: 0, y: 0, toJSON: () => ({}) },
    )
    HTMLElement.prototype.scrollIntoView = vi.fn(function (this: HTMLElement) {
      revealedTaskIds.push(this.querySelector('.task-card')?.getAttribute('data-task-id') ?? null)
    })
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView
  })

  const mountList = async (props: { boardless?: boolean } = {}) => {
    wrapper = mount(Host, {
      props,
      attachTo: document.body,
      global: { stubs: { TaskCard: TaskCardStub } },
    }) as HostWrapper
    await nextFrames()
    return wrapper
  }

  it('reveals and focuses a mounted card, keeping cards out of the tab order', async () => {
    const w = await mountList()
    expect(w.vm.reveal('t2')).toBe(true)
    expect(revealedTaskIds).toEqual(['t2'])
    expect(focusedTaskId()).toBe('t2')

    const cards = w.findAll('.virtual-task-card-container')
    expect(cards.length).toBeGreaterThan(1)
    expect(cards.every(card => card.attributes('tabindex') === '-1')).toBe(true)
  })

  it('renders plain, unfocusable cards outside a board', async () => {
    const w = await mountList({ boardless: true })
    const cards = w.findAll('.virtual-task-card-container')
    expect(cards.length).toBeGreaterThan(1)
    expect(cards.every(card => card.attributes('data-board-card') === undefined)).toBe(true)
    expect(cards.every(card => card.attributes('tabindex') === undefined)).toBe(true)
  })

  it('reports tasks it does not hold without scrolling', async () => {
    const w = await mountList()
    expect(w.vm.reveal('missing')).toBe(false)
    expect(revealedTaskIds).toEqual([])
    expect(w.get('.scroll-parent').element.scrollTop).toBe(0)
  })

  it('virtualizes an offscreen card into view, revealing it in outer viewports', async () => {
    const w = await mountList()
    expect(w.find('[data-task-id="t30"]').exists()).toBe(false)

    w.vm.reveal('t30')
    await nextFrames()

    expect(w.get('.scroll-parent').element.scrollTop).toBeGreaterThan(0)
    expect(revealedTaskIds.at(-1)).toBe('t30')
    expect(focusedTaskId()).toBe('t30')
  })

  it('neither scrolls nor takes focus from another element unless forced', async () => {
    const w = await mountList()
    const button = document.createElement('button')
    document.body.appendChild(button)
    button.focus()
    try {
      w.vm.reveal('t30', false)
      await nextFrames()
      expect(w.get('.scroll-parent').element.scrollTop).toBe(0)
      expect(revealedTaskIds).toEqual([])
      expect(document.activeElement).toBe(button)
    } finally {
      button.remove()
    }
  })

  it('drops a pending reveal once superseded', async () => {
    const w = await mountList()
    w.vm.reveal('t30')
    w.vm.reveal('t31')
    await nextFrames()
    expect(focusedTaskId()).toBe('t31')
    expect(revealedTaskIds).not.toContain('t30')
  })
})