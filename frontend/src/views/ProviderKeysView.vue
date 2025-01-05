<template>
  <div class="provider-keys-view">
    <div class="header">
      <h1>API Keys</h1>
      <button @click="openAddKeyModal" class="add-btn">Add Key</button>
    </div>
    
    <div class="keys-list">
      <div v-for="key in providerKeys" :key="key.id" class="key-item">
        <div class="key-info">
          <div class="key-header">
            <span class="key-nickname">{{ key.nickname || 'Unnamed Key' }}</span>
            <span class="key-type">{{ key.providerType }}</span>
          </div>
          <div class="key-details">
            <span class="key-id">ID: {{ key.id }}</span>
            <span class="key-created">Created: {{ new Date(key.created).toLocaleDateString() }}</span>
          </div>
        </div>
        <div class="key-actions">
          <button @click="editKey(key)" class="edit-btn">Edit</button>
          <button @click="deleteKey(key.id)" class="delete-btn">Delete</button>
        </div>
      </div>
      <div v-if="providerKeys.length === 0" class="no-keys">
        No API keys found. Click "Add Key" to create one.
      </div>
    </div>
  </div>
  
  <ProviderKeyForm
    v-if="isModalOpen"
    :key-to-edit="selectedKey"
    @close="closeModal"
    @saved="refreshKeys"
  />
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import ProviderKeyForm from '../components/ProviderKeyForm.vue'
import type { ProviderKey } from '../lib/models'

const providerKeys = ref<ProviderKey[]>([])
const isModalOpen = ref(false)
const selectedKey = ref<ProviderKey | undefined>(undefined)

const openAddKeyModal = () => {
  selectedKey.value = undefined
  isModalOpen.value = true
}

const editKey = (key: ProviderKey) => {
  selectedKey.value = key
  isModalOpen.value = true
}

const closeModal = () => {
  isModalOpen.value = false
  selectedKey.value = undefined
}

const refreshKeys = async () => {
  try {
    const response = await fetch('/api/v1/provider-keys')
    if (response.ok) {
      providerKeys.value = await response.json()
    } else {
      console.error('Failed to fetch provider keys:', response.status)
    }
  } catch (error) {
    console.error('Failed to fetch provider keys:', error)
  }
}

const deleteKey = async (id: string) => {
  if (!confirm('Are you sure you want to delete this API key? This action cannot be undone.')) {
    return
  }

  try {
    const response = await fetch(`/api/v1/provider-keys/${id}`, {
      method: 'DELETE'
    })
    
    if (response.ok) {
      await refreshKeys()
    } else {
      console.error('Failed to delete provider key:', response.status)
    }
  } catch (error) {
    console.error('Failed to delete provider key:', error)
  }
}

onMounted(refreshKeys)
</script>

<style scoped>
.provider-keys-view {
  padding: 2rem;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 2rem;
}

.add-btn {
  background-color: var(--color-primary);
  color: var(--color-text-contrast);
  border: none;
  border-radius: 0.25rem;
  padding: 0.5rem 1rem;
  cursor: pointer;
}

.keys-list {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.key-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1rem;
  background-color: var(--color-background-hover);
  border-radius: 0.5rem;
}

.key-info {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.key-header {
  display: flex;
  align-items: center;
  gap: 1rem;
}

.key-nickname {
  font-weight: bold;
  font-size: 1.1rem;
}

.key-type {
  padding: 0.25rem 0.5rem;
  background-color: var(--color-primary);
  color: var(--color-text-contrast);
  border-radius: 0.25rem;
  font-size: 0.875rem;
}

.key-details {
  display: flex;
  gap: 1rem;
  color: var(--color-text-muted);
  font-size: 0.875rem;
}

.key-actions {
  display: flex;
  gap: 0.5rem;
}

.edit-btn, .delete-btn {
  padding: 0.25rem 0.75rem;
  border: none;
  border-radius: 0.25rem;
  cursor: pointer;
}

.edit-btn {
  background-color: var(--color-primary);
  color: var(--color-text-contrast);
}

.delete-btn {
  background-color: var(--color-danger);
  color: var(--color-text-contrast);
}

.no-keys {
  text-align: center;
  padding: 2rem;
  color: var(--color-text-muted);
}
</style>