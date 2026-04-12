<template>
  <div ref="containerRef" class="virtual-task-list">
    <div :style="{ height: `${virtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }">
      <div
        v-for="item in virtualizer.getVirtualItems()"
        :key="tasks[item.index].id"
        :data-index="item.index"
        :ref="(el) => measureRef(el as HTMLElement | null, item)"
        :style="{
          position: 'absolute',
          top: 0,
          left: 0,
          width: '100%',
          transform: `translateY(${item.start - virtualizer.options.scrollMargin}px)`,
        }"
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
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import TaskCard from './TaskCard.vue'
import type { FullTask } from '../lib/models'

const props = withDefaults(defineProps<{
  tasks: FullTask[]
  estimateSize?: number
  gap?: number
  readonly?: boolean
}>(), {
  estimateSize: 130,
  gap: 8,
  readonly: false,
})

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

const measureRef = (el: HTMLElement | null, item: { index: number }) => {
  if (el) {
    virtualizer.value.measureElement(el)
  }
}

let resizeObserver: ResizeObserver | null = null

onMounted(() => {
  scrollElement.value = findScrollParent(containerRef.value)
  updateScrollMargin()

  resizeObserver = new ResizeObserver(updateScrollMargin)
  if (containerRef.value) {
    resizeObserver.observe(containerRef.value)
  }

})

onUnmounted(() => {
  resizeObserver?.disconnect()
})
</script>

<style scoped>
.virtual-task-list {
  width: 100%;
}
</style>