<template>
  <Select
    ref="selectRef"
    :modelValue="modelValue"
    :options="filteredOptions"
    :optionLabel="optionLabel"
    :optionValue="optionValue"
    filter
    autoFilterFocus
    resetFilterOnHide
    :filterFields="['_q']"
    :filterPlaceholder="filterPlaceholder"
    @update:modelValue="emit('update:modelValue', $event)"
    @filter="handleFilter"
    @hide="handleHide"
  >
    <template v-if="$slots.value" #value="slotProps">
      <slot name="value" v-bind="slotProps" />
    </template>
  </Select>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import Select from 'primevue/select'
import { fuzzyWordPrefixRank } from '../lib/fuzzyMatch'

const props = withDefaults(defineProps<{
  modelValue?: string | null
  options: unknown[]
  optionLabel: string
  optionValue: string
  filterPlaceholder?: string
  appendOption?: Record<string, unknown> | null
}>(), {
  modelValue: null,
  filterPlaceholder: 'Search',
  appendOption: null,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string | null): void
  (e: 'filter', value: string): void
}>()

const selectRef = ref<InstanceType<typeof Select> | null>(null)
const filterText = ref('')

const matchedOptions = computed(() => {
  const options = props.options as Record<string, unknown>[]
  const query = filterText.value
  if (!query) return options

  return options
    .map<Record<string, unknown> & { _rank: number; _q: string }>(option => {
      const label = String(option[props.optionLabel] ?? '')
      return {
        ...option,
        _rank: fuzzyWordPrefixRank(label, query),
        _q: query,
      }
    })
    .filter(option => option._rank >= 0)
    .sort((a, b) => {
      if (a._rank !== b._rank) return a._rank - b._rank
      return String(a[props.optionLabel] ?? '').localeCompare(String(b[props.optionLabel] ?? ''))
    })
})

// The appended option bypasses ranking so it always shows last, and carries the
// current query so PrimeVue's own filtering (on _q) keeps it visible.
const filteredOptions = computed(() => {
  if (!props.appendOption) return matchedOptions.value
  return [...matchedOptions.value, { ...props.appendOption, _q: filterText.value }]
})

const handleFilter = (event: { value: string }) => {
  filterText.value = event.value
  emit('filter', event.value)
  nextTick(() => {
    const instance = selectRef.value as any
    if (instance?.visibleOptions?.length > 0) {
      instance.changeFocusedOptionIndex(null, 0)
    }
  })
}

const handleHide = () => {
  filterText.value = ''
  emit('filter', '')
  nextTick(() => {
    const selectEl = selectRef.value?.$el as HTMLElement | undefined
    if (selectEl?.contains(document.activeElement)) {
      (document.activeElement as HTMLElement).blur()
    }
  })
}

const show = () => {
  selectRef.value?.show()
}

defineExpose({ show })
</script>