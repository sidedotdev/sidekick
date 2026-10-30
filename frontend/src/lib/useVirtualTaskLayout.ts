import { computed, onMounted, onUnmounted, ref, type Ref } from 'vue'

function findScrollParent(element: HTMLElement | null): HTMLElement | null {
  while (element) {
    const style = getComputedStyle(element)
    if (/(auto|scroll)/.test(style.overflow + style.overflowY)) {
      return element
    }
    element = element.parentElement
  }
  return null
}

export function useVirtualTaskLayout(
  container: Ref<HTMLElement | null>,
  estimateSize: () => number,
) {
  const scrollElement = ref<HTMLElement | null>(null)
  const scrollMargin = ref(0)
  const viewportHeight = ref(0)
  let resizeObserver: ResizeObserver | null = null

  const updateLayout = () => {
    const list = container.value
    const scroll = scrollElement.value
    if (!list || !scroll) return
    const scrollRect = scroll.getBoundingClientRect()
    scrollMargin.value = list.getBoundingClientRect().top
      - scrollRect.top - scroll.clientTop + scroll.scrollTop
    viewportHeight.value = scrollRect.height
  }

  // Buffer both directions so a fast gesture or reversal needn't wait for Vue
  // to mount the next screen of cards. Row estimates keep the DOM budget bounded.
  const overscan = computed(() =>
    Math.max(5, Math.ceil(2 * viewportHeight.value / Math.max(1, estimateSize()))),
  )

  onMounted(() => {
    scrollElement.value = findScrollParent(container.value)
    updateLayout()
    resizeObserver = new ResizeObserver(updateLayout)

    // Collapsing a sibling project moves the list without resizing it. Its
    // content-sized ancestors still resize, even with a fixed-height scrollport.
    let element = container.value
    while (element) {
      resizeObserver.observe(element)
      if (element === scrollElement.value) break
      element = element.parentElement
    }
  })

  onUnmounted(() => resizeObserver?.disconnect())

  return { scrollElement, scrollMargin, overscan }
}