<template>
  <div class="expandable-section">
    <div class="header" @click="toggleExpanded">
      <h3>{{ title }}</h3>
      <span class="chevron" :class="{ expanded: modelValue }">▼</span>
    </div>
    <div class="content" :class="{ expanded: modelValue }">
      <div class="content-inner">
        <slot></slot>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
const props = defineProps<{
  title: string
  modelValue: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

const toggleExpanded = () => {
  emit('update:modelValue', !props.modelValue)
}
</script>

<style scoped>
.expandable-section {
  width: 100%;
  margin: 0.5rem 0;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  cursor: pointer;
  padding: 0.5rem;
  border-radius: 0.25rem;
  transition: background-color 0.3s;
}

.header:hover {
  background-color: var(--color-background-hover);
}

.header h3 {
  margin: 0;
  font-size: 1rem;
  font-weight: 500;
  color: var(--color-text);
}

.chevron {
  font-size: 0.8rem;
  transition: transform 0.3s;
  color: var(--color-text);
}

.chevron.expanded {
  transform: rotate(180deg);
}

.content {
  max-height: 0;
  overflow: hidden;
  transition: max-height 0.3s ease-out;
}

.content.expanded {
  max-height: 1000px; /* Large enough to contain content */
}

.content-inner {
  padding: 0.5rem;
  padding-top: 0;
}
</style>