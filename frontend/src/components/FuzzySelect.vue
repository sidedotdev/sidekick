<template>
  <Select
    ref="selectRef"
    :modelValue="modelValue"
    :options="matchedOptions"
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
    <template v-if="appendOption" #footer>
      <button
        type="button"
        class="fuzzy-append-option"
        @click="selectAppendOption"
      >{{ String(appendOption[optionLabel] ?? '') }}</button>
    </template>
  </Select>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
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

// The appended option is rendered in the Select's footer slot rather than as a
// regular option: PrimeVue re-filters the options list internally, which could
// hide a synthetic entry, while the footer stays visible even when the list
// shows "no results".
const selectAppendOption = () => {
  if (!props.appendOption) return
  emit('update:modelValue', String(props.appendOption[props.optionValue] ?? ''))
  ;(selectRef.value as any)?.hide?.(true)
}

// The overlay (including the filter input) is teleported to the body, so Enter
// is intercepted with a document-level capture listener scoped to this
// select's overlay. When no regular option is focused, Enter picks the
// appended option instead of just closing the dropdown.
const onDocumentKeydown = (event: KeyboardEvent) => {
  if (event.key !== 'Enter' || !props.appendOption) return
  const instance = selectRef.value as any
  const overlay = instance?.overlay as HTMLElement | undefined
  if (!overlay || !(event.target instanceof Node) || !overlay.contains(event.target)) return
  if (instance?.focusedOptionIndex >= 0) return
  event.preventDefault()
  event.stopPropagation()
  selectAppendOption()
}

onMounted(() => document.addEventListener('keydown', onDocumentKeydown, true))
onBeforeUnmount(() => document.removeEventListener('keydown', onDocumentKeydown, true))

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

<style scoped>
.fuzzy-append-option {
  display: block;
  width: 100%;
  padding: 0.5rem 0.75rem;
  border: none;
  border-top: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.fuzzy-append-option:hover,
.fuzzy-append-option:focus-visible {
  background-color: var(--color-background-mute);
}
</style>