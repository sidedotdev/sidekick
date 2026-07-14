import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import FlowEditorLinks from '../FlowEditorLinks.vue'

const mountLinks = (props: Record<string, unknown> = {}) => mount(FlowEditorLinks, {
  props: {
    flowId: 'flow-1',
    ...props,
  },
  global: {
    stubs: {
      RouterLink: true,
    },
  },
})

describe('FlowEditorLinks', () => {
  it('renders the accessible model configuration action alongside worktree links', async () => {
    const wrapper = mountLinks({
      showModelConfiguration: true,
      worktrees: [{
        id: 'worktree-1',
        workingDirectory: '/repo/worktree',
        created: new Date(),
        workspaceId: 'workspace-1',
      }],
    })

    const button = wrapper.get('[aria-label="Model configuration"]')
    expect(button.attributes('title')).toBe('Model configuration')
    expect(wrapper.get('.toolbar').text()).toContain('Open Worktree')

    await button.trigger('click')
    expect(wrapper.emitted('model-configuration')).toHaveLength(1)
  })

  it('omits the action when the flow view does not enable it', () => {
    const wrapper = mountLinks()
    expect(wrapper.find('[aria-label="Model configuration"]').exists()).toBe(false)
  })

  it('retains the embedded toolbar offset', () => {
    const wrapper = mountLinks({
      subtask: true,
      showModelConfiguration: true,
    })

    expect(wrapper.get('.editor-links').classes()).toContain('editor-links-subtask')
    expect(wrapper.find('[aria-label="Model configuration"]').exists()).toBe(true)
  })
})