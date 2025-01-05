<template>
  <div class="overlay" @click="safeClose"></div>
  <div class="modal" @click.stop>
    <h2>{{ isEditMode ? 'Edit API Key' : 'Add API Key' }}</h2>
    <form @submit.prevent="submitKey">
      <div>
        <label>Provider</label>
        <select v-model="providerType" required>
          <option value="">Select Provider</option>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Anthropic</option>
        </select>
      </div>
      
      <div>
        <label>Nickname (Optional)</label>
        <input v-model="nickname" placeholder="e.g., Production Key">
      </div>
      
      <div>
        <label>API Key</label>
        <input
          type="password"
          v-model="apiKey"
          :placeholder="isEditMode ? '(unchanged)' : 'Enter API key'"
          :required="!isEditMode"
        >
      </div>

      <div class="button-container">
        <button class="cancel" type="button" @click="close">Cancel</button>
        <button type="submit">{{ isEditMode ? 'Update Key' : 'Add Key' }}</button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import type { ProviderKey } from '../lib/models'

const props = defineProps<{
  keyToEdit?: ProviderKey
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved'): void
}>()

const isEditMode = computed(() => !!props.keyToEdit)

const providerType = ref(props.keyToEdit?.providerType || '')
const nickname = ref(props.keyToEdit?.nickname || '')
const apiKey = ref('')

const submitKey = async () => {
  const formData = {
    providerType: providerType.value,
    nickname: nickname.value || null,
    keyValue: apiKey.value || undefined
  }

  try {
    const url = isEditMode.value
      ? `/api/v1/provider_keys/${props.keyToEdit!.id}`
      : '/api/v1/provider_keys'
    
    const method = isEditMode.value ? 'PUT' : 'POST'

    const response = await fetch(url, {
      method,
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(formData)
    })

    if (response.ok) {
      emit('saved')
      close()
    } else {
      console.error(`Failed to ${isEditMode.value ? 'update' : 'create'} provider key:`, response.status)
    }
  } catch (error) {
    console.error(`Failed to ${isEditMode.value ? 'update' : 'create'} provider key:`, error)
  }
}

const safeClose = () => {
  const hasChanges = isEditMode.value
    ? providerType.value !== props.keyToEdit!.providerType ||
      nickname.value !== props.keyToEdit!.nickname ||
      apiKey.value !== ''
    : providerType.value !== '' || nickname.value !== '' || apiKey.value !== ''

  if (hasChanges) {
    if (!window.confirm('Are you sure you want to close this modal? Your changes will be lost.')) {
      return
    }
  }
  close()
}

const close = () => {
  emit('close')
}
</script>

<style scoped>
.overlay {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  left: 0;
  background: rgba(0, 0, 0, 0.7);
  z-index: 100000;
}

.modal {
  font-family: sans-serif;
  border: 1px solid rgba(255, 255, 255, 0.02);
  border-radius: 5px;
  justify-content: center;
  background-color: var(--color-modal-background);
  color: var(--color-modal-text);
  z-index: 100000 !important;
  padding: 30px;
  width: 30rem;
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  overflow: auto;
  max-height: 100%;
  transition: background-color 0.5s, color 0.5s;
}

h2 {
  margin-top: 0;
  margin-bottom: 1.5rem;
}

form {
  width: 100%;
}

form > div {
  width: 100%;
  margin-top: 1rem;
}

.button-container {
  display: flex;
  justify-content: flex-end;
  gap: 1rem;
  margin-top: 2rem;
}

button {
  padding: 0.5rem 1rem;
  border: none;
  border-radius: 0.25rem;
  cursor: pointer;
  transition: background-color 0.3s;
}

button[type="submit"] {
  background-color: var(--color-primary);
  color: var(--color-text-contrast);
}

button[type="button"] {
  background-color: var(--color-background-hover);
  color: var(--color-text);
}

label {
  display: block;
  margin-bottom: 0.5rem;
}

input, select {
  width: 100%;
  padding: 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background-color: var(--color-background);
  color: var(--color-text);
}

input:focus, select:focus {
  outline: none;
  border-color: var(--color-primary);
}
</style>