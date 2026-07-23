import { beforeEach, describe, expect, it } from 'vitest'
import {
  getWorkspacePreferenceKey,
  loadWorkspacePreference,
  saveWorkspacePreference,
} from './workspacePreferenceStorage'

describe('workspace preference storage', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('prefers a workspace-scoped value and falls back to the global value', () => {
    localStorage.setItem('preference', 'global')
    localStorage.setItem(getWorkspacePreferenceKey('preference', 'workspace-1'), 'scoped')

    expect(loadWorkspacePreference('preference', 'workspace-1')).toBe('scoped')
    expect(loadWorkspacePreference('preference', 'workspace-2')).toBe('global')
    expect(loadWorkspacePreference('preference')).toBe('global')
  })

  it('saves both global and workspace-scoped values', () => {
    saveWorkspacePreference('preference', 'selected', 'workspace-1')

    expect(localStorage.getItem('preference')).toBe('selected')
    expect(localStorage.getItem('preference_workspace-1')).toBe('selected')
  })
})