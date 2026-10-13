<template>
  <select
    v-if="options.length > 1"
    class="service-tier-select"
    :value="selectedId"
    :disabled="disabled"
    title="Service tier"
    aria-label="Service tier"
    @change="onChange(($event.target as HTMLSelectElement).value)"
  >
    <option v-for="option in options" :key="option.id" :value="option.id">{{ option.label }}</option>
  </select>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ModelsData } from '../lib/store'
import {
  applyServiceTier,
  findServiceTierOptions,
  selectedServiceTierId,
  type ServiceTierSelection,
} from '../lib/serviceTiers'

const props = defineProps<{
  modelValue: ServiceTierSelection
  provider: string
  model: string
  modelsData: ModelsData
  disabled?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: ServiceTierSelection): void
}>()

const options = computed(() => findServiceTierOptions(props.modelsData, props.provider, props.model, props.modelValue))
const selectedId = computed(() => selectedServiceTierId(options.value, props.modelValue))

const onChange = (id: string) => {
  const option = options.value.find((o) => o.id === id)
  if (option) emit('update:modelValue', applyServiceTier(option))
}
</script>

<style scoped>
.service-tier-select {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
  font-size: 0.875rem;
  min-width: 5.5rem;
}

.service-tier-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>