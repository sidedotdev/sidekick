<template>
  <div class="segmented-control">
    <button
      v-for="option in options"
      :key="option.value"
      :class="{ active: modelValue === option.value }"
      @click="$emit('update:modelValue', option.value)"
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
  display: flex;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  overflow: hidden;
}

button {
  flex: 1;
  padding: 0.5rem 1rem;
  background-color: var(--color-background);
  color: var(--color-text);
  border: none;
  cursor: pointer;
  transition: background-color 0.3s, color 0.3s;
}

button:not(:last-child) {
  border-right: 1px solid var(--color-border);
}

button.active {
  background-color: var(--color-primary);
  color: var(--color-text-inverse);
}
</style>