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

function builtinResultBlock(result: Record<string, unknown>) {
  return {
    id: '',
    type: 'builtin_tool_result',
    builtinToolResult: {
      toolCallId: 'srvtoolu_test',
      name: 'web_search',
      ...result
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

  it.each([
    { provider: 'anthropic', id: 'srvtoolu_test', args: { query: 'golang' }, queries: ['golang'] },
    { provider: 'google', id: 'google_ws_1', args: { queries: ['golang', 'vue'] }, queries: ['golang', 'vue'] }
  ])('renders untyped $provider search queries', async ({ id, args, queries }) => {
    const block = {
      id: '',
      type: 'builtin_tool_use',
      builtinToolUse: { id, name: 'web_search', arguments: JSON.stringify(args) }
    }
    const wrapper = await renderBlock(block, history)
    const search = wrapper.get('.web-search-block')
    expect(search.text()).toContain('Web search')
    expect(search.findAll('li').map(item => item.text())).toEqual(queries)
    expect(search.findComponent(JsonTree).exists()).toBe(false)
  })

  it('renders search results with safe links and without encrypted content', async () => {
    const wrapper = await renderBlock(builtinResultBlock({
      searchResults: [
        { url: 'https://example.com/a', title: 'Example A', pageAge: '2 days ago', encryptedContent: 'SECRET' },
        { url: 'https://github.com/org/repo/pull/1', title: 'github.com', encryptedContent: 'SECRET' }
      ]
    }), history)
    const results = wrapper.get('.web-search-results')
    expect(results.text()).toContain('Web search results')
    expect(results.text()).toContain('2 days ago')
    expect(results.text()).not.toContain('SECRET')
    const links = results.findAll('a')
    expect(links.map(link => link.attributes('href'))).toEqual([
      'https://example.com/a', 'https://github.com/org/repo/pull/1'
    ])
    expect(links.map(link => link.text())).toEqual(['Example A', 'github.com'])
    for (const link of links) {
      expect(link.attributes('rel')).toContain('noopener')
      expect(link.attributes('rel')).toContain('noreferrer')
    }
    expect(results.findComponent(JsonTree).exists()).toBe(false)
  })

  it('renders search result errors', async () => {
    const wrapper = await renderBlock(builtinResultBlock({ isError: true, content: 'max_uses_exceeded' }), history)
    const results = wrapper.get('.web-search-results')
    expect(results.text()).toContain('error')
    expect(results.text()).toContain('max_uses_exceeded')
    expect(results.find('a').exists()).toBe(false)
  })

  it('renders empty search results', async () => {
    const wrapper = await renderBlock(builtinResultBlock({}), history)
    expect(wrapper.get('.web-search-results').text()).toContain('No results')
  })

  it.each([
    { searchResults: 'nope' },
    { searchResults: [null] },
    { searchResults: [{ url: 'javascript:alert(1)', title: 'bad' }] },
    { searchResults: [{ url: 'https://example.com', title: 42 }] },
    { searchResults: [{ url: 'https://example.com', title: 'ok', pageAge: 42 }] },
    { content: 42 },
    { name: 'other_tool' }
  ])('preserves malformed or unsupported results: %j', async (result) => {
    const block = builtinResultBlock(result)
    const wrapper = await renderBlock(block, history)
    expect(wrapper.find('.web-search-results').exists()).toBe(false)
    expect(wrapper.findAllComponents(JsonTree).some(tree =>
      JSON.stringify(tree.props('data')) === JSON.stringify(block)
    )).toBe(true)
    expect(wrapper.find('a[href]').exists()).toBe(false)
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
    '{"query":42}',
    '{"queries":"golang"}',
    '{"url":"https://example.com"}',
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