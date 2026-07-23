export const getWorkspacePreferenceKey = (key: string, workspaceId: string): string =>
  `${key}_${workspaceId}`

export const loadWorkspacePreference = (
  key: string,
  workspaceId?: string | null,
): string | null => {
  try {
    if (workspaceId) {
      const workspaceValue = localStorage.getItem(getWorkspacePreferenceKey(key, workspaceId))
      if (workspaceValue !== null) return workspaceValue
    }

    return localStorage.getItem(key)
  } catch {
    return null
  }
}

export const saveWorkspacePreference = (
  key: string,
  value: string,
  workspaceId?: string | null,
): void => {
  try {
    localStorage.setItem(key, value)
    if (workspaceId) {
      localStorage.setItem(getWorkspacePreferenceKey(key, workspaceId), value)
    }
  } catch {
    // Preferences remain usable for the current view when storage is unavailable.
  }
}