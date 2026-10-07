import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useModelConfigPresets } from '../useModelConfigPresets'
import {
  LAST_PRESET_SELECTION_STORAGE_KEY,
  invalidatePresetsCache,
  savePresets,
} from '../../lib/llmPresetStorage'
import type { LLMConfig } from '../../lib/models'

type Editor = ReturnType<typeof useModelConfigPresets>

const WORKSPACE_ID = 'workspace-1'

const startedConfig: LLMConfig = {
  defaults: [{ provider: 'anthropic', model: 'claude' }],
  useCaseConfigs: {},
}

const otherConfig: LLMConfig = {
  defaults: [{ provider: 'openai', model: 'gpt' }],
  useCaseConfigs: {},
}

const startTaskWith = (presetValue: string) => {
  const editor = useModelConfigPresets(undefined, { workspaceId: WORKSPACE_ID })
  editor.handlePresetChange(presetValue)
  expect(editor.saveOrUpdatePreset({ finalSave: true })).toBe(true)
}

const nextEditor = (): Editor => {
  invalidatePresetsCache()
  return useModelConfigPresets(undefined, { workspaceId: WORKSPACE_ID })
}

describe('remembered model configuration selection', () => {
  beforeEach(() => {
    localStorage.clear()
    invalidatePresetsCache()
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: false, json: () => Promise.resolve({}) })))
    savePresets([
      { id: 'started', name: 'Started models', config: startedConfig },
      { id: 'other', name: 'Other models', config: otherConfig },
    ])
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
    invalidatePresetsCache()
  })

  it.each([
    { name: 'switching to a custom configuration', abandon: (editor: Editor) => editor.handlePresetChange('add_preset') },
    { name: 'editing another preset', abandon: (editor: Editor) => editor.editPreset('other') },
    { name: 'choosing another preset', abandon: (editor: Editor) => editor.handlePresetChange('other') },
    { name: 'choosing the default models', abandon: (editor: Editor) => editor.handlePresetChange('default') },
  ])('restores the last started configuration after $name without starting a task', ({ abandon }) => {
    startTaskWith('started')

    abandon(nextEditor())

    const editor = nextEditor()
    expect(editor.selectedPresetValue.value).toBe('started')
    expect(editor.llmConfig.value).toEqual(startedConfig)
  })

  it('restores the default models when they were last used to start a task', () => {
    startTaskWith('started')
    startTaskWith('default')

    expect(nextEditor().selectedPresetValue.value).toBe('default')
  })

  it('does not restore a remembered custom selection, which has no models to restore', () => {
    localStorage.setItem(`${LAST_PRESET_SELECTION_STORAGE_KEY}_${WORKSPACE_ID}`, 'add_preset')

    expect(nextEditor().selectedPresetValue.value).toBe('default')
  })
})