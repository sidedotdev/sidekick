import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, onMounted, ref, type PropType } from 'vue'
import VirtualTaskList from '../VirtualTaskList.vue'
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
  props: { task: { type: Object as PropType<FullTask>, required: true }, highlighted: Boolean },
  template: '<div class="task-card" :data-task-id="task.id" :class="{ highlighted }"></div>',
})

// jsdom has no layout, so give every element a fixed size and make the scroll
// parent emit scroll events as a browser would after programmatic scrolling.
const Host = defineComponent({
  props: { highlightedTaskId: { type: String as PropType<string | null>, default: null } },
  setup(props) {
    const scrollParent = ref<HTMLElement | null>(null)
    onMounted(() => {
      const el = scrollParent.value!
      el.scrollTo = ((options: ScrollToOptions) => {
        el.scrollTop = options.top ?? 0
        el.dispatchEvent(new Event('scroll'))
      }) as typeof el.scrollTo
    })
    return () => h('div', { ref: scrollParent, class: 'scroll-parent', style: 'overflow: auto' }, [
      h(VirtualTaskList, { tasks, highlightedTaskId: props.highlightedTaskId }),
    ])
  },
})

const nextFrames = async (count = 4) => {
  for (let i = 0; i < count; i++) {
    await new Promise(resolve => requestAnimationFrame(() => resolve(null)))
    await flushPromises()
  }
}

describe('VirtualTaskList highlighted task visibility', () => {
  let wrapper: VueWrapper | null = null
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

  const mountList = async (highlightedTaskId: string | null) => {
    wrapper = mount(Host, {
      props: { highlightedTaskId },
      attachTo: document.body,
      global: { stubs: { TaskCard: TaskCardStub } },
    })
    await nextFrames()
    return wrapper
  }

  it('reveals the highlighted card on mount', async () => {
    await mountList('t2')
    expect(revealedTaskIds).toContain('t2')
  })

  it('does not scroll when nothing is highlighted', async () => {
    const w = await mountList(null)
    expect(revealedTaskIds).toEqual([])
    expect(w.get('.scroll-parent').element.scrollTop).toBe(0)
  })

  it('virtualizes an offscreen highlighted card into view and reveals it in outer viewports', async () => {
    const w = await mountList('t0')
    expect(w.find('[data-task-id="t30"]').exists()).toBe(false)

    await w.setProps({ highlightedTaskId: 't30' })
    await nextFrames()

    expect(w.get('.scroll-parent').element.scrollTop).toBeGreaterThan(0)
    expect(w.get('[data-task-id="t30"]').classes()).toContain('highlighted')
    expect(revealedTaskIds.at(-1)).toBe('t30')
  })
})