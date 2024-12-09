import { describe, it, expect } from 'vitest'
import { mount, shallowMount } from '@vue/test-utils'
import ChatCompletionFlowAction from './ChatCompletionFlowAction.vue'
import type { FlowAction } from '../lib/models'

describe('ChatCompletionFlowAction', () => {
  const flowAction: FlowAction = {
    id: 'id',
    flowId: 'flowId',
    workspaceId: 'workspaceId',
    created: new Date(),
    updated: new Date(),
    actionType: 'actionType',
    actionStatus: 'complete',
    actionParams: {
      messages: [
        { role: 'role1', content: 'content1' },
        { role: 'role2', content: 'content2' }
      ]
    },
    actionResult: JSON.stringify({
      message: {
        content: 'content',
        function_call: {
          name: 'functionName',
          arguments: JSON.stringify({ arg1: 'arg1' })
        }
      },
      stopReason: 'done'
    }),
    subflow: 'flow',
    isHumanAction: false,
  }

  it('renders action parameters correctly when expand is true', async () => {
    const wrapper = shallowMount(ChatCompletionFlowAction, {
      propsData: { flowAction, expand: true }
    })
    await wrapper.find('.show-params').trigger('click') // show action params
    expect(wrapper.findAll('.action-params .message').length).toBe(2)
  })

  it('does not render action params and results when expand is false', () => {
    const wrapper = shallowMount(ChatCompletionFlowAction, {
      propsData: { flowAction, expand: false }
    })
    expect(wrapper.findAll('.action-params').length).toBe(0)
    expect(wrapper.findAll('.action-result').length).toBe(0)
  })

  it('renders action result correctly when actionResult is a valid JSON string', () => {
    const actionResult = JSON.stringify({
      content: 'content',
      toolCalls: [
        {
          name: 'functionName',
          arguments: JSON.stringify({ arg1: 'arg1' })
        },
      ],
      stopReason: 'done'
    })
    const flowActionWithActionResult = { ...flowAction, actionResult }
    const wrapper = mount(ChatCompletionFlowAction, {
      props: { flowAction: flowActionWithActionResult, expand: true }
    })
    expect(wrapper.find('.action-result .action-result-function-name').exists()).toBe(true);
    expect(wrapper.find('.action-result .action-result-function-name').text()).toBe('Tool Call: functionName');
    expect(wrapper.find('.action-result .action-result-function-args').exists()).toBe(true);
    expect(wrapper.find('.action-result .message-content').text()).toBe('content');
    expect(wrapper.find('.action-result .action-result-stop-reason').text()).toBe('Stop Reason: done');
  })

  it('displays an error message when actionResult is an invalid JSON string', () => {
    const invalidActionResult = "{ invalid json string }";
    const flowActionWithInvalidActionResult = { ...flowAction, actionResult: invalidActionResult }
    const wrapper = shallowMount(ChatCompletionFlowAction, {
      propsData: { flowAction: flowActionWithInvalidActionResult, expand: true }
    })
    expect(wrapper.find('.error-message').exists()).toBe(true);
    expect(wrapper.find('.error-message').text()).toBe("Error: Invalid JSON string in actionResult{ invalid json string }")
  })
})