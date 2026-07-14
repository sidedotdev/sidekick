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

    const firstEditor = useModelConfigPresets(undefined)
    firstEditor.handlePresetChange('zebra')

    const restoredEditor = useModelConfigPresets(undefined)

    expect(restoredEditor.selectedPresetValue.value).toBe('zebra')
    expect(restoredEditor.llmConfig.value.defaults[0]).toMatchObject({
      provider: 'anthropic',
      model: 'claude',
    })
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