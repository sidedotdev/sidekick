import type { InjectionKey, Ref } from 'vue'

export const pairingEntryWorkspaceKey: InjectionKey<Ref<string | null>> = Symbol('pairingEntryWorkspace')

interface Activity {
  created?: string
  updated?: string
}

const activityTime = (activity: Activity): number =>
  Math.max(Date.parse(activity.created ?? '') || 0, Date.parse(activity.updated ?? '') || 0)

export async function resolvePairingWorkspace(activeWorkspaceId: string | null): Promise<string | undefined> {
  if (activeWorkspaceId) return activeWorkspaceId

  try {
    const response = await fetch('/api/v1/workspaces')
    if (!response.ok) return undefined
    const data = await response.json()
    const workspaces: (Activity & { id: string })[] = data.workspaces ?? []
    const candidates = await Promise.all(workspaces.map(async workspace => {
      let lastActivity = activityTime(workspace)
      try {
        const response = await fetch(`/api/v1/workspaces/${encodeURIComponent(workspace.id)}/tasks/`)
        if (response.ok) {
          const data = await response.json()
          for (const task of (data.tasks ?? []) as Activity[]) {
            lastActivity = Math.max(lastActivity, activityTime(task))
          }
        }
      } catch {
        // Workspace metadata remains usable when task history is unavailable.
      }
      return { id: workspace.id, lastActivity }
    }))
    candidates.sort((a, b) => b.lastActivity - a.lastActivity || a.id.localeCompare(b.id))
    return candidates[0]?.id
  } catch {
    // An optional workspace hint must not prevent device pairing.
    return undefined
  }
}