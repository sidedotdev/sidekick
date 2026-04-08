import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import FlowActionItem from '../FlowActionItem.vue'
import type { FlowAction } from '../../lib/models'

describe('FlowActionItem', () => {
  let flowAction: FlowAction

  beforeEach(() => {
    // not available in jsdom
    window.HTMLElement.prototype.scrollIntoView = vi.fn()

    flowAction = {
      workspaceId: 'ws_1',
      flowId: '1',
      id: '1',
      actionType: 'something_something',
      actionStatus: 'pending',
      actionParams: { requestContent: 'Test request', requestKind: 'free_form' },
      actionResult: '',
      subflow: 'testSubflow',
      created: new Date(),
      updated: new Date(),
      isHumanAction: false,
    }
  })

  it('renders without errors', () => {
    const wrapper = mount(FlowActionItem, {
      props: { flowAction },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('displays the correct action type and status', () => {
    flowAction.actionStatus = 'started'
    const wrapper = mount(FlowActionItem, {
      props: { flowAction },
    })
    expect(wrapper.text()).toContain('Something Something')
    expect(wrapper.text()).toContain('...')
  })

  /*
  it('displays the request content and a textarea for user input when the action is a pending user request', () => {
    const wrapper = mount(FlowActionItem, {
      props: { flowAction },
    })
    expect(wrapper.text()).toContain(flowAction.actionParams.requestContent)
    expect(wrapper.find('textarea').exists()).toBe(true)
  })
    */

  it('displays the action parameters and result when details are shown and the action is not a pending user request', async () => {
    const nonUserRequestAction: FlowAction = {
      ...flowAction,
      actionType: 'non_user_request',
      actionParams: { param: 'Test param' },
      actionResult: JSON.stringify({ Response: 'Test result' }),
      actionStatus: 'complete',
      isHumanAction: false,
    }
    const wrapper = mount(FlowActionItem, {
      props: { flowAction: nonUserRequestAction },
    })
    await wrapper.find('a').trigger('click')
    expect(wrapper.text()).toContain('{param: "Test param"}')
    expect(wrapper.text()).toContain('Test result')
  })

  it('maintains manually expanded state and updates rendered content when flowAction prop changes', async () => {
    flowAction.actionType = 'generic_action'
    flowAction.actionStatus = 'started'
    flowAction.actionResult = ''
    flowAction.actionParams = { step: 'processing' }
    const wrapper = mount(FlowActionItem, {
      props: { flowAction, defaultExpanded: false },
    })

    // Initially not expanded — no detail content rendered
    expect(wrapper.classes()).not.toContain('expanded-action')
    expect(wrapper.text()).toContain('...')

    // Manually expand
    await wrapper.find('a').trigger('click')
    await nextTick()
    expect(wrapper.classes()).toContain('expanded-action')
    // Expanded generic action shows params
    expect(wrapper.text()).toContain('step')
    expect(wrapper.text()).toContain('processing')

    // Update flowAction prop to simulate real-time completion
    const updatedFlowAction: FlowAction = {
      ...flowAction,
      actionStatus: 'complete',
      actionResult: JSON.stringify({ Response: 'task completed successfully' }),
      updated: new Date(),
    }
    await wrapper.setProps({ flowAction: updatedFlowAction })
    await nextTick()

    // Should still be expanded
    expect(wrapper.classes()).toContain('expanded-action')
    // Rendered content should reflect the update
    expect(wrapper.text()).not.toContain('...')
    expect(wrapper.text()).toContain('task completed successfully')
  })

  it('maintains manually collapsed state and updates header when flowAction prop changes', async () => {
    flowAction.actionType = 'generic_action'
    flowAction.actionStatus = 'started'
    const wrapper = mount(FlowActionItem, {
      props: { flowAction, defaultExpanded: true },
    })

    // Initially expanded, header shows "started" indicator
    expect(wrapper.classes()).toContain('expanded-action')
    expect(wrapper.text()).toContain('...')

    // Manually collapse
    await wrapper.find('a').trigger('click')
    await nextTick()
    expect(wrapper.classes()).not.toContain('expanded-action')

    // Update flowAction prop
    const updatedFlowAction: FlowAction = {
      ...flowAction,
      actionStatus: 'complete',
      actionResult: JSON.stringify({ Response: 'result' }),
      updated: new Date(),
    }
    await wrapper.setProps({ flowAction: updatedFlowAction })
    await nextTick()

    // Should still be collapsed
    expect(wrapper.classes()).not.toContain('expanded-action')
    // Header should reflect updated status (no more "..." indicator)
    expect(wrapper.text()).not.toContain('...')
  })

  it('makes a POST request with the correct arguments when the submit button is clicked', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) })
    global.fetch = mockFetch

    flowAction.actionType = 'user_request'
    const wrapper = mount(FlowActionItem, {
      props: { flowAction },
    })

    const responseContent = 'Test input'
    const textarea = wrapper.find('textarea')

    await textarea.setValue(responseContent)
    await wrapper.find('form').trigger('submit.prevent')

    expect(mockFetch).toHaveBeenCalledWith(`/api/v1/workspaces/${flowAction.workspaceId}/flow_actions/${flowAction.id}/complete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ userResponse: {content: responseContent} }),
    })
  })
})
