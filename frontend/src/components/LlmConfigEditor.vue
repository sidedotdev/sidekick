<template>
  <div class="llm-config-editor">
    <!-- Default Model -->
    <div class="model-row">
      <span class="model-label">Default</span>
      <select v-model="defaultConfig.provider" class="provider-select" @change="onModelChange(defaultConfig)">
        <option value="">Provider</option>
        <option v-for="p in providerOptions" :key="p" :value="p">{{ getProviderLabel(p) }}</option>
      </select>
      <AutoComplete
        v-model="defaultConfig.model"
        placeholder="model"
        class="model-input-wrapper"
        inputClass="model-input-inner"
        :suggestions="filteredModels"
        overlay-class="llm-config-editor-overlay"
        @complete="(e) => searchModels(e, defaultConfig.provider)"
        @item-select="onModelChange(defaultConfig)"
        @change="onModelChange(defaultConfig)"
        @input="(e: Event) => onModelInput(e, defaultConfig)"
        @keydown.enter.stop
      />
      <ReasoningEffortControl
        v-model="defaultConfig.reasoningEffort"
        :provider="defaultConfig.provider"
        :model="defaultConfig.model"
        :models-data="modelsData"
        @update:model-value="emitUpdate"
      />
      <ServiceTierControl
        :model-value="tierSelection(defaultConfig)"
        :provider="defaultConfig.provider"
        :model="defaultConfig.model"
        :models-data="modelsData"
        @update:model-value="(tier) => applyTier(defaultConfig, tier)"
      />
    </div>
    <p v-if="requiresUsageCredits(defaultConfig.provider, tierSelection(defaultConfig))" class="usage-credits-note" role="note">
      {{ USAGE_CREDITS_NOTE }}
    </p>

    <div v-if="providersError" class="providers-error" role="alert">
      {{ providersError }}
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
          @change="onModelChange(useCaseStates[useCase].config)"
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
          overlay-class="llm-config-editor-overlay"
          @complete="(e) => searchModels(e, useCaseStates[useCase].config.provider)"
          @item-select="onModelChange(useCaseStates[useCase].config)"
          @change="onModelChange(useCaseStates[useCase].config)"
          @input="(e: Event) => onModelInput(e, useCaseStates[useCase].config)"
          @keydown.enter.stop
        />
        <ReasoningEffortControl
          v-if="useCaseStates[useCase].enabled"
          v-model="useCaseStates[useCase].config.reasoningEffort"
          :provider="useCaseStates[useCase].config.provider"
          :model="useCaseStates[useCase].config.model"
          :models-data="modelsData"
          @update:model-value="emitUpdate"
        />
        <ServiceTierControl
          v-if="useCaseStates[useCase].enabled"
          :model-value="tierSelection(useCaseStates[useCase].config)"
          :provider="useCaseStates[useCase].config.provider"
          :model="useCaseStates[useCase].config.model"
          :models-data="modelsData"
          @update:model-value="(tier) => applyTier(useCaseStates[useCase].config, tier)"
        />
      </div>
      <p
        v-if="useCaseStates[useCase].enabled && requiresUsageCredits(useCaseStates[useCase].config.provider, tierSelection(useCaseStates[useCase].config))"
        class="usage-credits-note"
        role="note"
      >
        {{ USAGE_CREDITS_NOTE }}
      </p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, onMounted, toRaw } from 'vue'
import AutoComplete from 'primevue/autocomplete'
import ReasoningEffortControl from './ReasoningEffortControl.vue'
import ServiceTierControl from './ServiceTierControl.vue'
import type { ModelConfig, LLMConfig } from '../lib/models'
import { store, type ModelsData } from '../lib/store'
import { completeModel, searchModels as searchModelIds } from '../lib/modelSuggestions'
import { keepOfferedTier, requiresUsageCredits, type ServiceTierSelection } from '../lib/serviceTiers'

const props = defineProps<{
  modelValue?: LLMConfig | null
  profileId?: string
  workspaceId?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: LLMConfig): void
}>()

const USAGE_CREDITS_NOTE = 'Fast mode requires usage credits; it isn’t covered by your subscription.'

const USE_CASES = ['planning', 'judging', 'code_localization', 'advising'] as const
type UseCase = typeof USE_CASES[number]

const getUseCaseLabel = (useCase: UseCase): string => {
  const labels: Record<UseCase, string> = {
    planning: 'Plan',
    judging: 'Review',
    code_localization: 'Context',
    advising: 'Advisor',
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
const providersError = ref('')

// When a workspaceId is provided, providers are filtered authoritatively by
// that workspace's persisted profile; otherwise the profileId prop (if any)
// is used, e.g. while a workspace's profile is still being chosen.
const providersUrl = (): string => {
  if (props.workspaceId) {
    return `/api/v1/workspaces/${encodeURIComponent(props.workspaceId)}/providers`
  }
  return props.profileId
    ? `/api/v1/providers?profileId=${encodeURIComponent(props.profileId)}`
    : '/api/v1/providers'
}

// Profile changes can leave earlier requests in flight, so only the most
// recently issued response is applied.
let latestProvidersRequestId = 0

const fetchProviders = async () => {
  const requestId = ++latestProvidersRequestId
  const fallbackError = 'Failed to load providers. Please check your local configuration and refresh the page.'
  try {
    const response = await fetch(providersUrl())
    if (requestId !== latestProvidersRequestId) return
    const data = await response.json()
    if (requestId !== latestProvidersRequestId) return
    if (response.ok) {
      providerOptions.value = data.providers || []
      providersError.value = ''
    } else {
      providerOptions.value = []
      providersError.value = typeof data?.error === 'string' && data.error
        ? data.error
        : fallbackError
    }
  } catch {
    if (requestId !== latestProvidersRequestId) return
    providerOptions.value = []
    providersError.value = fallbackError
  }
}

watch(() => [props.profileId, props.workspaceId], () => {
  fetchProviders()
})

const modelsData = ref<ModelsData>({})
const filteredModels = ref<string[]>([])

const searchModels = (event: { query: string }, provider: string) => {
  filteredModels.value = searchModelIds(modelsData.value, provider, event.query)
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

const tierSelection = (config: ModelConfig): ServiceTierSelection => ({
  serviceTier: config.serviceTier ?? '',
  speed: config.speed ?? '',
})

// The tier each row last had chosen or loaded. Provider/model edits only
// change the shown tier, so the chosen one comes back once a model offers it
// again, e.g. after passing through partially typed model names.
const chosenTiers = new WeakMap<ModelConfig, ServiceTierSelection>()

const rememberTier = (config: ModelConfig) => {
  chosenTiers.set(toRaw(config), tierSelection(config))
}

const setTier = (config: ModelConfig, tier: ServiceTierSelection) => {
  config.serviceTier = tier.serviceTier
  config.speed = tier.speed
}

const applyTier = (config: ModelConfig, tier: ServiceTierSelection) => {
  setTier(config, tier)
  rememberTier(config)
  emitUpdate()
}

// Called once the provider or model has been updated, so the tier can be
// checked against the newly selected model.
const onModelChange = (config: ModelConfig) => {
  const chosen = chosenTiers.get(toRaw(config)) ?? tierSelection(config)
  setTier(config, keepOfferedTier(modelsData.value, config.provider, config.model, chosen))
  emitUpdate()
}

// Completes typed text with the newest matching model, selecting the
// completed remainder so continued typing replaces it. Deletions, pastes and
// edits before the end are left alone so they behave as usual.
const completeTypedModel = (event: Event, config: ModelConfig) => {
  const input = event.target
  if ((event as InputEvent).inputType !== 'insertText' || !(input instanceof HTMLInputElement)) return
  const typed = input.value
  if (input.selectionEnd !== typed.length) return
  const completion = completeModel(modelsData.value, config.provider, typed)
  if (!completion || completion.length <= typed.length) return
  input.value = completion
  input.setSelectionRange(typed.length, completion.length)
  config.model = completion
}

const onModelInput = (event: Event, config: ModelConfig) => {
  completeTypedModel(event, config)
  onModelChange(config)
}

onMounted(() => {
  loadModelsData()
  fetchProviders()
})

const initModelConfig = (source?: ModelConfig): ModelConfig => ({
  provider: source?.provider ?? '',
  model: source?.model ?? '',
  reasoningEffort: source?.reasoningEffort ?? '',
  serviceTier: source?.serviceTier ?? '',
  speed: source?.speed ?? '',
})

const defaultConfig = reactive<ModelConfig>(initModelConfig(props.modelValue?.defaults?.[0]))

type UseCaseState = { enabled: boolean; config: ModelConfig }

const initUseCaseStates = (): Record<UseCase, UseCaseState> => {
  const states = {} as Record<UseCase, UseCaseState>
  for (const useCase of USE_CASES) {
    const existingConfig = props.modelValue?.useCaseConfigs?.[useCase]?.[0]
    states[useCase] = {
      enabled: !!existingConfig,
      config: initModelConfig(existingConfig),
    }
  }
  return states
}

const useCaseStates = reactive(initUseCaseStates())

rememberTier(defaultConfig)
for (const useCase of USE_CASES) rememberTier(useCaseStates[useCase].config)

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

const MODEL_CONFIG_FIELDS = ['provider', 'model', 'reasoningEffort', 'serviceTier', 'speed'] as const

// The parent echoes back each config we emit; only a config that differs from
// the current state (e.g. a newly loaded preset) replaces the remembered tier.
const syncModelConfig = (config: ModelConfig, incoming?: ModelConfig) => {
  const next = initModelConfig(incoming)
  const isEcho = MODEL_CONFIG_FIELDS.every((field) => (config[field] ?? '') === next[field])
  Object.assign(config, next)
  if (!isEcho) rememberTier(config)
}

watch(() => props.modelValue, (newValue) => {
  if (newValue) {
    syncModelConfig(defaultConfig, newValue.defaults?.[0])

    for (const useCase of USE_CASES) {
      const existingConfig = newValue.useCaseConfigs?.[useCase]?.[0]
      useCaseStates[useCase].enabled = !!existingConfig
      if (existingConfig) {
        syncModelConfig(useCaseStates[useCase].config, existingConfig)
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

.providers-error {
  padding: 0.5rem;
  margin-bottom: 0.75rem;
  border: 0.0625rem solid var(--color-error-border);
  border-radius: 0.25rem;
  background-color: var(--color-error-background);
  color: var(--color-error-text);
  font-size: 0.875rem;
}

.usage-credits-note {
  margin: -0.25rem 0 0.5rem;
  font-size: 0.75rem;
  color: var(--color-text-2);
  text-align: right;
}
</style>

<!-- PrimeVue teleports the autocomplete overlay to the body, out of reach of
     scoped styles, so it needs an explicit stack level above the modals hosting
     this editor, the topmost of which is .model-config-modal at 1101 -->
<style>
.llm-config-editor-overlay {
  z-index: 1102 !important;
}
</style>