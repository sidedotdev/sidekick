import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  useModelConfigPresets,
  validateLlmConfig,
} from './useModelConfigPresets'
import {
  invalidatePresetsCache,
  savePresets,
} from '../lib/llmPresetStorage'

describe('model configuration presets', () => {
  beforeEach(() => {
    localStorage.clear()
    invalidatePresetsCache()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('hides presets whose providers belong to another profile', async () => {
    savePresets([
      {
        id: 'work',
        name: 'Work models',
        config: {
          defaults: [{ provider: 'work_openai', model: 'gpt' }],
          useCaseConfigs: {},
        },
      },
      {
        id: 'personal',
        name: 'Personal models',
        config: {
          defaults: [{ provider: 'work_openai', model: 'gpt' }],
          useCaseConfigs: {
            planning: [{ provider: 'personal_anthropic', model: 'claude' }],
          },
        },
      },
    ])

    const jsonResponse = (body: unknown) => Promise.resolve({
      ok: true,
      json: () => Promise.resolve(body),
    })
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url === '/api/v1/workspaces/workspace-work') {
        return jsonResponse({ workspace: { id: 'workspace-work', profileId: 'work' } })
      }
      if (url === '/api/v1/workspaces/workspace-default') {
        return jsonResponse({ workspace: { id: 'workspace-default' } })
      }
      if (url === '/api/v1/providers?profileId=work') {
        return jsonResponse({ providers: ['work_openai'] })
      }
      return jsonResponse({ providers: ['work_openai', 'personal_anthropic'] })
    }))

    const workEditor = useModelConfigPresets(undefined, { workspaceId: 'workspace-work' })
    const defaultEditor = useModelConfigPresets(undefined, { workspaceId: 'workspace-default' })

    await vi.waitFor(() => {
      expect(workEditor.presetOptions.value.map((option) => option.label)).toEqual([
        'Default',
        'Work models',
        'Custom',
      ])
      expect(defaultEditor.presetOptions.value.map((option) => option.label)).toEqual([
        'Default',
        'Personal models',
        'Work models',
        'Custom',
      ])
    })
  })

  it('drops a restored selection whose providers are outside the workspace profile', async () => {
    savePresets([
      {
        id: 'personal',
        name: 'Personal models',
        config: {
          defaults: [{ provider: 'personal_anthropic', model: 'claude' }],
          useCaseConfigs: {},
        },
      },
      {
        id: 'work',
        name: 'Work models',
        config: {
          defaults: [{ provider: 'work_openai', model: 'gpt' }],
          useCaseConfigs: {},
        },
      },
    ])
    localStorage.setItem('sidekick_last_model_preset_selection_workspace-work', 'personal')

    vi.stubGlobal('fetch', vi.fn((url: string) => {
      const body = url === '/api/v1/workspaces/workspace-work'
        ? { workspace: { id: 'workspace-work', profileId: 'work' } }
        : { providers: ['work_openai'] }
      return Promise.resolve({ ok: true, json: () => Promise.resolve(body) })
    }))

    const editor = useModelConfigPresets(undefined, { workspaceId: 'workspace-work' })
    expect(editor.selectedPresetValue.value).toBe('personal')

    await vi.waitFor(() => {
      expect(editor.selectedPresetValue.value).toBe('default')
    })
    expect(editor.presetOptions.value.map((option) => option.label)).toEqual([
      'Default',
      'Work models',
      'Custom',
    ])
  })

  it('validates defaults and every configured use case', () => {
    expect(validateLlmConfig({
      defaults: [{ provider: 'anthropic', model: 'claude' }],
      useCaseConfigs: {
        planning: [{ provider: 'openai', model: 'gpt' }],
      },
    })).toBe(true)

    expect(validateLlmConfig({
      defaults: [{ provider: 'anthropic', model: 'claude' }],
      useCaseConfigs: {
        planning: [{ provider: '', model: 'gpt' }],
      },
    })).toBe(false)
  })

  it('restores the last selected preset and orders presets alphabetically', () => {
    savePresets([
      {
        id: 'zebra',
        name: 'Zebra models',
        config: {
          defaults: [{ provider: 'anthropic', model: 'claude' }],
          useCaseConfigs: {},
        },
      },
      {
        id: 'alpha',
        name: 'Alpha models',
        config: {
          defaults: [{ provider: 'openai', model: 'gpt' }],
          useCaseConfigs: {},
        },
      },
    ])

    const firstEditor = useModelConfigPresets(undefined, { workspaceId: 'workspace-1' })
    firstEditor.handlePresetChange('zebra')

    expect(localStorage.getItem('sidekick_last_model_preset_selection')).toBe('zebra')
    expect(localStorage.getItem('sidekick_last_model_preset_selection_workspace-1')).toBe('zebra')

    localStorage.setItem('sidekick_last_model_preset_selection', 'alpha')
    const restoredEditor = useModelConfigPresets(undefined, { workspaceId: 'workspace-1' })
    const fallbackEditor = useModelConfigPresets(undefined, { workspaceId: 'workspace-2' })

    expect(restoredEditor.selectedPresetValue.value).toBe('zebra')
    expect(restoredEditor.llmConfig.value.defaults[0]).toMatchObject({
      provider: 'anthropic',
      model: 'claude',
    })
    expect(fallbackEditor.selectedPresetValue.value).toBe('alpha')
    expect(restoredEditor.presetOptions.value.map((option) => option.label)).toEqual([
      'Default',
      'Alpha models',
      'Zebra models',
      'Custom',
    ])
  })

  it('creates and updates a preset without duplicating it', () => {
    vi.stubGlobal('crypto', { randomUUID: () => 'preset-1' })
    const editor = useModelConfigPresets(undefined)
    editor.handlePresetChange('add_preset')
    editor.newPresetName.value = 'Flow models'
    editor.llmConfig.value = {
      defaults: [{ provider: 'anthropic', model: 'claude' }],
      useCaseConfigs: {},
    }

    expect(editor.saveOrUpdatePreset({ finalSave: true })).toBe(true)
    editor.newPresetName.value = 'Updated flow models'
    expect(editor.saveOrUpdatePreset()).toBe(true)

    const saved = JSON.parse(localStorage.getItem('sidekick_model_presets') || '[]')
    expect(saved).toHaveLength(1)
    expect(saved[0]).toMatchObject({
      id: 'preset-1',
      name: 'Updated flow models',
    })
  })
})