import { describe, expect, it } from 'vitest'
import { Llm2StreamAccumulator, accumulateLlm2StreamEvents } from './llm2StreamAccumulator'
import type { Llm2ContentBlock, Llm2StreamEvent } from './models'

type Payload = Pick<Llm2StreamEvent, 'type' | 'index' | 'contentBlock' | 'delta'>

const started = (index: number, contentBlock: Llm2ContentBlock): Payload => ({ type: 'block_started', index, contentBlock })
const textDelta = (index: number, delta: string): Payload => ({ type: 'text_delta', index, delta })
const summaryDelta = (index: number, delta: string): Payload => ({ type: 'summary_text_delta', index, delta })
const done = (index: number, contentBlock?: Llm2ContentBlock): Payload => ({ type: 'block_done', index, contentBlock })

describe('accumulateLlm2StreamEvents', () => {
  it('returns no blocks for no events', () => {
    expect(accumulateLlm2StreamEvents([])).toEqual([])
  })

  it('appends text deltas to a text block', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(0, { type: 'text', text: '' }),
      textDelta(0, 'Hello'),
      textDelta(0, ', world'),
      done(0),
    ])
    expect(blocks).toEqual([{ type: 'text', text: 'Hello, world' }])
  })

  // Go's ContentBlock.Text has omitempty, so an empty text block arrives on
  // the wire without a text property at all.
  it.each([
    ['text_delta', textDelta],
    ['summary_text_delta', summaryDelta],
  ])('appends a first %s to a text block whose start omitted the empty text field', (_name, firstDelta) => {
    const wireStart = JSON.parse('{"type":"block_started","index":0,"contentBlock":{"id":"b0","type":"text"}}')
    const blocks = accumulateLlm2StreamEvents([wireStart, firstDelta(0, 'Hi'), textDelta(0, '!')])
    expect(blocks).toEqual([{ id: 'b0', type: 'text', text: 'Hi!' }])
  })

  it('appends text deltas to tool_use arguments', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(0, { type: 'tool_use', toolUse: { id: 'call_1', name: 'read_file', arguments: '' } }),
      textDelta(0, '{"path":'),
      textDelta(0, ' "a.go"}'),
    ])
    expect(blocks).toEqual([
      { type: 'tool_use', toolUse: { id: 'call_1', name: 'read_file', arguments: '{"path": "a.go"}' } },
    ])
  })

  it('routes text and summary deltas to reasoning text and summary respectively', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(0, { type: 'reasoning', reasoning: { text: '', summary: '' } }),
      textDelta(0, 'Thinking '),
      summaryDelta(0, 'Short '),
      textDelta(0, 'hard'),
      summaryDelta(0, 'summary'),
    ])
    expect(blocks).toEqual([
      { type: 'reasoning', reasoning: { text: 'Thinking hard', summary: 'Short summary' } },
    ])
  })

  it('appends summary deltas to a text block like the Go accumulator', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(0, { type: 'text', text: 'a' }),
      summaryDelta(0, 'b'),
    ])
    expect(blocks).toEqual([{ type: 'text', text: 'ab' }])
  })

  it('overrides reasoning fields from a block_done payload only when non-empty', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(0, { type: 'reasoning', reasoning: { text: '', summary: '' } }),
      textDelta(0, 'streamed text'),
      summaryDelta(0, 'streamed summary'),
      done(0, { type: 'reasoning', reasoning: { text: '', summary: 'final summary', encryptedContent: 'enc' } }),
    ])
    expect(blocks).toEqual([
      { type: 'reasoning', reasoning: { text: 'streamed text', summary: 'final summary', encryptedContent: 'enc' } },
    ])
  })

  it('replaces builtin_tool_use payload on block_done', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(0, { type: 'builtin_tool_use', builtinToolUse: { id: 'ws_1', name: 'web_search', arguments: '', status: 'in_progress' } }),
      done(0, { type: 'builtin_tool_use', builtinToolUse: { id: 'ws_1', name: 'web_search', arguments: '{"query":"go"}', status: 'completed' } }),
    ])
    expect(blocks).toEqual([
      { type: 'builtin_tool_use', builtinToolUse: { id: 'ws_1', name: 'web_search', arguments: '{"query":"go"}', status: 'completed' } },
    ])
  })

  it('preserves image, tool_result, refusal and builtin_tool_result start payloads in order, ignoring deltas and block_done for them', () => {
    const image: Llm2ContentBlock = { type: 'image', image: { url: 'https://example.com/a.png' } }
    const toolResult: Llm2ContentBlock = {
      type: 'tool_result',
      toolResult: { toolCallId: 'call_1', name: 'read_file', isError: true, content: [{ type: 'text', text: 'not found' }] },
    }
    const refusal: Llm2ContentBlock = { type: 'refusal', refusal: { type: 'safety', reason: 'nope' } }
    const builtinResult: Llm2ContentBlock = {
      type: 'builtin_tool_result',
      builtinToolResult: {
        toolCallId: 'ws_1',
        name: 'web_search',
        content: 'ok',
        searchResults: [{ url: 'https://go.dev', title: 'Go', pageAge: '1d', encryptedContent: 'enc' }],
      },
    }
    // Go's accumulator only folds block_done payloads into builtin_tool_use
    // and reasoning blocks; for every other kind the started payload wins.
    const blocks = accumulateLlm2StreamEvents([
      started(3, builtinResult),
      started(0, image),
      started(1, toolResult),
      started(2, refusal),
      textDelta(0, 'ignored'),
      textDelta(1, 'ignored'),
      summaryDelta(2, 'ignored'),
      textDelta(3, 'ignored'),
      done(0, { type: 'image', image: { url: 'https://example.com/other.png' } }),
      done(1, { type: 'tool_result', toolResult: { toolCallId: 'call_1', name: 'read_file', content: [] } }),
      done(2, { type: 'refusal', refusal: { reason: 'changed' } }),
      done(3, { type: 'builtin_tool_result', builtinToolResult: { toolCallId: 'ws_1', name: 'web_search', isError: true, content: 'changed' } }),
    ])
    expect(blocks).toEqual([image, toolResult, refusal, builtinResult])
  })

  it('treats heartbeat events as no-ops', () => {
    const blocks = accumulateLlm2StreamEvents([
      { type: 'heartbeat', index: 0 },
      started(0, { type: 'text', text: 'a' }),
      { type: 'heartbeat', index: 0 },
      { type: 'heartbeat', index: 5 },
    ])
    expect(blocks).toEqual([{ type: 'text', text: 'a' }])
  })

  it('ignores block_done payloads whose type differs from the started block', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(0, { type: 'text', text: 'kept' }),
      done(0, { type: 'reasoning', reasoning: { text: 'ignored' } }),
    ])
    expect(blocks).toEqual([{ type: 'text', text: 'kept' }])
  })

  it('orders blocks by index regardless of event interleaving and skips missing indexes', () => {
    const blocks = accumulateLlm2StreamEvents([
      started(2, { type: 'text', text: '' }),
      started(0, { type: 'reasoning', reasoning: { text: '' } }),
      textDelta(2, 'answer'),
      textDelta(0, 'thought'),
      done(3),
    ])
    expect(blocks.map((b) => b.type)).toEqual(['reasoning', 'text'])
    expect(blocks).toEqual([
      { type: 'reasoning', reasoning: { text: 'thought' } },
      { type: 'text', text: 'answer' },
    ])
  })

  it('drops deltas for blocks that were never started', () => {
    expect(accumulateLlm2StreamEvents([textDelta(0, 'orphan'), summaryDelta(1, 'orphan')])).toEqual([])
  })

  it('does not mutate the block carried by the block_started event', () => {
    const original: Llm2ContentBlock = { type: 'tool_use', toolUse: { id: 'c', name: 'n', arguments: '' } }
    accumulateLlm2StreamEvents([started(0, original), textDelta(0, '{}')])
    expect(original.toolUse.arguments).toBe('')
  })
})

describe('Llm2StreamAccumulator', () => {
  it('exposes progressively complete blocks after each event', () => {
    const accumulator = new Llm2StreamAccumulator()
    accumulator.applyEvent(started(0, { type: 'text', text: '' }))
    expect(accumulator.blocks).toEqual([{ type: 'text', text: '' }])

    accumulator.applyEvent(textDelta(0, 'partial'))
    expect(accumulator.blocks).toEqual([{ type: 'text', text: 'partial' }])

    accumulator.applyEvent(started(1, { type: 'tool_use', toolUse: { id: 'c', name: 'edit', arguments: '' } }))
    expect(accumulator.blocks.map((b) => b.type)).toEqual(['text', 'tool_use'])
  })

  it('stores encrypted content from signature_delta on reasoning blocks', () => {
    const accumulator = new Llm2StreamAccumulator()
    accumulator.applyEvent(started(0, { type: 'reasoning', reasoning: { text: 't' } }))
    accumulator.applyEvent({ type: 'signature_delta', index: 0, delta: 'ciphertext' })
    expect(accumulator.blocks).toEqual([
      { type: 'reasoning', reasoning: { text: 't', encryptedContent: 'ciphertext' } },
    ])
  })
})