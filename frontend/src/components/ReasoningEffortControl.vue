<template>
  <select
    v-if="ladder"
    class="reasoning-select-inline"
    :value="modelValue ?? ''"
    :disabled="disabled"
    @change="onManualChange(($event.target as HTMLSelectElement).value)"
  >
    <option value="">{{ effortLabel('') }}</option>
    <option v-for="level in ladder" :key="level" :value="level">{{ effortLabel(level) }}</option>
  </select>
  <AutoComplete
    v-else-if="supportsReasoning"
    :model-value="modelValue ?? ''"
    placeholder="Reasoning"
    class="reasoning-effort-input"
    inputClass="reasoning-effort-input-inner"
    :disabled="disabled"
    :suggestions="filteredSuggestions"
    complete-on-focus
    overlay-class="llm-config-editor-overlay"
    @complete="searchSuggestions"
    @update:model-value="onManualChange"
    @keydown.enter.stop
  >
    <template #option="{ option }">{{ effortLabel(option) }}</template>
  </AutoComplete>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AutoComplete from 'primevue/autocomplete'
import type { ModelInfo, ModelsData } from '../lib/store'

const props = defineProps<{
  modelValue?: string
  provider: string
  model: string
  modelsData: ModelsData
  disabled?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const DEFAULT_SUGGESTIONS = ['low', 'medium', 'high', 'xhigh', 'max']
const REFERENCE_ORDER = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']

const effortLabel = (value: string): string => {
  if (!value) return 'Default'
  if (value === 'xhigh') return 'Extra'
  return value.charAt(0).toUpperCase() + value.slice(1)
}

const findModelInfo = (provider: string, model: string): ModelInfo | undefined => {
  if (!model) return undefined
  const providerInfo = props.modelsData[provider]
  if (providerInfo?.models) return providerInfo.models[model]
  // Unknown providers may still proxy a known model, so prefer any provider's reasoning-capable entry
  return Object.values(props.modelsData)
    .map((p) => p.models?.[model])
    .find((info) => info?.reasoning === true)
}

const modelInfo = computed(() => findModelInfo(props.provider, props.model))
const supportsReasoning = computed(() => modelInfo.value?.reasoning === true)
const ladder = computed<string[] | null>(() => {
  if (!supportsReasoning.value) return null
  const effortOption = modelInfo.value?.reasoning_options?.find((o) => o.type === 'effort')
  return effortOption?.values?.length ? effortOption.values : null
})

const filteredSuggestions = ref<string[]>([])

const searchSuggestions = (event: { query: string }) => {
  const query = event.query.toLowerCase()
  filteredSuggestions.value = DEFAULT_SUGGESTIONS.filter(
    (v) => v.includes(query) || effortLabel(v).toLowerCase().includes(query),
  )
}

// Only user-chosen values are remembered, so auto-switching never overwrites the user's intent.
const manualEffort = ref<string | null>(null)

const onManualChange = (value: string | null) => {
  const effort = value ?? ''
  manualEffort.value = effort
  emit('update:modelValue', effort)
}

const nearestSupportedEffort = (effort: string, levels: string[]): string => {
  const target = effort === 'lowest' ? 'low' : effort === 'highest' ? levels[levels.length - 1] : effort
  if (levels.includes(target)) return target

  const targetIndex = REFERENCE_ORDER.indexOf(target)
  if (targetIndex < 0) return ''

  let nearest = ''
  let nearestDistance = Infinity
  for (const level of levels) {
    const index = REFERENCE_ORDER.indexOf(level)
    if (index < 0) continue
    const distance = Math.abs(index - targetIndex)
    if (distance < nearestDistance) {
      nearest = level
      nearestDistance = distance
    }
  }
  return nearest
}

watch([() => props.provider, () => props.model, ladder], () => {
  const levels = ladder.value
  if (!levels) return

  const current = props.modelValue ?? ''
  const manual = manualEffort.value
  let next: string
  if (manual !== null && (manual === '' || levels.includes(manual))) {
    next = manual
  } else if (current === '' || levels.includes(current)) {
    next = current
  } else {
    next = nearestSupportedEffort(current, levels)
  }

  if (next !== current) {
    emit('update:modelValue', next)
  }
}, { immediate: true })
</script>

<style scoped>
.reasoning-select-inline {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
  font-size: 0.875rem;
  min-width: 5.5rem;
}

.reasoning-effort-input {
  width: 6.5rem;
}

:deep(.reasoning-effort-input-inner) {
  width: 100%;
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
  font-size: 0.875rem;
}

:disabled, :deep(.p-disabled), :deep(:disabled) {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>