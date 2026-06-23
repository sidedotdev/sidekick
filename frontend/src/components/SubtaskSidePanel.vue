<template>
  <div
    ref="rootRef"
    class="side-panel"
    role="dialog"
    aria-label="Sub-task"
    tabindex="-1"
  >
    <div
      class="side-panel-resize"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize sub-task panel"
      @mousedown="(e) => emit('resize-start', e)"
    ></div>
    <header class="side-panel-head">
      <span class="eyebrow">Sub-task</span>
      <button
        type="button"
        class="side-panel-close"
        @click="emit('close')"
        aria-label="Dismiss sub-task"
      >×</button>
    </header>
    <div class="side-panel-body">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue'

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'resize-start', event: MouseEvent): void
}>()

const rootRef = ref<HTMLElement | null>(null)

const isFocusInside = (): boolean => {
  const root = rootRef.value
  if (!root) return false
  const active = document.activeElement as Node | null
  if (!active) return false
  return root === active || root.contains(active)
}

const handleKeydown = (event: KeyboardEvent) => {
  if (event.key !== 'Escape') return
  // Only dismiss when the side panel itself or one of its descendants holds
  // focus, so escape elsewhere on the canvas (e.g. the editor) is unaffected.
  if (!isFocusInside()) return
  event.stopPropagation()
  emit('close')
}

onMounted(() => {
  nextTick(() => {
    rootRef.value?.focus()
  })
  window.addEventListener('keydown', handleKeydown, true)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeydown, true)
})
</script>

<style scoped>
.side-panel:focus {
  outline: none;
}

.side-panel {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(var(--side-panel-w, 60rem), 90vw);
  display: flex;
  flex-direction: column;
  background-color: var(--color-background);
  border-left: 1px solid var(--color-border);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.25);
  /* Must sit above the fixed FlowEditorLinks overlay (z-index 1000) so the
     dismiss control and sticky sub-flow headers remain interactive. */
  z-index: 1001;
}

.side-panel-resize {
  position: absolute;
  top: 0;
  bottom: 0;
  left: -0.2rem;
  width: 0.5rem;
  cursor: col-resize;
  z-index: 2;
  background-color: transparent;
  transition: background-color 120ms ease;
}

.side-panel-resize:hover,
.side-panel-resize:active {
  background-color: var(--color-primary);
  opacity: 0.6;
}

.side-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.85rem 1.25rem;
  border-bottom: 1px solid var(--color-border);
  background-color: var(--color-background);
  position: relative;
  z-index: 1;
}

.side-panel-close {
  background: none;
  border: none;
  color: var(--color-text-2);
  font-size: 1.4rem;
  line-height: 1;
  cursor: pointer;
  padding: 0 0.25rem;
}

.side-panel-close:hover {
  color: var(--color-text);
}

.side-panel-body {
  flex: 1;
  min-height: 0;
  position: relative;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  isolation: isolate;
}

.side-panel-body :deep(.flow-actions-container) {
  flex: 1;
  min-height: 0;
}

.eyebrow {
  font-size: 0.7rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--color-text-2);
}
</style>