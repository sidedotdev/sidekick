import { beforeEach, describe, expect, it } from 'vitest'
import { config, mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import Dropdown from 'primevue/dropdown'
import ModelConfigPresetEditor from './ModelConfigPresetEditor.vue'
import { useModelConfigPresets } from '../composables/useModelConfigPresets'
import type { LLMConfig } from '../lib/models'

config.global.plugins.push(PrimeVue)

const initialConfig: LLMConfig = {
  defaults: [{ provider: 'anthropic', model: 'claude-3' }],
  useCaseConfigs: {},
}

describe('ModelConfigPresetEditor', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('places the preset overlay above a containing modal', () => {
    const editor = useModelConfigPresets(initialConfig, {
      initiallyCustom: true,
      useDefaultWhenMissing: false,
    })
    const wrapper = mount(ModelConfigPresetEditor, {
      props: {
        editor,
        overlayBaseZIndex: 1102,
      },
      global: {
        stubs: {
          LlmConfigEditor: true,
        },
      },
    })

    const dropdown = wrapper.findComponent(Dropdown)
    expect(dropdown.props('overlayClass')).toEqual({
      'model-config-editor-overlay': true,
    })
    expect(dropdown.props('overlayStyle')).toEqual({
      '--model-config-editor-overlay-z-index': 1102,
    })
  })
})