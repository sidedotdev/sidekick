<template>
  <div class="provider-auth-request">
    <p v-if="content" class="provider-auth-content">{{ content }}</p>
    <p v-if="note" class="provider-auth-note">{{ note }}</p>

    <div class="provider-auth-methods">
      <button
        v-for="(info, index) in methods"
        :key="info.method"
        type="button"
        :class="['provider-auth-method', index === 0 ? 'cta-button-color' : 'secondary', { active: info.method === activeMethod }]"
        :disabled="busy || submitted"
        @click="selectMethod(info.method)"
      >
        {{ info.label }}
      </button>
    </div>

    <div v-if="oauthSession" class="provider-auth-oauth">
      <p>
        Finish signing in using the opened tab, or open
        <a :href="oauthSession.authUrl" target="_blank" rel="noopener noreferrer" class="provider-auth-link">the authorization page</a>.
      </p>
      <p class="provider-auth-waiting">Waiting for authorization…</p>
      <label :for="manualCodeInputId">If the redirect doesn't complete, paste the authorization code or callback URL:</label>
      <div class="provider-auth-input-row">
        <input
          :id="manualCodeInputId"
          v-model="manualCode"
          type="text"
          class="provider-auth-manual-code"
          autocomplete="off"
          spellcheck="false"
          @keydown.enter.prevent="submitManualCode"
        />
        <button
          type="button"
          class="secondary"
          :disabled="busy || !manualCode.trim()"
          @click="submitManualCode"
        >
          Submit code
        </button>
      </div>
    </div>

    <div v-else-if="showApiKeyInput" class="provider-auth-api-key">
      <label :for="apiKeyInputId">{{ providerLabel }} API key</label>
      <div class="provider-auth-input-row">
        <input
          :id="apiKeyInputId"
          ref="apiKeyInputRef"
          v-model="apiKey"
          type="password"
          class="provider-auth-api-key-input"
          autocomplete="off"
          @keydown.enter.prevent="saveApiKey"
        />
        <button
          type="button"
          class="cta-button-color"
          :disabled="busy || !apiKey.trim()"
          @click="saveApiKey"
        >
          Save
        </button>
      </div>
    </div>

    <div v-if="error" class="provider-auth-error" role="alert">
      <span>{{ error }}</span>
      <button v-if="retryAction" type="button" class="secondary provider-auth-retry" :disabled="busy" @click="retry">
        Retry
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref } from 'vue'
import type { FlowAction } from '../lib/models'
import { DEFAULT_PROFILE_ID } from '../lib/profiles'
import { capitalizeProvider } from '../lib/llmPresets'
import {
  getProviderOAuthStatus,
  isOAuthMethod,
  providerAPIKeyExists,
  saveProviderAPIKey,
  startProviderOAuth,
  submitProviderOAuthCode,
  type ProviderAuthMethod,
  type ProviderAuthMethodInfo,
  type ProviderOAuthSession,
  type ProviderOAuthStatus,
} from '../lib/providerAuth'

const OAUTH_POLL_INTERVAL_MS = 1000

const props = defineProps<{
  flowAction: FlowAction
  // Resolves once the server accepts the chosen method; rejects with a
  // user-facing message otherwise, so completion can be retried without
  // re-acquiring credentials.
  submitChoice: (method: ProviderAuthMethod) => Promise<void>
}>()

const content = computed<string>(() => props.flowAction.actionParams.requestContent ?? '')
const note = computed<string>(() => props.flowAction.actionParams.note ?? '')
const provider = computed<string>(() => props.flowAction.actionParams.provider ?? '')
const profileId = computed<string>(() => props.flowAction.actionParams.profileId || DEFAULT_PROFILE_ID)
const reason = computed<string>(() => props.flowAction.actionParams.reason ?? '')
const methods = computed<ProviderAuthMethodInfo[]>(() => props.flowAction.actionParams.methods ?? [])
const providerLabel = computed(() => capitalizeProvider(provider.value))

const manualCodeInputId = `provider-auth-code-${props.flowAction.id}`
const apiKeyInputId = `provider-auth-key-${props.flowAction.id}`

const activeMethod = ref<ProviderAuthMethod | null>(null)
const oauthSession = ref<ProviderOAuthSession | null>(null)
const showApiKeyInput = ref(false)
const manualCode = ref('')
const apiKey = ref('')
const busy = ref(false)
const error = ref('')
const retryAction = ref<(() => Promise<void>) | null>(null)
const apiKeyInputRef = ref<HTMLInputElement | null>(null)

const submitted = ref(false)

let pollTimer: ReturnType<typeof setTimeout> | null = null
let submitting = false
// Bumped whenever the user switches methods so results of abandoned attempts
// are ignored when they eventually settle.
let attempt = 0

const messageOf = (err: unknown): string => (err instanceof Error ? err.message : String(err))

const stopPolling = () => {
  if (pollTimer !== null) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
}

const fail = (message: string, retryWith: () => Promise<void>) => {
  error.value = message
  retryAction.value = retryWith
}

const clearError = () => {
  error.value = ''
  retryAction.value = null
}

const complete = async (method: ProviderAuthMethod) => {
  if (submitted.value || submitting) return
  submitting = true
  stopPolling()
  clearError()
  // Credentials are in place at this point, so only submission remains.
  oauthSession.value = null
  showApiKeyInput.value = false
  busy.value = true
  try {
    await props.submitChoice(method)
    submitted.value = true
  } catch (err) {
    fail(messageOf(err), () => complete(method))
  } finally {
    submitting = false
    busy.value = false
  }
}

const schedulePoll = (currentAttempt: number, method: ProviderAuthMethod) => {
  stopPolling()
  pollTimer = setTimeout(() => {
    pollTimer = null
    void pollOAuthStatus(currentAttempt, method)
  }, OAUTH_POLL_INTERVAL_MS)
}

const handleOAuthStatus = async (status: ProviderOAuthStatus, currentAttempt: number, method: ProviderAuthMethod) => {
  if (status.status === 'succeeded') {
    await complete(method)
  } else if (status.status === 'pending') {
    schedulePoll(currentAttempt, method)
  } else {
    stopPolling()
    oauthSession.value = null
    fail(status.error || 'Authorization failed', () => startOAuth(method))
  }
}

const pollOAuthStatus = async (currentAttempt: number, method: ProviderAuthMethod) => {
  const session = oauthSession.value
  if (!session || currentAttempt !== attempt || submitted.value) return
  let status: ProviderOAuthStatus
  try {
    status = await getProviderOAuthStatus(session.sessionId)
  } catch (err) {
    if (currentAttempt !== attempt) return
    fail(messageOf(err), async () => {
      clearError()
      await pollOAuthStatus(currentAttempt, method)
    })
    return
  }
  if (currentAttempt !== attempt) return
  await handleOAuthStatus(status, currentAttempt, method)
}

const startOAuth = async (method: ProviderAuthMethod) => {
  const currentAttempt = attempt
  clearError()
  busy.value = true
  try {
    const session = await startProviderOAuth(provider.value, profileId.value, method)
    if (currentAttempt !== attempt) return
    oauthSession.value = session
    window.open(session.authUrl, '_blank', 'noopener,noreferrer')
    schedulePoll(currentAttempt, method)
  } catch (err) {
    if (currentAttempt !== attempt) return
    fail(messageOf(err), () => startOAuth(method))
  } finally {
    if (currentAttempt === attempt) busy.value = false
  }
}

const submitManualCode = async () => {
  const session = oauthSession.value
  const method = activeMethod.value
  const input = manualCode.value.trim()
  if (!session || !method || !input || busy.value) return

  const currentAttempt = attempt
  clearError()
  busy.value = true
  stopPolling()
  let status: ProviderOAuthStatus
  try {
    status = await submitProviderOAuthCode(session.sessionId, input)
  } catch (err) {
    if (currentAttempt !== attempt) return
    busy.value = false
    fail(messageOf(err), submitManualCode)
    // The browser redirect may still complete the login.
    schedulePoll(currentAttempt, method)
    return
  }
  if (currentAttempt !== attempt) return
  busy.value = false
  manualCode.value = ''
  await handleOAuthStatus(status, currentAttempt, method)
}

const promptForApiKey = async () => {
  showApiKeyInput.value = true
  await nextTick()
  apiKeyInputRef.value?.focus()
}

// An existing key is reused unless it is the one the provider just rejected.
const useApiKey = async () => {
  if (reason.value === 'invalid_api_key') {
    await promptForApiKey()
    return
  }

  const currentAttempt = attempt
  clearError()
  busy.value = true
  let exists: boolean
  try {
    exists = await providerAPIKeyExists(provider.value, profileId.value)
  } catch (err) {
    if (currentAttempt !== attempt) return
    busy.value = false
    fail(messageOf(err), useApiKey)
    return
  }
  if (currentAttempt !== attempt) return
  busy.value = false
  if (exists) {
    await complete('api_key')
  } else {
    await promptForApiKey()
  }
}

const saveApiKey = async () => {
  const key = apiKey.value.trim()
  if (!key || busy.value) return

  const currentAttempt = attempt
  clearError()
  busy.value = true
  try {
    await saveProviderAPIKey(provider.value, profileId.value, key)
  } catch (err) {
    if (currentAttempt !== attempt) return
    busy.value = false
    fail(messageOf(err), saveApiKey)
    return
  }
  if (currentAttempt !== attempt) return
  busy.value = false
  await complete('api_key')
}

const selectMethod = async (method: ProviderAuthMethod) => {
  if (submitted.value || submitting) return
  attempt++
  stopPolling()
  clearError()
  busy.value = false
  activeMethod.value = method
  oauthSession.value = null
  showApiKeyInput.value = false
  manualCode.value = ''

  if (isOAuthMethod(method)) {
    await startOAuth(method)
  } else {
    await useApiKey()
  }
}

const retry = async () => {
  const action = retryAction.value
  if (!action) return
  clearError()
  await action()
}

onUnmounted(() => {
  attempt++
  stopPolling()
})
</script>

<style scoped>
.provider-auth-request {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.provider-auth-request p {
  margin: 0;
}

.provider-auth-note,
.provider-auth-waiting {
  font-size: 0.875rem;
  opacity: 0.75;
}

.provider-auth-methods,
.provider-auth-input-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
}

.provider-auth-oauth,
.provider-auth-api-key {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.provider-auth-link {
  color: var(--color-link);
  word-break: break-all;
}

button {
  padding: 0.5rem 1rem;
  border: none;
  border-radius: 0.25rem;
  cursor: pointer;
}

button.secondary {
  background-color: var(--color-background-mute);
  color: var(--color-text);
  border: 0.0625rem solid var(--color-border-contrast);
}

button.active {
  outline: 0.125rem solid var(--color-primary);
  outline-offset: 0.125rem;
}

button:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

input {
  flex: 1;
  min-width: 12rem;
  padding: 0.5rem;
  border: 0.0625rem solid var(--color-border-contrast);
  border-radius: 0.25rem;
  background-color: var(--color-background-soft);
  color: var(--color-text);
  font-family: inherit;
}

.provider-auth-error {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
  padding: 0.5rem 0.75rem;
  border: 0.0625rem solid var(--color-error-border);
  border-radius: 0.25rem;
  background-color: var(--color-error-background);
  color: var(--color-error-text);
}
</style>