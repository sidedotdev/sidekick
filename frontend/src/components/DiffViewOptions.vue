<template>
  <div class="view-options-container">
    <button 
      type="button" 
      class="view-options-button" 
      @click="toggle"
      :disabled="disabled"
      title="View options"
      ref="trigger"
    >
      <GearIcon />
    </button>
    <Popover ref="op">
      <div class="view-options-content">
        <div class="view-option">
          <label class="checkbox-label">
            <input 
              type="checkbox" 
              v-model="localIgnoreWhitespace"
            >
            Ignore whitespace in diff
          </label>
        </div>
        
        <div class="view-option">
          <label class="view-mode-label">Diff view mode:</label>
          <div class="radio-group">
            <label class="radio-label">
              <input 
                type="radio" 
                v-model="localDiffMode" 
                value="unified"
              >
              Unified
            </label>
            <label class="radio-label">
              <input 
                type="radio" 
                v-model="localDiffMode" 
                value="split"
              >
              Split
            </label>
          </div>
        </div>
      </div>
    </Popover>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import Popover from 'primevue/popover'
import GearIcon from './icons/GearIcon.vue'

const props = defineProps<{
  ignoreWhitespace: boolean
  diffMode: 'unified' | 'split'
  disabled?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:ignoreWhitespace', value: boolean): void
  (e: 'update:diffMode', value: 'unified' | 'split'): void
}>()

const op = ref()

const toggle = (event: Event) => {
  op.value.toggle(event)
}

const localIgnoreWhitespace = computed({
  get: () => props.ignoreWhitespace,
  set: (value) => emit('update:ignoreWhitespace', value)
})

const localDiffMode = computed({
  get: () => props.diffMode,
  set: (value) => emit('update:diffMode', value)
})
</script>

<style scoped>
.view-options-container {
  position: relative;
  display: inline-block;
}

.view-options-button {
  background: var(--color-background-soft);
  border: 1px solid var(--color-border);
  padding: 0.25rem 0.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0;
  cursor: pointer;
  border-radius: 4px;
}

.view-options-button:hover:not(:disabled) {
  background: var(--color-background-hover);
}

.view-options-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.view-options-button svg {
  width: 1.25rem;
  height: 1.25rem;
}

.view-options-content {
  padding: 0.75rem;
  min-width: 15rem;
}

.view-option {
  margin-bottom: 0.75rem;
}

.view-option:last-child {
  margin-bottom: 0;
}

.view-mode-label {
  display: block;
  margin-bottom: 0.25rem;
  font-weight: normal;
  color: var(--color-text);
}

.checkbox-label {
  display: flex;
  align-items: center;
  font-weight: normal;
  color: var(--color-text);
  cursor: pointer;
}

.checkbox-label input[type="checkbox"] {
  margin-right: 0.5rem;
}

.radio-group {
  display: flex;
  gap: 1rem;
  margin-left: 0.5rem;
}

.radio-label {
  display: inline-flex;
  align-items: center;
  font-weight: normal;
  cursor: pointer;
}

.radio-label input[type="radio"] {
  margin-right: 0.25rem;
}
</style>