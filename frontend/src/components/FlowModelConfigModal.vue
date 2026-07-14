<template>
  <Teleport to="body">
    <div class="model-config-overlay" @click="cancel"></div>
    <section
      class="model-config-modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="model-config-title"
    >
      <header>
        <h2 id="model-config-title">Model configuration</h2>
        <button
          type="button"
          class="close-button"
          aria-label="Cancel"
          :disabled="isApplying"
          @click="cancel"
        >×</button>
      </header>

      <div v-if="isLoading" class="loading-state" role="status">
        Loading model configuration…
      </div>

      <div v-else-if="loadError" class="error-state" role="alert">
        <p>{{ loadError }}</p>
        <button type="button" @click="loadConfiguration">Retry loading</button>
      </div>

      <template v-else-if="editor">
        <ModelConfigPresetEditor
          :editor="editor"
          :overlay-base-z-index="1102"
          always-show-editor
        />

        <div v-if="applyError" class="error-state" role="alert">
          {{ applyError }}
        </div>

        <footer>
          <button type="button" :disabled="isApplying" @click="cancel">Cancel</button>
          <button
            type="button"
            class="apply-button"
            :disabled="isApplying"
            @click="applyConfiguration"
          >
            {{ isApplying ? 'Applying…' : 'Apply' }}
          </button>
        </footer>
      </template>
    </section>
  </Teleport>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import ModelConfigPresetEditor from './ModelConfigPresetEditor.vue'
import {
  useModelConfigPresets,
  validateLlmConfig,
  type ModelConfigPresetEditorState,
} from '../composables/useModelConfigPresets'
import { llmConfigsEqual } from '../lib/llmPresetStorage'
import type { LLMConfig } from '../lib/models'

const props = defineProps<{
  workspaceId: string
  flowId: string
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'applied'): void
}>()

const POLL_INTERVAL_MS = 250
const MAX_POLL_ATTEMPTS = 20

const editor = shallowRef<ModelConfigPresetEditorState | null>(null)
const isLoading = ref(true)
const isApplying = ref(false)
const loadError = ref('')
const applyError = ref('')
let disposed = false

const getErrorMessage = async (response: Response): Promise<string> => {
  try {
    const body = await response.json()
    return body?.error || response.statusText
  } catch {
    return response.statusText
  }
}

const queryConfiguration = async (): Promise<LLMConfig> => {
  const response = await fetch(
    `/api/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/query`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: 'model_config' }),
    },
  )

  if (!response.ok) {
    throw new Error(await getErrorMessage(response))
  }

  const body = await response.json()
  if (!body?.result?.defaults || !Array.isArray(body.result.defaults)) {
    throw new Error('The workflow returned an invalid model configuration.')
  }
  return {
    defaults: body.result.defaults,
    useCaseConfigs: body.result.useCaseConfigs || {},
  } as LLMConfig
}

const loadConfiguration = async () => {
  isLoading.value = true
  loadError.value = ''

  try {
    const config = await queryConfiguration()
    if (!disposed) {
      editor.value = useModelConfigPresets(config, {
        initiallyCustom: true,
        useDefaultWhenMissing: false,
      })
    }
  } catch (error) {
    if (!disposed) {
      loadError.value = `Unable to load the flow model configuration. ${error instanceof Error ? error.message : ''}`.trim()
    }
  } finally {
    if (!disposed) {
      isLoading.value = false
    }
  }
}

const wait = () => new Promise<void>((resolve) => {
  setTimeout(resolve, POLL_INTERVAL_MS)
})

const pollUntilApplied = async (submittedConfig: LLMConfig): Promise<boolean> => {
  for (let attempt = 0; attempt < MAX_POLL_ATTEMPTS; attempt++) {
    const currentConfig = await queryConfiguration()
    if (llmConfigsEqual(currentConfig, submittedConfig)) {
      return true
    }
    if (attempt < MAX_POLL_ATTEMPTS - 1) {
      await wait()
    }
  }
  return false
}

const applyConfiguration = async () => {
  if (!editor.value || isApplying.value) return

  const submittedConfig: LLMConfig = JSON.parse(JSON.stringify(editor.value.llmConfig.value))
  applyError.value = ''

  if (!validateLlmConfig(submittedConfig)) {
    applyError.value = 'Select a provider for the default model and every enabled use case before applying.'
    return
  }

  isApplying.value = true
  try {
    const response = await fetch(
      `/api/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/model_config`,
      {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: submittedConfig }),
      },
    )
    if (!response.ok) {
      throw new Error(await getErrorMessage(response))
    }

    const applied = await pollUntilApplied(submittedConfig)
    if (!applied) {
      applyError.value = 'The workflow accepted the update but did not apply it in time. Retry to check or submit again.'
      return
    }

    editor.value.saveOrUpdatePreset()
    emit('applied')
    emit('close')
  } catch (error) {
    applyError.value = `Unable to apply the model configuration. ${error instanceof Error ? error.message : ''} Retry when the workflow is available.`.trim()
  } finally {
    isApplying.value = false
  }
}

const cancel = () => {
  if (!isApplying.value) {
    emit('close')
  }
}

onMounted(loadConfiguration)
onBeforeUnmount(() => {
  disposed = true
})
</script>

<style scoped>
.model-config-overlay {
  position: fixed;
  inset: 0;
  z-index: 1100;
  background: var(--vt-c-black);
  opacity: 0.7;
}

.model-config-modal {
  position: fixed;
  top: 50%;
  left: 50%;
  z-index: 1101;
  width: min(50rem, calc(100vw - 2rem));
  max-height: calc(100vh - 2rem);
  padding: 1.5rem;
  overflow: auto;
  transform: translate(-50%, -50%);
  border: 1px solid var(--color-border);
  border-radius: 0.5rem;
  background: var(--color-modal-background);
  color: var(--color-modal-text);
}

header,
footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

header {
  margin-bottom: 1rem;
}

header h2 {
  margin: 0;
}

footer {
  justify-content: flex-end;
  gap: 0.75rem;
  margin-top: 1.5rem;
}

button {
  padding: 0.5rem 0.875rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background: var(--color-background-soft);
  color: var(--color-text);
  cursor: pointer;
}

button:disabled {
  cursor: wait;
  opacity: 0.6;
}

.close-button {
  padding: 0;
  border: 0;
  background: none;
  font-size: 1.5rem;
}

.apply-button {
  background: var(--color-cta-button-bg);
  color: var(--color-cta-button-text);
}

.loading-state {
  padding: 2rem;
  text-align: center;
  color: var(--color-text-2);
}

.error-state {
  padding: 0.75rem;
  margin: 1rem 0;
  border: 1px solid var(--color-error-border);
  border-radius: 0.25rem;
  background: var(--color-error-background);
  color: var(--color-error-text);
}

.error-state p {
  margin-top: 0;
}
</style>