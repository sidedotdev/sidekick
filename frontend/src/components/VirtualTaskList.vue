<template>
  <div ref="containerRef" class="virtual-task-list">
    <div :style="{ height: `${virtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }">
      <div
        v-for="item in virtualizer.getVirtualItems()"
        :key="tasks[item.index].id"
        :data-index="item.index"
        :ref="(el) => measureRef(el as HTMLElement | null)"
        :style="{
          position: 'absolute',
          top: 0,
          left: 0,
          width: '100%',
          transform: `translateY(${item.start - virtualizer.options.scrollMargin}px)`,
        }"
        class="virtual-task-card-container"
        data-board-card
        :data-task-id="tasks[item.index].id"
        :tabindex="item.index === highlightedIndex ? -1 : undefined"
        :draggable="draggable || undefined"
        @dragstart="(event: DragEvent) => onDragStart(event, tasks[item.index])"
      >
        <TaskCard
          :task="tasks[item.index]"
          :readonly="readonly"
          :highlighted="highlightedTaskId != null && tasks[item.index].id === highlightedTaskId"
          v-bind="$attrs"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch, onMounted, onUnmounted } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import TaskCard from './TaskCard.vue'
import type { FullTask } from '../lib/models'
import { canMoveNavigationFocus } from '../lib/dom'
import { BOARD_CARD_SELECTOR } from '../lib/kanbanNavigation'

const props = withDefaults(defineProps<{
  tasks: FullTask[]
  estimateSize?: number
  gap?: number
  readonly?: boolean
  draggable?: boolean
  highlightedTaskId?: string | null
}>(), {
  estimateSize: 130,
  gap: 8,
  readonly: false,
  draggable: false,
  highlightedTaskId: null,
})

const onDragStart = (event: DragEvent, task: FullTask) => {
  if (!props.draggable || !event.dataTransfer) return
  event.dataTransfer.setData('text/plain', task.id)
  event.dataTransfer.effectAllowed = 'move'
}

defineOptions({ inheritAttrs: false })

const containerRef = ref<HTMLElement | null>(null)
const scrollElement = ref<HTMLElement | null>(null)
const scrollMargin = ref(0)

function findScrollParent(el: HTMLElement | null): HTMLElement | null {
  while (el) {
    const style = getComputedStyle(el)
    if (/(auto|scroll)/.test(style.overflow + style.overflowY)) {
      return el
    }
    el = el.parentElement
  }
  return null
}

function updateScrollMargin() {
  if (containerRef.value && scrollElement.value) {
    scrollMargin.value = containerRef.value.getBoundingClientRect().top
      - scrollElement.value.getBoundingClientRect().top
      + scrollElement.value.scrollTop
  }
}

const virtualizer = useVirtualizer({
  get count() { return props.tasks.length },
  getScrollElement: () => scrollElement.value,
  estimateSize: () => props.estimateSize,
  get scrollMargin() { return scrollMargin.value },
  get gap() { return props.gap },
  overscan: 5,
})

watch(() => props.tasks.length, () => {
  virtualizer.value.measure()
})

const measureRef = (el: HTMLElement | null) => {
  if (el) {
    virtualizer.value.measureElement(el)
  }
}

const highlightedIndex = computed(() =>
  props.highlightedTaskId ? props.tasks.findIndex(task => task.id === props.highlightedTaskId) : -1,
)

// The virtualized card may take a few frames to mount after scrolling
const REVEAL_MAX_FRAMES = 3

// The virtualizer only scrolls the nearest scroll parent, so the mounted card
// is revealed directly to also bring it into outer board/page viewports.
// Focus follows the highlight unless something else on the page holds it.
function revealHighlightedCard(index: number, framesLeft = REVEAL_MAX_FRAMES) {
  if (highlightedIndex.value !== index) return
  const card = containerRef.value?.querySelector<HTMLElement>(`[data-index="${index}"]`)
  if (card) {
    card.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    if (canMoveNavigationFocus(BOARD_CARD_SELECTOR)) {
      card.focus({ preventScroll: true })
    }
  } else if (framesLeft > 0) {
    requestAnimationFrame(() => revealHighlightedCard(index, framesLeft - 1))
  }
}

function scrollToHighlightedCard() {
  const index = highlightedIndex.value
  if (index < 0) return
  if (scrollElement.value) {
    virtualizer.value.scrollToIndex(index, { align: 'auto' })
  }
  void nextTick(() => revealHighlightedCard(index))
}

watch(highlightedIndex, scrollToHighlightedCard)

let resizeObserver: ResizeObserver | null = null

onMounted(() => {
  scrollElement.value = findScrollParent(containerRef.value)
  updateScrollMargin()

  resizeObserver = new ResizeObserver(updateScrollMargin)
  if (containerRef.value) {
    resizeObserver.observe(containerRef.value)
  }

  scrollToHighlightedCard()
})

onUnmounted(() => {
  resizeObserver?.disconnect()
})
</script>

<style scoped>
.virtual-task-list {
  width: 100%;
  /* Establish a stacking context so hover-expanded task cards paint above
     sibling controls (e.g. the "Draft Task" / "Queue Task" buttons below). */
  position: relative;
  z-index: 1;
}
/* Sibling lists are equal stacking contexts painted in DOM order, so the list
   owning the hovered card must be raised for its expansion to reach across
   lists in later groups. */
.virtual-task-list:has(.virtual-task-card-container:hover) {
  z-index: 10;
}
.virtual-task-card-container {
  z-index: 0;
  /* Lets the surrounding layout keep revealed cards clear of headers
     overlaying the top of the scrollport */
  scroll-margin-top: var(--task-list-scroll-margin-top, 0);
}
/* The highlighted task card draws its own focus outline */
.virtual-task-card-container:focus {
  outline: none;
}
/* Each transformed row is its own stacking context, so the hovered row must
   be raised for its expanded card to paint over later sibling rows. */
.virtual-task-card-container:hover {
  z-index: 10;
}
</style>
