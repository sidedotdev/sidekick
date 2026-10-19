import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import FlowActionItem from '../FlowActionItem.vue'
import type { FlowAction } from '../../lib/models'

const action = (actionStatus: FlowAction['actionStatus']): FlowAction => ({
  id: 'switch-1',
  flowId: 'flow-1',
  workspaceId: 'workspace-1',
  created: new Date(),
  updated: new Date(),
  subflow: '',
  actionType: 'env_switch',
  actionStatus,
  actionParams: { from: 'local', to: 'devpod' },
  actionResult: actionStatus === 'failed' ? 'failed: target HEAD did not match' : '',
  isHumanAction: false,
})

describe('environment switch flow action', () => {
  it.each(['started', 'complete', 'failed'] as const)('renders the %s status and switch endpoints', (status) => {
    const wrapper = mount(FlowActionItem, { props: { flowAction: action(status) } })

    expect(wrapper.get('h3').text()).toContain('Switch environment: local → devpod')
    expect(wrapper.get('.env-switch-status').text().toLowerCase()).toBe(status)
    if (status === 'failed') {
      expect(wrapper.get('[role="alert"]').text()).toBe('failed: target HEAD did not match')
    } else {
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    }
    wrapper.unmount()
  })

  it('updates an in-progress action when the outcome arrives', async () => {
    const wrapper = mount(FlowActionItem, { props: { flowAction: action('started') } })
    await wrapper.setProps({ flowAction: action('failed') })

    expect(wrapper.get('.env-switch-status').text().toLowerCase()).toBe('failed')
    expect(wrapper.get('[role="alert"]').text()).toContain('target HEAD did not match')
    wrapper.unmount()
  })
})