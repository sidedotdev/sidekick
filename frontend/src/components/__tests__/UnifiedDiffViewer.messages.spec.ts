import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import UnifiedDiffViewer from '../UnifiedDiffViewer.vue'

describe('UnifiedDiffViewer messages', () => {
  it.each([
    'No changes since the last review.',
    'Failed to generate diff since last review: incompatible patch context'
  ])('renders non-patch content outside file hunks: %s', (message) => {
    const wrapper = mount(UnifiedDiffViewer, {
      props: { diffString: message }
    })

    expect(wrapper.text()).toContain(message)
    expect(wrapper.text()).not.toContain('Unknown file')
    expect(wrapper.findComponent({ name: 'DiffFile' }).exists()).toBe(false)
  })
})