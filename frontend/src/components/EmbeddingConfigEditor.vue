<template>
  <div class="embedding-config-editor">
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
        @complete="searchModels($event, defaultConfig.provider)"
        @item-select="emitUpdate"
        @change="emitUpdate"
        @input="emitUpdate"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, onMounted } from 'vue'
import AutoComplete from 'primevue/autocomplete'
import type { ModelConfig, EmbeddingConfig } from '../lib/models'
import { store, type ModelsData } from '../lib/store'

const props = defineProps<{
  modelValue?: EmbeddingConfig | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: EmbeddingConfig): void
}>()

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

const searchModels = (event: { query: string } | undefined, provider: string) => {
  const query = (event?.query || '').toLowerCase()

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
    // Silently fail - autocomplete will work without suggestions
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

onMounted(() => {
  loadModelsData()
  fetchProviders()
})

const defaultConfig = reactive<ModelConfig>(
  props.modelValue?.defaults?.[0]
    ? { ...props.modelValue.defaults[0] }
    : { provider: '', model: '' }
)

const buildEmbeddingConfig = (): EmbeddingConfig => {
  return {
    defaults: [{ ...defaultConfig }],
    useCaseConfigs: {},
  }
}

const emitUpdate = () => {
  emit('update:modelValue', buildEmbeddingConfig())
}

watch(() => props.modelValue, (newValue) => {
  if (newValue) {
    const newDefault = newValue.defaults?.[0] || { provider: '', model: '' }
    defaultConfig.provider = newDefault.provider
    defaultConfig.model = newDefault.model
  }
}, { deep: true })
</script>

<style scoped>
:disabled, :deep(.p-disabled), :deep(:disabled) {
  opacity: 0.5;
  cursor: not-allowed;
}

.embedding-config-editor {
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
  border: 1px solid var(--color-error, #dc3545);
  border-radius: 0.25rem;
  background-color: var(--color-error-bg, rgba(220, 53, 69, 0.1));
  color: var(--color-error, #dc3545);
  font-size: 0.875rem;
}
</style>