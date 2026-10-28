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
        :data-board-card="boardCardLists ? '' : undefined"
        :data-task-id="tasks[item.index].id"
        :tabindex="boardCardLists ? -1 : undefined"
        :draggable="draggable || undefined"
        @dragstart="(event: DragEvent) => onDragStart(event, tasks[item.index])"
      >
        <TaskCard
          :task="tasks[item.index]"
          :readonly="readonly"
          v-bind="$attrs"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { inject, nextTick, ref, watch, onMounted, onUnmounted } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import TaskCard from './TaskCard.vue'
import type { FullTask } from '../lib/models'
import { canMoveNavigationFocus } from '../lib/dom'
import { useVirtualTaskLayout } from '../lib/useVirtualTaskLayout'
import {
  BOARD_CARD_LISTS_KEY,
  BOARD_CARD_SELECTOR,
  type BoardCardList,
  type CardFocusRequest,
} from '../lib/kanbanNavigation'

const props = withDefaults(defineProps<{
  tasks: FullTask[]
  estimateSize?: number
  gap?: number
  readonly?: boolean
  draggable?: boolean
}>(), {
  estimateSize: 130,
  gap: 8,
  readonly: false,
  draggable: false,
})

const onDragStart = (event: DragEvent, task: FullTask) => {
  if (!props.draggable || !event.dataTransfer) return
  event.dataTransfer.setData('text/plain', task.id)
  event.dataTransfer.effectAllowed = 'move'
}

defineOptions({ inheritAttrs: false })

const containerRef = ref<HTMLElement | null>(null)
const { scrollElement, scrollMargin, overscan } = useVirtualTaskLayout(
  containerRef,
  () => props.estimateSize,
)

const virtualizer = useVirtualizer({
  get count() { return props.tasks.length },
  getScrollElement: () => scrollElement.value,
  estimateSize: () => props.estimateSize,
  get scrollMargin() { return scrollMargin.value },
  get gap() { return props.gap },
  get overscan() { return overscan.value },
})

watch(() => props.tasks.length, () => {
  virtualizer.value.measure()
})

const measureRef = (el: HTMLElement | null) => {
  if (el) {
    virtualizer.value.measureElement(el)
  }
}

// The virtualized card may take a few frames to mount after scrolling
const REVEAL_MAX_FRAMES = 3

// Unless forced, focus moves only while a card holds it or nothing does
const mayFocus = (request: CardFocusRequest): boolean =>
  request.isCurrent() && (request.force || canMoveNavigationFocus(BOARD_CARD_SELECTOR))

// The virtualizer only scrolls the nearest scroll parent, so the mounted card
// is revealed directly to also bring it into outer board/page viewports.
// Returns false while the card is not mounted yet.
function revealMountedCard(request: CardFocusRequest): boolean {
  const card = [...containerRef.value?.querySelectorAll<HTMLElement>(BOARD_CARD_SELECTOR) ?? []]
    .find(el => el.dataset.taskId === request.taskId)
  if (!card) return false
  if (mayFocus(request)) {
    card.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    card.focus({ preventScroll: true })
  }
  return true
}

function revealWhenMounted(request: CardFocusRequest, framesLeft = REVEAL_MAX_FRAMES) {
  if (!mayFocus(request) || revealMountedCard(request) || framesLeft === 0) return
  requestAnimationFrame(() => revealWhenMounted(request, framesLeft - 1))
}

function revealCard(request: CardFocusRequest): boolean {
  const index = props.tasks.findIndex(task => task.id === request.taskId)
  if (index < 0) return false
  if (mayFocus(request) && !revealMountedCard(request)) {
    if (scrollElement.value) {
      virtualizer.value.scrollToIndex(index, { align: 'auto' })
    }
    void nextTick(() => revealWhenMounted(request))
  }
  return true
}

// Only lists on a board hold (focusable, navigable) board cards
const boardCardLists = inject(BOARD_CARD_LISTS_KEY, null)
const cardList: BoardCardList = {
  hasCard: taskId => props.tasks.some(task => task.id === taskId),
  revealCard,
}
let unregisterCardList: (() => void) | null = null

onMounted(() => {
  unregisterCardList = boardCardLists?.register(cardList) ?? null
})

onUnmounted(() => {
  unregisterCardList?.()
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
/* The focused task card draws its own focus outline */
.virtual-task-card-container:focus {
  outline: none;
}
/* Each transformed row is its own stacking context, so the hovered row must
   be raised for its expanded card to paint over later sibling rows. */
.virtual-task-card-container:hover {
  z-index: 10;
}
</style>
