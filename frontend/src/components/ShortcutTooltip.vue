<template>
  <Tooltip v-bind="$attrs" :position="position">
    <slot />
    <template #content>
      <span class="shortcut-tooltip-label">{{ label }}</span>
      <ShortcutHint v-if="shortcut" :label="shortcut" />
    </template>
  </Tooltip>
</template>

<script setup lang="ts">
import Tooltip from './Tooltip.vue'
import ShortcutHint from './ShortcutHint.vue'

withDefaults(defineProps<{
  label: string
  shortcut?: string
  position?: 'right' | 'top' | 'bottom' | 'left'
}>(), {
  position: 'right'
})
</script>

<style scoped>
.shortcut-tooltip-label {
  opacity: 0.9;
}
</style>