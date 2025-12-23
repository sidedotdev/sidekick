import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, shallowMount, flushPromises } from '@vue/test-utils'
import ChatCompletionFlowAction from './ChatCompletionFlowAction.vue'
import type { FlowAction, Llm2Message } from '../lib/models'

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
      ],
      model: 'gpt9',
      reasoningEffort: 'low',
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

    expect(wrapper.find('.model-reasoning-effort').text()).toBe('(low reasoning)')

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
      stopReason: 'done',
      reasoningEffort: 'medium',
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

  describe('llm2 format messages', () => {
    const llm2FlowAction: FlowAction = {
      id: 'id',
      flowId: 'testFlowId',
      workspaceId: 'testWorkspaceId',
      created: new Date(),
      updated: new Date(),
      actionType: 'actionType',
      actionStatus: 'complete',
      actionParams: {
        messages: {
          type: 'llm2' as const,
          refs: [
            { flowId: '', blockIds: ['block1'], role: 'user' },
            { flowId: '', blockIds: ['block2', 'block3'], role: 'assistant' },
          ]
        },
        model: 'gpt9',
      },
      actionResult: JSON.stringify({ content: 'result', stopReason: 'done' }),
      subflow: 'flow',
      isHumanAction: false,
    }

    const hydratedMessages: Llm2Message[] = [
      {
        role: 'user',
        content: [{ type: 'text', text: 'Hello from user' }]
      },
      {
        role: 'assistant',
        content: [
          { type: 'text', text: 'Hello from assistant' },
          { type: 'tool_use', toolUse: { id: 'tool1', name: 'myTool', arguments: '{"arg1":"value1"}' } }
        ]
      }
    ]

    let fetchMock: ReturnType<typeof vi.fn>

    beforeEach(() => {
      fetchMock = vi.fn()
      vi.stubGlobal('fetch', fetchMock)
    })

    afterEach(() => {
      vi.unstubAllGlobals()
    })

    it('shows loading state while hydrating llm2 messages', async () => {
      let resolveHydration: (value: Response) => void
      const hydrationPromise = new Promise<Response>((resolve) => {
        resolveHydration = resolve
      })
      fetchMock.mockReturnValue(hydrationPromise)

      const wrapper = shallowMount(ChatCompletionFlowAction, {
        props: { flowAction: llm2FlowAction, expand: true }
      })

      await wrapper.find('.show-params').trigger('click')

      expect(wrapper.find('.llm2-loading').exists()).toBe(true)
      expect(wrapper.find('.llm2-loading').text()).toBe('Loading message history...')

      resolveHydration!(new Response(JSON.stringify({ messages: hydratedMessages }), { status: 200 }))
      await flushPromises()

      expect(wrapper.find('.llm2-loading').exists()).toBe(false)
    })

    it('calls hydration endpoint with correct refs and renders hydrated messages', async () => {
      fetchMock.mockResolvedValue(new Response(JSON.stringify({ messages: hydratedMessages }), { status: 200 }))

      const wrapper = mount(ChatCompletionFlowAction, {
        props: { flowAction: llm2FlowAction, expand: true }
      })

      await wrapper.find('.show-params').trigger('click')
      await flushPromises()

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/workspaces/testWorkspaceId/flows/testFlowId/chat_history/hydrate',
        expect.objectContaining({
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refs: llm2FlowAction.actionParams.messages.refs })
        })
      )

      const messages = wrapper.findAll('.action-params .message')
      expect(messages.length).toBe(2)

      expect(messages[0].find('.message-role').text()).toBe('user:')
      expect(messages[0].find('.llm2-text-block pre').text()).toBe('Hello from user')

      expect(messages[1].find('.message-role').text()).toBe('assistant:')
      expect(messages[1].find('.llm2-tool-use-block').exists()).toBe(true)
      expect(messages[1].find('.message-function-call-name').text()).toBe('myTool')
    })

    it('renders tool_result blocks correctly', async () => {
      const messagesWithToolResult: Llm2Message[] = [
        {
          role: 'user',
          content: [{ type: 'tool_result', toolResult: { toolCallId: 'tool1', name: 'myTool', text: 'Tool output here' } }]
        }
      ]
      fetchMock.mockResolvedValue(new Response(JSON.stringify({ messages: messagesWithToolResult }), { status: 200 }))

      const wrapper = mount(ChatCompletionFlowAction, {
        props: { flowAction: llm2FlowAction, expand: true }
      })

      await wrapper.find('.show-params').trigger('click')
      await flushPromises()

      expect(wrapper.find('.llm2-tool-result-block').exists()).toBe(true)
      expect(wrapper.find('.tool-result-header').text()).toContain('Tool Result')
      expect(wrapper.find('.tool-result-text').text()).toBe('Tool output here')
    })

    it('shows error state when hydration fails', async () => {
      fetchMock.mockResolvedValue(new Response('Internal Server Error', { status: 500 }))

      const wrapper = shallowMount(ChatCompletionFlowAction, {
        props: { flowAction: llm2FlowAction, expand: true }
      })

      await wrapper.find('.show-params').trigger('click')
      await flushPromises()

      expect(wrapper.find('.llm2-error').exists()).toBe(true)
      expect(wrapper.find('.llm2-error').text()).toContain('Error loading message history')
    })

    it('shows error state when fetch rejects', async () => {
      fetchMock.mockRejectedValue(new Error('Network error'))

      const wrapper = shallowMount(ChatCompletionFlowAction, {
        props: { flowAction: llm2FlowAction, expand: true }
      })

      await wrapper.find('.show-params').trigger('click')
      await flushPromises()

      expect(wrapper.find('.llm2-error').exists()).toBe(true)
      expect(wrapper.find('.llm2-error').text()).toContain('Network error')
    })
  })
})