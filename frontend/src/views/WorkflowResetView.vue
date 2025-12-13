<template>
  <div class="workflow-reset-view">
    <div v-if="!devMode" class="not-available">
      <p>Workflow reset is only available in development mode.</p>
    </div>
    <div v-else>
      <div class="header">
        <h1>Reset Workflow</h1>
        <router-link :to="{ name: 'flow', params: { id: flowId } }" class="back-link">
          ← Back to Flow
        </router-link>
      </div>

      <div class="filter-container">
        <input
          v-model="filterText"
          type="text"
          placeholder="Filter by activity or signal name..."
          class="filter-input"
        />
      </div>

      <div v-if="isLoading" class="loading">Loading workflow history...</div>
      <div v-else-if="error" class="error">{{ error }}</div>
      <div v-else-if="filteredEvents.length === 0" class="no-events">
        No events found{{ filterText ? ' matching filter' : '' }}.
      </div>
      <table v-else class="events-table">
        <thead>
          <tr>
            <th>Event ID</th>
            <th>Event Type</th>
            <th>Name</th>
            <th>Timestamp</th>
            <th>Action</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="event in filteredEvents" :key="event.eventId">
            <td>{{ event.eventId }}</td>
            <td>{{ event.eventType }}</td>
            <td>{{ event.name || '-' }}</td>
            <td>{{ formatTimestamp(event.timestamp) }}</td>
            <td>
              <button
                @click="resetToEvent(event.eventId)"
                :disabled="isResetting"
                class="reset-button"
              >
                Reset Here
              </button>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="resetMessage" :class="['reset-message', resetSuccess ? 'success' : 'error']">
        {{ resetMessage }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { store } from '../lib/store'

interface HistoryEvent {
  eventId: number
  eventType: string
  name: string
  timestamp: number
}

const devMode = import.meta.env.MODE === 'development'
const route = useRoute()
const router = useRouter()
const flowId = route.params.id as string

const events = ref<HistoryEvent[]>([])
const filterText = ref('')
const isLoading = ref(false)
const isResetting = ref(false)
const error = ref<string | null>(null)
const resetMessage = ref<string | null>(null)
const resetSuccess = ref(false)

const filteredEvents = computed(() => {
  if (!filterText.value) {
    return events.value.slice(0, 100)
  }
  const filter = filterText.value.toLowerCase()
  return events.value
    .filter(event => event.name && event.name.toLowerCase().includes(filter))
    .slice(0, 100)
})

const formatTimestamp = (timestamp: number): string => {
  return new Date(timestamp).toLocaleString()
}

const fetchHistory = async () => {
  isLoading.value = true
  error.value = null
  try {
    const response = await fetch(
      `/api/v1/workspaces/${store.workspaceId}/flows/${flowId}/history`
    )
    if (!response.ok) {
      throw new Error(`Failed to fetch history: ${response.statusText}`)
    }
    const data = await response.json()
    events.value = data.events || []
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to fetch workflow history'
  } finally {
    isLoading.value = false
  }
}

const resetToEvent = async (eventId: number) => {
  isResetting.value = true
  resetMessage.value = null
  try {
    const response = await fetch(
      `/api/v1/workspaces/${store.workspaceId}/flows/${flowId}/reset`,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ eventId }),
      }
    )
    if (!response.ok) {
      const data = await response.json()
      throw new Error(data.error || `Reset failed: ${response.statusText}`)
    }
    resetSuccess.value = true
    resetMessage.value = 'Workflow reset successfully! Redirecting...'
    setTimeout(() => {
      router.push({ name: 'flow', params: { id: flowId } })
    }, 1500)
  } catch (e) {
    resetSuccess.value = false
    resetMessage.value = e instanceof Error ? e.message : 'Failed to reset workflow'
  } finally {
    isResetting.value = false
  }
}

onMounted(() => {
  if (devMode) {
    fetchHistory()
  }
})
</script>

<style scoped>
.workflow-reset-view {
  padding: 1.5rem;
  max-width: 80rem;
  margin: 0 auto;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1.5rem;
}

.header h1 {
  margin: 0;
  font-size: 1.5rem;
}

.back-link {
  color: var(--color-text);
  text-decoration: none;
}

.back-link:hover {
  text-decoration: underline;
}

.filter-container {
  margin-bottom: 1rem;
}

.filter-input {
  width: 100%;
  max-width: 25rem;
  padding: 0.5rem;
  font-size: 1rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background: var(--color-background);
  color: var(--color-text);
}

.loading,
.error,
.no-events,
.not-available {
  padding: 1rem;
  text-align: center;
}

.error {
  color: var(--color-error, #dc3545);
}

.events-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.events-table th,
.events-table td {
  padding: 0.5rem;
  text-align: left;
  border-bottom: 1px solid var(--color-border);
}

.events-table th {
  font-weight: 600;
  background: var(--color-background-soft);
}

.events-table tr:hover {
  background: var(--color-background-soft);
}

.reset-button {
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  background: var(--color-background-soft);
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  cursor: pointer;
  color: var(--color-text);
}

.reset-button:hover:not(:disabled) {
  background: var(--color-background-mute);
}

.reset-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.reset-message {
  margin-top: 1rem;
  padding: 0.75rem;
  border-radius: 0.25rem;
  text-align: center;
}

.reset-message.success {
  background: var(--color-background-soft);
  color: var(--color-text);
}

.reset-message.error {
  background: var(--color-background-soft);
  color: var(--color-error, #dc3545);
}
</style>