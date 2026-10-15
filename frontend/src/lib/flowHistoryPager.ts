import { computed, ref } from 'vue'

export function createFlowHistoryPager<T>(
  collection: 'actions' | 'subflows',
  responseKey: 'flowActions' | 'subflows',
  merge: (items: T[]) => void,
) {
  const loading = ref(false)
  const error = ref<string | null>(null)
  const cursor = ref<string | null>(null)
  const hasMore = computed(() => cursor.value !== '')
  let baseUrl: string | undefined
  let generation = 0
  let controller: AbortController | undefined

  function reset(workspaceId?: string, flowId?: string) {
    generation++
    controller?.abort()
    controller = undefined
    baseUrl = workspaceId && flowId
      ? `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/flows/${encodeURIComponent(flowId)}/${collection}`
      : undefined
    cursor.value = null
    loading.value = false
    error.value = null
  }

  async function load() {
    if (!baseUrl || loading.value || !hasMore.value) return
    const requestGeneration = generation
    const abort = new AbortController()
    controller = abort
    loading.value = true
    error.value = null
    const query = new URLSearchParams({ limit: '50' })
    if (cursor.value) query.set('before', cursor.value)
    try {
      const response = await fetch(`${baseUrl}?${query}`, { signal: abort.signal })
      if (!response.ok) throw new Error(`Failed to load ${collection} (${response.status})`)
      const data = await response.json()
      if (requestGeneration !== generation) return
      if (!Array.isArray(data[responseKey]) || typeof data.nextCursor !== 'string') {
        throw new Error(`Invalid ${collection} page`)
      }
      merge(data[responseKey] as T[])
      cursor.value = data.nextCursor
    } catch (err) {
      if (requestGeneration === generation && !abort.signal.aborted) {
        error.value = err instanceof Error ? err.message : `Failed to load ${collection}`
      }
    } finally {
      if (requestGeneration === generation) {
        loading.value = false
        controller = undefined
      }
    }
  }

  return { loading, error, hasMore, reset, load }
}