import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Llm2ContentBlocks from './Llm2ContentBlocks.vue'
import JsonTree from './JsonTree.vue'
import ImagePreview from './ImagePreview.vue'
import BuiltinToolBlock from './BuiltinToolBlock.vue'
import type { Llm2ContentBlock } from '../lib/models'

function toolUse(args: string, id = 'call_1'): Llm2ContentBlock {
  return { type: 'tool_use', toolUse: { id, name: 'search', arguments: args } }
}

describe('Llm2ContentBlocks', () => {
  it('renders text as markdown', () => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: { blocks: [{ type: 'text', text: 'Hello **world**' }] }
    })
    expect(wrapper.find('.llm2-text-block .message-content').text()).toBe('Hello world')
    expect(wrapper.find('.llm2-text-block strong').exists()).toBe(true)
  })

  it('renders tool calls with the name and parsed arguments', () => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: { blocks: [toolUse('{"query":"hello","limit":2}')] }
    })
    expect(wrapper.find('.llm2-tool-use-block .action-result-function-name').text()).toBe('Tool Call: search')
    const tree = wrapper.find('.llm2-tool-use-block').findComponent(JsonTree)
    expect(tree.classes()).toContain('action-result-function-args')
    expect(tree.props('data')).toEqual({ query: 'hello', limit: 2 })
  })

  it.each([
    'definitely not json',
    '{"query":"hello","paths":["a"',
    '{"query":"hello"} trailing garbage'
  ])('falls back to a raw wrapper for unparseable arguments %j', (args) => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: { blocks: [toolUse(args)] }
    })
    const tree = wrapper.find('.llm2-tool-use-block').findComponent(JsonTree)
    expect(tree.props('data')).toEqual({ raw: args })
  })

  it('prefers pre-parsed tool arguments for their block index', async () => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: {
        blocks: [toolUse('{"query":"hel'), toolUse('{"other":true}', 'call_2')],
        toolArguments: { 0: { query: 'hel' } }
      }
    })
    const trees = () => wrapper.findAll('.llm2-tool-use-block').map((block) => block.findComponent(JsonTree).props('data'))
    expect(trees()).toEqual([{ query: 'hel' }, { other: true }])

    await wrapper.setProps({
      blocks: [toolUse('{"query":"hello"'), toolUse('{"other":true}', 'call_2')],
      toolArguments: { 0: { query: 'hello' } }
    })
    expect(trees()).toEqual([{ query: 'hello' }, { other: true }])
  })

  it('renders reasoning text with its summary', () => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: { blocks: [{ type: 'reasoning', reasoning: { text: 'Thinking hard', summary: 'Short version' } }] }
    })
    expect(wrapper.find('.llm2-text-block .reasoning').text()).toBe('Thinking hard')
    expect(wrapper.find('.reasoning-summary').text()).toBe('Summary: Short version')
    expect(wrapper.find('.reasoning-redacted').exists()).toBe(false)
  })

  it('renders a redacted placeholder for reasoning without text', () => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: { blocks: [{ type: 'reasoning', reasoning: { summary: 'Only a summary' } }] }
    })
    expect(wrapper.find('.reasoning-redacted').text()).toContain('Reasoning (content not available)')
    expect(wrapper.find('.reasoning-summary').text()).toContain('Only a summary')
  })

  it('renders images, builtin tool blocks and unknown blocks', () => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: {
        blocks: [
          { type: 'image', image: { url: 'data:image/png;base64,abc' } },
          {
            type: 'builtin_tool_use',
            builtinToolUse: { id: 'b1', name: 'web_search', arguments: '{"type":"search","query":"vue testing"}' }
          },
          { type: 'refusal', refusal: { reason: 'nope' } }
        ] as Llm2ContentBlock[]
      }
    })
    expect(wrapper.find('.llm2-image-block').findComponent(ImagePreview).props('src')).toBe('data:image/png;base64,abc')
    expect(wrapper.findComponent(BuiltinToolBlock).find('.web-search-queries').text()).toBe('vue testing')
    expect(wrapper.find('.llm2-unknown-block').findComponent(JsonTree).props('data')).toEqual({ type: 'refusal', refusal: { reason: 'nope' } })
  })

  it('skips empty text blocks', () => {
    const wrapper = mount(Llm2ContentBlocks, {
      props: { blocks: [{ type: 'text', text: '' }] }
    })
    expect(wrapper.find('.llm2-text-block').exists()).toBe(false)
    expect(wrapper.find('.llm2-unknown-block').exists()).toBe(false)
  })
})