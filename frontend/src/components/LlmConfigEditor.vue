<template>
  <div class="llm-config-editor">
    <!-- Default Model -->
    <div class="model-row">
      <span class="model-label">Default</span>
      <select v-model="defaultConfig.provider" class="provider-select" @change="emitUpdate">
        <option value="">Select</option>
        <option v-for="p in providerOptions" :key="p" :value="p">{{ p }}</option>
      </select>
      <input
        type="text"
        v-model="defaultConfig.model"
        placeholder="Model name"
        class="model-input"
        @input="emitUpdate"
      />
      <button type="button" class="options-toggle" @click="defaultOptionsExpanded = !defaultOptionsExpanded">
        Options {{ defaultOptionsExpanded ? '▲' : '▼' }}
      </button>
    </div>
    <div v-if="defaultOptionsExpanded" class="options-row">
      <label class="option-label">
        Reasoning Effort
        <select v-model="defaultConfig.reasoningEffort" class="reasoning-select" @change="emitUpdate">
          <option v-for="r in reasoningEffortOptions" :key="r" :value="r">{{ r || 'Select' }}</option>
        </select>
      </label>
    </div>

    <!-- Use Case Models -->
    <template v-for="useCase in USE_CASES" :key="useCase">
      <div class="model-row">
        <label class="use-case-checkbox">
          <input type="checkbox" v-model="useCaseStates[useCase].enabled" @change="emitUpdate" />
          <span class="model-label">{{ useCase }}</span>
        </label>
        <select
          v-model="useCaseStates[useCase].config.provider"
          class="provider-select"
          :disabled="!useCaseStates[useCase].enabled"
          @change="emitUpdate"
        >
          <option value="">Select</option>
          <option v-for="p in providerOptions" :key="p" :value="p">{{ p }}</option>
        </select>
        <input
          type="text"
          v-model="useCaseStates[useCase].config.model"
          placeholder="Model name"
          class="model-input"
          :disabled="!useCaseStates[useCase].enabled"
          @input="emitUpdate"
        />
        <button
          type="button"
          class="options-toggle"
          :disabled="!useCaseStates[useCase].enabled"
          @click="useCaseStates[useCase].optionsExpanded = !useCaseStates[useCase].optionsExpanded"
        >
          Options {{ useCaseStates[useCase].optionsExpanded ? '▲' : '▼' }}
        </button>
      </div>
      <div v-if="useCaseStates[useCase].enabled && useCaseStates[useCase].optionsExpanded" class="options-row">
        <label class="option-label">
          Reasoning Effort
          <select v-model="useCaseStates[useCase].config.reasoningEffort" class="reasoning-select" @change="emitUpdate">
            <option v-for="r in reasoningEffortOptions" :key="r" :value="r">{{ r || 'Select' }}</option>
          </select>
        </label>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue'
import type { ModelConfig, LLMConfig } from '../lib/models'

const props = defineProps<{
  modelValue?: LLMConfig | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: LLMConfig): void
}>()

const USE_CASES = ['planning', 'judging', 'code_localization'] as const
type UseCase = typeof USE_CASES[number]

const providerOptions = ['google', 'anthropic', 'openai']
const reasoningEffortOptions = ['', 'minimal', 'low', 'medium', 'high'] as const

const defaultConfig = reactive<ModelConfig>(
  props.modelValue?.defaults?.[0]
    ? { ...props.modelValue.defaults[0] }
    : { provider: '', model: '', reasoningEffort: '' }
)
const defaultOptionsExpanded = ref(false)

type UseCaseState = { enabled: boolean; config: ModelConfig; optionsExpanded: boolean }

const initUseCaseStates = (): Record<UseCase, UseCaseState> => {
  const states = {} as Record<UseCase, UseCaseState>
  for (const useCase of USE_CASES) {
    const existingConfig = props.modelValue?.useCaseConfigs?.[useCase]?.[0]
    states[useCase] = {
      enabled: !!existingConfig,
      config: existingConfig ? { ...existingConfig } : { provider: '', model: '', reasoningEffort: '' },
      optionsExpanded: false,
    }
  }
  return states
}

const useCaseStates = reactive(initUseCaseStates())

const buildLlmConfig = (): LLMConfig => {
  const llmConfig: LLMConfig = {
    defaults: [{ ...defaultConfig }],
    useCaseConfigs: {},
  }

  for (const useCase of USE_CASES) {
    const state = useCaseStates[useCase]
    if (state.enabled && state.config.provider && state.config.model) {
      llmConfig.useCaseConfigs[useCase] = [{ ...state.config }]
    }
  }

  return llmConfig
}

const emitUpdate = () => {
  emit('update:modelValue', buildLlmConfig())
}

watch(() => props.modelValue, (newValue) => {
  if (newValue) {
    const newDefault = newValue.defaults?.[0] || { provider: '', model: '', reasoningEffort: '' }
    defaultConfig.provider = newDefault.provider
    defaultConfig.model = newDefault.model
    defaultConfig.reasoningEffort = newDefault.reasoningEffort || ''

    for (const useCase of USE_CASES) {
      const existingConfig = newValue.useCaseConfigs?.[useCase]?.[0]
      useCaseStates[useCase].enabled = !!existingConfig
      if (existingConfig) {
        useCaseStates[useCase].config.provider = existingConfig.provider
        useCaseStates[useCase].config.model = existingConfig.model
        useCaseStates[useCase].config.reasoningEffort = existingConfig.reasoningEffort || ''
      }
    }
  }
}, { deep: true })
</script>

<style scoped>
.llm-config-editor {
  padding: 0.75rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background-soft);
}

.model-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}

.model-label {
  min-width: 8rem;
  font-weight: 500;
}

.use-case-checkbox {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  margin: 0;
  min-width: 8rem;
}

.use-case-checkbox input {
  margin: 0;
}

.provider-select {
  min-width: 6rem;
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}

.model-input {
  flex: 1;
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}

.options-toggle {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
  cursor: pointer;
  font-size: 0.875rem;
}

.options-toggle:hover:not(:disabled) {
  background-color: var(--color-background-soft);
}

.options-toggle:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.options-row {
  margin-left: 8.5rem;
  margin-bottom: 0.75rem;
  padding: 0.5rem;
  background-color: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
}

.option-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin: 0;
  min-width: auto;
  font-size: 0.875rem;
}

.reasoning-select {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}
</style>