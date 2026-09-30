import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CheckCriteriaFulfillmentFlowAction from './CheckCriteriaFulfillmentFlowAction.vue'
import FlowActionItem from './FlowActionItem.vue'
import type { FlowAction } from '@/lib/models'

function action(result?: unknown): FlowAction {
  return {
    id: 'criteria-1',
    flowId: 'flow-1',
    workspaceId: 'workspace-1',
    created: new Date(),
    updated: new Date(),
    subflow: 'Review',
    actionType: 'check_criteria_fulfillment',
    actionParams: {},
    actionStatus: result === undefined ? 'started' : 'complete',
    actionResult: result === undefined ? '' : JSON.stringify({
      output: {
        content: [{
          type: 'tool_use',
          toolUse: { arguments: JSON.stringify(result) },
        }],
      },
    }),
    isHumanAction: false,
  }
}

describe('criteria fulfillment streaming completion', () => {
  it.each([
    {},
    { analysis: null },
    { analysis: { text: 'Unexpected object' } },
    { analysis: ['Unexpected array'] },
    { analysis: 42 },
  ])('survives invalid criteria data and renders the next update: %j', async (result) => {
    const errors: unknown[] = []
    const wrapper = mount(CheckCriteriaFulfillmentFlowAction, {
      props: { flowAction: action(), expand: true },
      global: {
        stubs: { ChatCompletionFlowAction: true, UnifiedDiffViewer: true },
        config: {
          errorHandler: (error) => { errors.push(error) },
        },
      },
    })

    try {
      await wrapper.setProps({ flowAction: action(result) })
      expect(errors).toEqual([])
      expect(wrapper.text()).toContain('Unable to parse criteria fulfillment data')

      await wrapper.setProps({
        flowAction: action({
          whatWasActuallyDone: 'Implemented the requested change',
          analysis: '**Verified** the result',
          isFulfilled: true,
          confidence: 5,
        }),
      })
      expect(errors).toEqual([])
      expect(wrapper.get('.analysis strong').text()).toBe('Analysis:')
      expect(wrapper.get('.analysis p strong').text()).toBe('Verified')
      expect(wrapper.text()).not.toContain('Unable to parse criteria fulfillment data')
    } finally {
      wrapper.unmount()
    }
  })
})

describe('criteria tool response selection', () => {
  it.each([false, true])('summarizes expansion requests without a verdict error (expanded: %s)', (expanded) => {
    const flowAction = action()
    flowAction.actionStatus = 'complete'
    flowAction.actionResult = JSON.stringify({
      output: {
        content: [{
          type: 'tool_use',
          toolUse: {
            name: 'expand_tool_call',
            arguments: '{"ids":["E","D","1A"]}',
          },
        }],
      },
    })
    const wrapper = mount(FlowActionItem, {
      props: { flowAction, defaultExpanded: expanded },
      global: { stubs: { ChatCompletionFlowAction: true, UnifiedDiffViewer: true } },
    })
    try {
      expect(wrapper.get('.action-summary').text()).toContain('View 3 tool calls')
      expect(wrapper.text()).not.toContain('Unable to parse criteria fulfillment data')
      expect(wrapper.find('.analysis').exists()).toBe(false)
      expect(wrapper.findComponent({ name: 'ChatCompletionFlowAction' }).exists()).toBe(expanded)
    } finally {
      wrapper.unmount()
    }
  })

  it('renders a verdict without confidence after an expansion call', async () => {
    const errors: unknown[] = []
    const wrapper = mount(CheckCriteriaFulfillmentFlowAction, {
      props: { flowAction: action(), expand: true },
      global: {
        stubs: { ChatCompletionFlowAction: true, UnifiedDiffViewer: true },
        config: { errorHandler: (error) => { errors.push(error) } },
      },
    })
    const tool = (name: string, args: unknown) => ({
      type: 'tool_use',
      toolUse: { name, arguments: JSON.stringify(args) },
    })
    const expansion = tool('expand_tool_call', { ids: ['E', 'D', '1A'] })
    const verdict = tool('determine_criteria_fulfillment', {
      analysis: 'The requirements are fulfilled.',
      feedbackMessage: null,
      isFulfilled: true,
      whatWasActuallyDone: 'Updated the implementation and verified the tests.',
    })
    try {
      await wrapper.setProps({
        flowAction: {
          ...action({}),
          actionResult: JSON.stringify({ output: { content: [expansion] } }),
        },
      })
      expect(errors).toEqual([])
      expect(wrapper.find('.analysis').exists()).toBe(false)

      await wrapper.setProps({
        flowAction: {
          ...action({}),
          actionResult: JSON.stringify({ output: { content: [expansion, verdict] } }),
        },
      })
      expect(errors).toEqual([])
      expect(wrapper.get('.analysis').text()).toContain('The requirements are fulfilled.')
      expect(wrapper.find('.confidence').exists()).toBe(false)
    } finally {
      wrapper.unmount()
    }
  })
})