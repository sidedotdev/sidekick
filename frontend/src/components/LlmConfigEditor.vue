<template>
  <div class="llm-config-editor">
    <!-- Error state for providers fetch -->
    <div v-if="providersError" class="providers-error">
      Failed to load providers. Please refresh the page.
    </div>

    <!-- Default Model -->
    <div class="model-row">
      <span class="model-label">Default</span>
      <select v-model="defaultConfig.provider" class="provider-select" @change="emitUpdate">
        <option value="">Provider</option>
        <option v-for="p in providerOptions" :key="p" :value="p">{{ getProviderLabel(p) }}</option>
      </select>
      <AutoComplete
        v-model="defaultConfig.model"
        placeholder="model"
        class="model-input-wrapper"
        inputClass="model-input-inner"
        :suggestions="filteredModels"
        @complete="(e: { query: string }) => searchModels(e, defaultConfig.provider)"
        @item-select="emitUpdate"
        @change="emitUpdate"
        @input="emitUpdate"
      />
      <select
        v-if="modelSupportsReasoning(defaultConfig.provider, defaultConfig.model)"
        v-model="defaultConfig.reasoningEffort"
        class="reasoning-select-inline"
        @change="emitUpdate"
      >
        <option v-for="r in reasoningEffortOptions" :key="r" :value="r">{{ r || 'Reasoning' }}</option>
      </select>
    </div>

    <!-- Use Case Models -->
    <template v-for="useCase in USE_CASES" :key="useCase">
      <div class="model-row">
        <label class="use-case-checkbox">
          <input type="checkbox" v-model="useCaseStates[useCase].enabled" @change="emitUpdate" />
          <span class="model-label">{{ getUseCaseLabel(useCase) }}</span>
        </label>
        <select
          v-model="useCaseStates[useCase].config.provider"
          class="provider-select"
          :disabled="!useCaseStates[useCase].enabled"
          @change="emitUpdate"
        >
          <option value="">Provider</option>
          <option v-for="p in providerOptions" :key="p" :value="p">{{ getProviderLabel(p) }}</option>
        </select>
        <AutoComplete
          v-model="useCaseStates[useCase].config.model"
          placeholder="model"
          class="model-input-wrapper"
          inputClass="model-input-inner"
          :disabled="!useCaseStates[useCase].enabled"
          :suggestions="filteredModels"
          @complete="(e: { query: string }) => searchModels(e, useCaseStates[useCase].config.provider)"
          @item-select="emitUpdate"
          @change="emitUpdate"
          @input="emitUpdate"
        />
        <select
          v-if="useCaseStates[useCase].enabled && modelSupportsReasoning(useCaseStates[useCase].config.provider, useCaseStates[useCase].config.model)"
          v-model="useCaseStates[useCase].config.reasoningEffort"
          class="reasoning-select-inline"
          @change="emitUpdate"
        >
          <option v-for="r in reasoningEffortOptions" :key="r" :value="r">{{ r || 'Reasoning' }}</option>
        </select>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, onMounted } from 'vue'
import AutoComplete from 'primevue/autocomplete'
import type { ModelConfig, LLMConfig } from '../lib/models'
import { store, type ModelsData } from '../lib/store'

const props = defineProps<{
  modelValue?: LLMConfig | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: LLMConfig): void
}>()

const USE_CASES = ['planning', 'judging', 'code_localization'] as const
type UseCase = typeof USE_CASES[number]

const getUseCaseLabel = (useCase: UseCase): string => {
  const labels: Record<UseCase, string> = {
    planning: 'Plan',
    judging: 'Review',
    code_localization: 'Context',
  }
  return labels[useCase]
}

const getProviderLabel = (provider: string): string => {
  const labels: Record<string, string> = {
    google: 'Google',
    anthropic: 'Anthropic',
    openai: 'OpenAI',
  }
  return labels[provider] || provider
}

const providerOptions = ref<string[]>([])
const providersError = ref(false)
const reasoningEffortOptions = ['', 'minimal', 'low', 'medium', 'high'] as const

const fetchProviders = async () => {
  try {
    const response = await fetch('/api/v1/providers')
    if (response.ok) {
      const data = await response.json()
      providerOptions.value = data.providers || []
      providersError.value = false
    } else {
      providersError.value = true
    }
  } catch {
    providersError.value = true
  }
}

const modelsData = ref<ModelsData>({})
const filteredModels = ref<string[]>([])

const searchModels = (event: { query: string }, provider: string) => {
  const query = event.query.toLowerCase()

  let candidates: string[] = []
  if (modelsData.value[provider]) {
    candidates = Object.keys(modelsData.value[provider].models || {})
  } else {
    const allModels = new Set<string>()
    for (const p of Object.values(modelsData.value)) {
      if (p.models) {
        Object.keys(p.models).forEach((m) => allModels.add(m))
      }
    }
    candidates = Array.from(allModels)
  }

  filteredModels.value = candidates.filter((m) => m.toLowerCase().includes(query))
}

const fetchModelsData = async () => {
  try {
    const response = await fetch('/api/v1/models')
    if (response.ok) {
      const data = await response.json()
      modelsData.value = data
      store.setModelsCache(data)
    }
  } catch {
    // Silently fail - reasoning selectors will be hidden if data unavailable
  }
}

const loadModelsData = () => {
  const cache = store.getModelsCache()
  if (cache) {
    modelsData.value = cache.data
    if (store.isModelsCacheStale()) {
      fetchModelsData()
    }
  } else {
    fetchModelsData()
  }
}

const modelSupportsReasoning = (provider: string, model: string): boolean => {
  if (!model) return false
  const providerInfo = modelsData.value[provider]
  if (!providerInfo?.models) {
    // for unknown providers, check against all providers, any matching model is acceptable
    return Object.values(modelsData.value).some((p) => p.models?.[model]?.reasoning === true)
  }
  const modelInfo = providerInfo.models[model]
  return modelInfo?.reasoning === true
}

onMounted(() => {
  loadModelsData()
  fetchProviders()
})

const defaultConfig = reactive<ModelConfig>(
  props.modelValue?.defaults?.[0]
    ? { ...props.modelValue.defaults[0] }
    : { provider: '', model: '', reasoningEffort: '' }
)

type UseCaseState = { enabled: boolean; config: ModelConfig }

const initUseCaseStates = (): Record<UseCase, UseCaseState> => {
  const states = {} as Record<UseCase, UseCaseState>
  for (const useCase of USE_CASES) {
    const existingConfig = props.modelValue?.useCaseConfigs?.[useCase]?.[0]
    states[useCase] = {
      enabled: !!existingConfig,
      config: existingConfig ? { ...existingConfig } : { provider: '', model: '', reasoningEffort: '' },
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
    if (state.enabled) {
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
:disabled, :deep(.p-disabled), :deep(:disabled) {
  opacity: 0.5;
  cursor: not-allowed;
}

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
  min-width: 5rem;
  font-weight: 500;
}

.use-case-checkbox {
  min-width: 5rem;
}

.use-case-checkbox input {
  vertical-align: middle;
  margin-right: 0.25rem;
}

.provider-select {
  min-width: 6rem;
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}

.model-input-wrapper {
  flex: 1;
}

:deep(.model-input-inner) {
  width: 100%;
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}

.reasoning-select-inline {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
  font-size: 0.875rem;
  min-width: 5.5rem;
}

.providers-error {
  padding: 0.5rem;
  margin-bottom: 0.75rem;
  border: 1px solid var(--color-error, #dc3545);
  border-radius: 0.25rem;
  background-color: var(--color-error-bg, rgba(220, 53, 69, 0.1));
  color: var(--color-error, #dc3545);
  font-size: 0.875rem;
}
</style>