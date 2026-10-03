<template>
  <div
    class="tooltip-wrapper"
    @mouseenter="trigger.hover()"
    @mouseleave="trigger.leave"
    @focusin="trigger.focus()"
    @focusout="trigger.leave"
  >
    <slot />
    <div v-if="visible" class="tooltip-popup" :class="positionClass">
      <slot name="content" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useTooltipTrigger } from '../composables/useTooltipTrigger'

const props = withDefaults(defineProps<{
  position?: 'right' | 'top' | 'bottom' | 'left'
}>(), {
  position: 'right'
})

const visible = ref(false)
const positionClass = computed(() => `tooltip-${props.position}`)
const trigger = useTooltipTrigger({
  show: () => { visible.value = true },
  hide: () => { visible.value = false },
})
</script>

<style scoped>
.tooltip-wrapper {
  position: relative;
}

.tooltip-popup {
  position: absolute;
  padding: 0.375rem 0.625rem;
  background: var(--color-background-mute);
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.375rem;
  white-space: nowrap;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8125rem;
  color: var(--color-text);
  z-index: var(--z-index-tooltip);
  pointer-events: none;
  box-shadow: 0 0.125rem 0.5rem rgba(0, 0, 0, 0.15);
}

.tooltip-popup::before {
  content: '';
  position: absolute;
  width: 0.5rem;
  height: 0.5rem;
  background: var(--color-background-mute);
  border: 1px solid var(--color-border-contrast);
  transform: rotate(45deg);
}

.tooltip-right {
  left: 100%;
  top: 50%;
  transform: translateY(-50%);
  margin-left: 0.5rem;
}

.tooltip-right::before {
  left: -0.3125rem;
  top: 50%;
  transform: translateY(-50%) rotate(45deg);
  border-right: none;
  border-top: none;
}

.tooltip-left {
  right: 100%;
  top: 50%;
  transform: translateY(-50%);
  margin-right: 0.5rem;
}

.tooltip-left::before {
  right: -0.3125rem;
  top: 50%;
  transform: translateY(-50%) rotate(45deg);
  border-left: none;
  border-bottom: none;
}

.tooltip-top {
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  margin-bottom: 0.5rem;
}

.tooltip-top::before {
  bottom: -0.3125rem;
  left: 50%;
  transform: translateX(-50%) rotate(45deg);
  border-top: none;
  border-left: none;
}

.tooltip-bottom {
  top: 100%;
  left: 50%;
  transform: translateX(-50%);
  margin-top: 0.5rem;
}

.tooltip-bottom::before {
  top: -0.3125rem;
  left: 50%;
  transform: translateX(-50%) rotate(45deg);
  border-bottom: none;
  border-right: none;
}
</style>