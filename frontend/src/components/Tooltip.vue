<template>
  <div
    class="tooltip-wrapper"
    @mouseenter="show = true"
    @mouseleave="show = false"
    @focusin="show = true"
    @focusout="show = false"
  >
    <slot />
    <div v-if="show" class="tooltip-popup" :class="positionClass">
      <slot name="content" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'

const props = withDefaults(defineProps<{
  position?: 'right' | 'top' | 'bottom' | 'left'
}>(), {
  position: 'right'
})

const show = ref(false)
const positionClass = computed(() => `tooltip-${props.position}`)
</script>

<style scoped>
.tooltip-wrapper {
  position: relative;
}

.tooltip-popup {
  position: absolute;
  padding: 0.375rem 0.625rem;
  background: var(--color-background-mute);
  border: 1px solid var(--color-border);
  border-radius: 0.375rem;
  white-space: nowrap;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8125rem;
  color: var(--color-text);
  z-index: 200;
  pointer-events: none;
  box-shadow: 0 0.125rem 0.5rem rgba(0, 0, 0, 0.15);
}

.tooltip-right {
  left: 100%;
  top: 50%;
  transform: translateY(-50%);
  margin-left: 0.5rem;
}

.tooltip-left {
  right: 100%;
  top: 50%;
  transform: translateY(-50%);
  margin-right: 0.5rem;
}

.tooltip-top {
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  margin-bottom: 0.5rem;
}

.tooltip-bottom {
  top: 100%;
  left: 50%;
  transform: translateX(-50%);
  margin-top: 0.5rem;
}
</style>