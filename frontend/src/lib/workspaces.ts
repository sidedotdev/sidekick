import type { Workspace } from './models'

// fetchWorkspace loads a workspace, returning null when it is unavailable so
// callers can fall back to unscoped behavior.
export const fetchWorkspace = async (workspaceId: string): Promise<Workspace | null> => {
  try {
    const response = await fetch(`/api/v1/workspaces/${workspaceId}`)
    if (!response.ok) return null
    return (await response.json()).workspace ?? null
  } catch {
    return null
  }
}
