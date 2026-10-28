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

      <details v-if="diagnostic?.failure" :key="diagnostic.eventId" class="workflow-diagnostic">
        <summary>
          {{ diagnostic.eventType.startsWith('WorkflowExecution') ? 'Workflow failed' : 'Workflow task failed — progress may be blocked' }}
          — {{ diagnostic.failure.cause }}
        </summary>
        <p>Event {{ diagnostic.eventId }} · {{ formatTimestamp(diagnostic.timestamp) }}</p>
        <p class="failure-message">{{ diagnostic.failure.message || 'No error message available.' }}</p>
        <pre v-if="diagnostic.failure.stackTrace" class="failure-stack" tabindex="0" aria-label="Workflow failure stack trace">{{ diagnostic.failure.stackTrace }}</pre>
        <p v-else>No stack trace available.</p>
      </details>

      <div class="filter-container">
        <input
          v-model="filterText"
          type="text"
          aria-label="Search workflow history"
          placeholder="Search by name, event type, or ID..."
          class="filter-input"
        />
      </div>

      <div v-if="!isLoading && !error && filteredEvents.length === 0" class="no-events">
        No events found{{ filterText ? ' matching filter' : '' }}.
      </div>
      <table v-if="filteredEvents.length" class="events-table">
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
          <template v-for="group in filteredEvents" :key="group.eventId">
            <tr>
              <td>
                {{ group.firstEventId }}
                <span v-if="group.lastEventId !== group.firstEventId">–{{ group.lastEventId }}</span>
                <span v-if="group.partial" title="Earlier lifecycle events have not been loaded yet"> (partial)</span>
              </td>
              <td>{{ group.eventType }}</td>
              <td>{{ group.name || '-' }}</td>
              <td>{{ formatTimestamp(group.timestamp) }}</td>
              <td class="reset-buttons">
                <button @click="toggleDetails(group)" class="reset-button" :aria-expanded="expandedEventIds.has(group.eventId)">
                  {{ expandedEventIds.has(group.eventId) ? 'Hide Details' : 'Details' }}
                </button>
                <button
                  @click="resetToEvent(group.resetBeforeEventId!)"
                  :disabled="isResetting || group.resetBeforeEventId === null"
                  class="reset-button"
                >
                  Reset Before
                </button>
                <button
                  @click="resetToEvent(group.resetAfterEventId!)"
                  :disabled="isResetting || group.resetAfterEventId === null"
                  class="reset-button"
                >
                  Reset After
                </button>
              </td>
            </tr>
            <tr v-if="expandedEventIds.has(group.eventId)" class="details-row">
              <td colspan="5">
                <section v-for="event in group.events" :key="event.eventId" class="lifecycle-event">
                  <h3>{{ event.eventId }} · {{ event.eventType }}</h3>
                  <div v-if="detailLoading[event.eventId]" class="details-loading">Loading event details...</div>
                  <div v-else-if="detailError[event.eventId]" class="details-error">
                    {{ detailError[event.eventId] }}
                    <button @click="loadDetails(event.eventId)" class="reset-button">Retry details</button>
                  </div>
                  <div v-else-if="eventDetails[event.eventId]" class="details-content">
                    <section v-if="eventDetails[event.eventId].failure" class="details-section failure-details">
                      <h3>Failure</h3>
                      <dl>
                        <dt>Error cause</dt>
                        <dd><code>{{ eventDetails[event.eventId].failure?.cause }}</code></dd>
                        <dt>Error message</dt>
                        <dd class="failure-message">{{ eventDetails[event.eventId].failure?.message || 'No error message available.' }}</dd>
                        <dt>Stack trace</dt>
                        <dd>
                          <pre v-if="eventDetails[event.eventId].failure?.stackTrace" class="failure-stack" tabindex="0" aria-label="Stack trace">{{ eventDetails[event.eventId].failure?.stackTrace }}</pre>
                          <span v-else class="details-empty">No stack trace available.</span>
                        </dd>
                      </dl>
                    </section>
                    <div class="details-section">
                      <h3>Input</h3>
                      <div v-if="eventDetails[event.eventId].input.length === 0" class="details-empty">No input payloads.</div>
                      <JsonTree
                        v-for="(payload, index) in eventDetails[event.eventId].input"
                        :key="`input-${index}`"
                        :data="payload"
                        class="details-tree"
                      />
                    </div>
                    <div class="details-section">
                      <h3>Output</h3>
                      <div v-if="eventDetails[event.eventId].output.length === 0" class="details-empty">No output payloads.</div>
                      <JsonTree
                        v-for="(payload, index) in eventDetails[event.eventId].output"
                        :key="`output-${index}`"
                        :data="payload"
                        class="details-tree"
                      />
                    </div>
                    <details v-if="eventDetails[event.eventId].rawEvent" class="details-section">
                      <summary>Full event metadata</summary>
                      <JsonTree :data="eventDetails[event.eventId].rawEvent!" />
                    </details>
                  </div>
                </section>
              </td>
            </tr>
          </template>
        </tbody>
      </table>

      <div ref="loadMoreTarget" class="history-pagination">
        <div v-if="isLoading" role="status" class="loading">
          {{ filterText ? 'Searching workflow history...' : 'Loading workflow history...' }}
        </div>
        <div v-if="error" role="alert" class="error">
          {{ error }}
          <button @click="fetchHistory" class="reset-button">Retry</button>
        </div>
        <button v-else-if="hasMore" @click="loadMore" :disabled="isLoading" class="reset-button">
          Load more
        </button>
      </div>

      <div v-if="resetMessage" :class="['reset-message', resetSuccess ? 'success' : 'error']">
        {{ resetMessage }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { store } from '../lib/store'
import JsonTree from '../components/JsonTree.vue'
import type { JSONDataType } from 'vue-json-pretty/types/utils'
import { groupWorkflowHistory, type HistoryEvent, type HistoryGroup } from '../lib/workflowResetHistory'


interface EventDetail {
  eventId: number
  eventType: string
  input: JSONDataType[]
  output: JSONDataType[]
  failure?: HistoryEvent['failure']
  rawEvent?: JSONDataType
}

interface EventDetailResponse {
  event: Omit<EventDetail, 'input' | 'output'> & {
    input: JSONDataType[] | null
    output: JSONDataType[] | null
  }
}

const devMode = import.meta.env.MODE === 'development'
const route = useRoute()
const router = useRouter()
const flowId = computed(() => route.params.id as string)

const events = ref<HistoryEvent[]>([])
const filterText = ref('')
const isLoading = ref(false)
const isResetting = ref(false)
const error = ref<string | null>(null)
const resetMessage = ref<string | null>(null)
const resetSuccess = ref(false)
const expandedEventIds = ref(new Set<number>())
const nextCursor = ref<string | null>(null)
const runId = ref('')
const visibleCount = ref(100)
const loadMoreTarget = ref<HTMLElement | null>(null)
let observer: IntersectionObserver | undefined
let controller = new AbortController()
let generation = 0
let redirectTimer: ReturnType<typeof setTimeout> | undefined
const eventDetails = ref<Record<number, EventDetail>>({})
const detailLoading = ref<Record<number, boolean>>({})
const detailError = ref<Record<number, string | null>>({})

const groupedEvents = computed(() => groupWorkflowHistory(events.value))
const matchingEvents = computed(() => {
  const filter = filterText.value.trim().toLowerCase()
  if (!filter) return groupedEvents.value
  return groupedEvents.value.filter(group =>
    group.events.some(event =>
      `${event.name || ''} ${event.eventType} ${event.eventId}`.toLowerCase().includes(filter),
    ),
  )
})
const filteredEvents = computed(() => matchingEvents.value.slice(0, visibleCount.value))
const hasMore = computed(() => nextCursor.value !== '' || matchingEvents.value.length > visibleCount.value)
const diagnostic = computed(() => {
  for (const event of events.value) {
    if (event.eventType.startsWith('WorkflowExecution') &&
        ['Completed', 'Canceled', 'Terminated', 'ContinuedAsNew'].some(type => event.eventType.endsWith(type))) {
      return null
    }
    if (event.eventType === 'WorkflowTaskCompleted') return null
    if (['WorkflowExecutionFailed', 'WorkflowExecutionTimedOut', 'WorkflowTaskFailed', 'WorkflowTaskTimedOut'].includes(event.eventType)) {
      return event
    }
  }
  return null
})

const formatTimestamp = (timestamp: number): string => {
  return new Date(timestamp).toLocaleString()
}

const fetchHistory = async () => {
  const workspaceId = store.workspaceId
  if (!workspaceId || !flowId.value || isLoading.value || nextCursor.value === '') return
  const requestGeneration = generation
  isLoading.value = true
  error.value = null
  try {
    while (nextCursor.value !== '' && matchingEvents.value.length < visibleCount.value) {
      const query = nextCursor.value ? `?${new URLSearchParams({ cursor: nextCursor.value })}` : ''
      const response = await fetch(
        `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/flows/${encodeURIComponent(flowId.value)}/history${query}`,
        { signal: controller.signal },
      )
      if (!response.ok) {
        throw new Error(`Failed to fetch history: ${response.statusText}`)
      }
      const data = await response.json()
      if (requestGeneration !== generation) return
      if (!Array.isArray(data.events)) throw new Error('Invalid history page')
      const cursor = data.nextCursor || ''
      if (cursor && cursor === nextCursor.value) throw new Error('History cursor did not advance')
      if (runId.value && data.runId !== runId.value) throw new Error('Workflow run changed while loading history')
      runId.value = data.runId || ''
      const known = new Set(events.value.map(event => event.eventId))
      events.value.push(...data.events.filter((event: HistoryEvent) => !known.has(event.eventId)))
      nextCursor.value = cursor
    }
  } catch (e) {
    if (requestGeneration === generation) {
      error.value = e instanceof Error ? e.message : 'Failed to fetch workflow history'
    }
  } finally {
    if (requestGeneration === generation) isLoading.value = false
  }
}

const loadMore = () => {
  if (isLoading.value || !hasMore.value) return
  visibleCount.value += 100
  void fetchHistory()
}

const loadDetails = async (eventId: number) => {
  const workspaceId = store.workspaceId
  if (!workspaceId || eventDetails.value[eventId] || detailLoading.value[eventId]) return
  const requestGeneration = generation
  detailLoading.value[eventId] = true
  detailError.value[eventId] = null
  try {
    const query = runId.value ? `?${new URLSearchParams({ runId: runId.value })}` : ''
    const response = await fetch(
      `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/flows/${encodeURIComponent(flowId.value)}/history/${eventId}${query}`,
      { signal: controller.signal },
    )
    if (!response.ok) {
      const data = await response.json().catch(() => ({}))
      throw new Error(data.error || `Failed to fetch event details: ${response.statusText}`)
    }
    const data = await response.json() as EventDetailResponse
    if (requestGeneration !== generation) return
    const event = data.event
    eventDetails.value[eventId] = {
      ...event,
      input: Array.isArray(event.input) ? event.input : [],
      output: Array.isArray(event.output) ? event.output : [],
    }
  } catch (e) {
    if (requestGeneration === generation) {
      detailError.value[eventId] = e instanceof Error ? e.message : 'Failed to fetch event details'
    }
  } finally {
    if (requestGeneration === generation) detailLoading.value[eventId] = false
  }
}

const toggleDetails = (group: HistoryGroup) => {
  if (expandedEventIds.value.has(group.eventId)) {
    expandedEventIds.value.delete(group.eventId)
    return
  }
  expandedEventIds.value.add(group.eventId)
  for (const event of group.events) void loadDetails(event.eventId)
}

const resetToEvent = async (eventId: number) => {
  const workspaceId = store.workspaceId
  if (!workspaceId || isResetting.value) return
  const requestGeneration = generation
  const targetFlowId = flowId.value
  isResetting.value = true
  resetMessage.value = null
  try {
    const response = await fetch(
      `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/flows/${encodeURIComponent(targetFlowId)}/reset`,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ eventId }),
      }
    )
    if (requestGeneration !== generation) return
    if (!response.ok) {
      const data = await response.json()
      throw new Error(data.error || `Reset failed: ${response.statusText}`)
    }
    resetSuccess.value = true
    resetMessage.value = 'Workflow reset successfully! Redirecting...'
    redirectTimer = setTimeout(() => {
      router.push({ name: 'flow', params: { id: targetFlowId } })
    }, 1500)
  } catch (e) {
    if (requestGeneration === generation) {
      resetSuccess.value = false
      resetMessage.value = e instanceof Error ? e.message : 'Failed to reset workflow'
    }
  } finally {
    if (requestGeneration === generation) isResetting.value = false
  }
}

watch(filterText, () => {
  visibleCount.value = 100
  if (devMode) void fetchHistory()
})

watch(groupedEvents, groups => {
  for (const group of groups) {
    if (!expandedEventIds.value.has(group.eventId)) continue
    for (const event of group.events) {
      if (!detailError.value[event.eventId]) void loadDetails(event.eventId)
    }
  }
})

watch(() => [store.workspaceId, flowId.value], () => {
  generation++
  controller.abort()
  controller = new AbortController()
  clearTimeout(redirectTimer)
  events.value = []
  nextCursor.value = null
  runId.value = ''
  visibleCount.value = 100
  isLoading.value = false
  isResetting.value = false
  resetMessage.value = null
  expandedEventIds.value.clear()
  eventDetails.value = {}
  detailError.value = {}
  detailLoading.value = {}
  if (devMode) void fetchHistory()
}, { immediate: true })

onMounted(() => {
  if (!devMode || !loadMoreTarget.value || typeof IntersectionObserver === 'undefined') return
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting) && !error.value) loadMore()
  })
  observer.observe(loadMoreTarget.value)
})

onBeforeUnmount(() => {
  generation++
  controller.abort()
  observer?.disconnect()
  clearTimeout(redirectTimer)
})
</script>

<style scoped>
.workflow-diagnostic {
  margin-bottom: 1rem;
  padding: 1rem;
  border: 0.0625rem solid var(--color-border);
  border-radius: 0.25rem;
  background: var(--color-background-soft);
}

.workflow-diagnostic summary {
  cursor: pointer;
  font-weight: 600;
}

.history-pagination {
  min-height: 3rem;
  padding: 1rem;
  text-align: center;
}

.lifecycle-event + .lifecycle-event {
  border-top: 0.0625rem solid var(--color-border);
  margin-top: 1rem;
}

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

.reset-buttons {
  display: flex;
  gap: 0.5rem;
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

.details-row td {
  background: var(--color-background-soft);
}

.details-content {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding: 0.5rem 0;
}

.details-section h3 {
  margin: 0 0 0.5rem 0;
  font-size: 0.9375rem;
}

.details-tree {
  margin-bottom: 0.5rem;
}

.failure-details {
  min-width: 0;
  padding: 1rem;
  border: 0.0625rem solid var(--color-error-border);
  border-radius: 0.375rem;
  background: var(--color-error-background);
  overflow-wrap: anywhere;
}

.failure-details h3 {
  color: var(--color-error-text);
}

.failure-details dl {
  margin: 0;
}

.failure-details dt {
  margin-top: 0.75rem;
  font-weight: 600;
}

.failure-details dd {
  margin: 0.25rem 0 0;
}

.failure-message {
  white-space: pre-wrap;
}

.failure-stack {
  max-height: 24rem;
  margin: 0;
  padding: 0.75rem;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: var(--color-background);
  color: var(--color-text);
  border-radius: 0.25rem;
  font-size: 0.8125rem;
  line-height: 1.5;
}

.details-loading,
.details-error,
.details-empty {
  padding: 0.5rem 0;
}

.details-error {
  color: var(--color-error, #dc3545);
}

.details-empty {
  opacity: 0.7;
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