<template>
  <div class="segmented-control">
    <button
      v-for="option in options"
      :key="option.value"
      :class="{ active: modelValue === option.value }"
      @click="$emit('update:modelValue', option.value)"
      type="button"
    >
      {{ option.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
interface Option {
  label: string;
  value: string;
}

defineProps<{
  modelValue: string;
  options: Option[];
}>();

defineEmits<{
  (e: 'update:modelValue', value: string): void;
}>();
</script>

<style scoped>
.segmented-control {
  display: inline-flex;
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.5rem;
  overflow: hidden;
}

button {
  border: 0;
  padding: 0.5rem 1rem;
  background-color: var(--color-background);
  color: var(--color-text-2);
  cursor: pointer;
  transition: background-color 0.3s, color 0.3s;
  font-size: var(--font-size);
}

button.active {
  color: var(--color-text);
  background-color: var(--color-background-hover);
  box-shadow: inset 0 0 1px 1px var(--color-border-hover);
  border-radius: 0.5rem;
  border-width: 0;
}
</style>