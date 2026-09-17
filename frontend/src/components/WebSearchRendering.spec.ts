import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ChatCompletionFlowAction from './ChatCompletionFlowAction.vue'
import JsonTree from './JsonTree.vue'
import type { FlowAction } from '../lib/models'

function builtinBlock(argumentsValue: string, name = 'web_search') {
  return {
    id: 'ws_test',
    type: 'builtin_tool_use',
    builtinToolUse: {
      id: 'ws_test',
      name,
      arguments: argumentsValue,
      status: 'completed'
    }
  }
}

async function renderBlock(block: object, history: boolean) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
    messages: [{ role: 'assistant', content: [block] }]
  }), { status: 200 })))

  const flowAction: FlowAction = {
    id: 'action',
    flowId: 'flow',
    workspaceId: 'workspace',
    created: new Date(),
    updated: new Date(),
    subflow: 'flow',
    actionType: 'chat_completion',
    actionStatus: 'complete',
    isHumanAction: false,
    actionParams: history
      ? { messages: { type: 'llm2', refs: [{ role: 'assistant', blockKeys: ['block'] }] } }
      : {},
    actionResult: JSON.stringify({
      output: { role: 'assistant', content: history ? [] : [block] },
      stopReason: 'stop'
    })
  }
  const wrapper = mount(ChatCompletionFlowAction, {
    props: { flowAction, expand: true }
  })
  if (history) {
    await wrapper.get('.show-params').trigger('click')
    await flushPromises()
  }
  return wrapper
}

afterEach(() => vi.unstubAllGlobals())

describe.each([false, true])('web search rendering (history: %s)', (history) => {
  it.each([
    { type: 'search', query: 'golang' },
    { type: 'search', queries: ['golang', 'vue'] },
    { type: 'search', query: 'golang', queries: ['golang', 'vue'] }
  ])('renders search queries for $type', async (action) => {
    const wrapper = await renderBlock(builtinBlock(JSON.stringify(action)), history)
    const search = wrapper.get('.web-search-block')
    expect(search.text()).toContain('Web search')
    expect(search.text()).toContain('completed')
    expect(search.findAll('li').map(item => item.text())).toEqual(action.queries ?? [action.query])
    expect(search.findComponent(JsonTree).exists()).toBe(false)
  })

  it.each(['open_page', 'find_in_page', 'find'])('renders %s with a safe page link', async (type) => {
    const url = 'https://example.com/sdk/source.go'
    const wrapper = await renderBlock(builtinBlock(JSON.stringify({
      type, url, pattern: 'Reload'
    })), history)
    const search = wrapper.get('.web-search-block')
    expect(search.text()).toContain(type === 'open_page' ? 'Open page' : 'Find in page')
    if (type !== 'open_page') expect(search.text()).toContain('Reload')
    const link = search.get('a')
    expect(link.attributes('href')).toBe(url)
    expect(link.attributes('rel')).toContain('noopener')
    expect(link.attributes('rel')).toContain('noreferrer')
  })

  it.each([
    '{invalid',
    'null',
    '[]',
    '"search"',
    '{}',
    '{"query":"other provider format"}',
    '{"type":"new_action","query":"test"}',
    '{"type":"search","query":42}',
    '{"type":"search","queries":["valid",42]}',
    '{"type":"search","queries":[]}',
    '{"type":"open_page"}',
    '{"type":"open_page","url":42}',
    '{"type":"find_in_page","url":"https://example.com"}',
    '{"type":"find_in_page","url":"https://example.com","pattern":42}',
    '{"type":"open_page","url":"javascript:alert(1)"}',
    '{"type":"open_page","url":"data:text/html,test"}'
  ])('preserves malformed or unsupported arguments: %s', async (args) => {
    const block = builtinBlock(args)
    const wrapper = await renderBlock(block, history)
    expect(wrapper.find('.web-search-block').exists()).toBe(false)
    expect(wrapper.findAllComponents(JsonTree).some(tree =>
      JSON.stringify(tree.props('data')) === JSON.stringify(block)
    )).toBe(true)
    expect(wrapper.find('a[href]').exists()).toBe(false)
  })

  it('preserves unknown builtin tools', async () => {
    const block = builtinBlock('{"type":"search","query":"test"}', 'other_tool')
    const wrapper = await renderBlock(block, history)
    expect(wrapper.find('.web-search-block').exists()).toBe(false)
    expect(wrapper.findAllComponents(JsonTree).some(tree =>
      JSON.stringify(tree.props('data')) === JSON.stringify(block)
    )).toBe(true)
  })
})