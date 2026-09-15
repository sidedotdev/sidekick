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
        <fieldset class="model-editor" :disabled="isApplying" :inert="isApplying || undefined">
          <ModelConfigPresetEditor
            :editor="editor"
            :workspace-id="workspaceId"
            always-show-editor
          />
        </fieldset>

        <fieldset v-if="modalConfig" class="modal-environment">
          <legend>Modal environment</legend>
          <p>Applying these settings snapshots and recreates the current sandbox.</p>
          <label>
            <input v-model="modalConfig.vm" type="checkbox" :disabled="isApplying" />
            VM runtime
          </label>
          <label>
            CPU request
            <input
              v-model.number="modalConfig.cpu"
              type="number"
              min="0.125"
              step="0.125"
              :disabled="isApplying"
            />
          </label>
          <label>
            CPU limit
            <input
              v-model.number="modalConfig.cpuLimit"
              type="number"
              min="0"
              step="0.125"
              :disabled="isApplying"
            />
          </label>
          <label>
            Memory request (MiB)
            <input
              v-model.number="modalConfig.memory"
              type="number"
              min="128"
              step="128"
              :disabled="isApplying"
            />
          </label>
          <label>
            Memory limit (MiB)
            <input
              v-model.number="modalConfig.memoryLimit"
              type="number"
              min="0"
              step="128"
              :disabled="isApplying"
            />
          </label>
        </fieldset>

        <fieldset v-if="portForwardRows" class="port-forwards">
          <legend>Port mappings</legend>
          <p>
            Each mapping exposes a port on this machine's localhost as a port on the sandbox's
            localhost. Leave the sandbox port blank to reuse the host port. Applying mappings
            updates the running sandbox without recreating it.
          </p>
          <ul v-if="portForwardRows.length" class="port-forward-list">
            <li v-for="(row, index) in portForwardRows" :key="row.id" class="port-forward">
              <label>
                Host port
                <input
                  v-model="row.hostPort"
                  class="host-port"
                  type="number"
                  :min="MIN_PORT"
                  :max="MAX_PORT"
                  :disabled="isApplying"
                />
              </label>
              <label>
                Sandbox port
                <input
                  v-model="row.containerPort"
                  class="container-port"
                  type="number"
                  :min="MIN_PORT"
                  :max="MAX_PORT"
                  :placeholder="containerPortPlaceholder(row)"
                  :disabled="isApplying"
                />
              </label>
              <button
                type="button"
                class="remove-port-forward"
                :disabled="isApplying"
                @click="removePortForward(index)"
              >Remove</button>
            </li>
          </ul>
          <p v-else class="no-port-forwards">No host ports are forwarded into the sandbox.</p>
          <button
            type="button"
            class="add-port-forward"
            :disabled="isApplying"
            @click="addPortForward"
          >Add port mapping</button>
        </fieldset>

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
import type { LLMConfig, ModalEnvConfig, PortForwardConfig } from '../lib/models'

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
const MIN_PORT = 1
const MAX_PORT = 65535

interface PortForwardRow {
  id: number
  hostPort: number | string
  containerPort: number | string
}

let nextPortForwardRowId = 0

const newPortForwardRow = (forward?: PortForwardConfig): PortForwardRow => {
  nextPortForwardRowId += 1
  return {
    id: nextPortForwardRowId,
    hostPort: forward?.hostPort ?? '',
    containerPort: forward?.containerPort || '',
  }
}

const isBlank = (value: number | string): boolean => typeof value === 'string' && value.trim() === ''

const parsePort = (value: number | string): number | null => {
  const port = typeof value === 'number' ? value : Number(value.trim())
  return Number.isInteger(port) && port >= MIN_PORT && port <= MAX_PORT ? port : null
}

// Mappings are compared in canonical form so a sandbox port that merely
// repeats the host port default does not look like a pending edit.
const canonicalizePortForwards = (forwards: PortForwardConfig[]): string => JSON.stringify(
  forwards.map((forward) => ({
    hostPort: forward.hostPort,
    containerPort: forward.containerPort && forward.containerPort !== forward.hostPort
      ? forward.containerPort
      : undefined,
  })),
)

const buildPortForwards = (
  rows: PortForwardRow[],
): { forwards: PortForwardConfig[]; error: string } => {
  const forwards: PortForwardConfig[] = []
  const hostPortsBySandboxPort = new Map<number, number>()

  for (const row of rows) {
    const hostPort = parsePort(row.hostPort)
    if (hostPort === null) {
      return {
        forwards,
        error: `Every port mapping needs a host port between ${MIN_PORT} and ${MAX_PORT}.`,
      }
    }

    let containerPort: number | null = null
    if (!isBlank(row.containerPort)) {
      containerPort = parsePort(row.containerPort)
      if (containerPort === null) {
        return {
          forwards,
          error: `Sandbox ports must be between ${MIN_PORT} and ${MAX_PORT}, or blank to match the host port.`,
        }
      }
    }

    const sandboxPort = containerPort ?? hostPort
    const conflictingHostPort = hostPortsBySandboxPort.get(sandboxPort)
    if (conflictingHostPort !== undefined) {
      return {
        forwards,
        error: `Sandbox port ${sandboxPort} is mapped from both host port ${conflictingHostPort} and host port ${hostPort}.`,
      }
    }

    hostPortsBySandboxPort.set(sandboxPort, hostPort)
    forwards.push(containerPort === null ? { hostPort } : { hostPort, containerPort })
  }

  return { forwards, error: '' }
}

const editor = shallowRef<ModelConfigPresetEditorState | null>(null)
const appliedLlmConfig = ref<LLMConfig | null>(null)
const modalConfig = ref<ModalEnvConfig | null>(null)
const initialModalConfig = ref<ModalEnvConfig | null>(null)
const portForwardRows = ref<PortForwardRow[] | null>(null)
const appliedPortForwards = ref('')
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

// A failed query means the environment cannot report mappings at all, which
// is not the same as it reporting that no ports are forwarded.
const queryPortForwards = async (): Promise<PortForwardConfig[] | null> => {
  const response = await fetch(
    `/api/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/query`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: 'port_forwards' }),
    },
  )
  if (!response.ok) {
    return null
  }
  const body = await response.json()
  return body?.result?.portForwards ?? []
}

const addPortForward = () => {
  portForwardRows.value?.push(newPortForwardRow())
}

const removePortForward = (index: number) => {
  portForwardRows.value?.splice(index, 1)
}

const containerPortPlaceholder = (row: PortForwardRow): string => {
  const hostPort = parsePort(row.hostPort)
  return hostPort === null ? 'host port' : String(hostPort)
}

const loadConfiguration = async () => {
  isLoading.value = true
  loadError.value = ''

  try {
    const config = await queryConfiguration()
    let loadedModalConfig: ModalEnvConfig | null = null
    const modalResponse = await fetch(
      `/api/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/query`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query: 'modal_config' }),
      },
    )
    if (modalResponse.ok) {
      const body = await modalResponse.json()
      loadedModalConfig = body.result ?? null
    }
    const loadedPortForwards = await queryPortForwards()
    if (!disposed) {
      editor.value = useModelConfigPresets(config, {
        initiallyCustom: true,
        useDefaultWhenMissing: false,
        workspaceId: props.workspaceId,
      })
      appliedLlmConfig.value = JSON.parse(JSON.stringify(editor.value.llmConfig.value))
      modalConfig.value = loadedModalConfig
      initialModalConfig.value = loadedModalConfig
        ? JSON.parse(JSON.stringify(loadedModalConfig))
        : null
      portForwardRows.value = loadedPortForwards
        ? loadedPortForwards.map((forward) => newPortForwardRow(forward))
        : null
      appliedPortForwards.value = loadedPortForwards
        ? canonicalizePortForwards(loadedPortForwards)
        : ''
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

  const modelConfigChanged = !appliedLlmConfig.value
    || !llmConfigsEqual(submittedConfig, appliedLlmConfig.value)
  if (modelConfigChanged && !validateLlmConfig(submittedConfig)) {
    applyError.value = 'Select a provider for the default model and every enabled use case before applying.'
    return
  }

  let desiredPortForwards: PortForwardConfig[] | null = null
  if (portForwardRows.value) {
    const built = buildPortForwards(portForwardRows.value)
    if (built.error) {
      applyError.value = built.error
      return
    }
    if (canonicalizePortForwards(built.forwards) !== appliedPortForwards.value) {
      desiredPortForwards = built.forwards
    }
  }

  const modalConfigChanged = !!modalConfig.value
    && JSON.stringify(modalConfig.value) !== JSON.stringify(initialModalConfig.value)

  isApplying.value = true
  try {
    if (modelConfigChanged) {
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
    }

    // Each section that lands records what the environment now reflects, so
    // a retry after a later failure does not resubmit it, which would mean
    // recreating the sandbox again.
    if (desiredPortForwards) {
      const portForwardsResponse = await fetch(
        `/api/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/port_forwards`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ portForwards: desiredPortForwards }),
        },
      )
      if (!portForwardsResponse.ok) {
        throw new Error(await getErrorMessage(portForwardsResponse))
      }
      const body = await portForwardsResponse.json()
      appliedPortForwards.value = canonicalizePortForwards(body?.portForwards ?? desiredPortForwards)
    }

    if (modalConfigChanged) {
      const modalResponse = await fetch(
        `/api/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/modal_config`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ config: modalConfig.value }),
        },
      )
      if (!modalResponse.ok) {
        throw new Error(await getErrorMessage(modalResponse))
      }
      initialModalConfig.value = JSON.parse(JSON.stringify(modalConfig.value))
    }

    if (modelConfigChanged) {
      const applied = await pollUntilApplied(submittedConfig)
      if (!applied) {
        applyError.value = 'The workflow accepted the update but did not apply it in time. Retry to check or submit again.'
        return
      }
      appliedLlmConfig.value = submittedConfig
      editor.value.saveOrUpdatePreset()
    }

    emit('applied')
    emit('close')
  } catch (error) {
    applyError.value = `Unable to apply the configuration. ${error instanceof Error ? error.message : ''} Retry when the workflow is available.`.trim()
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

.model-editor {
  min-width: 0;
  padding: 0;
  border: 0;
  margin: 0;
}

.model-editor:disabled {
  opacity: 0.6;
}

.modal-environment {
  display: grid;
  gap: 0.75rem;
  margin-top: 1rem;
}

.modal-environment label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.modal-environment input[type='number'] {
  width: 8rem;
}

.port-forwards {
  display: grid;
  gap: 0.75rem;
  margin-top: 1rem;
}

.port-forwards p {
  margin: 0;
}

.port-forward-list {
  display: grid;
  gap: 0.5rem;
  margin: 0;
  padding: 0;
  list-style: none;
}

.port-forward {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.port-forward label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.port-forward input[type='number'] {
  width: 7rem;
}

.no-port-forwards {
  color: var(--color-text-2);
}

.add-port-forward {
  justify-self: start;
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