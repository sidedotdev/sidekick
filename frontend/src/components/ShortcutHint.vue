<template>
  <span class="shortcut-hint">{{ label }}</span>
</template>

<script setup lang="ts">
defineProps<{
  label: string
}>()
</script>

<style scoped>
.shortcut-hint {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  font-size: 0.6875rem;
  line-height: 1;
  opacity: 0.7;
  padding: 0.1875rem 0.3125rem;
  background: rgba(255, 255, 255, 0.15);
  border-radius: 0.25rem;
  display: inline-flex;
  align-items: center;
  vertical-align: middle;
}

@media (prefers-color-scheme: light) {
  .shortcut-hint {
    background: rgba(0, 0, 0, 0.1);
  }
}
</style>