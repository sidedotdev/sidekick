<template>
  <div class="model-config-preset-editor">
    <div class="preset-section">
      <label>Model Config</label>
      <Dropdown
        ref="presetDropdownRef"
        v-model="selectedPresetValue"
        :options="editor.presetOptions.value"
        optionLabel="label"
        optionValue="value"
        class="preset-dropdown"
        @change="(event: any) => handlePresetChange(event.value)"
      >
        <template #option="{ option }">
          <div class="preset-option">
            <div class="preset-option-content">
              <div class="preset-option-text">
                <div class="preset-name">{{ option.label }}</div>
                <div
                  v-if="option.preset && option.label !== getModelSummary(option.preset.config)"
                  class="preset-summary"
                >
                  {{ getModelSummary(option.preset.config) }}
                </div>
              </div>
              <span v-if="option.preset" class="preset-action-icons">
                <button
                  type="button"
                  class="preset-edit-icon"
                  :aria-label="`Edit ${option.label}`"
                  @click.stop="editPreset(option.preset.id)"
                >✎</button>
                <button
                  type="button"
                  class="preset-delete-icon"
                  :aria-label="`Delete ${option.label}`"
                  @click.stop="editor.deletePreset(option.preset.id, $event)"
                >×</button>
              </span>
            </div>
          </div>
        </template>
      </Dropdown>
    </div>

    <div v-if="showConfigEditor" class="add-preset-section">
      <div v-if="editor.isEditingPreset.value" class="preset-editing-label">Editing preset</div>
      <input
        v-if="editor.isAddPresetMode.value"
        v-model="newPresetName"
        type="text"
        :placeholder="editor.isEditingPreset.value ? 'Preset name' : 'Preset name (optional)'"
        class="preset-name-input"
      />
      <LlmConfigEditor v-model="llmConfig" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import Dropdown from 'primevue/dropdown'
import LlmConfigEditor from './LlmConfigEditor.vue'
import { getModelSummary } from '../lib/llmPresets'
import type { ModelConfigPresetEditorState } from '../composables/useModelConfigPresets'

const props = withDefaults(defineProps<{
  editor: ModelConfigPresetEditorState
  alwaysShowEditor?: boolean
}>(), {
  alwaysShowEditor: false,
})

const presetDropdownRef = ref<InstanceType<typeof Dropdown> | null>(null)

const selectedPresetValue = computed({
  get: () => props.editor.selectedPresetValue.value,
  set: props.editor.setSelectedPresetValue,
})

const newPresetName = computed({
  get: () => props.editor.newPresetName.value,
  set: props.editor.setNewPresetName,
})

const llmConfig = computed({
  get: () => props.editor.llmConfig.value,
  set: props.editor.setLlmConfig,
})

const showConfigEditor = computed(
  () => props.alwaysShowEditor || props.editor.isAddPresetMode.value,
)

const handlePresetChange = (value: string) => {
  props.editor.handlePresetChange(value)
}

const editPreset = (presetId: string) => {
  props.editor.editPreset(presetId)
  presetDropdownRef.value?.hide()
}
</script>

<style scoped>
.preset-section {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.preset-section label {
  min-width: 7.5rem;
}

.preset-dropdown {
  flex: 1;
  max-width: 20rem;
}

.preset-option {
  width: 100%;
}

.preset-option-content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.preset-option-text {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 0.125rem;
}

.preset-name {
  font-weight: 500;
}

.preset-summary,
.preset-editing-label {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.preset-editing-label {
  font-style: italic;
}

.preset-action-icons {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  visibility: hidden;
}

.preset-option:hover .preset-action-icons {
  visibility: visible;
}

.preset-edit-icon,
.preset-delete-icon {
  padding: 0.25rem;
  border: 0;
  background: none;
  color: inherit;
  cursor: pointer;
  font-size: 0.875rem;
  opacity: 0.4;
  transition: opacity 0.2s ease;
}

.preset-edit-icon:hover,
.preset-delete-icon:hover {
  opacity: 1;
}

.add-preset-section {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  margin-top: 0.5rem;
}

.preset-name-input {
  max-width: 20rem;
  padding: 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}
</style>