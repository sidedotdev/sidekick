<template>
  <div ref="containerRef" class="virtual-task-grid">
    <div :style="{ height: `${virtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }">
      <div
        v-for="item in virtualizer.getVirtualItems()"
        :key="item.index"
        :data-index="item.index"
        :ref="(el) => measureRef(el as HTMLElement | null, item)"
        class="grid-row"
        :style="{
          position: 'absolute',
          top: 0,
          left: 0,
          width: '100%',
          transform: `translateY(${item.start - virtualizer.options.scrollMargin}px)`,
        }"
      >
        <TaskCard
          v-for="task in rows[item.index]"
          :key="task.id"
          :task="task"
          :readonly="readonly"
          v-bind="$attrs"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import TaskCard from './TaskCard.vue'
import type { FullTask } from '../lib/models'

const props = withDefaults(defineProps<{
  tasks: FullTask[]
  minColumnWidth?: number
  estimateRowHeight?: number
  rowGap?: number
  readonly?: boolean
}>(), {
  minColumnWidth: 300,
  estimateRowHeight: 130,
  rowGap: 16,
  readonly: false,
})

defineOptions({ inheritAttrs: false })

const containerRef = ref<HTMLElement | null>(null)
const scrollElement = ref<HTMLElement | null>(null)
const scrollMargin = ref(0)
const containerWidth = ref(0)

const columns = computed(() => {
  const w = containerWidth.value
  if (w === 0) return 1
  const gapPx = 16
  return Math.max(1, Math.floor((w + gapPx) / (props.minColumnWidth + gapPx)))
})

const rows = computed(() => {
  const result: FullTask[][] = []
  const cols = columns.value
  for (let i = 0; i < props.tasks.length; i += cols) {
    result.push(props.tasks.slice(i, i + cols))
  }
  return result
})

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
  get count() { return rows.value.length },
  getScrollElement: () => scrollElement.value,
  estimateSize: () => props.estimateRowHeight,
  get scrollMargin() { return scrollMargin.value },
  get gap() { return props.rowGap },
  overscan: 3,
})

watch([() => rows.value.length, columns], () => {
  virtualizer.value.measure()
})

const measureRef = (el: HTMLElement | null, item: { index: number }) => {
  if (el) {
    virtualizer.value.measureElement(el)
  }
}

function updateContainerWidth() {
  if (containerRef.value) {
    containerWidth.value = containerRef.value.clientWidth
  }
}

let resizeObserver: ResizeObserver | null = null

onMounted(() => {
  scrollElement.value = findScrollParent(containerRef.value)
  updateScrollMargin()
  updateContainerWidth()

  resizeObserver = new ResizeObserver(() => {
    updateScrollMargin()
    updateContainerWidth()
  })
  if (containerRef.value) {
    resizeObserver.observe(containerRef.value)
  }
})

onUnmounted(() => {
  resizeObserver?.disconnect()
})
</script>

<style scoped>
.virtual-task-grid {
  width: 100%;
}

.grid-row {
  display: grid;
  grid-template-columns: repeat(v-bind(columns), 1fr);
  gap: 1rem;
}
</style>